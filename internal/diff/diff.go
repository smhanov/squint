package diff

import (
	"slices"
	"strconv"
	"strings"
)

// MaxDiffBytes caps how much raw diff text ParseUnified consumes. Larger
// diffs are cut at the boundary and the affected file is flagged with
// Truncated, so the viewer can render a "[diff truncated]" marker row.
const MaxDiffBytes = 3 << 20 // 3 MiB

// devNull marks the missing side of an added or deleted file.
const devNull = "/dev/null"

// LineKind classifies a line of a hunk.
type LineKind int8

// The three kinds of diff line.
const (
	Context LineKind = iota
	Add
	Del
)

func (k LineKind) String() string {
	switch k {
	case Add:
		return "add"
	case Del:
		return "del"
	default:
		return "context"
	}
}

// DiffLine is one line inside a hunk. Line numbers are 1-based; the
// number of the missing side (an added line has no old position) is 0.
type DiffLine struct {
	Kind  LineKind
	Text  string // content without the leading +/-/space marker
	OldNo int
	NewNo int
	// Words carries intraline word-diff segments (ParseWordDiff only).
	// Nil for plain unified lines; rendering falls back to Text.
	Words []WordSeg
	// Marks holds the per-parent marker columns of a combined (merge)
	// hunk line, verbatim as git printed them, one per parent: Add
	// ('+'), Del ('-'), or Context (' '). Nil for plain hunks. Kind is
	// the folded view (any + wins over any -) kept for coloring and
	// context trimming. The columns are git's own attribution
	// bookkeeping: only the result side follows an exact rule (a line
	// is in the result iff no column is '-'), so OldNo stays 0 for
	// combined lines and NewNo carries the exact result position.
	Marks []LineKind
}

// ParentRange is one parent's "@@ -a,b" range of a combined hunk.
type ParentRange struct {
	Start, Count int
}

// Hunk is one "@@ -a,b +c,d @@" block. Combined merge hunks ("@@@")
// also carry Parents, one range per parent.
type Hunk struct {
	Header             string
	OldStart, OldCount int
	NewStart, NewCount int
	// Parents holds every parent's range of a combined hunk, first
	// parent first; nil for plain hunks (Old*/New* are the two sides).
	Parents []ParentRange
	Lines   []DiffLine
}

// FileDiff is one file's diff as parsed from unified output.
type FileDiff struct {
	// Path is the file's path at the new side; for deletions it is the
	// old path (there is no new side).
	Path string
	// OldPath is the rename/copy source; empty when not renamed.
	OldPath string
	// NewFile and Deleted mark additions and removals.
	NewFile bool
	Deleted bool
	// Binary marks binary files (marker line or a NUL byte sniffed in
	// the patch body). Their Hunks stay empty.
	Binary bool
	// Size is the blob size in bytes for binary files when known
	// (enriched from the tree); 0 when unknown. Text files don't carry it.
	Size int64
	// Truncated is set on the file that lost text to MaxDiffBytes.
	Truncated bool
	Hunks     []Hunk
}

// ParseUnified parses git unified diff text into per-file diffs. It is
// lenient: unknown lines are skipped, malformed hunks never abort the
// parse, and empty input yields nil.
//
// Combined merge diffs ("diff --cc" with "@@@" headers) are modeled
// exactly as far as the format defines: each hunk carries one range per
// parent (Hunk.Parents), each body line carries its verbatim per-parent
// marker columns (DiffLine.Marks), and NewNo tracks the result side,
// which follows the one exact rule of the combined format — a line is
// in the result iff no column is '-'.
func ParseUnified(out []byte) []FileDiff { return parse(out, false) }

// ParseWordDiff parses the output of git --word-diff=plain into per-file
// diffs. Changed lines carry inline intraline markers ({+added+} and
// [-removed-]) instead of paired +/- rows; each parsed DiffLine carries
// its marker segments in Words so the viewer can render word-level
// emphasis. Unchanged lines in this format have no leading marker
// character, so the parser classifies them from their segments.
func ParseWordDiff(out []byte) []FileDiff { return parse(out, true) }

func parse(out []byte, wordDiff bool) []FileDiff {
	truncated := false
	if len(out) > MaxDiffBytes {
		out = out[:MaxDiffBytes]
		truncated = true
	}

	var files []FileDiff
	var cur *fileState
	var hunk *Hunk // points into cur.Hunks
	oldNo, newNo := 0, 0
	combinedCols := 0 // marker columns of a combined hunk; 0 = plain

	flush := func() {
		if cur == nil {
			return
		}
		files = append(files, FileDiff{
			Path:    cur.path(),
			OldPath: cur.renameFrom,
			NewFile: cur.newFile,
			Deleted: cur.deleted,
			Binary:  cur.binary,
			Hunks:   cur.hunks,
		})
		cur = nil
		hunk = nil
		combinedCols = 0
	}

	lines := strings.Split(string(out), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // artifact after the final newline
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &fileState{}
			cur.fromSummary, cur.toSummary = parseSummaryLine(line)

		case strings.HasPrefix(line, "diff --cc "), strings.HasPrefix(line, "diff --combined "):
			// Combined merge header: a single path follows the prefix.
			flush()
			cur = &fileState{}
			rest := strings.TrimPrefix(line, "diff --cc ")
			rest = strings.TrimPrefix(rest, "diff --combined ")
			cur.toSummary = unquote(rest)

		case strings.HasPrefix(line, "@@@"):
			// Combined merge hunk: "@@@ -a,b -c,d +e,f @@@". Checked
			// before the hunk-body branch: body lines carry a marker
			// prefix and can never start with "@@".
			h, parents, ok := parseCombinedHeader(line)
			if !ok || cur == nil {
				continue
			}
			cur.hunks = append(cur.hunks, h)
			hunk = &cur.hunks[len(cur.hunks)-1]
			combinedCols = len(parents)
			oldNo, newNo = 0, h.NewStart

		case strings.HasPrefix(line, "@@ "):
			h, ok := parseHunkHeader(line)
			if !ok || cur == nil {
				continue
			}
			cur.hunks = append(cur.hunks, h)
			hunk = &cur.hunks[len(cur.hunks)-1]
			oldNo, newNo = h.OldStart, h.NewStart
			combinedCols = 0

		case hunk != nil:
			// Inside a hunk every line is body until the next header;
			// content may legitimately start with "--- " or "index ".
			var kind LineKind
			var text string
			var words []WordSeg
			var marks []LineKind
			switch {
			case combinedCols > 0:
				// Combined body: one marker column per parent, verbatim,
				// then the content. Only the result side follows an
				// exact rule: the line is in the result iff no column
				// is '-'; the columns themselves are git's attribution
				// bookkeeping (see DiffLine.Marks).
				if len(line) < combinedCols {
					continue
				}
				prefix, body := line[:combinedCols], line[combinedCols:]
				hasPlus, hasMinus := false, false
				marks = make([]LineKind, combinedCols)
				for i := 0; i < combinedCols; i++ {
					switch prefix[i] {
					case '+':
						marks[i], hasPlus = Add, true
					case '-':
						marks[i], hasMinus = Del, true
					default:
						marks[i] = Context
					}
				}
				switch {
				case hasPlus:
					kind = Add
				case hasMinus:
					kind = Del
				default:
					kind = Context
				}
				text = body
			case line == "":
				// Tolerate a bare empty line as an empty context row.
				kind, text = Context, ""
			case wordDiff && (strings.Contains(line, "{+") || strings.Contains(line, "[-")):
				// Word-diff body line: inline {+added+}/[-removed-]
				// markers instead of a prefix. The kind derives from the
				// segments so line counters and rendering stay honest: a
				// line with any plain text exists on both sides; only a
				// fully wrapped {+…+} or [-…-] row is a pure add/del.
				words = Words(line)
				hasAdd, hasDel, hasPlain := false, false, false
				for _, w := range words {
					switch w.Kind {
					case SegAdd:
						hasAdd = true
					case SegDel:
						hasDel = true
					case SegPlain:
						hasPlain = true
					}
				}
				switch {
				case hasAdd && !hasPlain && !hasDel:
					kind = Add
				case hasDel && !hasPlain && !hasAdd:
					kind = Del
				default:
					kind = Context
				}
				text = line
			default:
				switch line[0] {
				case '+':
					kind, text = Add, line[1:]
				case '-':
					kind, text = Del, line[1:]
				case '\\':
					// "\ No newline at end of file": an annotation,
					// not a content line.
					continue
				default:
					// ' ' is context; anything else is tolerated the
					// same way so malformed patches never abort.
					kind, text = Context, strings.TrimPrefix(line, " ")
				}
			}
			l := DiffLine{Kind: kind, Text: text, Words: words, Marks: marks}
			if marks != nil {
				// Combined: the result position is exact (a line is in
				// the result iff no column is '-'); there is no
				// derivable per-parent position (see the Marks doc).
				if !slices.Contains(marks, Del) {
					l.NewNo = newNo
					newNo++
				}
				hunk.Lines = append(hunk.Lines, l)
				if strings.ContainsRune(text, 0) {
					cur.binary = true
				}
				continue
			}
			switch kind {
			case Add:
				l.NewNo = newNo
				newNo++
			case Del:
				l.OldNo = oldNo
				oldNo++
			default:
				l.OldNo, l.NewNo = oldNo, newNo
				oldNo++
				newNo++
			}
			if strings.ContainsRune(text, 0) {
				cur.binary = true // NUL sniff: git never prints NULs in text patches
			}
			hunk.Lines = append(hunk.Lines, l)

		case strings.HasPrefix(line, "--- "):
			if cur != nil {
				cur.pathA = headerPath(line, "--- ")
			}

		case strings.HasPrefix(line, "+++ "):
			if cur != nil {
				cur.pathB = headerPath(line, "+++ ")
			}

		case strings.HasPrefix(line, "new file mode"):
			if cur != nil {
				cur.newFile = true
			}

		case strings.HasPrefix(line, "deleted file mode"):
			if cur != nil {
				cur.deleted = true
			}

		case strings.HasPrefix(line, "rename from "):
			if cur != nil {
				cur.renameFrom = unquote(strings.TrimPrefix(line, "rename from "))
			}

		case strings.HasPrefix(line, "rename to "):
			if cur != nil {
				cur.renameTo = unquote(strings.TrimPrefix(line, "rename to "))
			}

		case strings.HasPrefix(line, "copy from "):
			if cur != nil {
				cur.renameFrom = unquote(strings.TrimPrefix(line, "copy from "))
			}

		case strings.HasPrefix(line, "copy to "):
			if cur != nil {
				cur.renameTo = unquote(strings.TrimPrefix(line, "copy to "))
			}

		case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
			if cur != nil {
				cur.binary = true
			}

		default:
			// index/similarity/mode headers and stray noise between
			// files: nothing to record.
		}
	}
	flush()

	if truncated && len(files) > 0 {
		files[len(files)-1].Truncated = true
	}
	return files
}

// fileState accumulates the header facts of the file being parsed.
type fileState struct {
	pathA, pathB             string // ---/+++ sides, /dev/null possible
	fromSummary, toSummary   string // heuristic fallback from "diff --git"
	renameFrom, renameTo     string
	newFile, deleted, binary bool
	hunks                    []Hunk
}

// path resolves the file's new-side path: rename target first, then the
// +++ side, then the --- side (for deletions), then the summary line.
func (s *fileState) path() string {
	switch {
	case s.renameTo != "":
		return s.renameTo
	case s.pathB != "" && s.pathB != devNull:
		return s.pathB
	case s.pathA != "" && s.pathA != devNull:
		return s.pathA
	case s.toSummary != "" && s.toSummary != devNull:
		return s.toSummary
	case s.fromSummary != "" && s.fromSummary != devNull:
		return s.fromSummary
	default:
		return ""
	}
}

// parseSummaryLine extracts the a/ and b/ paths from a "diff --git"
// line. Quoted paths defeat the heuristic and are left empty; the
// ---/+++ and rename lines carry them instead.
func parseSummaryLine(line string) (from, to string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	a, b, ok := strings.Cut(rest, " b/")
	if !ok || !strings.HasPrefix(a, "a/") {
		return "", ""
	}
	return stripAB(a), stripAB("b/" + b)
}

// headerPath extracts a path from a "--- " or "+++ " line: strip prefix
// and trailing tab (git appends one when the path contains spaces), then
// unquote and drop the a/ or b/ prefix.
func headerPath(line, prefix string) string {
	p := strings.TrimPrefix(line, prefix)
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	return stripAB(unquote(p))
}

// stripAB removes the a/ or b/ prefix git puts in front of paths.
func stripAB(p string) string {
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// unquote decodes git's C-style quoting of paths with special characters.
func unquote(p string) string {
	if len(p) >= 2 && p[0] == '"' {
		if s, err := strconv.Unquote(p); err == nil {
			return s
		}
	}
	return p
}

// parseHunkHeader parses "@@ -a,b +c,d @@ section". A missing ",count"
// means 1.
func parseHunkHeader(line string) (Hunk, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != "@@" {
		return Hunk{}, false
	}
	oldStart, oldCount, ok1 := parseRange(fields[1])
	newStart, newCount, ok2 := parseRange(fields[2])
	if !ok1 || !ok2 {
		return Hunk{}, false
	}
	return Hunk{
		Header:   line,
		OldStart: oldStart, OldCount: oldCount,
		NewStart: newStart, NewCount: newCount,
	}, true
}

// parseCombinedHeader parses "@@@ -a,b -c,d +e,f @@@": one range per
// parent plus the new side. It returns the hunk with every parent's
// range (Parents; OldStart/OldCount alias the first parent, New* the
// result) and the parent list for the parser's column width.
func parseCombinedHeader(line string) (Hunk, []ParentRange, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != "@@@" || fields[len(fields)-1] != "@@@" {
		return Hunk{}, nil, false
	}
	ranges := fields[1 : len(fields)-1]
	if len(ranges) < 2 {
		return Hunk{}, nil, false
	}
	parents := make([]ParentRange, len(ranges)-1)
	for i := range parents {
		start, count, ok := parseRange(ranges[i])
		if !ok {
			return Hunk{}, nil, false
		}
		parents[i] = ParentRange{Start: start, Count: count}
	}
	newStart, newCount, ok := parseRange(ranges[len(ranges)-1])
	if !ok {
		return Hunk{}, nil, false
	}
	return Hunk{
		Header:   line,
		OldStart: parents[0].Start, OldCount: parents[0].Count,
		NewStart: newStart, NewCount: newCount,
		Parents: parents,
	}, parents, true
}

// parseRange parses "-a,b" or "+a,b"; the count defaults to 1.
func parseRange(s string) (start, count int, ok bool) {
	if len(s) < 2 || (s[0] != '-' && s[0] != '+') {
		return 0, 0, false
	}
	s = s[1:]
	if i := strings.IndexByte(s, ','); i >= 0 {
		start, err1 := strconv.Atoi(s[:i])
		count, err2 := strconv.Atoi(s[i+1:])
		return start, count, err1 == nil && err2 == nil
	}
	start, err := strconv.Atoi(s)
	return start, 1, err == nil
}

// TrimContext returns a copy of files whose hunks keep at most keep
// leading and trailing context lines. Interior context is untouched:
// exact windows require a re-diff with -U<n>; this helper is for quick
// local shrinking of an already parsed diff.
func TrimContext(files []FileDiff, keep int) []FileDiff {
	if keep < 0 {
		keep = 0
	}
	out := make([]FileDiff, len(files))
	copy(out, files)
	for i := range out {
		hunks := make([]Hunk, len(out[i].Hunks))
		copy(hunks, out[i].Hunks)
		for j := range hunks {
			h := &hunks[j]
			first, last := -1, -1
			for k, l := range h.Lines {
				if l.Kind != Context {
					if first < 0 {
						first = k
					}
					last = k
				}
			}
			if first < 0 {
				continue // context-only hunk: leave as-is
			}
			lo := max(first-keep, 0)
			hi := min(last+keep+1, len(h.Lines))
			h.Lines = append([]DiffLine(nil), h.Lines[lo:hi]...)
		}
		out[i].Hunks = hunks
	}
	return out
}

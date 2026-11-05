package diff

import "strings"

// words.go splits intraline word-diff text into colored segments. The
// --word-diff=plain format marks added text with {+…+} and removed text
// with [-…-]; everything between the markers is unchanged context.

// SegKind classifies one intraline segment.
type SegKind int8

const (
	SegPlain SegKind = iota
	SegAdd           // {+…+}: text added on the new side
	SegDel           // [-…-]: text removed from the old side
)

// WordSeg is one run of text inside a diff line, tagged with its kind.
type WordSeg struct {
	Kind SegKind
	Text string
}

// Words splits a word-diff body line into segments. Unterminated markers
// are kept as plain text, so a literal "{+" in source content never
// swallows the rest of the line.
func Words(text string) []WordSeg {
	if !strings.Contains(text, "{+") && !strings.Contains(text, "[-") {
		return nil
	}
	var segs []WordSeg
	flush := func(kind SegKind, s string) {
		if s == "" {
			return
		}
		segs = append(segs, WordSeg{Kind: kind, Text: s})
	}
	rest := text
	for {
		iAdd, iDel := strings.Index(rest, "{+"), strings.Index(rest, "[-")
		start, segKind := -1, SegAdd
		switch {
		case iAdd >= 0 && (iDel < 0 || iAdd < iDel):
			start, segKind = iAdd, SegAdd
		case iDel >= 0:
			start, segKind = iDel, SegDel
		default:
			flush(SegPlain, rest)
			return segs
		}
		open, closer := "{+", "+}"
		if segKind == SegDel {
			open, closer = "[-", "-]"
		}
		flush(SegPlain, rest[:start])
		end := strings.Index(rest[start+len(open):], closer)
		if end < 0 {
			// Unterminated marker: keep the raw tail as plain text.
			flush(SegPlain, rest[start:])
			return segs
		}
		body := rest[start+len(open) : start+len(open)+end]
		flush(segKind, body)
		rest = rest[start+len(open)+end+len(closer):]
	}
}

// HasWords reports whether a line carries intraline segments.
func (l DiffLine) HasWords() bool { return len(l.Words) > 0 }

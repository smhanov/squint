package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/squint-tui/squint/internal/diff"
	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/ui"
)

// detail.go is the message + diff pane: the header block (subject, author,
// relative and absolute time, full body) followed by one section per
// changed file with its hunks. While rendering it records a fileSpan row
// index per file (spec §17.5: build the index during render), which
// drives file jumps and the files-pane enter key (FR-4).

// diffContextDefault is git's default context size; the + and - keys move
// it within 0–10.
const (
	diffContextDefault = 3
	diffContextMax     = 10
)

// wrapMode is the wrap cycle of the detail pane: none → word → character.
type wrapMode int

const (
	wrapNone wrapMode = iota
	wrapWord
	wrapChar
)

func (w wrapMode) String() string {
	switch w {
	case wrapWord:
		return "word"
	case wrapChar:
		return "character"
	default:
		return "none"
	}
}

// fileSpan is the range of rendered content rows one file occupies. The
// first row is the file rule line.
type fileSpan struct {
	path       string
	start, end int
}

// detailPane holds the detail pane state. hash is the commit whose diff is
// loaded; dirtyOpts marks a refetch whose viewer options differ from the
// rendered content (its result was requested but not yet landed).
type detailPane struct {
	commit git.Commit

	hash      string
	diffs     []diff.FileDiff
	err       string
	dirtyOpts bool

	context  int
	ignoreWS bool
	wrap     wrapMode

	view     ui.Scroller
	spans    []fileSpan
	hunkRows []int // content rows of hunk headers, in order

	// row is the logical navigation position: the row last jumped to,
	// unclamped. The viewport clamps what it actually shows, but
	// navigation must keep advancing across jumps even when the whole
	// diff fits on screen (nothing to scroll).
	row int
}

// newDetailPane returns a detail pane with git's default viewer options.
func newDetailPane() detailPane {
	return detailPane{context: diffContextDefault, view: ui.NewScroller()}
}

// dropContent clears rendered diff content that belongs to a different
// commit than the one now selected.
func (d *detailPane) dropContent() {
	d.hash = ""
	d.diffs = nil
	d.spans = nil
	d.hunkRows = nil
	d.row = 0
	d.view.Top()
}

// reset clears everything; used when the repository has no commits.
func (d *detailPane) reset() {
	d.commit = git.Commit{}
	d.hash = ""
	d.diffs = nil
	d.err = ""
	d.dirtyOpts = false
	d.spans = nil
	d.hunkRows = nil
	d.row = 0
	d.view.SetLines(nil)
	d.view.Top()
}

// apply stores a landed diff. A new commit starts scrolled to the top; a
// refetch of the same commit keeps the reading position.
func (d *detailPane) apply(hash string, diffs []diff.FileDiff, err error) {
	if err != nil {
		d.err = err.Error()
		d.dirtyOpts = false
		return
	}
	if hash != d.hash {
		d.view.Top()
		d.row = 0
	}
	d.err = ""
	d.hash = hash
	d.dirtyOpts = false
	d.diffs = diffs
}

// jumpTo moves the logical navigation row and scrolls the viewport there
// (best effort: the viewport clamps when the content fits on screen).
func (d *detailPane) jumpTo(row int) {
	d.row = row
	d.view.SetOffset(row)
}

// scrollBy scrolls line-wise and re-syncs the logical row with what is
// visible.
func (d *detailPane) scrollBy(n int) {
	if n > 0 {
		d.view.Down(n)
	} else {
		d.view.Up(-n)
	}
	d.row = d.view.Offset()
}

// scrollToFile scrolls the pane so the named file's first hunk area is at
// the top (FR-4).
func (d *detailPane) scrollToFile(path string) {
	for _, s := range d.spans {
		if s.path == path {
			d.jumpTo(s.start)
			return
		}
	}
}

// nextHunk jumps to the first hunk header below the current position.
func (d *detailPane) nextHunk() {
	for _, row := range d.hunkRows {
		if row > d.row {
			d.jumpTo(row)
			return
		}
	}
}

// prevHunk jumps to the last hunk header above the current position.
func (d *detailPane) prevHunk() {
	prev, found := 0, false
	for _, row := range d.hunkRows {
		if row < d.row {
			prev, found = row, true
		}
	}
	if found {
		d.jumpTo(prev)
	}
}

// nextFileSpan returns the first file section starting below the current
// position.
func (d *detailPane) nextFileSpan() (fileSpan, bool) {
	for _, s := range d.spans {
		if s.start > d.row {
			return s, true
		}
	}
	return fileSpan{}, false
}

// prevFileSpan returns the last file section starting above the current
// position.
func (d *detailPane) prevFileSpan() (fileSpan, bool) {
	for i := len(d.spans) - 1; i >= 0; i-- {
		if d.spans[i].start < d.row {
			return d.spans[i], true
		}
	}
	return fileSpan{}, false
}

// detailEnv carries the per-frame render inputs the detail pane needs
// from the root model.
type detailEnv struct {
	width   int
	now     time.Time
	ascii   bool
	spinner string
	loading bool
}

// rebuild renders the whole detail content: header block, per-file diff
// sections, and the span/hunk row index. Called from Update so View stays
// pure.
func (d *detailPane) rebuild(env detailEnv) {
	d.spans = nil
	d.hunkRows = nil
	b := &lineBuilder{width: env.width, wrap: d.wrap}

	if d.commit.Hash == "" {
		b.add(ui.StyleFaint.Render("(select a commit)"))
		d.view.SetLines(b.lines)
		return
	}

	// Header block (spec §7.1: full message, author line).
	c := d.commit
	b.add(ui.StyleBold.Render(c.Subject))
	b.add(ui.StyleFaint.Render(fmt.Sprintf("%s <%s> · %s ago · %s",
		c.Author, c.Email, relAge(c.At, env.now), c.At.UTC().Format(absTimeFormat))))
	b.add("")
	if body := strings.Trim(c.Body, "\n"); body != "" {
		for _, line := range strings.Split(body, "\n") {
			b.add(line)
		}
		b.add("")
	}

	// Diff section: a spinner while loading, the error row on failure,
	// otherwise one rule-delimited section per file.
	switch {
	case d.err != "":
		b.add(ui.StyleDel.Render("git error: " + d.err))
	case env.loading:
		b.add(ui.StyleFaint.Render(env.spinner + " loading diff…"))
	}
	for _, fd := range d.diffs {
		start := len(b.lines)
		b.add(fileRule(fd, env.width, env.ascii))
		if fd.Binary {
			b.add(ui.StyleFaint.Render("[binary]"))
		}
		if fd.Truncated {
			b.add(ui.StyleDel.Render("[diff truncated]"))
		}
		for _, h := range fd.Hunks {
			d.hunkRows = append(d.hunkRows, len(b.lines))
			b.add(ui.StyleFaint.Render(h.Header))
			for _, l := range h.Lines {
				b.add(diffLine(l))
			}
		}
		d.spans = append(d.spans, fileSpan{path: fd.Path, start: start, end: len(b.lines) - 1})
	}
	d.view.SetLines(b.lines)
}

// absTimeFormat is the absolute timestamp shown next to the relative age.
const absTimeFormat = "2006-01-02 15:04 MST"

// fileRule renders the "── path ─────" section rule; renames show
// "old → new".
func fileRule(fd diff.FileDiff, width int, ascii bool) string {
	title := fd.Path
	if fd.OldPath != "" {
		arrow := " → "
		if ascii {
			arrow = " -> "
		}
		title = fd.OldPath + arrow + fd.Path
	}
	rule := "─"
	if ascii {
		rule = "-"
	}
	head := rule + rule + " " + title + " "
	fill := max(0, width-lipgloss.Width(head))
	return ui.StyleFileRule.Render(head + strings.Repeat(rule, fill))
}

// diffLine renders one diff row with its marker colored by kind.
func diffLine(l diff.DiffLine) string {
	switch l.Kind {
	case diff.Add:
		return ui.StyleAdd.Render("+" + l.Text)
	case diff.Del:
		return ui.StyleDel.Render("-" + l.Text)
	default:
		return " " + l.Text
	}
}

// lineBuilder accumulates content rows, wrapping each logical line to the
// pane width according to the wrap mode. All row indices (spans, hunks)
// are recorded against the wrapped rows, so navigation stays exact.
type lineBuilder struct {
	lines []string
	width int
	wrap  wrapMode
}

// add appends one logical line, wrapped as needed.
func (b *lineBuilder) add(line string) {
	if b.width <= 0 || b.wrap == wrapNone || lipgloss.Width(line) <= b.width {
		b.lines = append(b.lines, line)
		return
	}
	var wrapped string
	if b.wrap == wrapWord {
		wrapped = ansi.Wrap(line, b.width, "-")
	} else {
		wrapped = ansi.Hardwrap(line, b.width, true)
	}
	b.lines = append(b.lines, strings.Split(wrapped, "\n")...)
}

// detailKey handles the detail pane keys (spec §10.4).
func (a App) detailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "j", "down":
		a.detail.scrollBy(1)
	case "k", "up":
		a.detail.scrollBy(-1)
	case ")":
		a.detail.nextHunk()
	case "(":
		a.detail.prevHunk()
	case "]":
		a.detailJumpFile(1)
	case "[":
		a.detailJumpFile(-1)
	case "w":
		a.detail.wrap = (a.detail.wrap + 1) % 3
		a.rebuildDetailLines()
	case "W":
		a.detail.ignoreWS = !a.detail.ignoreWS
		return a, a.refetchDiff()
	case "+", "=":
		if a.detail.context < diffContextMax {
			a.detail.context++
			return a, a.refetchDiff()
		}
	case "-":
		if a.detail.context > 0 {
			a.detail.context--
			return a, a.refetchDiff()
		}
	}
	return a, nil
}

// refetchDiff re-runs the diff load for the selected commit under the
// current viewer options. dirtyOpts keeps the status spinner honest even
// if this load is later dropped as stale by a refresh.
//
// Pointer receiver: the dirty flag and the re-rendered content must
// survive into the model the returned command belongs to.
func (a *App) refetchDiff() tea.Cmd {
	if a.repo == nil || a.selectedHash == "" || a.cursor >= len(a.commits) {
		return nil
	}
	a.gen++
	a.detail.dirtyOpts = true
	a.rebuildDetailLines()
	return tea.Batch(a.loadDiffCmd(a.commits[a.cursor]), a.spinKick())
}

// detailJumpFile scrolls to the previous or next file section and moves
// the files-pane cursor along with it. Pointer receiver: the jump must
// persist in the caller's model.
func (a *App) detailJumpFile(delta int) {
	var (
		s  fileSpan
		ok bool
	)
	if delta > 0 {
		s, ok = a.detail.nextFileSpan()
	} else {
		s, ok = a.detail.prevFileSpan()
	}
	if !ok {
		return
	}
	a.detail.jumpTo(s.start)
	a.syncFileCursorByPath(s.path)
}

// syncFileCursorByPath moves the files-pane cursor to the named file.
func (a *App) syncFileCursorByPath(path string) {
	for i := range a.files {
		if a.files[i].Path == path {
			if i != a.fileCursor {
				a.fileCursor = i
				a.rebuildFilesLines()
			}
			return
		}
	}
}

// rebuildDetailLines re-renders the detail content for the current width,
// clock, and load state.
func (a *App) rebuildDetailLines() {
	if !a.laidOut() {
		return
	}
	a.detail.rebuild(detailEnv{
		width:   a.lay.rightW - 2,
		now:     a.now(),
		ascii:   a.cfg.ASCII,
		spinner: a.spinner.View(),
		loading: a.detailStale(),
	})
}

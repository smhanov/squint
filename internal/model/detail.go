package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/smhanov/squint/internal/diff"
	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/ui"
)

// detail.go is the message + diff pane: the header block (subject, author,
// relative and absolute time, full body) followed by one section per
// changed file with its hunks. It also carries the since-diff mode (FR-10)
// and the viewer options: unified or side-by-side (FR-6), syntax
// highlighting (FR-7), intraline word-diff (FR-8), wrap, context,
// ignore-whitespace, and horizontal scroll. While rendering it records a
// fileSpan row index per file (spec §17.5: build the index during
// render), which drives file jumps and the files-pane enter key (FR-4).

// diffContextDefault is git's default context size; the + and - keys move
// it within 0–10.
const (
	diffContextDefault = 3
	diffContextMax     = 10
	// hScrollStep is the number of columns h/l shift the diff body.
	hScrollStep = 4
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

// sinceView holds the since-diff (FR-10): the cumulative diff from the
// selected commit's first parent to HEAD. on selects the mode; forHash
// records the selection the loaded range belongs to.
type sinceView struct {
	on      bool
	forHash string
	base    string
	// count is the number of commits in base..HEAD; -1 when unknown
	// (the count read failed).
	count int
	diffs []diff.FileDiff
	err   string
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

	context    int
	ignoreWS   bool
	wrap       wrapMode
	sideBySide bool // s: two columns, old left of new (FR-6)
	wordDiff   bool // i: intraline word-diff (FR-8)
	scrollX    int  // horizontal scroll in columns (wrap off)

	since sinceView

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

// dropSince exits since-diff mode and clears its content.
func (d *detailPane) dropSince() {
	d.since = sinceView{}
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
	d.since = sinceView{}
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

// applySince stores a landed since-diff for the selection.
func (d *detailPane) applySince(hash, base string, count int, diffs []diff.FileDiff, err error) {
	d.dirtyOpts = false
	if err != nil {
		d.since.forHash = hash
		d.since.err = err.Error()
		return
	}
	if hash != d.since.forHash {
		d.view.Top()
		d.row = 0
	}
	d.since.forHash = hash
	d.since.base = base
	d.since.count = count
	d.since.err = ""
	d.since.diffs = diffs
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

// rebuild renders the whole detail content: header block (or the
// since-diff banner), per-file diff sections, and the span/hunk row
// index. Called from Update so View stays pure.
func (d *detailPane) rebuild(env detailEnv) {
	d.spans = nil
	d.hunkRows = nil
	b := &lineBuilder{width: env.width, wrap: d.wrap, scrollX: d.scrollX}

	if d.commit.Hash == "" {
		b.add(ui.StyleFaint.Render("(select a commit)"))
		d.view.SetLines(b.lines)
		return
	}

	if d.since.on {
		d.rebuildSince(b, env)
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

	switch {
	case d.err != "":
		b.add(ui.StyleDel.Render("git error: " + d.err))
	case env.loading:
		b.add(ui.StyleFaint.Render(env.spinner + " loading diff…"))
	}
	d.renderFiles(b, env, d.diffs)
	d.view.SetLines(b.lines)
}

// rebuildSince renders the since-diff body under its banner (FR-10).
func (d *detailPane) rebuildSince(b *lineBuilder, env detailEnv) {
	dirty := d.since.forHash != d.commit.Hash || d.dirtyOpts
	b.add(ui.StyleFileRule.Render(d.sinceBanner()))
	switch {
	case d.since.err != "":
		b.add(ui.StyleDel.Render("git error: " + d.since.err))
	case dirty:
		b.add(ui.StyleFaint.Render(env.spinner + " loading diff…"))
	}
	d.renderFiles(b, env, d.since.diffs)
}

// sinceBanner renders the "since <abbr> (N commits)" mode banner.
func (d *detailPane) sinceBanner() string {
	abbr := d.since.base
	if len(abbr) > abbrevLen {
		abbr = abbr[:abbrevLen]
	}
	count := "…"
	if d.since.count >= 0 {
		count = fmt.Sprintf("%d commits", d.since.count)
	}
	return fmt.Sprintf("since %s (%s)", abbr, count)
}

// renderFiles renders one rule-delimited section per file, in the
// current viewer mode. spent budgets the syntax-highlighting work of
// this pass.
func (d *detailPane) renderFiles(b *lineBuilder, env detailEnv, files []diff.FileDiff) {
	spent := 0
	for _, fd := range files {
		start := len(b.lines)
		b.add(fileRule(fd, env.width, env.ascii))
		if fd.Binary {
			note := "[binary]"
			if fd.Size > 0 {
				note = "[binary · " + humanSize(fd.Size) + "]"
			}
			b.add(ui.StyleFaint.Render(note))
		}
		if fd.Truncated {
			b.add(ui.StyleDel.Render("[diff truncated]"))
		}
		for _, h := range fd.Hunks {
			d.hunkRows = append(d.hunkRows, len(b.lines))
			b.add(ui.StyleFaint.Render(h.Header))
			if d.sideBySide && len(h.Parents) <= 1 {
				d.renderHunkSideBySide(b, env, fd.Path, h, &spent)
			} else {
				// Combined merge hunks have no old/new pairing (the
				// columns describe parents, not sides); render unified.
				for _, l := range h.Lines {
					b.add(d.unifiedLine(&l, fd.Path, &spent))
				}
			}
		}
		d.spans = append(d.spans, fileSpan{path: fd.Path, start: start, end: len(b.lines) - 1})
	}
}

// renderHunkSideBySide renders a hunk as aligned old|new columns (FR-6).
func (d *detailPane) renderHunkSideBySide(b *lineBuilder, env detailEnv, path string, h diff.Hunk, spent *int) {
	pairs := diff.PairLines(h.Lines)
	colW := sideColumnWidth(env.width)
	div := "│"
	if env.ascii {
		div = "|"
	}
	for _, p := range pairs {
		var left, right string
		if p.Left != nil {
			left = d.cell(p.Left, path, spent)
		}
		if p.Right != nil {
			right = d.cell(p.Right, path, spent)
		}
		b.addSidePair(left, right, colW, div)
	}
}

// sideColumnWidth splits the available width into two columns around a
// one-cell divider.
func sideColumnWidth(avail int) int {
	if avail < 3 {
		return 1
	}
	return (avail - 1) / 2
}

// cell renders one side of a side-by-side row: the kind marker followed
// by the content, highlighted (or word-emphasized) per viewer options.
func (d *detailPane) cell(l *diff.DiffLine, path string, spent *int) string {
	marker := " "
	var kindStyle lipgloss.Style
	switch l.Kind {
	case diff.Add:
		marker, kindStyle = "+", ui.StyleAdd
	case diff.Del:
		marker, kindStyle = "-", ui.StyleDel
	}
	var text string
	if l.HasWords() {
		text = renderWords(l.Words)
	} else {
		text = ui.Highlight(path, l.Text, spent)
	}
	return kindStyle.Render(marker + text)
}

// unifiedLine renders one diff row in unified mode: kind-colored marker
// and body, with syntax highlighting or intraline word emphasis.
// Combined merge lines render their verbatim per-parent marker columns,
// each column colored by its own marker.
func (d *detailPane) unifiedLine(l *diff.DiffLine, path string, spent *int) string {
	kindStyle := lipgloss.NewStyle()
	var marker string
	if len(l.Marks) > 1 {
		marker = d.combinedMarks(l.Marks)
		kindStyle = markerStyle(l.Kind)
	} else {
		switch l.Kind {
		case diff.Add:
			marker, kindStyle = "+", ui.StyleAdd
		case diff.Del:
			marker, kindStyle = "-", ui.StyleDel
		default:
			marker = " "
		}
	}
	var text string
	if l.HasWords() {
		text = renderWords(l.Words)
	} else {
		text = ui.Highlight(path, l.Text, spent)
	}
	return kindStyle.Render(marker + text)
}

// combinedMarks renders the verbatim per-parent marker columns of a
// combined hunk line, coloring each column by its own marker so the row
// matches git's output exactly (spec §5.3 combined modeling).
func (d *detailPane) combinedMarks(marks []diff.LineKind) string {
	var b strings.Builder
	for _, m := range marks {
		switch m {
		case diff.Add:
			b.WriteString(ui.StyleAdd.Render("+"))
		case diff.Del:
			b.WriteString(ui.StyleDel.Render("-"))
		default:
			b.WriteString(" ")
		}
	}
	return b.String()
}

// markerStyle picks the row style for a combined line from its folded
// kind: any '+' colors the row as an add, else any '-' as a deletion.
func markerStyle(kind diff.LineKind) lipgloss.Style {
	switch kind {
	case diff.Add:
		return ui.StyleAdd
	case diff.Del:
		return ui.StyleDel
	default:
		return lipgloss.NewStyle()
	}
}

// renderWords joins intraline segments, emphasizing added and removed
// words with the add/del colors (FR-8).
func renderWords(ws []diff.WordSeg) string {
	var b strings.Builder
	for _, w := range ws {
		switch w.Kind {
		case diff.SegAdd:
			b.WriteString(ui.StyleAdd.Render(w.Text))
		case diff.SegDel:
			b.WriteString(ui.StyleDel.Render(w.Text))
		default:
			b.WriteString(w.Text)
		}
	}
	return b.String()
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

// humanSize formats a byte count with binary units; whole values drop
// the decimal, others keep one.
func humanSize(n int64) string {
	const unit = 1024
	units := []string{"B", "KiB", "MiB", "GiB"}
	f := float64(n)
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d %s", int64(f), units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// lineBuilder accumulates content rows, wrapping each logical line to the
// pane width according to the wrap mode. All row indices (spans, hunks)
// are recorded against the wrapped rows, so navigation stays exact.
type lineBuilder struct {
	lines   []string
	width   int
	wrap    wrapMode
	scrollX int // horizontal scroll (columns), applied when wrap is off
}

// wrapLines splits one logical line into display rows for the current
// width and wrap mode.
func (b *lineBuilder) wrapLines(line string) []string {
	if b.width <= 0 || b.wrap == wrapNone || lipgloss.Width(line) <= b.width {
		return []string{line}
	}
	var wrapped string
	if b.wrap == wrapWord {
		wrapped = ansi.Wrap(line, b.width, "-")
	} else {
		wrapped = ansi.Hardwrap(line, b.width, true)
	}
	return strings.Split(wrapped, "\n")
}

// add appends one logical line, wrapped as needed.
func (b *lineBuilder) add(line string) {
	b.lines = append(b.lines, b.wrapLines(line)...)
}

// addSidePair appends one side-by-side row: both columns wrapped to colW
// cells and interleaved so paired rows stay aligned (FR-6).
func (b *lineBuilder) addSidePair(left, right string, colW int, divider string) {
	leftRows := b.pairCell(left, colW)
	rightRows := b.pairCell(right, colW)
	for i := range max(len(leftRows), len(rightRows)) {
		var l, r string
		if i < len(leftRows) {
			l = leftRows[i]
		}
		if i < len(rightRows) {
			r = rightRows[i]
		}
		b.lines = append(b.lines, l+divider+r)
	}
}

// pairCell clips a side-by-side cell to colW cells: word-wrapped when
// wrap is on, scrolled and cut when off.
func (b *lineBuilder) pairCell(cell string, colW int) []string {
	if b.wrap == wrapNone {
		// Horizontal scroll: cut the [scrollX, scrollX+colW) window.
		return []string{padTo(ansi.Cut(cell, b.scrollX, b.scrollX+colW), colW)}
	}
	rows := wrapToWidth(cell, b.wrap, colW)
	for i, r := range rows {
		rows[i] = padTo(r, colW)
	}
	return rows
}

// wrapToWidth wraps one cell independently of the pane width.
func wrapToWidth(cell string, mode wrapMode, width int) []string {
	if width <= 0 || lipgloss.Width(cell) <= width {
		return []string{cell}
	}
	if mode == wrapWord {
		return strings.Split(ansi.Wrap(cell, width, "-"), "\n")
	}
	return strings.Split(ansi.Hardwrap(cell, width, true), "\n")
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
	case "s":
		// Side-by-side toggle (FR-6). Word wrap reads best there, so a
		// no-wrap pane switches to it when entering the mode.
		a.detail.sideBySide = !a.detail.sideBySide
		a.detail.scrollX = 0
		if a.detail.sideBySide && a.detail.wrap == wrapNone {
			a.detail.wrap = wrapWord
		}
		a.rebuildDetailLines()
	case "h", "l":
		// Horizontal scroll, only meaningful when rows are not wrapped.
		if a.detail.wrap == wrapNone {
			if key == "h" {
				a.detail.scrollX = max(0, a.detail.scrollX-hScrollStep)
			} else {
				a.detail.scrollX += hScrollStep
			}
			a.rebuildDetailLines()
		}
	case "W":
		a.detail.ignoreWS = !a.detail.ignoreWS
		return a, a.refetchDetail()
	case "i":
		// Intraline word-diff toggle (FR-8): re-fetch with
		// --word-diff=plain.
		a.detail.wordDiff = !a.detail.wordDiff
		return a, a.refetchDetail()
	case "+", "=":
		if a.detail.context < diffContextMax {
			a.detail.context++
			return a, a.refetchDetail()
		}
	case "-":
		if a.detail.context > 0 {
			a.detail.context--
			return a, a.refetchDetail()
		}
	}
	return a, nil
}

// refetchDetail re-runs the content load for the selected commit in the
// current mode (commit diff or since-diff) under the current viewer
// options. dirtyOpts keeps the status spinner honest even if this load
// is later dropped as stale by a refresh.
//
// Pointer receiver: the dirty flag and the re-rendered content must
// survive into the model the returned command belongs to.
func (a *App) refetchDetail() tea.Cmd {
	if a.repo == nil || a.selectedHash == "" || a.cursor >= len(a.commits) {
		return nil
	}
	a.gen++
	a.detail.dirtyOpts = true
	a.rebuildDetailLines()
	if a.detail.since.on {
		return tea.Batch(a.loadSinceCmd(a.commits[a.cursor]), a.spinKick())
	}
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
// clock, and load state. A zoomed view gives the pane the full canvas.
func (a *App) rebuildDetailLines() {
	if !a.laidOut() {
		return
	}
	w := a.lay.rightW - 2
	if a.zoom {
		w = a.width - 2
	}
	a.detail.rebuild(detailEnv{
		width:   w,
		now:     a.now(),
		ascii:   a.cfg.ASCII,
		spinner: a.spinner.View(),
		loading: a.detailStale(),
	})
}

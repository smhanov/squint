package model

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

// mouse.go routes mouse events to panes (spec §11): the wheel scrolls
// the pane under the cursor, clicks select commits/files, double-clicks
// focus the detail pane, and dragging the divider resizes the split
// (session only). Hit-testing is geometry, not markup: the pane layout
// is fully known, so (x, y) maps to pane and content row arithmetically.
// --no-mouse disables the entire path (events are ignored, and the
// program never enables the terminal's mouse mode, spec FR-15).

const (
	// mouseWheelStep is the number of content rows one wheel notch moves.
	mouseWheelStep = 3
	// doubleClickWindow is the maximum gap between the two clicks of a
	// double-click (matches most operating systems' 500ms).
	doubleClickWindow = 500 * time.Millisecond
)

// minSplitRatio/maxSplitRatio bound the divider drag so either column
// stays usable.
const (
	minSplitRatio = 0.2
	maxSplitRatio = 0.8
)

// rect is an inclusive screen-coordinate rectangle (content area of a
// pane; borders excluded).
type rect struct{ x0, y0, x1, y1 int }

// empty reports whether the rectangle holds no cells. The zero rect is
// empty: a real content area always starts inside the border (x0 = 1).
func (r rect) empty() bool { return r.x0 <= 0 || r.x1 < r.x0 || r.y1 < r.y0 }

// contains reports whether (x, y) is inside the rectangle.
func (r rect) contains(x, y int) bool {
	return x >= r.x0 && x <= r.x1 && y >= r.y0 && y <= r.y1
}

// paneRects returns the content rectangles of the three panes as laid
// out on screen. Zoomed views leave the unfocused panes empty. ok is
// false before the first usable window size.
func (a App) paneRects() (commits, files, detail rect, ok bool) {
	if !a.laidOut() {
		return rect{}, rect{}, rect{}, false
	}
	inner := func(w, h int) rect {
		if w < 3 || h < 3 {
			return rect{} // no usable content area (borders would overlap)
		}
		return rect{x0: 1, y0: 1, x1: w - 2, y1: h - 2}
	}
	if a.zoom {
		switch a.focus {
		case focusCommits:
			return inner(a.lay.leftW, a.lay.commitsH), rect{}, rect{}, true
		case focusFiles:
			return rect{}, inner(a.width, a.lay.filesH), rect{}, true
		default:
			return rect{}, rect{}, inner(a.width, a.lay.detailH), true
		}
	}
	commits = inner(a.lay.leftW, a.lay.commitsH)
	if a.lay.filesH >= 3 {
		files = rect{x0: a.lay.leftW + 1, y0: 1, x1: a.width - 2, y1: a.lay.filesH - 2}
	}
	if a.lay.detailH >= 3 {
		detail = rect{x0: a.lay.leftW + 1, y0: a.lay.filesH + 1, x1: a.width - 2, y1: a.lay.commitsH - 2}
	}
	return commits, files, detail, true
}

// onDivider reports whether (x, y) sits on the columns between the two
// panes — the drag handle. Only defined for the split (non-zoom) layout.
func (a App) onDivider(x, y int) bool {
	if a.zoom || !a.laidOut() || y >= a.lay.commitsH {
		return false
	}
	if a.lay.leftW < 3 || a.lay.rightW < 3 {
		return false
	}
	return x == a.lay.leftW-1 || x == a.lay.leftW
}

// mouseHit resolves where a mouse event landed.
type mouseHit struct {
	pane paneFocus
	// row is the pane's content row under the cursor, or -1 when the
	// cursor is outside that pane's content area.
	row int
}

// hitAt maps screen coordinates to a pane content row.
func (a App) hitAt(x, y int) (mouseHit, bool) {
	commits, files, detail, ok := a.paneRects()
	if !ok {
		return mouseHit{}, false
	}
	switch {
	case commits.contains(x, y):
		return mouseHit{pane: focusCommits, row: a.commitsView.Offset() + (y - commits.y0)}, true
	case files.contains(x, y):
		return mouseHit{pane: focusFiles, row: a.filesView.Offset() + (y - files.y0)}, true
	case detail.contains(x, y):
		return mouseHit{pane: focusDetail, row: a.detail.view.Offset() + (y - detail.y0)}, true
	}
	return mouseHit{row: -1}, true
}

// drag holds the state of an active divider drag.
// updateMouse routes one mouse message. It returns (model, cmd, ok):
// ok is false when the message was not a mouse message (the caller
// falls through to other handling).
func (a App) updateMouse(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	if a.cfg.NoMouse {
		return a, nil, true
	}
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		if os.Getenv("SQUINT_DEBUG") != "" {
			println("squint: wheel at", msg.X, msg.Y)
		}
		m, cmd := a.mouseWheel(msg)
		return m, cmd, true
	case tea.MouseClickMsg:
		m, cmd := a.mouseClick(msg)
		return m, cmd, true
	case tea.MouseMotionMsg:
		m, cmd := a.mouseMotion(msg)
		return m, cmd, true
	case tea.MouseReleaseMsg:
		if a.dragging && msg.Button == tea.MouseLeft {
			a.dragging = false
		}
		return a, nil, true
	}
	return a, nil, false
}

// mouseWheel scrolls the pane under the cursor (spec §11: wheel scrolls
// that pane).
func (a App) mouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	hit, ok := a.hitAt(msg.X, msg.Y)
	if !ok {
		return a, nil
	}
	step := mouseWheelStep
	if msg.Button == tea.MouseWheelUp {
		step = -step
	} else if msg.Button != tea.MouseWheelDown {
		return a, nil // horizontal wheel: nothing to scroll
	}
	switch hit.pane {
	case focusCommits:
		if step > 0 {
			a.commitsView.Down(step)
		} else {
			a.commitsView.Up(-step)
		}
	case focusFiles:
		if step > 0 {
			a.filesView.Down(step)
		} else {
			a.filesView.Up(-step)
		}
	default:
		a.detail.scrollBy(step)
	}
	return a, nil
}

// mouseClick handles press events: divider drag start, commit/file
// selection, and double-click-to-detail.
func (a App) mouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return a, nil
	}
	if a.onDivider(msg.X, msg.Y) {
		a.dragging = true
		a.applyDrag(msg.X)
		return a, nil
	}
	hit, ok := a.hitAt(msg.X, msg.Y)
	if !ok {
		return a, nil
	}
	switch hit.pane {
	case focusCommits:
		if hit.row < 0 || hit.row >= len(a.commits) {
			return a, nil
		}
		if a.isDoubleClick(focusCommits, hit.row) {
			// Second click on the same row: focus the detail pane
			// (≡ enter, spec §11).
			a.dragging = false
			a.setFocus(focusDetail)
			return a, nil
		}
		a.setFocus(focusCommits)
		a.selectAt(hit.row)
		return a, a.startFanout()
	case focusFiles:
		if hit.row < 0 || hit.row >= len(a.files) {
			return a, nil
		}
		a.fileCursor = hit.row
		a.rebuildFilesLines()
		a.detail.scrollToFile(a.files[hit.row].Path)
		return a, nil
	default:
		a.setFocus(focusDetail)
		return a, nil
	}
}

// isDoubleClick reports whether this click completes a double-click on
// the same pane and row within the window, recording the click when it
// does not.
func (a *App) isDoubleClick(pane paneFocus, row int) bool {
	now := a.now()
	same := a.lastClick.pane == pane && a.lastClick.row == row &&
		now.Sub(a.lastClick.at) <= doubleClickWindow
	a.lastClick = clickRecord{pane: pane, row: row, at: now}
	return same
}

// clickRecord remembers the previous click for double-click detection.
type clickRecord struct {
	pane paneFocus
	row  int
	at   time.Time
}

// mouseMotion moves the split during an active drag; motion without a
// drag is ignored.
func (a App) mouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	if !a.dragging || msg.Button != tea.MouseLeft {
		return a, nil
	}
	a.applyDrag(msg.X)
	return a, nil
}

// applyDrag sets the split ratio so the divider follows column x,
// clamped to usable bounds, and relayouts. Session-only state: nothing
// persists across runs (spec §7.1). Pointer receiver: the ratio must
// survive into the caller's model.
func (a *App) applyDrag(x int) {
	if a.width <= 0 {
		return
	}
	ratio := clampRatio(float64(x+1) / float64(a.width))
	if ratio == a.splitRatio {
		return
	}
	a.splitRatio = ratio
	a.relayout()
}

// clampRatio bounds a split ratio to the usable window.
func clampRatio(r float64) float64 {
	if r < minSplitRatio {
		return minSplitRatio
	}
	if r > maxSplitRatio {
		return maxSplitRatio
	}
	return r
}

// layoutForRatio derives pane geometry for a terminal size and split
// ratio. It reports false below the minimum size (spec §7.1: 80×24).
func layoutForRatio(width, height int, ratio float64) (paneLayout, bool) {
	if width < minWidth || height < minHeight {
		return paneLayout{}, false
	}
	h := height - statusRows // rows available to the panes
	leftW := int(float64(width) * clampRatio(ratio))
	leftW = min(max(leftW, 20), width-20)
	filesH := clamp(h/3, 4, 10)
	return paneLayout{
		leftW:    leftW,
		rightW:   width - leftW,
		commitsH: h,
		filesH:   filesH,
		detailH:  h - filesH,
	}, true
}

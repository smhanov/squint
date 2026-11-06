package model

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/git/testfixture"
)

// ---- pure geometry ----

func TestClampRatio(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-1, minSplitRatio},
		{0.1, minSplitRatio},
		{0.5, 0.5},
		{0.9, maxSplitRatio},
		{5, maxSplitRatio},
	}
	for _, tc := range cases {
		if got := clampRatio(tc.in); got != tc.want {
			t.Errorf("clampRatio(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLayoutForRatio(t *testing.T) {
	// The default ratio mirrors layoutFor.
	lay, ok := layoutForRatio(120, 40, defaultSplitRatio)
	if !ok || lay.leftW != 60 || lay.rightW != 60 {
		t.Errorf("layoutForRatio(120, 40, 0.5) = %v, %v; want 60/60", lay, ok)
	}
	// A 30% split on 100 columns.
	lay, ok = layoutForRatio(100, 40, 0.3)
	if !ok || lay.leftW != 30 || lay.rightW != 70 {
		t.Errorf("layoutForRatio(100, 40, 0.3) = %+v, %v; want 30/70", lay, ok)
	}
	// Extreme ratios clamp, and the two columns always sum to the width.
	for _, ratio := range []float64{0.0, 0.05, 0.95, 2.0} {
		lay, ok = layoutForRatio(100, 40, ratio)
		if !ok {
			t.Fatalf("layoutForRatio(100, 40, %v) = false", ratio)
		}
		if lay.leftW+lay.rightW != 100 || lay.leftW < 20 || lay.rightW < 20 {
			t.Errorf("ratio %v: %d/%d, want both sides >= 20 summing to 100", ratio, lay.leftW, lay.rightW)
		}
	}
}

// ---- harness-driven routing ----

// click builds a left-button click message at screen coordinates.
func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheelAt(x, y int, up bool) tea.MouseWheelMsg {
	b := tea.MouseWheelDown
	if up {
		b = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: b}
}

// TestMouseClickSelectsCommit: clicking a commit row selects and focuses
// it; clicking outside the content area does nothing (AC1).
func TestMouseClickSelectsCommit(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	// The first content row of the commits pane is screen row 1.
	h.send(click(5, 2)) // second commit row
	if h.app.focus != focusCommits {
		t.Errorf("focus after click = %v, want commits", h.app.focus)
	}
	if h.app.cursor != 1 {
		t.Errorf("cursor after click = %d, want 1", h.app.cursor)
	}
	if h.app.selectedHash != h.app.commits[1].Hash {
		t.Error("click did not move the selection to the clicked commit")
	}
	if h.app.filesStale() || h.app.detailStale() {
		t.Error("click did not start the file/diff loads")
	}

	// A click on the border column selects nothing.
	h.send(click(0, 2))
	if h.app.cursor != 1 {
		t.Errorf("border click moved the cursor to %d", h.app.cursor)
	}
	// A click past the last row does nothing.
	h.send(click(5, 30))
	if h.app.cursor != 1 {
		t.Errorf("out-of-range click moved the cursor to %d", h.app.cursor)
	}
}

// TestMouseDoubleClickFocusesDetail: two clicks on the same row inside
// the window act like enter (spec §11).
func TestMouseDoubleClickFocusesDetail(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.send(click(5, 1)) // select row 0
	if h.app.focus != focusCommits {
		t.Fatalf("focus after first click = %v, want commits", h.app.focus)
	}
	// The pinned clock does not advance, so the next click is within the
	// double-click window by construction.
	h.send(click(5, 1))
	if h.app.focus != focusDetail {
		t.Errorf("focus after double click = %v, want detail", h.app.focus)
	}

	// A click on a different row resets the sequence, so a following
	// click on yet another row is not a double-click.
	h.press("1") // back to commits
	h.send(click(5, 1))
	h.send(click(5, 2))
	if h.app.focus != focusCommits {
		t.Errorf("focus after row change = %v, want commits", h.app.focus)
	}

	// The same row after the window closed (fresh harness with an
	// advancing clock) is not a double-click either.
	h2 := newHarness(t, testfixture.Basic)
	steps := 0
	h2.app.nowFn = func() time.Time { steps++; return pinnedNow.Add(time.Duration(steps) * time.Second) }
	h2.send(click(5, 1))
	h2.send(click(5, 1))
	if h2.app.focus != focusCommits {
		t.Errorf("slow double click changed focus to %v", h2.app.focus)
	}
}

// TestMouseClickSelectsFileAndScrollsDetail: clicking a file row moves
// the file cursor and jumps the detail pane to that file's hunk (AC1).
func TestMouseClickSelectsFileAndScrollsDetail(t *testing.T) {
	h := newHarness(t, testfixture.ManyFiles)
	h.press("1")
	h.press("j") // the 60-file commit
	h.press("3")
	h.press("1") // back to commits focus; detail stays on the commit

	// Click the 5th file row. The files pane content starts at screen
	// row 1, x inside the right column (leftW = 60 at 120 wide).
	h.send(click(80, 5))
	if h.app.fileCursor != 4 {
		t.Fatalf("fileCursor after click = %d, want 4", h.app.fileCursor)
	}
	want := h.app.files[4].Path
	span, ok := h.spanFor(want)
	if !ok {
		t.Fatalf("no span for %q", want)
	}
	if h.app.detail.row != span.start {
		t.Errorf("detail row = %d, want the clicked file's span start %d", h.app.detail.row, span.start)
	}
	// Focus stays where it was: clicking a file only selects it.
	if h.app.focus != focusCommits {
		t.Errorf("focus after file click = %v, want unchanged (commits)", h.app.focus)
	}
}

// TestMouseWheelScrollsPaneUnderCursor: the wheel scrolls the pane the
// cursor is over, not the focused pane (spec §11).
func TestMouseWheelScrollsPaneUnderCursor(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("1") // focus commits

	// The basic fixture's five commits fit on screen; inject a list tall
	// enough that the commits viewport can actually scroll.
	var tall []git.Commit
	for i := range 50 {
		tall = append(tall, git.Commit{Hash: fmt.Sprintf("hash%02d", i), Abbrev: fmt.Sprintf("hash%02d", i),
			Author: "x", At: pinnedNow.Add(-time.Duration(i) * time.Minute), Subject: fmt.Sprintf("commit %d", i)})
	}
	h.send(commitsLoadedMsg{commits: tall, gen: h.app.globalSeq})

	// Wheel over the commits pane: the commits viewport moves.
	before := h.app.commitsView.Offset()
	h.send(wheelAt(10, 10, false))
	if got := h.app.commitsView.Offset(); got <= before {
		t.Errorf("wheel down over commits: offset %d → %d, want scroll down", before, got)
	}
	h.send(wheelAt(10, 10, true))
	if got := h.app.commitsView.Offset(); got != before {
		t.Errorf("wheel up over commits: offset %d, want back to %d", got, before)
	}

	// Wheel over the detail pane scrolls the detail, not commits. The
	// selected commit is the first fixture commit's stand-in with no
	// diff loaded — the many-files content below exercises real scroll;
	// here a synthetic detail list is too invasive, so assert on the
	// commits pane staying put while the detail row re-syncs.
	detailBefore := h.app.detail.view.Offset()
	h.send(wheelAt(80, 30, false))
	if got := h.app.commitsView.Offset(); got != before {
		t.Error("wheel over detail moved the commits viewport")
	}
	if h.app.detail.view.Offset() < detailBefore {
		t.Error("wheel down over detail scrolled up")
	}
	if h.app.detail.row != h.app.detail.view.Offset() {
		t.Error("wheel scroll left detail.row out of sync")
	}

	// A real long diff: the 60-file commit gives the detail pane hundreds
	// of rows, so the wheel scrolls it for real.
	h2 := newHarness(t, testfixture.ManyFiles)
	h2.press("1")
	h2.press("j") // select the 60-file commit
	d0 := h2.app.detail.view.Offset()
	h2.send(wheelAt(80, 30, false))
	if got := h2.app.detail.view.Offset(); got <= d0 {
		t.Errorf("wheel down over a long diff: offset %d → %d, want scroll down", d0, got)
	}
	if h2.app.detail.row != h2.app.detail.view.Offset() {
		t.Error("detail.row out of sync after wheel scroll")
	}
}

// TestMouseDividerDragResizes: press on the divider, drag, release —
// the split follows and the ratio stays in bounds (AC1).
func TestMouseDividerDragResizes(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	if h.app.lay.leftW != 60 {
		t.Fatalf("initial leftW = %d, want 60", h.app.lay.leftW)
	}

	// Press on the divider columns (59 or 60 at a 120-col terminal).
	h.send(click(59, 10))
	if !h.app.dragging {
		t.Fatal("press on the divider did not start a drag")
	}
	h.send(tea.MouseMotionMsg{X: 30, Y: 10, Button: tea.MouseLeft})
	// The divider follows the cursor: ratio (x+1)/width, here 31/120.
	if h.app.lay.leftW != 31 {
		t.Errorf("leftW after drag to x=30 = %d, want 31", h.app.lay.leftW)
	}
	h.send(tea.MouseMotionMsg{X: 100, Y: 10, Button: tea.MouseLeft})
	// 101/120 exceeds the max ratio and clamps to 80% of 120 = 96.
	if h.app.lay.leftW != 96 {
		t.Errorf("leftW after drag to x=100 = %d, want clamped 96", h.app.lay.leftW)
	}
	h.send(tea.MouseReleaseMsg{X: 100, Y: 10, Button: tea.MouseLeft})
	if h.app.dragging {
		t.Error("release did not end the drag")
	}

	// Motion without a drag does nothing.
	h.send(tea.MouseMotionMsg{X: 30, Y: 10, Button: tea.MouseLeft})
	if h.app.lay.leftW != 96 {
		t.Error("motion after release moved the divider")
	}

	// The ratio survives a resize (session state).
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := h.app.lay.leftW; got != 80 {
		t.Errorf("leftW after resize = %d, want the ratio kept (80)", got)
	}
}

// TestNoMouseDisablesEverything: with --no-mouse, no mouse message
// changes any state (AC1).
func TestNoMouseDisablesEverything(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	app := New(Config{RepoName: "demo", HasCommits: true, Repo: repo, NoMouse: true, NoAnimations: true,
		Now: func() time.Time { return pinnedNow }})
	app = update(t, app, tea.WindowSizeMsg{Width: 120, Height: 40})
	h := &harness{t: t, repo: repo, app: app}
	h.exec(h.app.Init())

	before := h.app.cursor
	beforeLay := h.app.lay.leftW
	h.send(click(5, 2))
	h.send(wheelAt(5, 5, false))
	h.send(click(59, 10))
	h.send(tea.MouseMotionMsg{X: 20, Y: 10, Button: tea.MouseLeft})
	if h.app.cursor != before {
		t.Error("click moved the selection with --no-mouse")
	}
	if h.app.lay.leftW != beforeLay {
		t.Error("drag resized with --no-mouse")
	}
	if v := h.app.View(); v.MouseMode != tea.MouseModeNone {
		t.Error("--no-mouse still enables the terminal mouse mode")
	}
}

// TestHitGeometry: paneRects maps the layout to screen rectangles,
// including the zoomed forms.
func TestHitGeometry(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	commits, files, detail, ok := h.app.paneRects()
	if !ok {
		t.Fatal("paneRects not ok on a laid-out app")
	}
	if commits != (rect{x0: 1, y0: 1, x1: 58, y1: 36}) {
		t.Errorf("commits rect = %+v", commits)
	}
	if files != (rect{x0: 61, y0: 1, x1: 118, y1: 8}) {
		t.Errorf("files rect = %+v", files)
	}
	if detail != (rect{x0: 61, y0: 11, x1: 118, y1: 36}) {
		t.Errorf("detail rect = %+v", detail)
	}
	if !h.app.onDivider(59, 10) || !h.app.onDivider(60, 10) {
		t.Error("divider columns 59/60 not detected")
	}
	if h.app.onDivider(30, 10) || h.app.onDivider(59, 38) {
		t.Error("onDivider matched a non-divider position")
	}

	// Zoom: only the focused pane has a rectangle.
	h.press("z")
	c, f, d, ok := h.app.paneRects()
	if !ok || c.empty() || !f.empty() || !d.empty() {
		t.Errorf("zoomed commits rects = %+v/%+v/%+v", c, f, d)
	}
	if got := c.x1; got != 118 {
		t.Errorf("zoomed commits x1 = %d, want 118", got)
	}
	h.press("2") // focus moves: the zoomed pane follows
	c, f, d, _ = h.app.paneRects()
	if f.empty() || !c.empty() || !d.empty() {
		t.Errorf("zoomed files rects = %+v/%+v/%+v", c, f, d)
	}
	if f != (rect{x0: 1, y0: 1, x1: 118, y1: 36}) {
		t.Errorf("zoomed files rect = %+v, want the full canvas", f)
	}
}

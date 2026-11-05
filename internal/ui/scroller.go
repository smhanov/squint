package ui

import (
	viewport "charm.land/bubbles/v2/viewport"
)

// Scroller is a thin wrapper around the bubbles v2 viewport, confining the
// third-party scrolling API to one place so upstream changes stay local.
// It exposes exactly the scrolling vocabulary squint uses: line/page
// movement, ensure-visible for cursor lists, and raw offsets for
// scroll-to-row jumps.
type Scroller struct {
	vp viewport.Model
	h  int
}

// NewScroller returns a scroller with mouse wheel support disabled (squint
// routes every input itself).
func NewScroller() Scroller {
	vp := viewport.New()
	vp.MouseWheelEnabled = false
	vp.FillHeight = true
	return Scroller{vp: vp}
}

// SetWidth sets the visible width in cells.
func (s *Scroller) SetWidth(w int) { s.vp.SetWidth(w) }

// SetHeight sets the visible height in rows.
func (s *Scroller) SetHeight(h int) { s.h = h; s.vp.SetHeight(h) }

// Height returns the visible height in rows.
func (s *Scroller) Height() int { return s.h }

// SetLines replaces the scrollable content.
func (s *Scroller) SetLines(lines []string) { s.vp.SetContentLines(lines) }

// View renders the visible window of content, padded to the full size.
func (s *Scroller) View() string { return s.vp.View() }

// Up scrolls n lines up.
func (s *Scroller) Up(n int) { s.vp.ScrollUp(n) }

// Down scrolls n lines down.
func (s *Scroller) Down(n int) { s.vp.ScrollDown(n) }

// HalfUp scrolls half a page up.
func (s *Scroller) HalfUp() { s.vp.HalfPageUp() }

// HalfDown scrolls half a page down.
func (s *Scroller) HalfDown() { s.vp.HalfPageDown() }

// PageUp scrolls a full page up.
func (s *Scroller) PageUp() { s.vp.PageUp() }

// PageDown scrolls a full page down.
func (s *Scroller) PageDown() { s.vp.PageDown() }

// Top jumps to the first row.
func (s *Scroller) Top() { s.vp.SetYOffset(0) }

// Bottom jumps to the last row.
func (s *Scroller) Bottom() { s.vp.GotoBottom() }

// Offset returns the first visible content row.
func (s *Scroller) Offset() int { return s.vp.YOffset() }

// SetOffset scrolls so that row is the first visible row (clamped).
func (s *Scroller) SetOffset(row int) { s.vp.SetYOffset(row) }

// EnsureVisible scrolls the minimum amount so that row is on screen.
func (s *Scroller) EnsureVisible(row int) { s.vp.EnsureVisible(row, 0, 0) }

// ContentRows returns the total number of content rows (not just the
// visible ones).
func (s *Scroller) ContentRows() int { return s.vp.TotalLineCount() }

// Percent reports the vertical scroll position as 0–1.
func (s *Scroller) Percent() float64 { return s.vp.ScrollPercent() }

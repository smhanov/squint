package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Pane geometry: exact outer size, title in the top border, content
// clipped to the inner box.
func TestPaneRender(t *testing.T) {
	p := Pane{Title: "commits (5)", Focused: true}
	box := p.Render(strings.Repeat("x", 100)+"\nshort", 60, 7)
	lines := strings.Split(box, "\n")
	if len(lines) != 7 {
		t.Fatalf("pane rows = %d, want 7", len(lines))
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != 60 {
			t.Errorf("row %d width = %d, want 60", i, w)
		}
	}
	if !strings.Contains(lines[0], "commits (5)") {
		t.Errorf("top border %q missing the title", lines[0])
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
		t.Errorf("top border %q missing rounded corners", lines[0])
	}
	if !strings.Contains(lines[6], "╰") || !strings.Contains(lines[6], "╯") {
		t.Errorf("bottom border %q missing rounded corners", lines[6])
	}
	// Long content lines are clipped, not wrapped: the first content row
	// holds the x-run, the second holds "short".
	if strings.Count(lines[1], "x") < 50 {
		t.Errorf("first content row %q does not hold the clipped x-run", lines[1])
	}
	if !strings.Contains(lines[2], "short") {
		t.Errorf("second content row %q missing the short line", lines[2])
	}
}

// ASCII panes use the ASCII border set.
func TestPaneASCII(t *testing.T) {
	p := Pane{Title: "t", ASCII: true}
	lines := strings.Split(p.Render("", 20, 5), "\n")
	if !strings.Contains(lines[0], "+") || !strings.Contains(lines[len(lines)-1], "+") {
		t.Errorf("ASCII borders wrong: %q / %q", lines[0], lines[len(lines)-1])
	}
	if strings.Contains(lines[0], "╭") {
		t.Error("ASCII pane still draws rounded corners")
	}
}

// Scroller: scrolling, ensure-visible, and clamping.
func TestScroller(t *testing.T) {
	s := NewScroller()
	s.SetWidth(20)
	s.SetHeight(4)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("a", 25) // wider than the viewport: clipped
	}
	s.SetLines(lines)

	if got := ansi.StringWidth(strings.Split(s.View(), "\n")[0]); got != 20 {
		t.Errorf("viewport row width = %d, want 20", got)
	}
	if rows := s.ContentRows(); rows != 20 {
		t.Errorf("ContentRows = %d, want 20", rows)
	}
	s.EnsureVisible(10)
	if off := s.Offset(); off < 7 || off > 10 {
		t.Errorf("offset after EnsureVisible(10) = %d, want within one page above", off)
	}
	s.Top()
	if off := s.Offset(); off != 0 {
		t.Errorf("offset after Top = %d, want 0", off)
	}
	s.Down(100) // clamps at the bottom
	if maxOff := 20 - 4; s.Offset() != maxOff {
		t.Errorf("offset after Down(100) = %d, want %d", s.Offset(), maxOff)
	}
	if pct := s.Percent(); pct != 1 {
		t.Errorf("Percent at bottom = %v, want 1", pct)
	}
}

// Status bar row 1: identity, branch, counts, dirty marker; the spinner
// rides right-aligned; width is padded exactly.
func TestStatusBar(t *testing.T) {
	info := StatusInfo{Repo: "demo", Branch: "main", NCommits: 50, Ahead: 2, Behind: 1}
	bar := StatusBar(info, 80)
	if !strings.Contains(bar, "squint · demo · main") {
		t.Errorf("bar %q missing identity and branch", bar)
	}
	if !strings.Contains(bar, "↑2") || !strings.Contains(bar, "↓1") {
		t.Errorf("bar %q missing ahead/behind", bar)
	}
	if !strings.Contains(bar, "50 commits") {
		t.Errorf("bar %q missing the commit count", bar)
	}
	if strings.Contains(bar, "dirty") {
		t.Errorf("bar %q shows dirty for a clean tree", bar)
	}
	if w := lipgloss.Width(bar); w != 80 {
		t.Errorf("bar width = %d, want padded to 80", w)
	}

	info.Dirty = true
	info.Loading = true
	info.Spinner = "⠋"
	bar = StatusBar(info, 80)
	if !strings.Contains(bar, "dirty") {
		t.Errorf("bar %q missing the dirty marker", bar)
	}
	if !strings.HasSuffix(bar, "⠋") {
		t.Errorf("bar %q does not end with the spinner", bar)
	}

	info = StatusInfo{Repo: "demo", Detached: true, DetachedAbbr: "a1b2c3d", NCommits: 1}
	if bar := StatusBar(info, 80); !strings.Contains(bar, "HEAD detached at a1b2c3d") {
		t.Errorf("bar %q missing the detached notice", bar)
	}

	info = StatusInfo{Repo: "demo", Branch: "main", Shallow: true, NCommits: 2}
	if bar := StatusBar(info, 80); !strings.Contains(bar, "shallow") {
		t.Errorf("bar %q missing the shallow note", bar)
	}
}

// Hints bar: hints left, percentage right, exact width.
func TestHintsBar(t *testing.T) {
	bar := HintsBar("j/k move", 41, 60)
	if !strings.Contains(bar, "j/k move") || !strings.Contains(bar, " 41%") {
		t.Errorf("bar %q missing hints or percentage", bar)
	}
	if w := lipgloss.Width(bar); w != 60 {
		t.Errorf("bar width = %d, want 60", w)
	}
	if bar := HintsBar(strings.Repeat("hint ", 30), 7, 30); lipgloss.Width(bar) != 30 {
		t.Errorf("overlong hints bar width = %d, want clipped to 30", lipgloss.Width(bar))
	}
}

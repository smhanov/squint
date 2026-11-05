package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Pane is a bordered, titled box: the frame every squint pane renders
// into. The focused pane gets the bright border color; the title sits in
// the top border row, gitk-style ("┌─ commits (5) ────").
type Pane struct {
	// Title is embedded in the top border; empty renders a plain border.
	Title string
	// Focused selects the bright border color.
	Focused bool
	// ASCII switches to the ASCII border set (--ascii rendering).
	ASCII bool
}

// asciiBorder is the --ascii fallback border set (lipgloss's own
// NormalBorder still draws Unicode box characters).
var asciiBorder = lipgloss.Border{
	Top: "-", Bottom: "-", Left: "|", Right: "|",
	TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
	MiddleLeft: "+", MiddleRight: "+", Middle: "+", MiddleTop: "+", MiddleBottom: "+",
}

// Render draws the pane with an outer size of width × height. Content is
// expected to already fit (width-2) × (height-2); it is clipped and
// padded to the inner box as a safety net. Note lipgloss v2 treats
// Width/Height as the size of the bordered block, borders included, so
// the body style gets the full outer size and the content gets inner.
func (p Pane) Render(content string, width, height int) string {
	if width < 2 || height < 2 {
		return ""
	}
	border := lipgloss.RoundedBorder()
	if p.ASCII {
		border = asciiBorder
	}
	color := ColorIdle
	if p.Focused {
		color = ColorFocus
	}

	inner := width - 2
	top := p.topBorder(border, inner)
	body := lipgloss.NewStyle().
		Border(border, false, true, true, true).
		BorderForeground(color).
		Width(width).
		Height(height - 1).
		Render(clipLines(content, inner))
	return lipgloss.NewStyle().Foreground(color).Render(top) + "\n" + body
}

// topBorder builds the top border row with the title embedded after the
// corner, the way gitk labels its panes.
func (p Pane) topBorder(border lipgloss.Border, inner int) string {
	if p.Title == "" {
		return border.TopLeft + strings.Repeat(border.Top, inner) + border.TopRight
	}
	title := p.Title
	room := inner - lipgloss.Width(title) - 3 // corner-side rule + spaces
	if room < 1 {
		title = ansi.Truncate(title, max(0, inner-4), "…")
		room = inner - lipgloss.Width(title) - 3
	}
	return border.TopLeft + border.Top + " " + title + " " +
		strings.Repeat(border.Top, max(0, room)) + border.TopRight
}

// clipLines clips every line of content to width cells and pads the block
// out to exactly n lines of at most width, so a pane's body always fills
// its frame. ANSI sequences and wide runes are width-safe.
func clipLines(content string, width int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = ansi.Truncate(line, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

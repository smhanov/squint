package model

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/squint-tui/squint/internal/ui"
)

// help.go is the ? overlay: the global keys plus the focused pane's own
// keymap (FR-14, spec §7.4).

// helpOverlay renders the key table centered over the full canvas. The
// overlay replaces the frame while open, keeping the terminal size stable.
func (a App) helpOverlay() string {
	type section struct {
		title string
		rows  []keyRow
	}
	secs := []section{
		{"global", globalKeyRows()},
		{a.focus.String(), paneKeyRows(a.focus)},
	}

	keyW := 0
	for _, sec := range secs {
		for _, r := range sec.rows {
			keyW = max(keyW, len(r.Keys))
		}
	}

	var lines []string
	for _, sec := range secs {
		lines = append(lines, ui.StyleBold.Render(sec.title))
		for _, r := range sec.rows {
			if r.Keys == "" && r.Desc == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, padTo(r.Keys, keyW)+"  "+r.Desc)
		}
		lines = append(lines, "")
	}
	lines = lines[:len(lines)-1] // drop the trailing blank row

	body := strings.Join(lines, "\n")
	boxW := keyW + 4 + 4 // keys column, gutter, description headroom, borders
	for _, line := range lines {
		boxW = max(boxW, lipgloss.Width(line)+4)
	}
	boxH := len(lines) + 2
	box := ui.Pane{
		Title: "squint keys · esc closes",
		ASCII: a.cfg.ASCII,
	}.Render(body, boxW, boxH)
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, box)
}

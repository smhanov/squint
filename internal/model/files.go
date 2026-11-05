package model

import (
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/ui"
)

// files.go renders the changed-file pane: one row per file with its
// name-status letter colored by kind (spec §7.5: A green, M yellow, D red,
// R/C blue).

// statusStyle maps a git name-status letter to its color.
func statusStyle(s byte) lipgloss.Style {
	switch s {
	case 'A':
		return ui.StyleAdd
	case 'M', 'T', 'B':
		return ui.StyleMod
	case 'D', 'U':
		return ui.StyleDel
	case 'R', 'C':
		return ui.StyleRename
	default:
		return lipgloss.NewStyle()
	}
}

// fileRow renders one row of the file pane. Renames and copies show
// "old → new" (spec §13); ASCII mode renders the arrow as ->.
func fileRow(e git.FileEntry, selected bool, width int, ascii bool) string {
	arrow := " → "
	if ascii {
		arrow = " -> "
	}
	path := e.Path
	if e.Orig != "" {
		path = e.Orig + arrow + e.Path
	}
	if room := width - 2; ansi.StringWidth(path) > room {
		path = ansi.Truncate(path, max(1, room), "…")
	}
	row := statusStyle(e.Status).Render(string(e.Status)) + " " + path
	if selected {
		row = ui.StyleReverse.Render(padTo(row, width))
	}
	return row
}

// rebuildFilesLines renders every row and pins the cursor on screen.
func (a *App) rebuildFilesLines() {
	if !a.laidOut() {
		return
	}
	if a.filesStale() && len(a.files) == 0 {
		a.filesView.SetLines([]string{
			ui.StyleFaint.Render(a.spinner.View() + " loading files…"),
		})
		return
	}
	if a.filesErr != "" && len(a.files) == 0 {
		a.filesView.SetLines([]string{ui.StyleDel.Render("git error: " + a.filesErr)})
		return
	}
	width := a.lay.rightW - 2
	lines := make([]string, 0, len(a.files))
	for i := range a.files {
		lines = append(lines, fileRow(a.files[i], i == a.fileCursor, width, a.cfg.ASCII))
	}
	a.filesView.SetLines(lines)
	if len(lines) > 0 {
		a.filesView.EnsureVisible(a.fileCursor)
	}
}

// filesKey handles the files pane keys (spec §10.3).
func (a App) filesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "j", "down", "]":
		a.moveFile(1)
	case "k", "up", "[":
		a.moveFile(-1)
	case "enter":
		if a.fileCursor < len(a.files) {
			// Jump the detail pane to this file's first hunk and move
			// focus with it (spec §10.3: focus diff at file, FR-4).
			a.detail.scrollToFile(a.files[a.fileCursor].Path)
			a.focus = focusDetail
		}
	}
	return a, nil
}

// moveFile moves the file cursor by delta rows.
func (a *App) moveFile(delta int) {
	if len(a.files) == 0 {
		return
	}
	a.fileCursor = clamp(a.fileCursor+delta, 0, len(a.files)-1)
	a.rebuildFilesLines()
}

package model

import (
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/ui"
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
// "old → new" (spec §13); ASCII mode renders the arrow as ->. Submodule
// entries show "⋗ path @sha" ("> path @sha" in ASCII mode).
func fileRow(e git.FileEntry, selected bool, width int, ascii bool) string {
	arrow := " → "
	if ascii {
		arrow = " -> "
	}
	path := e.Path
	if e.Orig != "" {
		path = e.Orig + arrow + e.Path
	}
	if e.Submodule != "" {
		glyph := "⋗"
		if ascii {
			glyph = ">"
		}
		path = glyph + " " + path + " @" + abbrev(e.Submodule)
	}
	if room := width - 2; ansi.StringWidth(path) > room {
		path = ansi.Truncate(path, max(1, room), "…")
	}
	letter := string(e.Status)
	if selected {
		// As in commitRow: the letter and the text each carry the
		// selection attributes so the highlight spans the whole row and
		// the letter keeps its color inside it.
		plain := " " + path
		if lipgloss.Width(letter)+lipgloss.Width(plain) < width {
			plain = padTo(plain, width-lipgloss.Width(letter))
		}
		return segStyle(statusStyle(e.Status), true, false).Render(letter) +
			ui.StyleReverse.Render(plain)
	}
	return statusStyle(e.Status).Render(letter) + " " + path
}

// abbrev shortens a full object hash to the conventional 7 characters.
func abbrev(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
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
		// The error itself lives in the modal overlay (spec §5).
		a.filesView.SetLines([]string{ui.StyleFaint.Render("unavailable — press r to retry")})
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
			a.setFocus(focusDetail)
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

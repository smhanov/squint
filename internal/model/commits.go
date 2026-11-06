package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/ui"
)

// commits.go renders the commit list pane: a single vertical rail with a
// node glyph per commit, the abbreviated hash, a relative age, and the
// ellipsized subject (spec §7.1 left pane, FR-1).

// abbrevLen is the width reserved for the abbreviated commit hash.
const abbrevLen = 7

// relAge formats a commit timestamp relative to now, compact enough for a
// list column: now, Nm, Nh, Nd, Nw, Ny.
func relAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// commitRow renders one row of the commit list. Merge commits show ◆ and
// a (merge) marker; the HEAD row is bold with a (HEAD) marker; the
// selected row is reverse video (spec §7.5).
func (a App) commitRow(c *git.Commit, selected bool, width int) string {
	glyph, gStyle := "●", ui.StyleDot
	marker := ""
	if c.Merge() {
		glyph, gStyle = "◆", ui.StyleMerge
		marker += " (merge)"
	}
	if a.cfg.ASCII {
		glyph = "*"
		if c.Merge() {
			glyph = "+"
		}
	}
	if c.IsHead {
		marker += " (HEAD)"
	}

	age := relAge(c.At, a.now())
	fixed := 1 + 1 + len(c.Abbrev) + 1 + 3 + 1 // glyph, hash, age columns
	subjW := width - fixed - lipgloss.Width(marker)
	if subjW < 0 {
		marker = ""
		subjW = width - fixed
	}
	subject := ansi.Truncate(c.Subject, max(0, subjW), "…")

	row := gStyle.Render(glyph) + " " + c.Abbrev +
		" " + padTo(age, 3) + " " + subject + marker
	if lipgloss.Width(row) < width {
		row = padTo(row, width)
	}
	switch {
	case selected && c.IsHead:
		row = ui.StyleReverse.Render(ui.StyleBold.Render(row))
	case selected:
		row = ui.StyleReverse.Render(row)
	case c.IsHead:
		row = ui.StyleBold.Render(row)
	}
	return row
}

// rebuildCommitsLines renders every row and pins the cursor on screen.
func (a *App) rebuildCommitsLines() {
	if !a.laidOut() {
		return
	}
	if a.commitsLoading && len(a.commits) == 0 {
		// First load: one spinner row instead of an empty pane.
		a.commitsView.SetLines([]string{
			ui.StyleFaint.Render(a.spinner.View() + " loading commits…"),
		})
		return
	}
	if a.commitsErr != "" {
		a.commitsView.SetLines([]string{ui.StyleDel.Render("git error: " + a.commitsErr)})
		return
	}
	width := a.lay.leftW - 2
	lines := make([]string, 0, len(a.commits))
	for i := range a.commits {
		lines = append(lines, a.commitRow(&a.commits[i], i == a.cursor, width))
	}
	a.commitsView.SetLines(lines)
	if len(lines) > 0 {
		a.commitsView.EnsureVisible(a.cursor)
	}
}

// commitsKey handles the commits pane keys (spec §10.2).
func (a App) commitsKey(key string) (tea.Model, tea.Cmd) {
	v := max(a.commitsView.Height(), 1)
	switch key {
	case "j", "down":
		a.selectAt(a.cursor + 1)
	case "k", "up":
		a.selectAt(a.cursor - 1)
	case "ctrl+d":
		a.selectAt(a.cursor + v/2)
	case "ctrl+u":
		a.selectAt(a.cursor - v/2)
	case "ctrl+f", "pgdn":
		a.selectAt(a.cursor + v)
	case "ctrl+b", "pgup":
		a.selectAt(a.cursor - v)
	case "g":
		if a.pendingG {
			a.selectAt(0)
		} else {
			a.pendingG = true
			return a, nil
		}
	case "G":
		a.selectAt(len(a.commits) - 1)
	case "enter":
		a.setFocus(focusDetail)
		return a, nil
	case "d":
		// Since-diff (FR-10): everything from this commit's parent to
		// HEAD, one keypress.
		return a.toggleSince()
	case "p":
		// First-parent toggle for merge commits (FR-9).
		return a.toggleFirstParent()
	default:
		a.pendingG = false
		return a, nil
	}
	return a, a.startFanout()
}

// toggleSince switches the detail pane between the selected commit's own
// diff and the cumulative base..HEAD range. The root commit has no
// parent to diff against, so it explains itself in the status bar.
func (a *App) toggleSince() (tea.Model, tea.Cmd) {
	if a.repo == nil || a.selectedHash == "" || a.cursor >= len(a.commits) {
		return *a, nil
	}
	commit := a.commits[a.cursor]
	if len(commit.Parents) == 0 {
		a.hint = "no parent: this is the root commit, there is nothing before it to diff"
		return *a, nil
	}
	a.detail.since.on = !a.detail.since.on
	a.detail.scrollX = 0
	if !a.detail.since.on {
		a.rebuildDetailLines()
		return *a, nil
	}
	a.detail.view.Top()
	a.detail.row = 0
	a.gen++ // kill any in-flight load for the previous view
	a.detail.dirtyOpts = true
	a.rebuildDetailLines()
	return *a, tea.Batch(a.loadSinceCmd(commit), a.spinKick())
}

// toggleFirstParent flips a merge commit between the combined diff and
// the first-parent diff, re-fetching files and diff (FR-9). Non-merges
// have nothing to toggle.
func (a *App) toggleFirstParent() (tea.Model, tea.Cmd) {
	if a.repo == nil || a.selectedHash == "" || a.cursor >= len(a.commits) {
		return *a, nil
	}
	if !a.commits[a.cursor].Merge() || a.detail.since.on {
		return *a, nil
	}
	a.firstParent = !a.firstParent
	a.gen++
	a.detail.dirtyOpts = true
	a.filesHash = "" // the file list changes with the merge mode
	a.rebuildFilesLines()
	a.rebuildDetailLines()
	return *a, tea.Batch(a.loadFilesCmd(a.commits[a.cursor]), a.loadDiffCmd(a.commits[a.cursor]), a.spinKick())
}

// padTo pads a (possibly styled) string with spaces to width cells.
func padTo(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// StatusInfo is everything the status bar renders: repository identity,
// HEAD state, and load progress.
type StatusInfo struct {
	Repo         string
	Branch       string // empty when Detached
	Detached     bool
	DetachedAbbr string // abbreviated hash shown when detached
	Ahead        int
	Behind       int
	Dirty        bool
	Shallow      bool
	NCommits     int
	Loading      bool
	Spinner      string // pre-rendered spinner frame; empty when idle
	ASCII        bool
}

// StatusBar renders status bar row 1: squint · repo · branch · ahead/behind
// · dirty · commit count, with the spinner right-aligned while loading.
func StatusBar(info StatusInfo, width int) string {
	parts := []string{"squint"}
	if info.Repo != "" {
		parts = append(parts, info.Repo)
	}
	switch {
	case info.Detached:
		abbr := info.DetachedAbbr
		if abbr == "" {
			abbr = "unknown"
		}
		parts = append(parts, "HEAD detached at "+abbr)
	case info.Branch != "":
		parts = append(parts, info.Branch)
	}
	if info.Ahead > 0 {
		parts = append(parts, StyleAhead.Render(fmt.Sprintf("↑%d", info.Ahead)))
	}
	if info.Behind > 0 {
		parts = append(parts, StyleBehind.Render(fmt.Sprintf("↓%d", info.Behind)))
	}
	if info.Dirty {
		dot := StyleDirty.Render("●")
		if info.ASCII {
			dot = StyleDirty.Render("!")
		}
		parts = append(parts, dot+" dirty")
	}
	parts = append(parts, fmt.Sprintf("%d commits", info.NCommits))
	if info.Shallow {
		parts = append(parts, StyleFaint.Render("shallow"))
	}

	left := strings.Join(parts, " · ")
	// Right-align the spinner, keeping the left side within width.
	right := ""
	if info.Loading && info.Spinner != "" {
		right = " " + info.Spinner
	}
	avail := width - ansi.StringWidth(right)
	if ansi.StringWidth(left) > avail {
		left = ansi.Truncate(left, max(0, avail), "…")
	} else if pad := avail - ansi.StringWidth(left); pad > 0 {
		left += strings.Repeat(" ", pad)
	}
	return left + right
}

// HintsBar renders status bar row 2: the focused pane's key hints on the
// left and the focused pane's scroll percentage on the right.
func HintsBar(hints string, pct int, width int) string {
	right := fmt.Sprintf("%3d%%", pct)
	avail := width - len(right)
	if len(hints) > avail {
		hints = ansi.Truncate(hints, max(0, avail), "…")
	} else if pad := avail - len(hints); pad > 0 {
		hints += strings.Repeat(" ", pad)
	}
	return StyleFaint.Render(hints) + right
}

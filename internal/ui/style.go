package ui

import "charm.land/lipgloss/v2"

// The fixed dark palette (spec §7.5). Colors are ANSI-16 so every profile
// (truecolor → 256 → 16 → mono) renders them; lipgloss and the terminal
// writer handle degradation, including NO_COLOR.
var (
	// ColorFocus is the border color of the focused pane.
	ColorFocus = lipgloss.Color("6")
	// ColorIdle is the border color of unfocused panes.
	ColorIdle = lipgloss.Color("8")
	// ColorAdd/ColorDel/ColorMod/ColorRename carry file-status and diff
	// line semantics: A green, M yellow, D red, R/C blue.
	ColorAdd    = lipgloss.Color("2")
	ColorDel    = lipgloss.Color("1")
	ColorMod    = lipgloss.Color("3")
	ColorRename = lipgloss.Color("4")
	ColorDot    = lipgloss.Color("4")
	ColorMerge  = lipgloss.Color("5")
	ColorDirty  = lipgloss.Color("1")
	ColorAhead  = lipgloss.Color("2")
	ColorBehind = lipgloss.Color("3")
)

// Shared styles, constructed once per process — never per row.
var (
	StyleAdd      = lipgloss.NewStyle().Foreground(ColorAdd)
	StyleDel      = lipgloss.NewStyle().Foreground(ColorDel)
	StyleMod      = lipgloss.NewStyle().Foreground(ColorMod)
	StyleRename   = lipgloss.NewStyle().Foreground(ColorRename)
	StyleDot      = lipgloss.NewStyle().Foreground(ColorDot)
	StyleMerge    = lipgloss.NewStyle().Foreground(ColorMerge)
	StyleDirty    = lipgloss.NewStyle().Foreground(ColorDirty)
	StyleAhead    = lipgloss.NewStyle().Foreground(ColorAhead)
	StyleBehind   = lipgloss.NewStyle().Foreground(ColorBehind)
	StyleBold     = lipgloss.NewStyle().Bold(true)
	StyleFaint    = lipgloss.NewStyle().Faint(true)
	StyleReverse  = lipgloss.NewStyle().Reverse(true)
	StyleFileRule = lipgloss.NewStyle().Bold(true)
)

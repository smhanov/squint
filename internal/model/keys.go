package model

// keys.go holds the fixed keymap (spec §10). Keybindings are documented,
// not remappable: the tables below are the single source of truth for both
// key handling and the help overlay.

type paneFocus int

const (
	focusCommits paneFocus = iota
	focusFiles
	focusDetail
)

// cycle advances (or rewinds) the focus ring: commits → files → detail.
func (f paneFocus) cycle(back bool) paneFocus {
	if back {
		return (f + 2) % 3
	}
	return (f + 1) % 3
}

func (f paneFocus) String() string {
	switch f {
	case focusFiles:
		return "files"
	case focusDetail:
		return "detail"
	default:
		return "commits"
	}
}

// keyRow is one help-table line: the keys and what they do.
type keyRow struct{ Keys, Desc string }

// globalKeyRows are the keys that work in every pane.
func globalKeyRows() []keyRow {
	return []keyRow{
		{"q, ctrl+c", "quit"},
		{"tab, shift+tab", "cycle panes"},
		{"1 2 3", "focus commits / files / detail"},
		{"z", "zoom the focused pane"},
		{"r", "refresh"},
		{"y, Y", "copy short / full commit hash"},
		{"?", "help overlay"},
		{"", ""},
		{"mouse", "wheel scrolls, click selects, drag the divider resizes"},
	}
}

// paneKeyRows returns the focused pane's own keys (spec §10.2–10.4).
func paneKeyRows(f paneFocus) []keyRow {
	switch f {
	case focusFiles:
		return []keyRow{
			{"j, k", "move selection"},
			{"[, ]", "previous / next file"},
			{"enter", "jump to the file's hunk"},
		}
	case focusDetail:
		return []keyRow{
			{"j, k", "scroll"},
			{"(, )", "previous / next hunk"},
			{"[, ]", "previous / next file"},
			{"h, l", "scroll left / right (wrap off)"},
			{"s", "side-by-side diff"},
			{"i", "intraline word-diff"},
			{"w", "wrap: none → word → character"},
			{"W", "ignore whitespace"},
			{"+, -", "diff context lines"},
		}
	default:
		return []keyRow{
			{"j, k", "move selection"},
			{"ctrl+d, ctrl+u", "half page"},
			{"ctrl+f, ctrl+b", "page"},
			{"g g, G", "top / bottom"},
			{"enter", "focus detail"},
			{"d", "since-diff to HEAD"},
			{"p", "merge first-parent diff"},
		}
	}
}

// hintsFor is the one-line key summary shown in status bar row 2 for the
// focused pane.
func hintsFor(f paneFocus) string {
	switch f {
	case focusFiles:
		return "j/k move · enter open · [/] file · ? help"
	case focusDetail:
		return "j/k scroll · () hunk · [/] file · s side · i words · w wrap · W ws · ? help"
	default:
		return "j/k move · enter detail · d since · r refresh · ? help"
	}
}

// focusDirect maps the 1/2/3 keys to panes; ok is false for other keys.
func focusDirect(key string) (paneFocus, bool) {
	switch key {
	case "1":
		return focusCommits, true
	case "2":
		return focusFiles, true
	case "3":
		return focusDetail, true
	default:
		return 0, false
	}
}

// percentPosition is the scroll percentage of a cursor list.
func percentPosition(cursor, n int) int {
	if n <= 0 {
		return 0
	}
	return (cursor + 1) * 100 / n
}

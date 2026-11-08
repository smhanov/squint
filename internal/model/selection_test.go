package model

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/smhanov/squint/internal/git"
)

// Full-row selection highlight: the selected row's reverse video must
// span the entire row — glyph, hash, age, subject, markers, and the
// trailing padding — in both panes. The assertions parse the rendered
// ANSI and track the attribute state of every visible cell, so a
// highlight that dies at the styled glyph's embedded reset (the old
// wrap-around-the-assembled-row bug) fails loudly.

// cellAttrs is the SGR state active on one visible cell of a row.
type cellAttrs struct {
	bold    bool
	reverse bool
}

// rowCells walks a rendered row, applies its SGR sequences to a running
// attribute state, and reports the state of every visible cell in
// order. Non-SGR escapes in a row are unexpected and fail the test.
func rowCells(t *testing.T, row string) []cellAttrs {
	t.Helper()
	var cells []cellAttrs
	state := cellAttrs{}
	runes := []rune(row)
	for i := 0; i < len(runes); {
		if runes[i] != '\x1b' {
			cells = append(cells, state)
			i++
			continue
		}
		if i+1 >= len(runes) || runes[i+1] != '[' {
			t.Fatalf("row %q: unsupported escape at cell %d", row, len(cells))
		}
		end := i + 2
		for end < len(runes) && runes[end] >= 0x30 && runes[end] <= 0x3f {
			end++
		}
		if end >= len(runes) || runes[end] < 0x40 || runes[end] > 0x7e {
			t.Fatalf("row %q: unterminated escape at cell %d", row, len(cells))
		}
		params := string(runes[i+2 : end])
		if runes[end] != 'm' {
			t.Fatalf("row %q: non-SGR escape \\x1b[%s%s at cell %d", row, params, string(runes[end]), len(cells))
		}
		applySGR(&state, params)
		i = end + 1
	}
	return cells
}

// applySGR folds one SGR sequence's parameters into the cell state.
func applySGR(state *cellAttrs, params string) {
	if params == "" || params == "0" {
		*state = cellAttrs{}
		return
	}
	for _, p := range strings.Split(params, ";") {
		switch p {
		case "1":
			state.bold = true
		case "7":
			state.reverse = true
		case "22":
			state.bold = false
		case "27":
			state.reverse = false
		}
	}
}

// assertFullHighlight checks that every visible cell of the row sits
// inside a reverse-video span (bold too when bold is set) and that the
// row is exactly width cells wide, trailing padding included — the
// whole row is the highlight, with no unstyled gap.
func assertFullHighlight(t *testing.T, name, row string, width int, bold bool) {
	t.Helper()
	cells := rowCells(t, row)
	if len(cells) == 0 {
		t.Fatalf("%s: row %q has no visible cells", name, row)
	}
	for i, c := range cells {
		if !c.reverse {
			t.Fatalf("%s: visible cell %d is outside the reverse highlight: %q", name, i, row)
		}
		if bold && !c.bold {
			t.Fatalf("%s: visible cell %d is not bold: %q", name, i, row)
		}
	}
	if got := lipgloss.Width(row); got != width {
		t.Fatalf("%s: width %d, want %d — trailing padding must be inside the highlight", name, got, width)
	}
}

// Selected commit rows: the highlight covers the whole row and the node
// glyph keeps its color inside it; selected+HEAD composes bold+reverse
// on every cell. The raw-byte assertions pin the exact composed spans.
func TestSelectedCommitRowHighlight(t *testing.T) {
	now := pinnedNow
	app := New(Config{RepoName: "demo", HasCommits: true, Now: func() time.Time { return now }})
	plain := git.Commit{Hash: "333", Abbrev: "b7c8d9e", At: now.Add(-3 * time.Hour), Subject: "Small fix"}

	row := app.commitRow(&plain, true, 60)
	assertFullHighlight(t, "selected row", row, 60, false)
	// The glyph's reverse+color span is immediately followed by the
	// text's reverse span — no unstyled gap between them — and the
	// trailing padding sits before the final reset.
	if !strings.Contains(row, "\x1b[7;34m●\x1b[m\x1b[7m b7c8d9e") {
		t.Errorf("selected row %q: glyph span and text span are not adjacent reverse regions", row)
	}
	if !strings.HasSuffix(row, "Small fix"+strings.Repeat(" ", 37)+"\x1b[m") {
		t.Errorf("selected row %q: trailing padding is not inside the highlight", row)
	}

	merge := git.Commit{Hash: "222", Abbrev: "e4f5a6b", At: now.Add(-14 * time.Minute),
		Subject: "Merge branch 'feature'", Parents: []string{"x", "y"}}
	row = app.commitRow(&merge, true, 60)
	assertFullHighlight(t, "selected merge row", row, 60, false)
	if !strings.Contains(row, "\x1b[7;35m◆\x1b[m") {
		t.Errorf("selected merge row %q: diamond lost its color inside the highlight", row)
	}

	head := git.Commit{Hash: "111", Abbrev: "a1b2c3d", At: now.Add(-2 * time.Minute),
		Subject: "Fix lane flicker on reload", IsHead: true}
	row = app.commitRow(&head, true, 60)
	assertFullHighlight(t, "selected HEAD row", row, 60, true)
	if !strings.Contains(row, "\x1b[1;7;34m●\x1b[m\x1b[1;7m a1b2c3d") {
		t.Errorf("selected HEAD row %q: want bold+reverse composed on every segment", row)
	}
}

// Selected file rows: the same full-width highlight, with the status
// letter still colored inside it.
func TestSelectedFileRowHighlight(t *testing.T) {
	row := fileRow(git.FileEntry{Status: 'M', Path: "x.go"}, true, 40, false)
	assertFullHighlight(t, "selected file row", row, 40, false)
	if !strings.Contains(row, "\x1b[7;33mM\x1b[m\x1b[7m x.go") {
		t.Errorf("selected file row %q: letter span and text span are not adjacent reverse regions", row)
	}
	if !strings.HasSuffix(row, "x.go"+strings.Repeat(" ", 34)+"\x1b[m") {
		t.Errorf("selected file row %q: trailing padding is not inside the highlight", row)
	}

	ren := git.FileEntry{Status: 'R', Orig: "src/old.go", Path: "src/new.go"}
	row = fileRow(ren, true, 60, false)
	assertFullHighlight(t, "selected rename row", row, 60, false)
	if !strings.Contains(row, "\x1b[7;34mR\x1b[m\x1b[7m src/old.go → src/new.go") {
		t.Errorf("selected rename row %q: letter lost its color inside the highlight", row)
	}
}

// Unselected rows must render byte-identically to the pre-change
// renderer: HEAD rows bold-wrapped over the padded row, plain and merge
// rows raw. The exact bytes are pinned here verbatim.
func TestUnselectedRowBytesPinned(t *testing.T) {
	now := pinnedNow
	app := New(Config{RepoName: "demo", HasCommits: true, Now: func() time.Time { return now }})
	head := git.Commit{Hash: "111", Abbrev: "a1b2c3d", At: now.Add(-2 * time.Minute),
		Subject: "Fix lane flicker on reload", IsHead: true}
	merge := git.Commit{Hash: "222", Abbrev: "e4f5a6b", At: now.Add(-14 * time.Minute),
		Subject: "Merge branch 'feature'", Parents: []string{"x", "y"}}
	plain := git.Commit{Hash: "333", Abbrev: "b7c8d9e", At: now.Add(-3 * time.Hour),
		Subject: "Small fix"}

	want := "\x1b[1m\x1b[34m●\x1b[m a1b2c3d 2m  Fix lane flicker on reload (HEAD)" +
		strings.Repeat(" ", 13) + "\x1b[m"
	if got := app.commitRow(&head, false, 60); got != want {
		t.Errorf("unselected HEAD row:\n got %q\nwant %q", got, want)
	}

	want = "\x1b[35m◆\x1b[m e4f5a6b 14m Merge branch 'feature' (merge)" + strings.Repeat(" ", 16)
	if got := app.commitRow(&merge, false, 60); got != want {
		t.Errorf("unselected merge row:\n got %q\nwant %q", got, want)
	}

	want = "\x1b[34m●\x1b[m b7c8d9e 3h  Small fix" + strings.Repeat(" ", 37)
	if got := app.commitRow(&plain, false, 60); got != want {
		t.Errorf("unselected plain row:\n got %q\nwant %q", got, want)
	}

	want = "\x1b[33mM\x1b[m x.go"
	if got := fileRow(git.FileEntry{Status: 'M', Path: "x.go"}, false, 40, false); got != want {
		t.Errorf("unselected file row:\n got %q\nwant %q", got, want)
	}
}

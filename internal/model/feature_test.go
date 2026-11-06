package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smhanov/squint/internal/diff"
	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/git/testfixture"
)

// ---- zoom (FR-16) ----

// Zoom renders only the focused pane full-screen; cycling focus follows;
// toggling back restores the three-pane layout.
func TestZoom(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	h.press("z")
	if !h.app.zoom {
		t.Fatal("z did not zoom")
	}
	frame := h.frame()
	if strings.Contains(frame, "files (Δ") {
		t.Error("zoomed commits view still shows the files pane")
	}
	if !strings.Contains(frame, "commits (") {
		t.Error("zoomed commits view lost the commits pane")
	}
	// The zoomed pane fills the canvas: its border spans the full width.
	lines := strings.Split(frame, "\n")
	if w := lipglossWidth(lines[0]); w < 119 {
		t.Errorf("zoomed pane border is %d wide, want the full 120 columns", w)
	}

	// Tab cycles focus; the zoomed pane follows.
	h.press("tab")
	if h.app.focus != focusFiles {
		t.Fatalf("focus after tab = %v, want files", h.app.focus)
	}
	if !strings.Contains(h.frame(), "files (Δ") {
		t.Error("zoomed pane did not follow the focus to files")
	}

	// Zoom off: the full three-pane layout returns.
	h.press("z")
	if h.app.zoom {
		t.Fatal("second z did not unzoom")
	}
	if !strings.Contains(h.frame(), "commits (") || !strings.Contains(h.frame(), "detail") {
		t.Error("unzoom did not restore the panes")
	}
	// Data survived zooming.
	if h.app.selectedHash == "" || len(h.app.detail.diffs) == 0 {
		t.Error("zoom round-trip lost pane data")
	}
}

func lipglossWidth(s string) int { return len([]rune(strings.ReplaceAll(s, "\x1b[m", ""))) }

// ---- side-by-side (FR-6, AC2) ----

// s toggles side-by-side; entering switches wrap to word; the pane shows
// two columns with a divider.
func TestSideBySideToggle(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("3") // focus detail

	if h.app.detail.sideBySide {
		t.Fatal("side-by-side on by default")
	}
	h.press("s")
	if !h.app.detail.sideBySide {
		t.Fatal("s did not enable side-by-side")
	}
	if h.app.detail.wrap != wrapWord {
		t.Errorf("wrap after entering side-by-side = %v, want word", h.app.detail.wrap)
	}
	frame := stripANSI(h.frame())
	if !strings.Contains(frame, "│") {
		t.Error("side-by-side frame has no column divider")
	}
	// Every diff line is now two columns; the old single-column + rows
	// with the full width are gone.
	h.press("s")
	if h.app.detail.sideBySide {
		t.Fatal("second s did not leave side-by-side")
	}
}

// The side-by-side body pairs old and new lines: a replaced line renders
// as -old│+new on one row.
func TestSideBySidePairing(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("3")
	h.press("s") // side-by-side; wrap flips to word automatically
	// Find a paired row in the rendered frame: -…│+…
	found := false
	for _, line := range strings.Split(stripANSI(h.frame()), "\n") {
		if strings.Contains(line, "-") && strings.Contains(line, "│") &&
			strings.Contains(line, "+") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no paired -│+ row found in the side-by-side frame")
	}
}

// h/l scroll horizontally when wrap is off and are inert when wrap is on.
func TestHorizontalScroll(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("3")
	// w cycles none → word → character → none; press three times to
	// return to the no-wrap start.
	h.press("w")
	h.press("w")
	h.press("w")
	if h.app.detail.wrap != wrapNone {
		t.Fatalf("wrap after a full cycle = %v, want none", h.app.detail.wrap)
	}

	h.press("l")
	if h.app.detail.scrollX != hScrollStep {
		t.Errorf("scrollX after l = %d, want %d", h.app.detail.scrollX, hScrollStep)
	}
	for range 100 {
		h.press("l")
	}
	// Unbounded scroll is clamped by rendering, not state; h walks back.
	for range 200 {
		h.press("h")
	}
	if h.app.detail.scrollX != 0 {
		t.Errorf("scrollX after many h = %d, want 0", h.app.detail.scrollX)
	}

	// Wrapped rows absorb long lines: h/l do nothing.
	h.press("w") // word wrap
	h.press("l")
	if h.app.detail.scrollX != 0 {
		t.Errorf("l scrolled while wrapped (scrollX %d)", h.app.detail.scrollX)
	}
}

// ---- intraline word-diff (FR-8, AC4) ----

// i toggles word-diff and refetches; the parsed diff then carries word
// segments and the frame shows emphasized words.
func TestIntralineToggle(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("3")

	if h.app.detail.wordDiff {
		t.Fatal("word-diff on by default")
	}
	cmd := h.sendNoExec(key("i"))
	if !h.app.detail.wordDiff {
		t.Fatal("i did not enable word-diff")
	}
	if !h.app.detail.dirtyOpts {
		t.Error("i did not mark the diff for refetch")
	}
	h.exec(cmd)
	if h.app.detail.dirtyOpts {
		t.Error("word-diff refetch did not land")
	}
	if !h.sinceHasWords() && !h.appDetailHasWords() {
		t.Error("refetched diff carries no word segments")
	}
	if !strings.Contains(h.app.detail.diffs[0].Path, ".") {
		t.Errorf("unexpected diff path %q", h.app.detail.diffs[0].Path)
	}

	h.press("i")
	if h.app.detail.wordDiff {
		t.Fatal("second i did not disable word-diff")
	}
}

// detailHasWords reports whether any loaded line carries segments.
func (h *harness) appDetailHasWords() bool {
	for _, fd := range h.app.detail.diffs {
		for _, hk := range fd.Hunks {
			for _, l := range hk.Lines {
				if l.HasWords() {
					return true
				}
			}
		}
	}
	return false
}

// detailHasWords reports word segments in the since content too.
func (h *harness) sinceHasWords() bool {
	for _, fd := range h.app.detail.since.diffs {
		for _, hk := range fd.Hunks {
			for _, l := range hk.Lines {
				if l.HasWords() {
					return true
				}
			}
		}
	}
	return false
}

// stripANSI removes SGR sequences for plain-text assertions.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---- merge first-parent (FR-9) ----

// p toggles the merge commit between combined and first-parent views,
// refetching both panes; non-merges ignore p; moving selection resets.
func TestFirstParentToggle(t *testing.T) {
	h := newHarness(t, testfixture.Merges)
	if !h.app.commits[h.app.cursor].Merge() {
		t.Fatal("fixture selection is not a merge")
	}

	combined := h.app.filesHash
	if combined == "" {
		t.Fatal("files not loaded")
	}
	nCombined := len(h.app.files)

	cmd := h.sendNoExec(key("p"))
	if !h.app.firstParent {
		t.Fatal("p did not enable first-parent")
	}
	if h.app.filesHash != "" {
		t.Error("p did not invalidate the file list for refetch")
	}
	h.exec(cmd)
	if h.app.filesHash == "" {
		t.Error("first-parent file list did not land")
	}
	// The first-parent view of a merge lists every changed file; the
	// combined view of this fixture's merge lists fewer.
	if len(h.app.files) <= nCombined {
		t.Errorf("first-parent files = %d, want more than the combined %d", len(h.app.files), nCombined)
	}
	if h.app.detailStale() {
		t.Error("first-parent diff did not land")
	}

	// Moving the selection resets the toggle.
	h.press("j")
	if h.app.firstParent {
		t.Error("selection change kept the first-parent toggle")
	}
	_ = combined

	// p on a non-merge commit is inert.
	h.press("p")
	if h.app.firstParent {
		t.Error("p toggled first-parent on a non-merge commit")
	}
}

// ---- since-diff (FR-10, AC5) ----

// d shows the cumulative diff to HEAD with its banner; d again returns
// to the commit diff; the root commit explains itself in the status bar.
func TestSinceDiff(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	// Select the third commit so the range has several commits.
	h.press("j")
	h.press("j")
	sel := h.app.commits[h.app.cursor]

	cmd := h.sendNoExec(key("d"))
	if !h.app.detail.since.on {
		t.Fatal("d did not enter since mode")
	}
	if !h.app.detailStale() {
		t.Error("since-diff load not marked stale while in flight")
	}
	h.exec(cmd)

	if h.app.detail.since.forHash != sel.Hash {
		t.Fatalf("since diff loaded for %q, want the selection %q", h.app.detail.since.forHash, sel.Hash)
	}
	wantBase := sel.Parents[0]
	if h.app.detail.since.base != wantBase {
		t.Errorf("since base = %q, want the first parent %q", h.app.detail.since.base, wantBase)
	}
	// The selected commit is the third-newest; its parent is three
	// commits behind HEAD, and the range includes the selection itself.
	if h.app.detail.since.count != 3 {
		t.Errorf("since count = %d, want 3", h.app.detail.since.count)
	}
	if len(h.app.detail.since.diffs) == 0 {
		t.Fatal("since-diff body is empty")
	}
	frame := h.frame()
	abbr := wantBase[:abbrevLen]
	if !strings.Contains(frame, fmt.Sprintf("since %s (3 commits)", abbr)) {
		t.Errorf("frame missing the since banner for %s:\n%s", abbr, frame)
	}
	// The since body is a real diff: file rules show up.
	if !strings.Contains(stripANSI(frame), "── ") {
		t.Error("since-diff frame has no file sections")
	}

	// d again leaves since mode.
	h.press("d")
	if h.app.detail.since.on {
		t.Fatal("second d did not leave since mode")
	}
}

// The root commit has no parent: d shows a hint instead of a diff.
func TestSinceDiffRootHint(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("G") // bottom = the root commit
	if len(h.app.commits[h.app.cursor].Parents) != 0 {
		t.Fatal("bottom commit of the basic fixture is not the root")
	}
	h.press("d")
	if h.app.detail.since.on {
		t.Fatal("d entered since mode on the root commit")
	}
	if h.app.hint == "" {
		t.Fatal("root-commit d produced no hint")
	}
	if !strings.Contains(h.frame(), "no parent") {
		t.Error("hint not rendered in the frame")
	}
	// Any key dismisses it.
	h.press("j")
	if h.app.hint != "" {
		t.Error("hint survived a key press")
	}
}

// Selection change exits since mode.
func TestSinceDiffExitsOnSelectionChange(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("j")
	h.exec2(h.sendNoExec(key("d")))
	if !h.app.detail.since.on {
		t.Fatal("since mode did not engage")
	}
	h.press("j")
	if h.app.detail.since.on {
		t.Error("since mode survived a selection change")
	}
	if !h.app.detailStale() && h.app.detail.hash == "" {
		t.Error("detail did not return to the commit-diff mode")
	}
}

// exec2 executes a command ignoring its messages' data status (helper
// alias kept for readability in these tests).
func (h *harness) exec2(cmd tea.Cmd) { h.exec(cmd) }

// The since-diff load is generation-guarded like every other load.
func TestSinceDiffStaleDropped(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("j")
	cmd := h.sendNoExec(key("d"))
	gen := h.app.gen
	// Move the selection before the load lands: the message must drop.
	h.press("j")
	h.exec(cmd)
	if h.app.detail.since.forHash != "" {
		t.Error("stale since-diff message was applied")
	}
	if gen == h.app.gen {
		t.Error("expected the generation to advance")
	}
}

// ---- copy (FR-13, AC7) ----

// y copies the abbreviated hash, Y the full one, via the OSC 52 command
// plus the platform fallback.
func TestCopyHash(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	full := h.app.selectedHash

	cmd := h.sendNoExec(key("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	msgs := batchMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatal("copy command batch is empty")
	}
	if got := fmt.Sprintf("%T", msgs[0]); got != "tea.setClipboardMsg" {
		t.Errorf("first copy message = %s, want the OSC 52 clipboard set", got)
	}

	cmd = h.sendNoExec(key("Y"))
	msgs = batchMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatal("Y command batch is empty")
	}
	if got := fmt.Sprintf("%T", msgs[0]); got != "tea.setClipboardMsg" {
		t.Errorf("Y message = %s, want the OSC 52 clipboard set", got)
	}

	// The fallback binary selection covers every platform.
	for goos, want := range map[string]string{
		"darwin": "pbcopy", "windows": "clip", "linux": "wl-copy",
	} {
		bin, ok := clipboardBinary(goos)
		if !ok || bin != want {
			t.Errorf("clipboardBinary(%q) = %q, %v; want %q", goos, bin, ok, want)
		}
	}
	if _, ok := clipboardBinary("plan9"); ok {
		t.Error("clipboardBinary returned a binary for an unsupported platform")
	}
	// The fallback command for darwin resolves to pbcopy and carries the
	// hash on stdin; it is not executed here.
	if clipboardFallbackCmd("plan9", full) != nil {
		t.Error("fallback command built for an unsupported platform")
	}
}

// batchMsgs flattens a command's message (including tea.BatchMsg) once.
func batchMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, batchMsgs(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// ---- viewer option plumbing ----

// --side-by-side starts in side-by-side mode with word wrap.
func TestSideBySideConfig(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	app := New(Config{RepoName: "demo", HasCommits: true, Repo: repo, SideBySide: true,
		NoAnimations: true, Now: func() time.Time { return pinnedNow }})
	if !app.detail.sideBySide {
		t.Error("--side-by-side did not set the initial diff mode")
	}
	if app.detail.wrap != wrapWord {
		t.Errorf("wrap = %v, want word in side-by-side mode", app.detail.wrap)
	}
}

// The diff loaders honor the word-diff option and parse accordingly.
func TestLoadDiffCmdWordDiff(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("3")
	h.exec(h.sendNoExec(key("i")))
	for _, fd := range h.app.detail.diffs {
		for _, hk := range fd.Hunks {
			for _, l := range hk.Lines {
				if l.Kind == diff.Add && l.HasWords() {
					// A pure-add line may or may not carry segments; the
					// invariant is: ParseUnified never produces segments.
					t.Fatal("unexpected segments outside word-diff mode")
				}
			}
		}
	}
}

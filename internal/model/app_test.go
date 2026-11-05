package model

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/squint-tui/squint/internal/diff"
	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/git/testfixture"
)

// Layout geometry (AC7: the <80×24 rule lives in a pure layout function).
func TestLayoutFunction(t *testing.T) {
	t.Run("MinimumSize", func(t *testing.T) {
		lay, ok := layoutFor(80, 24)
		if !ok {
			t.Fatal("layoutFor(80, 24) = false, want true")
		}
		if lay.leftW != 40 || lay.rightW != 40 {
			t.Errorf("widths = %d/%d, want 50%% split 40/40", lay.leftW, lay.rightW)
		}
		if lay.commitsH != 22 {
			t.Errorf("commitsH = %d, want 22 (height minus status rows)", lay.commitsH)
		}
		if lay.filesH < 4 || lay.filesH > 10 {
			t.Errorf("filesH = %d, want clamped to 4..10", lay.filesH)
		}
		if lay.filesH+lay.detailH != lay.commitsH {
			t.Errorf("right column %d+%d != left height %d", lay.filesH, lay.detailH, lay.commitsH)
		}
	})
	t.Run("StandardSize", func(t *testing.T) {
		lay, ok := layoutFor(120, 40)
		if !ok {
			t.Fatal("layoutFor(120, 40) = false, want true")
		}
		if lay.leftW != 60 || lay.rightW != 60 {
			t.Errorf("widths = %d/%d, want 60/60", lay.leftW, lay.rightW)
		}
		if lay.commitsH != 38 || lay.filesH != 10 || lay.detailH != 28 {
			t.Errorf("heights = %d/%d/%d, want 38/10/28", lay.commitsH, lay.filesH, lay.detailH)
		}
	})
	t.Run("OddWidthKeepsTotal", func(t *testing.T) {
		lay, ok := layoutFor(81, 24)
		if !ok {
			t.Fatal("layoutFor(81, 24) = false, want true")
		}
		if lay.leftW+lay.rightW != 81 {
			t.Errorf("widths %d+%d != 81", lay.leftW, lay.rightW)
		}
	})
	t.Run("BelowMinimum", func(t *testing.T) {
		for _, size := range [][2]int{{79, 40}, {120, 23}, {10, 5}, {0, 0}} {
			if _, ok := layoutFor(size[0], size[1]); ok {
				t.Errorf("layoutFor(%d, %d) = true, want false", size[0], size[1])
			}
		}
	})
}

// ExitCode: quitting from the too-small screen is a clean error exit.
func TestExitCode(t *testing.T) {
	app := New(Config{RepoName: "demo", HasCommits: true})
	if app.ExitCode() != 0 {
		t.Errorf("ExitCode before sizing = %d, want 0", app.ExitCode())
	}
	app = update(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	if app.ExitCode() != 0 {
		t.Errorf("ExitCode in ok state = %d, want 0", app.ExitCode())
	}
	app = update(t, app, tea.WindowSizeMsg{Width: 70, Height: 20})
	if app.ExitCode() != 1 {
		t.Errorf("ExitCode in too-small state = %d, want 1", app.ExitCode())
	}
}

// spanFor finds the recorded file span for a path.
func (h *harness) spanFor(path string) (fileSpan, bool) {
	for _, s := range h.app.detail.spans {
		if s.path == path {
			return s, true
		}
	}
	return fileSpan{}, false
}

// detailContentRows returns the total (not visible) detail content rows.
func (h *harness) detailContentRows() int { return h.app.detail.view.ContentRows() }

// gitDiffFor parses the reference diff of a commit straight from git.
func gitDiffFor(h *harness, hash string) ([]diff.FileDiff, error) {
	commit := git.Commit{Hash: hash}
	raw, err := git.DiffFor(h.t.Context(), h.repo, commit, git.DiffOpts{Context: diffContextDefault})
	if err != nil {
		return nil, err
	}
	return diff.ParseUnified(raw), nil
}

// diffPaths renders the file paths of a parsed diff for comparison.
func diffPaths(files []diff.FileDiff) string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return strings.Join(paths, ",")
}

// Load failures surface inline in the affected pane, not as a crash.
func TestErrorRowsRendering(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	// Force a commits-list error through the current global sequence.
	h.send(commitsLoadedMsg{err: errFake("boom"), gen: h.app.globalSeq})
	if !strings.Contains(h.frame(), "git error: boom") {
		t.Error("commits pane does not render the load error")
	}

	// A files error shows in the files pane.
	filesHash := h.app.filesHash
	h.send(filesLoadedMsg{hash: h.app.selectedHash, err: errFake("nope"), gen: h.app.gen})
	if filesHash != h.app.filesHash {
		t.Error("a failed load must not update filesHash (pane would look loaded)")
	}

	// A status error shows in the hints bar.
	h.send(statusLoadedMsg{err: errFake("status broke"), gen: h.app.globalSeq})
	if !strings.Contains(h.frame(), "git error: status broke") {
		t.Error("hints bar does not render the status error")
	}
}

// errFake is a stand-in git error.
type errFake string

func (e errFake) Error() string { return string(e) }

// Loading rows: while the first loads are in flight the panes show
// spinner rows instead of emptiness (AC1, FR-3).
func TestLoadingRowsWhileLoadsInFlight(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	app := New(Config{RepoName: "demo", HasCommits: true, Repo: repo, NoAnimations: true})
	app = update(t, app, tea.WindowSizeMsg{Width: 120, Height: 40})

	if frame := app.View().Content; !strings.Contains(frame, "loading commits…") {
		t.Error("commits pane shows no loading row during the first load")
	}

	// Land the commit list without executing the fan-out command, so the
	// files and diff loads stay in flight.
	commits, err := git.ListCommits(t.Context(), repo, git.LogOpts{})
	if err != nil || len(commits) == 0 {
		t.Fatalf("fixture list: %v (%d commits)", err, len(commits))
	}
	app = update(t, app, commitsLoadedMsg{commits: commits, gen: app.globalSeq})

	frame := app.View().Content
	if !strings.Contains(frame, "loading files…") {
		t.Error("files pane shows no loading row while the file list loads")
	}
	if !strings.Contains(frame, "loading diff…") {
		t.Error("detail pane shows no loading row while the diff loads")
	}
}

// Loading pipeline: startup loads select the first commit and populate all
// three panes (AC1).
func TestStartupLoadsAndSelects(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	if len(h.app.commits) == 0 {
		t.Fatal("no commits loaded")
	}
	if h.app.selectedHash != h.app.commits[0].Hash {
		t.Errorf("selectedHash = %q, want the newest commit %q", h.app.selectedHash, h.app.commits[0].Hash)
	}
	if h.app.filesStale() || h.app.detailStale() {
		t.Error("panes still stale after startup loads")
	}
	if len(h.app.files) == 0 {
		t.Error("files pane empty after startup loads")
	}
	if len(h.app.detail.diffs) == 0 {
		t.Error("detail diff empty after startup loads")
	}
	if h.app.anyLoading() {
		t.Error("spinners still active after startup loads")
	}
}

// Help overlay: opens with ?, shows the focused pane's keys, closes with
// ? and esc; q quits from inside it (AC5).
func TestHelpOverlay(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	if strings.Contains(h.frame(), "squint keys") {
		t.Fatal("help overlay visible without ?")
	}
	h.press("?")
	if !strings.Contains(h.frame(), "squint keys") {
		t.Fatal("help overlay not visible after ?")
	}
	// Commits are focused by default: their table, not the detail table.
	if !strings.Contains(h.frame(), "half page") {
		t.Error("commits keymap missing from overlay")
	}
	if strings.Contains(h.frame(), "ignore whitespace") {
		t.Error("overlay shows detail keys while commits is focused")
	}
	h.press("?")
	if strings.Contains(h.frame(), "squint keys") {
		t.Fatal("? did not close the overlay")
	}

	// Detail pane gets its own table.
	h.press("3")
	h.press("?")
	if !strings.Contains(h.frame(), "ignore whitespace") {
		t.Error("detail keymap missing while detail focused")
	}

	// esc closes too.
	h.press("esc")
	if strings.Contains(h.frame(), "squint keys") {
		t.Fatal("esc did not close the overlay")
	}

	// q quits from any focus — including inside the overlay.
	h.press("?")
	_, cmd := h.app.Update(key("q"))
	if cmd == nil {
		t.Fatal("q inside overlay returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q produced %T, want tea.QuitMsg", cmd())
	}
}

// Pane focus: tab cycles, shift+tab rewinds, 1/2/3 jump directly. The
// hints bar mirrors the focused pane.
func TestFocusRouting(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	if h.app.focus != focusCommits {
		t.Fatalf("initial focus = %v, want commits", h.app.focus)
	}
	h.press("tab")
	if h.app.focus != focusFiles {
		t.Errorf("after tab focus = %v, want files", h.app.focus)
	}
	if !strings.Contains(h.frame(), "enter open") {
		t.Error("hints bar does not show file-pane hints")
	}
	h.press("tab")
	if h.app.focus != focusDetail {
		t.Errorf("after second tab focus = %v, want detail", h.app.focus)
	}
	h.press("shift+tab")
	if h.app.focus != focusFiles {
		t.Errorf("after shift+tab focus = %v, want files", h.app.focus)
	}
	h.press("1")
	if h.app.focus != focusCommits {
		t.Errorf("after 1 focus = %v, want commits", h.app.focus)
	}
	h.press("3")
	if h.app.focus != focusDetail {
		t.Errorf("after 3 focus = %v, want detail", h.app.focus)
	}
	h.press("2")
	if h.app.focus != focusFiles {
		t.Errorf("after 2 focus = %v, want files", h.app.focus)
	}
}

// File selection scrolls the detail pane to the file's first hunk (AC2,
// FR-4). On the basic fixture the diff fits on screen, so the jump lands
// on the file's span start and the viewport simply shows it; the second
// half proves a real scroll with the 60-file commit.
func TestFileSelectScrollsDetail(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("2") // focus files

	if len(h.app.files) < 2 {
		t.Fatalf("files = %d, want at least 2", len(h.app.files))
	}

	// Move to the second file and jump to it.
	h.press("j")
	if h.app.fileCursor != 1 {
		t.Fatalf("fileCursor = %d, want 1", h.app.fileCursor)
	}
	want := h.app.files[1].Path
	h.press("enter")

	if h.app.focus != focusDetail {
		t.Errorf("focus after enter = %v, want detail", h.app.focus)
	}
	span, ok := h.spanFor(want)
	if !ok {
		t.Fatalf("no span recorded for file %q (spans: %+v)", want, h.app.detail.spans)
	}
	if h.app.detail.row != span.start {
		t.Errorf("detail row = %d, want the span start %d of %q", h.app.detail.row, span.start, want)
	}
	maxOff := max(0, h.detailContentRows()-(h.app.lay.detailH-2))
	if got, wantOff := h.app.detail.view.Offset(), min(span.start, maxOff); got != wantOff {
		t.Errorf("viewport offset = %d, want %d (span start %d, max scroll %d)", got, wantOff, span.start, maxOff)
	}

	// Now a real scroll: 60 files do not fit on screen.
	h2 := newHarness(t, testfixture.ManyFiles)
	h2.press("1") // commits
	h2.press("j") // select the 60-file commit
	h2.press("2") // files
	for range 30 {
		h2.press("j")
	}
	path := h2.app.files[h2.app.fileCursor].Path
	h2.press("enter")
	span, ok = h2.spanFor(path)
	if !ok {
		t.Fatalf("no span for %q", path)
	}
	if h2.app.detail.row != span.start {
		t.Errorf("detail row = %d, want span start %d", h2.app.detail.row, span.start)
	}
	if got := h2.app.detail.view.Offset(); got != span.start {
		t.Errorf("viewport offset = %d, want %d (unclamped scroll expected)", got, span.start)
	}
}

// Rapid selection changes: only the final generation's diff may render
// (AC3, spec §5.1 stale-result guard).
func TestRapidSelectionDropsStale(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	// Move five times; the last selection clamps at the bottom commit,
	// so four real selection changes queue four generations of loads.
	if len(h.app.commits) < 5 {
		t.Fatalf("commits = %d, want at least 5", len(h.app.commits))
	}
	var pending []tea.Cmd
	for range 4 {
		pending = append(pending, h.sendNoExec(key("j")))
	}
	final := h.app.commits[h.app.cursor].Hash
	if h.app.gen == 0 {
		t.Fatal("generation did not advance on selection changes")
	}
	// Every queued load now lands, oldest first.
	for _, cmd := range pending {
		h.exec(cmd)
	}

	if h.app.selectedHash != final {
		t.Errorf("selectedHash = %q, want %q", h.app.selectedHash, final)
	}
	if h.app.filesHash != final {
		t.Errorf("filesHash = %q, want %q (a stale load overwrote the pane)", h.app.filesHash, final)
	}
	if h.app.detail.hash != final {
		t.Errorf("detail hash = %q, want %q (a stale diff overwrote the pane)", h.app.detail.hash, final)
	}
	if h.app.filesStale() || h.app.detailStale() {
		t.Error("panes stuck in loading state after the final loads landed")
	}
	// The rendered diff must be the final commit's.
	want, err := gitDiffFor(h, final)
	if err != nil {
		t.Fatalf("reference diff: %v", err)
	}
	if len(h.app.detail.diffs) == 0 {
		t.Fatal("no diff rendered")
	}
	if diffPaths(h.app.detail.diffs) != diffPaths(want) {
		t.Errorf("rendered diff files %s, want the final commit's %s",
			diffPaths(h.app.detail.diffs), diffPaths(want))
	}
}

// Explicit stale-message drops: a message with an old generation is
// ignored even when the hash matches.
func TestStaleMessagesDropped(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	gen := h.app.gen

	// A files message with the old generation must not land.
	h.send(filesLoadedMsg{hash: h.app.selectedHash, files: []git.FileEntry{{Status: 'A', Path: "bogus.txt"}}, gen: gen - 1})
	if len(h.app.files) > 0 && h.app.files[0].Path == "bogus.txt" {
		t.Error("stale filesLoadedMsg was applied")
	}
	// Same for status.
	h.send(statusLoadedMsg{status: git.RepoStatus{Branch: "stale"}, gen: h.app.globalSeq - 1})
	if h.app.status.Branch == "stale" {
		t.Error("stale statusLoadedMsg was applied")
	}
	// Current-generation messages still land.
	h.send(statusLoadedMsg{status: git.RepoStatus{Branch: "fresh"}, gen: h.app.globalSeq})
	if h.app.status.Branch != "fresh" {
		t.Errorf("status = %q, want the current-generation update", h.app.status.Branch)
	}
}

// Refresh keeps the selection stable by hash (AC4, FR-12).
func TestRefreshKeepsSelection(t *testing.T) {
	h := newHarness(t, testfixture.Basic)

	// Select the third commit.
	h.press("j")
	h.press("j")
	want := h.app.selectedHash
	cursor := h.app.cursor

	h.press("r")
	if h.app.commitsLoading {
		t.Error("commit load still flagged loading after its result landed")
	}
	if h.app.gen == 0 {
		t.Error("refresh did not bump the generation")
	}
	if h.app.selectedHash != want {
		t.Fatalf("refresh changed selectedHash to %q, want %q", h.app.selectedHash, want)
	}

	// Simulate a fresh list arriving with an extra commit on top: the
	// selection re-derives by hash and follows the shifted position.
	older := append([]git.Commit{{Hash: "aaa111", Abbrev: "aaa111", Author: "x",
		At: pinnedNow.Add(-time.Minute)}}, h.app.commits...)
	h.send(commitsLoadedMsg{commits: older, gen: h.app.globalSeq})

	if h.app.selectedHash != want {
		t.Errorf("selectedHash after reload = %q, want %q", h.app.selectedHash, want)
	}
	if h.app.cursor != cursor+1 {
		t.Errorf("cursor after reload = %d, want %d (shifted by the new top commit)", h.app.cursor, cursor+1)
	}
}

// A vanished selection falls back to the nearest index by position.
func TestRefreshVanishedSelection(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("G") // bottom
	cursor := h.app.cursor

	// A list without the selected commit.
	trimmed := h.app.commits[:len(h.app.commits)-1]
	h.send(commitsLoadedMsg{commits: trimmed, gen: h.app.globalSeq})

	nearest := min(cursor, len(trimmed)-1)
	if h.app.cursor != nearest {
		t.Errorf("cursor = %d, want nearest %d", h.app.cursor, nearest)
	}
	if h.app.selectedHash != trimmed[h.app.cursor].Hash {
		t.Errorf("selectedHash = %q, want the nearest commit %q", h.app.selectedHash, trimmed[h.app.cursor].Hash)
	}
}

// Resize relayouts panes but keeps the selection and its loaded data.
func TestResizePreservesSelection(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("j")
	want := h.app.selectedHash

	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if h.app.selectedHash != want {
		t.Errorf("selectedHash changed on resize: %q, want %q", h.app.selectedHash, want)
	}
	if h.app.lay.leftW != 50 || h.app.lay.rightW != 50 {
		t.Errorf("layout after resize = %d/%d, want 50/50", h.app.lay.leftW, h.app.lay.rightW)
	}
	if len(h.app.detail.diffs) == 0 {
		t.Error("detail content lost on resize")
	}
	// The frame fits the new size: 30 rows, 100 columns.
	frame := h.frame()
	lines := strings.Split(frame, "\n")
	if len(lines) != 30 {
		t.Errorf("frame has %d rows, want 30", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("row %d is %d cells wide, want <= 100", i, w)
		}
	}
}

// Zero-sized resize reports render nothing; growing back recovers.
func TestZeroSizeRecovers(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.send(tea.WindowSizeMsg{Width: 0, Height: 0})
	if got := h.frame(); got != "" {
		t.Errorf("frame after zero-size = %q, want empty", got)
	}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !strings.Contains(h.frame(), "commits (") {
		t.Error("frame did not recover after a zero-size report")
	}
}

// The empty repository shows the empty screen once loads confirm it.
func TestEmptyRepoViaLoads(t *testing.T) {
	h := newHarness(t, testfixture.Empty)
	if h.app.state != stateEmptyRepo {
		t.Fatalf("state = %v, want stateEmptyRepo", h.app.state)
	}
	if !strings.Contains(h.frame(), "No commits yet") {
		t.Error("frame does not show the empty-state message")
	}
}

// Detail viewer options: wrap cycle reflows, +/- refetches context, W
// refetches with ignore-whitespace.
func TestDetailOptions(t *testing.T) {
	h := newHarness(t, testfixture.LongMsg)
	h.press("3") // focus detail

	if h.app.detail.wrap != wrapNone {
		t.Fatalf("initial wrap = %v, want none", h.app.detail.wrap)
	}
	rowsBefore := h.detailContentRows()

	h.press("w") // → word
	if h.app.detail.wrap != wrapWord {
		t.Fatalf("wrap after w = %v, want word", h.app.detail.wrap)
	}
	if rows := h.detailContentRows(); rows <= rowsBefore {
		t.Errorf("word wrap did not add rows: %d → %d", rowsBefore, rows)
	}
	h.press("w") // → character
	if h.app.detail.wrap != wrapChar {
		t.Fatalf("wrap after second w = %v, want character", h.app.detail.wrap)
	}
	h.press("w") // → none
	if h.app.detail.wrap != wrapNone {
		t.Fatalf("wrap after third w = %v, want none", h.app.detail.wrap)
	}
	if rows := h.detailContentRows(); rows != rowsBefore {
		t.Errorf("cycling back to none changed the row count: %d → %d", rowsBefore, rows)
	}

	// Context: + bumps the value and queues a refetch; the refetch lands
	// and clears the dirty flag.
	cmd := h.sendNoExec(key("+"))
	if h.app.detail.context != 4 {
		t.Fatalf("context after + = %d, want 4", h.app.detail.context)
	}
	if !h.app.detail.dirtyOpts {
		t.Error("+ did not mark the diff dirty for refetch")
	}
	h.exec(cmd)
	if h.app.detail.dirtyOpts {
		t.Error("refetch landed but the dirty flag is stuck")
	}

	// Ignore-whitespace refetch toggles off again.
	h.press("W")
	if !h.app.detail.ignoreWS {
		t.Error("W did not enable ignore-whitespace")
	}
	h.press("W")
	if h.app.detail.ignoreWS {
		t.Error("second W did not disable ignore-whitespace")
	}

	// Context floor and ceiling.
	for range diffContextMax - 4 {
		h.press("+")
	}
	if h.app.detail.context != diffContextMax {
		t.Errorf("context after +%s = %d, want %d", strings.Repeat("+", diffContextMax-4), h.app.detail.context, diffContextMax)
	}
	for range diffContextMax + 1 {
		h.press("-")
	}
	if h.app.detail.context != 0 {
		t.Errorf("context after repeated - = %d, want 0", h.app.detail.context)
	}
}

// Hunk navigation walks the recorded hunk header rows, on a commit big
// enough to actually scroll.
func TestHunkNavigation(t *testing.T) {
	h := newHarness(t, testfixture.ManyFiles)
	h.press("j") // the 60-file commit
	h.press("3") // focus detail

	if len(h.app.detail.hunkRows) < 2 {
		t.Fatalf("hunkRows = %v, want at least 2 hunks", h.app.detail.hunkRows)
	}
	h.press(")")
	first := h.app.detail.hunkRows[0]
	if got := h.app.detail.view.Offset(); got != first {
		t.Errorf("offset after ) = %d, want first hunk row %d", got, first)
	}
	h.press(")")
	second := h.app.detail.hunkRows[1]
	if got := h.app.detail.view.Offset(); got != second {
		t.Errorf("offset after )) = %d, want second hunk row %d", got, second)
	}
	h.press("(")
	if got := h.app.detail.view.Offset(); got != first {
		t.Errorf("offset after ( = %d, want first hunk row %d", got, first)
	}
}

// File navigation from the detail pane jumps file spans and moves the
// files-pane cursor along.
func TestDetailFileNavigation(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("3")

	h.press("]") // first file section
	if h.app.fileCursor != 0 {
		t.Errorf("fileCursor = %d, want 0 (first file)", h.app.fileCursor)
	}
	firstOff := h.app.detail.row
	h.press("]") // next file
	if h.app.detail.row <= firstOff {
		t.Errorf("row after ]] = %d, want to advance past %d", h.app.detail.row, firstOff)
	}
	if h.app.fileCursor != 1 {
		t.Errorf("fileCursor after ]] = %d, want 1", h.app.fileCursor)
	}
	h.press("[") // back
	if h.app.detail.row != firstOff {
		t.Errorf("row after [ = %d, want %d", h.app.detail.row, firstOff)
	}
}

// The merge fixture: the merge commit is selected first and renders its
// combined diff; its list row shows the merge glyph and marker.
func TestMergeSelection(t *testing.T) {
	h := newHarness(t, testfixture.Merges)

	merge := h.app.commits[h.app.cursor]
	if !merge.Merge() {
		t.Fatalf("selected commit %q is not a merge", merge.Subject)
	}
	if !strings.Contains(h.frame(), "◆") {
		t.Error("merge glyph ◆ missing from the commit list")
	}
	if !strings.Contains(h.frame(), "(merge)") {
		t.Error("(merge) marker missing from the commit list")
	}
	if h.app.detail.hash != merge.Hash || len(h.app.detail.diffs) == 0 {
		t.Fatalf("merge diff did not load (hash %q, %d files)", h.app.detail.hash, len(h.app.detail.diffs))
	}
}

// Status bar content: repo, branch, commit count; dirty flag; detached
// HEAD.
func TestStatusBar(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	frame := h.frame()
	for _, want := range []string{"squint · demo", "main", "5 commits"} {
		if !strings.Contains(frame, want) {
			t.Errorf("status bar missing %q", want)
		}
	}
	// Fixture worktrees are clean: no dirty marker.
	if strings.Contains(frame, "dirty") {
		t.Error("status bar shows dirty for a clean fixture")
	}

	// Detach HEAD and refresh: the bar reports the detached position.
	testfixture.Git(h.t, h.repo.Root, "checkout", "-q", "--detach", "HEAD")
	h.press("r")
	abbr := h.app.selectedHash[:abbrevLen]
	if !strings.Contains(h.frame(), "HEAD detached at "+abbr) {
		t.Errorf("status bar missing detached notice for %s", abbr)
	}
}

// The dirty marker appears when the worktree has changes.
func TestStatusBarDirty(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	// An untracked file makes the tree dirty.
	if err := os.WriteFile(h.repo.Root+"/scratch.txt", []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("r")
	if !strings.Contains(h.frame(), "dirty") {
		t.Error("status bar missing the dirty marker after dirtying the tree")
	}
}

// Relative age formatting.
func TestRelAge(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "now"},
		{-time.Minute, "now"}, // future timestamps render as now
		{59 * time.Second, "now"},
		{time.Minute, "1m"},
		{90 * time.Minute, "1h"},
		{23 * time.Hour, "23h"},
		{25 * time.Hour, "1d"},
		{6 * 24 * time.Hour, "6d"},
		{8 * 24 * time.Hour, "1w"},
		{400 * 24 * time.Hour, "1y"},
	}
	for _, tt := range cases {
		if got := relAge(now.Add(-tt.d), now); got != tt.want {
			t.Errorf("relAge(-%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// File status letters get their spec colors.
func TestStatusColors(t *testing.T) {
	cases := map[byte]string{
		'A': "\x1b[32m", // green
		'M': "\x1b[33m", // yellow
		'D': "\x1b[31m", // red
		'R': "\x1b[34m", // blue
		'C': "\x1b[34m", // blue
	}
	for status, seq := range cases {
		got := statusStyle(status).Render(string(status))
		if !strings.Contains(got, seq) {
			t.Errorf("status %c rendered %q, want it to contain %q", status, got, seq)
		}
	}
}

// File rows: rename display, truncation, reverse-video selection.
func TestFileRows(t *testing.T) {
	rename := git.FileEntry{Status: 'R', Orig: "src/old.go", Path: "src/new.go"}
	row := fileRow(rename, false, 60, false)
	if !strings.Contains(row, "src/old.go → src/new.go") {
		t.Errorf("rename row %q missing old → new", row)
	}
	if row := fileRow(rename, false, 60, true); !strings.Contains(row, "src/old.go -> src/new.go") {
		t.Errorf("ASCII rename row %q missing old -> new", row)
	}

	long := git.FileEntry{Status: 'M', Path: strings.Repeat("d/", 30) + "file.txt"}
	row = fileRow(long, false, 40, false)
	if w := lipgloss.Width(row); w > 40 {
		t.Errorf("row width %d exceeds 40", w)
	}

	sel := git.FileEntry{Status: 'M', Path: "x.go"}
	row = fileRow(sel, true, 40, false)
	if !strings.Contains(row, "\x1b[7m") {
		t.Error("selected file row is not reverse video")
	}
}

// Commit rows: node glyph, hash, age, subject, HEAD/merge markers,
// ellipsized subjects, reverse-video selection.
func TestCommitRows(t *testing.T) {
	now := pinnedNow
	app := New(Config{RepoName: "demo", HasCommits: true, Now: func() time.Time { return now }})

	head := git.Commit{Hash: "111", Abbrev: "a1b2c3d", At: now.Add(-2 * time.Minute),
		Subject: "Fix lane flicker on reload", IsHead: true}
	merge := git.Commit{Hash: "222", Abbrev: "e4f5a6b", At: now.Add(-14 * time.Minute),
		Subject: "Merge branch 'feature'", Parents: []string{"x", "y"}}

	row := app.commitRow(&head, false, 60)
	if !strings.Contains(row, "a1b2c3d") || !strings.Contains(row, "2m") {
		t.Errorf("head row %q missing hash/age", row)
	}
	if !strings.Contains(row, "(HEAD)") {
		t.Errorf("head row %q missing (HEAD) marker", row)
	}
	if !strings.Contains(row, "\x1b[1m") {
		t.Error("head row is not bold")
	}

	row = app.commitRow(&merge, false, 60)
	if !strings.Contains(row, "◆") || !strings.Contains(row, "(merge)") {
		t.Errorf("merge row %q missing ◆/(merge)", row)
	}

	row = app.commitRow(&head, true, 60)
	if !strings.Contains(row, "\x1b[7m") {
		t.Error("selected row is not reverse video")
	}

	long := git.Commit{Abbrev: "f0e1d2c", At: now, Subject: strings.Repeat("word ", 40)}
	row = app.commitRow(&long, false, 60)
	if w := lipgloss.Width(row); w > 60 {
		t.Errorf("row width %d exceeds 60", w)
	}

	// ASCII mode swaps the glyphs.
	ascii := New(Config{RepoName: "demo", HasCommits: true, ASCII: true, Now: func() time.Time { return now }})
	if row := ascii.commitRow(&head, false, 60); strings.Contains(row, "●") {
		t.Error("ASCII row still uses ●")
	}
}

// gg top and G bottom, plus page keys.
func TestCursorMovement(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("G")
	if h.app.cursor != len(h.app.commits)-1 {
		t.Errorf("cursor after G = %d, want %d", h.app.cursor, len(h.app.commits)-1)
	}
	var pending []tea.Cmd
	pending = append(pending, h.sendNoExec(key("g")))
	pending = append(pending, h.sendNoExec(key("g")))
	for _, cmd := range pending {
		h.exec(cmd)
	}
	if h.app.cursor != 0 {
		t.Errorf("cursor after gg = %d, want 0", h.app.cursor)
	}

	h.press("ctrl+f")
	if h.app.cursor == 0 {
		t.Error("ctrl+f did not move the cursor")
	}
	h.press("ctrl+b")
	if h.app.cursor != 0 {
		t.Errorf("cursor after ctrl+b = %d, want back at 0", h.app.cursor)
	}
	h.press("ctrl+d")
	h.press("ctrl+u")
	if h.app.cursor != 0 {
		t.Errorf("cursor after ctrl+d ctrl+u = %d, want 0", h.app.cursor)
	}

	// Movement clamps at the edges.
	for range len(h.app.commits) + 3 {
		h.press("j")
	}
	if h.app.cursor != len(h.app.commits)-1 {
		t.Errorf("cursor after overscroll = %d, want %d", h.app.cursor, len(h.app.commits)-1)
	}
	for range len(h.app.commits) + 3 {
		h.press("k")
	}
	if h.app.cursor != 0 {
		t.Errorf("cursor after overscroll up = %d, want 0", h.app.cursor)
	}
}

// Unbound keys are inert.
func TestUnboundKeysInert(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	before := h.app.gen
	for _, k := range []string{"x", "s", "i", "z", "y", "n", "/", "p"} {
		h.press(k)
	}
	if h.app.gen != before {
		t.Error("unbound keys moved the generation (started a load)")
	}
	if h.app.selectedHash != h.app.commits[0].Hash {
		t.Error("unbound keys moved the selection")
	}
}

// Detail keys in the detail focus scroll the pane; the hint bar shows the
// detail hints only while detail is focused.
func TestDetailScrollKeys(t *testing.T) {
	h := newHarness(t, testfixture.ManyFiles)
	h.press("3")
	if !strings.Contains(h.frame(), "() hunk") {
		t.Error("hints bar missing detail hints")
	}

	// Select the 60-file commit: its diff cannot fit on screen.
	h.press("1")
	h.press("j")
	h.press("3")

	at := h.app.detail.view.Offset()
	for range 30 {
		h.press("j")
	}
	if h.app.detail.view.Offset() <= at {
		t.Errorf("30×j did not scroll (offset %d → %d)", at, h.app.detail.view.Offset())
	}
	for range 30 {
		h.press("k")
	}
	if got := h.app.detail.view.Offset(); got != at {
		t.Errorf("30×k did not return to the top (offset %d, want %d)", got, at)
	}
}

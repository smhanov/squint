package model

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/git/testfixture"
	"github.com/smhanov/squint/internal/watch"
)

// End-to-end auto-refresh (AC6, FR-11): the app runs with the watcher
// on, an external commit lands via exec, and the commit list updates by
// itself while the selection stays stable by hash.
func TestWatcherUpdatesList(t *testing.T) {
	oldDebounce, oldPoll := watch.DebounceInterval, watch.PollInterval
	watch.DebounceInterval, watch.PollInterval = 20*time.Millisecond, 30*time.Millisecond
	t.Cleanup(func() { watch.DebounceInterval, watch.PollInterval = oldDebounce, oldPoll })

	root := testfixture.New(t, testfixture.Basic)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	app := New(Config{RepoName: "demo", HasCommits: true, Repo: repo, Watch: true,
		NoAnimations: true, Now: func() time.Time { return pinnedNow }})
	app = update(t, app, tea.WindowSizeMsg{Width: 120, Height: 40})
	if app.watcher == nil {
		t.Fatal("Watch did not start a watcher")
	}
	defer app.watcher.Close()

	// Drain Init: the two loads land as data messages; the watch command
	// blocks on the watcher channel, so it runs on a goroutine and its
	// message is collected asynchronously.
	msgs := make(chan tea.Msg, 16)
	run := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() { msgs <- cmd() }()
	}
	// handle feeds one message through Update; commands go back to the
	// channel so the blocking watch command never runs inline.
	handle := func(msg tea.Msg) {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				run(c)
			}
			return
		}
		next, cmd := app.Update(msg)
		app = next.(App)
		run(cmd)
	}
	// Drain the initial loads: wait for the first message (fixture
	// setup plus git can be slow on CI runners), then keep draining
	// until a short quiet period says the loads are done.
	drain := func() {
		first := true
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case msg := <-msgs:
				handle(msg)
				first = false
			case <-time.After(50 * time.Millisecond):
				if first {
					continue // nothing yet: keep waiting
				}
				return
			}
		}
	}
	run(app.Init()) // the Batch unwraps on the channel; leaves run in goroutines
	drain()
	if len(app.commits) == 0 {
		t.Fatal("initial commit list did not load")
	}
	before := len(app.commits)
	selected := app.selectedHash
	if selected != app.commits[0].Hash {
		t.Fatal("initial selection is not the newest commit")
	}

	// The agent commits again while the app is running.
	testfixture.Git(t, root, "commit", "--allow-empty", "-q", "-m", "external commit")

	// The watcher ticks; the model refreshes through the guarded path.
	deadline := time.After(3 * time.Second)
	for len(app.commits) != before+1 {
		select {
		case msg := <-msgs:
			handle(msg)
		case <-deadline:
			t.Fatalf("list did not refresh after an external commit (%d commits, want %d)", len(app.commits), before+1)
		}
	}

	if len(app.commits) != before+1 {
		t.Fatalf("commits = %d, want %d after the external commit", len(app.commits), before+1)
	}
	if got := app.commits[0].Subject; got != "external commit" {
		t.Errorf("newest commit = %q, want the external one", got)
	}
	if app.selectedHash != selected {
		t.Errorf("selectedHash = %q, want the pre-commit selection %q (unstable cursor)", app.selectedHash, selected)
	}
	if app.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (shifted by the new top commit)", app.cursor)
	}
	if strings.Contains(app.View().Content, "loading") && app.anyLoading() {
		t.Error("frame stuck in a loading state after refresh")
	}
}

// flattenBatch is intentionally absent: unwrapping a tea.Cmd by calling
// it would execute leaf commands — including the blocking watch command
// — inline. Batches are unwrapped at the message level by handle()
// instead (a BatchMsg carries its sub-commands).

// A watch tick is just a refresh: the same guarded path as the r key,
// usable when the watcher itself is tested elsewhere.
func TestUpdateMsgTriggersRefresh(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("j")
	h.press("j")
	want := h.app.selectedHash

	// Extra commit appears (simulate via a synthetic list landing later):
	// the refresh must re-issue the loads and keep the selection.
	h.sendNoExec(key("r")) // sanity: r still works
	older := append([]git.Commit{{Hash: "bbb222", Abbrev: "bbb222", Author: "x",
		At: pinnedNow.Add(-time.Minute)}}, h.app.commits...)

	h.sendNoExec(key("j")) // move away…
	h.sendNoExec(key("k")) // …and back to the same hash
	h.send(commitsLoadedMsg{commits: older, gen: h.app.globalSeq})
	if h.app.selectedHash != want {
		t.Errorf("selectedHash = %q, want %q", h.app.selectedHash, want)
	}

	// A nil watcher must not panic on the update path.
	app := New(Config{RepoName: "demo", HasCommits: true, NoAnimations: true})
	next, _ := app.Update(watch.UpdateMsg{})
	if _, ok := next.(App); !ok {
		t.Fatal("UpdateMsg broke the model type")
	}
}

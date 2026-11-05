package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/git/testfixture"
)

// shortenIntervals speeds the watcher up for tests and restores the
// production cadence afterwards.
func shortenIntervals(t *testing.T) {
	t.Helper()
	oldD, oldP := DebounceInterval, PollInterval
	DebounceInterval, PollInterval = 20*time.Millisecond, 15*time.Millisecond
	t.Cleanup(func() { DebounceInterval, PollInterval = oldD, oldP })
}

// expectTick receives one change tick within the timeout.
func expectTick(t *testing.T, w *Watcher, what string) {
	t.Helper()
	select {
	case <-w.C():
	case <-time.After(3 * time.Second):
		t.Fatalf("no %s tick within 3s", what)
	}
}

// expectNoTick asserts silence — the watcher must not fire without a
// repository change.
func expectNoTick(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case <-w.C():
		t.Fatal("unexpected tick without a repository change")
	case <-time.After(120 * time.Millisecond):
	}
}

// A commit made behind the running watcher produces exactly the tick the
// app needs to refresh (AC6).
func TestWatcherTicksOnExternalCommit(t *testing.T) {
	shortenIntervals(t)
	root := testfixture.New(t, testfixture.Basic)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	w := New(repo.GitDir)
	defer w.Close()
	if !w.Start(repo.GitDir, repo.CommonDir()) {
		t.Skip("filesystem watching unavailable on this host")
	}
	expectNoTick(t, w)

	// An external commit: the "agent just committed" moment.
	testfixture.Git(t, root, "commit", "--allow-empty", "-q", "-m", "external")
	expectTick(t, w, "commit")

	// The tick collapsed: no second one is pending.
	expectNoTick(t, w)
}

// Branch updates and staged (index) changes tick as well — the watcher
// covers HEAD, index, refs, and packed-refs.
func TestWatcherTicksOnRefAndIndex(t *testing.T) {
	shortenIntervals(t)
	root := testfixture.New(t, testfixture.Basic)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	w := New(repo.GitDir)
	defer w.Close()
	if !w.Start(repo.GitDir, repo.CommonDir()) {
		t.Skip("filesystem watching unavailable on this host")
	}

	// Stage a change: index write.
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testfixture.Git(t, root, "add", "staged.txt")
	expectTick(t, w, "index")

	// New branch: a loose ref appears under refs/heads.
	testfixture.Git(t, root, "branch", "watched-branch")
	expectTick(t, w, "ref")
}

// When filesystem watching cannot be set up, Start falls back to the
// polling producer and ticks keep flowing.
func TestPollingFallback(t *testing.T) {
	shortenIntervals(t)
	missing := filepath.Join(t.TempDir(), "missing")
	w := New(missing)
	if w.Start(missing, missing) {
		t.Fatal("Start succeeded on a nonexistent directory, want polling fallback")
	}
	defer w.Close()
	expectTick(t, w, "poll")
}

// Close silences the producer.
func TestCloseStopsTicks(t *testing.T) {
	shortenIntervals(t)
	missing := filepath.Join(t.TempDir(), "missing")
	w := New(missing)
	w.Start(missing, missing)
	w.Close()
	// Drain one possibly-pending tick, then demand silence.
	select {
	case <-w.C():
	default:
	}
	expectNoTick(t, w)
}

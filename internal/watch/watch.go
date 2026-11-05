// Package watch reports when a repository changes under us (spec §6.4,
// FR-11): it watches HEAD, the index, packed-refs, and the refs tree via
// fsnotify, collapses event bursts with a short debounce, and falls back
// to polling when filesystem watching is unavailable (network mounts,
// exotic platforms). The viewer reacts to every tick by re-running its
// commit-list and status loads with the selection kept stable by hash.
package watch

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	fs "github.com/fsnotify/fsnotify"
)

// DebounceInterval collapses fsnotify event bursts into one tick: a
// commit fans out into several write events, and each must not trigger
// its own reload. Tests shorten it.
var DebounceInterval = 250 * time.Millisecond

// PollInterval is the fallback cadence when fsnotify is unavailable.
// Tests shorten it.
var PollInterval = 2 * time.Second

// Watcher emits a tick on its channel whenever the repository may have
// changed. Ticks carry no payload: consumers re-read the git state.
type Watcher struct {
	events chan struct{}
	done   chan struct{}
	once   sync.Once

	// debounce and pollInterval are captured from the package vars at
	// Start, so the producer goroutines never read mutable globals.
	debounce     time.Duration
	pollInterval time.Duration
}

// New returns a watcher for the repository rooted at gitDir — the
// worktree-private git directory (a linked worktree keeps its own HEAD
// and index there).
func New(gitDir string) *Watcher {
	return &Watcher{
		events: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// C returns the change-tick channel. Ticks are collapsed: at most one is
// pending, so a slow consumer never queues a backlog.
func (w *Watcher) C() <-chan struct{} { return w.events }

// Close stops the watcher and releases its filesystem watches.
func (w *Watcher) Close() {
	w.once.Do(func() { close(w.done) })
}

// UpdateMsg is the tick the watcher emits when the repository may have
// changed under the running program. The model answers it with its
// generation-guarded refresh (spec §6.4), keeping the selection stable
// by hash.
type UpdateMsg struct{}

// emit delivers one non-blocking tick.
func (w *Watcher) emit() {
	select {
	case w.events <- struct{}{}:
	default: // a tick is already pending: this burst is covered
	}
}

// targets lists the paths to watch for a repository. HEAD and the index
// live in the worktree-private gitdir; refs and packed-refs live in the
// common directory shared by all worktrees. Missing paths (no packed-refs
// yet, no refs/remotes) are skipped.
func targets(gitDir, commonDir string) []string {
	candidates := []string{
		filepath.Join(gitDir, "HEAD"),
		filepath.Join(gitDir, "index"),
		filepath.Join(commonDir, "packed-refs"),
		filepath.Join(commonDir, "refs"),
		filepath.Join(commonDir, "refs", "heads"),
		filepath.Join(commonDir, "refs", "remotes"),
	}
	var out []string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// Start begins watching. It reports whether filesystem watching is
// active; false means the polling fallback took over (and Start may be
// retried after conditions change — though squint does not retry).
func (w *Watcher) Start(gitDir, commonDir string) bool {
	w.debounce = DebounceInterval
	w.pollInterval = PollInterval
	paths := targets(gitDir, commonDir)
	if len(paths) == 0 {
		// Nothing watchable exists (a vanished or never-created repo
		// directory): some backends still accept the Add, so the empty
		// set is checked before touching fsnotify.
		go w.poll()
		return false
	}
	fw, err := fs.NewWatcher()
	if err != nil {
		go w.poll()
		return false
	}
	ok := true
	for _, p := range paths {
		if err := fw.Add(p); err != nil {
			ok = false
			break
		}
	}
	if !ok {
		_ = fw.Close()
		go w.poll()
		return false
	}
	go w.loop(fw)
	return true
}

// loop consumes fsnotify events, debounces bursts into single ticks, and
// extends the watch set when git creates new ref directories. When the
// event channel closes (backend failure), it escalates to polling so the
// app keeps refreshing.
func (w *Watcher) loop(fw *fs.Watcher) {
	defer func() {
		_ = fw.Close()
	}()
	var debounce <-chan time.Time
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-fw.Events:
			if !ok {
				go w.poll() // watcher backend died: fall back, never stall
				return
			}
			// Chmod-only events are noise: git touches index metadata
			// during reads, and reacting would make squint refresh
			// itself forever. Real changes write or rename files.
			if ev.Op&fs.Chmod == ev.Op {
				continue
			}
			if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
				_ = fw.Add(ev.Name) // git created a ref directory mid-flight
			}
			if debounce == nil {
				t := time.NewTimer(w.debounce)
				debounce = t.C
			}
		case err := <-fw.Errors:
			_ = err // transport-level noise: keep watching
		case <-debounce:
			debounce = nil
			w.emit()
		}
	}
}

// poll is the fallback producer: one tick per interval regardless of
// what changed. git reads are cheap and bounded, so a blind poll is
// safe; the consumer's generation guard makes repeats harmless.
func (w *Watcher) poll() {
	t := time.NewTicker(w.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-t.C:
			w.emit()
		}
	}
}

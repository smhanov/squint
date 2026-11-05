// Package watch observes a repository for external changes and emits
// debounced refresh notifications (spec §6.4, FR-11).
//
// It watches HEAD, the index, packed-refs, and the refs tree with
// fsnotify (worktree-aware: HEAD and the index are per-worktree, refs
// are shared via the common directory), collapses event bursts with a
// 250ms debounce, and falls back to a blind 2s poll when filesystem
// watching cannot be set up. The consumer — the root model — reacts to
// every tick with its generation-guarded refresh, so external commits
// land in the list while the selection stays stable by hash.
package watch

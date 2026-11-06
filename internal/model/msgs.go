package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smhanov/squint/internal/diff"
	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/watch"
)

// msgs.go defines the async messages and the tea.Cmd constructors that
// produce them. Every git read happens here — Update never blocks. Every
// message carries the generation captured at request time; Update drops
// messages whose generation is stale (spec §5.1 stale-result guard).

// commitsLoadedMsg carries the (bounded) commit list.
type commitsLoadedMsg struct {
	commits []git.Commit
	err     error
	gen     int
}

// filesLoadedMsg carries the changed files of one commit, identified by
// hash as well as generation.
type filesLoadedMsg struct {
	hash  string
	files []git.FileEntry
	err   error
	gen   int
}

// diffLoadedMsg carries the parsed diff of one commit.
type diffLoadedMsg struct {
	hash  string
	diffs []diff.FileDiff
	err   error
	gen   int
}

// sinceLoadedMsg carries the cumulative base..HEAD diff (FR-10) plus its
// commit count for the banner.
type sinceLoadedMsg struct {
	hash  string // the selection whose since-range was read
	base  string // range base (the selected commit's first parent)
	count int    // commits in base..HEAD; -1 when the count failed
	diffs []diff.FileDiff
	err   error
	gen   int
}

// statusLoadedMsg carries the HEAD state for the status bar.
type statusLoadedMsg struct {
	status git.RepoStatus
	err    error
	gen    int
}

// tickMsg re-renders relative ages; it carries no generation because it
// changes no selection state.
type tickMsg struct{ At time.Time }

// tickCmd arms the next 30-second relative-age refresh.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg {
		return tickMsg{At: time.Now()}
	})
}

// watchCmd drains the repository watcher: one blocked receive per
// external change, surfaced as a watch.UpdateMsg (spec §6.4).
func watchCmd(w *watch.Watcher) tea.Cmd {
	return func() tea.Msg {
		<-w.C()
		return watch.UpdateMsg{}
	}
}

// loadCommitsCmd reads the bounded commit list. It captures the repository
// handle, options, and the global sequence at call time.
func (a App) loadCommitsCmd() tea.Cmd {
	if a.repo == nil {
		return nil
	}
	repo := a.repo
	seq := a.globalSeq
	opts := git.LogOpts{
		N:        a.cfg.N,
		Since:    a.cfg.Since,
		RevRange: a.cfg.RevRange,
		Paths:    a.cfg.Paths,
	}
	return func() tea.Msg {
		ctx := context.Background() // the git runner applies its own timeout
		commits, err := git.ListCommits(ctx, repo, opts)
		return commitsLoadedMsg{commits: commits, err: err, gen: seq}
	}
}

// loadStatusCmd reads the HEAD state.
func (a App) loadStatusCmd() tea.Cmd {
	if a.repo == nil {
		return nil
	}
	repo := a.repo
	seq := a.globalSeq
	return func() tea.Msg {
		ctx := context.Background()
		st, err := git.Status(ctx, repo)
		return statusLoadedMsg{status: st, err: err, gen: seq}
	}
}

// mergeMode resolves how merge commits are shown for the current
// selection state.
func (a App) mergeMode() git.MergeMode {
	if a.firstParent {
		return git.MergeFirstParent
	}
	return git.MergeCombined
}

// diffOpts assembles the raw diff tuning shared by every diff loader.
func (a App) diffOpts() git.DiffOpts {
	return git.DiffOpts{
		IgnoreWhitespace: a.detail.ignoreWS,
		Context:          a.detail.context,
		FirstParent:      a.firstParent,
		WordDiff:         a.detail.wordDiff,
	}
}

// parseDiff selects the parser matching the word-diff option.
func (a App) parseDiff(raw []byte) []diff.FileDiff {
	if a.detail.wordDiff {
		return diff.ParseWordDiff(raw)
	}
	return diff.ParseUnified(raw)
}

// loadFilesCmd reads the changed files of commit, honoring the merge
// first-parent toggle.
func (a App) loadFilesCmd(commit git.Commit) tea.Cmd {
	if a.repo == nil || commit.Hash == "" {
		return nil
	}
	repo := a.repo
	gen := a.gen
	mode := a.mergeMode()
	return func() tea.Msg {
		ctx := context.Background()
		files, err := git.FilesFor(ctx, repo, commit, mode)
		return filesLoadedMsg{hash: commit.Hash, files: files, err: err, gen: gen}
	}
}

// loadDiffCmd reads and parses the diff of commit with the detail pane's
// current viewer options.
func (a App) loadDiffCmd(commit git.Commit) tea.Cmd {
	if a.repo == nil || commit.Hash == "" {
		return nil
	}
	repo := a.repo
	gen := a.gen
	opts := a.diffOpts()
	parse := a.parseDiff
	return func() tea.Msg {
		ctx := context.Background()
		raw, err := git.DiffFor(ctx, repo, commit, opts)
		if err != nil {
			return diffLoadedMsg{hash: commit.Hash, err: err, gen: gen}
		}
		return diffLoadedMsg{hash: commit.Hash, diffs: parse(raw), gen: gen}
	}
}

// loadSinceCmd reads the cumulative diff from commit's first parent to
// HEAD (FR-10) together with the commit count for the banner. The root
// commit has no parent; callers guard with a status-bar hint instead.
func (a App) loadSinceCmd(commit git.Commit) tea.Cmd {
	if a.repo == nil || commit.Hash == "" || len(commit.Parents) == 0 {
		return nil
	}
	repo := a.repo
	gen := a.gen
	base := commit.Parents[0]
	opts := a.diffOpts()
	parse := a.parseDiff
	return func() tea.Msg {
		ctx := context.Background()
		count, countErr := git.CountCommits(ctx, repo, base, "HEAD")
		raw, err := git.RangeDiff(ctx, repo, base, "HEAD", opts)
		if err != nil {
			return sinceLoadedMsg{hash: commit.Hash, base: base, count: -1, err: err, gen: gen}
		}
		n := -1
		if countErr == nil {
			n = count
		}
		return sinceLoadedMsg{hash: commit.Hash, base: base, count: n, diffs: parse(raw), gen: gen}
	}
}

// spinKick re-arms the spinner animation chain. The chain stops itself as
// soon as nothing is loading, so it costs nothing while the UI is idle.
func (a App) spinKick() tea.Cmd {
	if a.cfg.NoAnimations || !a.anyLoading() {
		return nil
	}
	return a.spinner.Tick
}

// anyLoading reports whether any pane is waiting on git.
func (a App) anyLoading() bool {
	return a.commitsLoading || a.statusLoading || a.filesStale() || a.detailStale()
}

// filesStale reports whether the file list shown belongs to the selected
// commit. Staleness is derived, never stored: an in-flight load keeps the
// pane in its loading state until a message with the matching generation
// and hash lands.
func (a App) filesStale() bool {
	return a.selectedHash != "" && a.filesHash != a.selectedHash
}

// detailStale reports whether the content shown belongs to the selected
// commit under the current viewer options, in either mode.
func (a App) detailStale() bool {
	if a.selectedHash == "" {
		return false
	}
	if a.detail.since.on {
		return a.detail.since.forHash != a.selectedHash || a.detail.dirtyOpts
	}
	return a.detail.hash != a.selectedHash || a.detail.dirtyOpts
}

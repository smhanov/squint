package git

import (
	"context"
	"strconv"
	"strings"
)

// FileEntry is one changed file of a commit (git name-status output).
type FileEntry struct {
	// Status is the git name-status letter: A/M/D/R/C/T (and U for
	// unmerged entries in combined output).
	Status byte
	// Path is the file's path at this commit — the new path for renames
	// and copies. Always raw (unquoted), thanks to -z.
	Path string
	// Orig is the previous path for renames and copies.
	Orig string
}

// MergeMode selects how a merge commit is diffed.
type MergeMode int

const (
	// MergeCombined shows the combined diff of merge commits (--cc) and
	// the plain diff for non-merges; it is the default view.
	MergeCombined MergeMode = iota
	// MergeFirstParent shows merges as a full diff against the first
	// parent (--diff-merges=first-parent).
	MergeFirstParent
)

// FilesFor lists the files changed by one commit, sorted by path. Merge
// commits default to the combined view; non-merges are unaffected by the
// mode. The root commit lists its added files (--root), so selecting the
// first commit of a repository shows its initial tree.
func FilesFor(ctx context.Context, repo *Repo, commit Commit, mode MergeMode) ([]FileEntry, error) {
	args := []string{"diff-tree", "-z", "--no-commit-id", "--name-status", "-r", "-M", "--root"}
	if commit.Merge() {
		if mode == MergeFirstParent {
			args = append(args, "--diff-merges=first-parent")
		} else {
			args = append(args, "--cc")
		}
	}
	args = append(args, commit.Hash)

	out, err := NewRunner(repo.Root).Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseFileEntries(out), nil
}

// parseFileEntries parses NUL-terminated name-status records:
//
//	status \0 path \0
//	status \0 orig \0 path \0   (renames and copies; old path first)
//
// Combined (--cc) merge records carry one status letter per parent, e.g.
// "MM"; the first letter wins. Trailing junk is ignored.
func parseFileEntries(out []byte) []FileEntry {
	fields := strings.Split(string(out), "\x00")
	var entries []FileEntry
	for i := 0; i < len(fields); {
		status := fields[i]
		if status == "" || i+1 >= len(fields) {
			break
		}
		if c := status[0]; c == 'R' || c == 'C' {
			if i+2 >= len(fields) {
				break
			}
			entries = append(entries, FileEntry{Status: c, Orig: fields[i+1], Path: fields[i+2]})
			i += 3
			continue
		}
		entries = append(entries, FileEntry{Status: status[0], Path: fields[i+1]})
		i += 2
	}
	return entries
}

// DiffOpts tunes a raw unified diff.
type DiffOpts struct {
	// IgnoreWhitespace passes --ignore-all-space.
	IgnoreWhitespace bool
	// Context is the number of context lines (0–10). Negative values
	// leave git's default (3) in place; 0 collapses all context.
	Context int
	// FirstParent shows merge commits against their first parent instead
	// of the combined view.
	FirstParent bool
}

// DefaultDiffOpts asks for git's defaults.
var DefaultDiffOpts = DiffOpts{Context: -1}

// DiffFor returns the raw unified diff of one commit
// ("show --format= --patch"), ready for diff.ParseUnified. Merge commits
// default to the combined diff unless opts.FirstParent is set.
func DiffFor(ctx context.Context, repo *Repo, commit Commit, opts DiffOpts) ([]byte, error) {
	args := []string{"show", "--format=", "--patch"}
	if commit.Merge() {
		if opts.FirstParent {
			args = append(args, "--diff-merges=first-parent")
		} else {
			args = append(args, "--cc")
		}
	}
	args = append(args, diffOptArgs(opts)...)
	args = append(args, commit.Hash)
	return NewRunner(repo.Root).Run(ctx, args...)
}

// RangeDiff returns the raw unified diff between two revisions — the
// cumulative "everything since this commit" view, where base is typically
// the selected commit's first parent and head is HEAD.
func RangeDiff(ctx context.Context, repo *Repo, base, head string, opts DiffOpts) ([]byte, error) {
	args := append([]string{"diff", "--patch"}, diffOptArgs(opts)...)
	args = append(args, base, head)
	return NewRunner(repo.Root).Run(ctx, args...)
}

// diffOptArgs renders the shared diff tuning flags.
func diffOptArgs(opts DiffOpts) []string {
	var args []string
	if opts.IgnoreWhitespace {
		args = append(args, "--ignore-all-space")
	}
	if opts.Context >= 0 {
		args = append(args, "-U"+strconv.Itoa(opts.Context))
	}
	return args
}

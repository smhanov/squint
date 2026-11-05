package git

import (
	"context"
	"strconv"
	"strings"
)

// RepoStatus is the repository HEAD state shown in the status bar: branch (or
// detached position), ahead/behind against the upstream, and whether the
// tree is dirty.
type RepoStatus struct {
	// Branch is the current branch name; empty when HEAD is detached.
	Branch string
	// Detached is true when HEAD points at a commit, not a branch.
	Detached bool
	// OID is the commit HEAD resolves to; empty on an unborn branch.
	OID string
	// Ahead and Behind count commits relative to the upstream; both zero
	// without one.
	Ahead, Behind int
	// Dirty reports any staged, unstaged, or untracked change.
	Dirty bool
	// Shallow marks a shallow clone (surfaced as a note in the status
	// bar).
	Shallow bool
}

// Status reads the HEAD state with `git status --porcelain=v2 --branch`.
func Status(ctx context.Context, repo *Repo) (RepoStatus, error) {
	out, err := NewRunner(repo.Root).Run(ctx, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return RepoStatus{}, err
	}
	st := parseStatus(out)
	st.Shallow = repo.IsShallow()
	return st, nil
}

// parseStatus parses porcelain=v2 --branch output. Header lines start
// with "# "; every other line is a change entry, which by definition makes
// the tree dirty.
func parseStatus(out []byte) RepoStatus {
	var st RepoStatus
	for line := range strings.SplitSeq(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if oid != "" && oid != "(initial)" {
				st.OID = oid
			}
		case strings.HasPrefix(line, "# branch.head "):
			name := strings.TrimPrefix(line, "# branch.head ")
			if name == "(detached)" {
				st.Detached = true
			} else {
				st.Branch = name
			}
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			// Format: "+<ahead> -<behind>".
			if len(fields) > 0 {
				st.Ahead = parseAB(fields[0])
			}
			if len(fields) > 1 {
				st.Behind = parseAB(fields[1])
			}
		case strings.HasPrefix(line, "#"), line == "":
			// Other headers (branch.upstream, …) and blank lines carry
			// nothing the status bar needs.
		default:
			// Change entries: staged ("1"), renamed ("2"), unmerged
			// ("u"), untracked ("?"). Any of them means dirty.
			st.Dirty = true
		}
	}
	return st
}

// parseAB converts a "+2"/"-1" ahead/behind token into its magnitude.
func parseAB(field string) int {
	n, err := strconv.Atoi(strings.TrimLeft(field, "+-"))
	if err != nil {
		return 0
	}
	return n
}

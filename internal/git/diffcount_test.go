package git

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/squint-tui/squint/internal/diff"
	"github.com/squint-tui/squint/internal/git/testfixture"
)

// WordDiff requests --word-diff=plain output: inline markers instead of
// paired +/- rows for partially changed lines.
func TestDiffForWordDiff(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := ListCommits(t.Context(), repo, LogOpts{})
	if err != nil || len(commits) < 4 {
		t.Fatalf("ListCommits: %v (%d commits)", err, len(commits))
	}
	// The "Fix lane flicker on reload" commit edits a line in place, so
	// word-diff mode must produce inline markers for it.
	var fix Commit
	for _, c := range commits {
		if strings.HasPrefix(c.Subject, "Fix lane") {
			fix = c
			break
		}
	}
	if fix.Hash == "" {
		t.Fatal("fixture commit not found")
	}

	raw, err := DiffFor(t.Context(), repo, fix, DiffOpts{Context: 3, WordDiff: true})
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	if !strings.Contains(out, "{+") {
		t.Errorf("word-diff output has no inline markers: %q", out[:min(300, len(out))])
	}
	files := diff.ParseWordDiff(raw)
	if len(files) == 0 || len(files[0].Hunks) == 0 {
		t.Fatal("ParseWordDiff produced no hunks for the word-diff output")
	}
	var words int
	for _, h := range files[0].Hunks {
		for _, l := range h.Lines {
			if l.HasWords() {
				words++
			}
		}
	}
	if words == 0 {
		t.Error("no parsed line carries word segments")
	}
}

// CountCommits counts base..head; the full range from the root commit
// counts every commit after it.
func TestCountCommits(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := ListCommits(t.Context(), repo, LogOpts{})
	if err != nil || len(commits) < 3 {
		t.Fatalf("ListCommits: %v (%d commits)", err, len(commits))
	}
	newest := commits[0]
	rootCommit := commits[len(commits)-1]
	if len(rootCommit.Parents) != 0 {
		t.Fatalf("last commit of a linear fixture has parents %v", rootCommit.Parents)
	}

	n, err := CountCommits(t.Context(), repo, rootCommit.Hash, newest.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(commits)-1 {
		t.Errorf("CountCommits(root..newest) = %d, want %d", n, len(commits)-1)
	}

	// Adjacent commits bound an empty range.
	n, err = CountCommits(t.Context(), repo, newest.Hash, newest.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("CountCommits(x..x) = %d, want 0", n)
	}
}

// CommonDir: a normal repository shares its gitdir; a linked worktree
// resolves the commondir file.
func TestCommonDir(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := repo.CommonDir(); got != repo.GitDir {
		t.Errorf("CommonDir = %q, want the gitdir %q", got, repo.GitDir)
	}

	// Create a linked worktree and check from its side.
	testfixture.Git(t, root, "worktree", "add", "-q", "../wt", "-b", "wt-branch")
	wtrepo, err := DiscoverRepo(root + "/../wt")
	if err != nil {
		t.Fatal(err)
	}
	common := wtrepo.CommonDir()
	// git records the commondir with symlinks resolved (/var → /private/var
	// on macOS), so compare resolved paths.
	realCommon, err := filepath.EvalSymlinks(common)
	if err != nil {
		t.Fatal(err)
	}
	realGitDir, err := filepath.EvalSymlinks(repo.GitDir)
	if err != nil {
		t.Fatal(err)
	}
	if realCommon != realGitDir {
		t.Errorf("worktree CommonDir = %q, want main gitdir %q", common, repo.GitDir)
	}
	if n := strings.Count(filepath.ToSlash(wtrepo.GitDir), "worktrees"); n == 0 {
		t.Errorf("worktree gitdir %q does not record a worktrees dir", wtrepo.GitDir)
	}
}

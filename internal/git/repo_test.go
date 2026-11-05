package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitPath returns the git executable, skipping the test when git is not
// installed.
func gitPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found in PATH")
	}
	return path
}

// mustGit runs a git command in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitPath(t), args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// initRepo creates an empty repository in a fresh temp directory and returns
// its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", ".")
	return dir
}

// commitAll makes an empty commit in dir so HEAD exists.
func commitAll(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir,
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "--allow-empty", "-q", "-m", "initial commit")
}

func TestDiscoverRepo(t *testing.T) {
	root := initRepo(t)

	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) failed: %v", root, err)
	}
	if repo.Root != root {
		t.Errorf("Root = %q, want %q", repo.Root, root)
	}
	if repo.GitDir != filepath.Join(root, ".git") {
		t.Errorf("GitDir = %q, want %q", repo.GitDir, filepath.Join(root, ".git"))
	}

	// Discovery from a nested directory walks up to the same root.
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err = DiscoverRepo(nested)
	if err != nil {
		t.Fatalf("DiscoverRepo(nested) failed: %v", err)
	}
	if repo.Root != root {
		t.Errorf("nested Root = %q, want %q", repo.Root, root)
	}

	// A relative start resolves to an absolute root.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) }) //nolint:errcheck // restoring test cwd
	repo, err = DiscoverRepo(".")
	if err != nil {
		t.Fatalf("DiscoverRepo(\".\") failed: %v", err)
	}
	// The working directory may traverse a symlink (macOS /var ->
	// /private/var), so compare against the resolved root.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(repo.Root) || repo.Root != resolved {
		t.Errorf("relative Root = %q, want %q", repo.Root, resolved)
	}
}

func TestDiscoverWorktree(t *testing.T) {
	main := initRepo(t)
	commitAll(t, main)

	wt := filepath.Join(t.TempDir(), "linked")
	mustGit(t, main, "worktree", "add", "-q", wt, "-b", "topic")

	repo, err := DiscoverRepo(wt)
	if err != nil {
		t.Fatalf("DiscoverRepo(worktree) failed: %v", err)
	}
	if repo.Root != wt {
		t.Errorf("Root = %q, want %q", repo.Root, wt)
	}
	// The .git file points into the main repository's worktrees directory.
	if !strings.Contains(repo.GitDir, filepath.Join(".git", "worktrees")) {
		t.Errorf("GitDir = %q, want a path under .git/worktrees", repo.GitDir)
	}
	if !fileExists(filepath.Join(repo.GitDir, "HEAD")) {
		t.Errorf("GitDir %q has no HEAD file", repo.GitDir)
	}
}

func TestDiscoverNotARepo(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, start := range []string{dir, nested} {
		_, err := DiscoverRepo(start)
		if !errors.Is(err, ErrNotARepo) {
			t.Errorf("DiscoverRepo(%q) error = %v, want ErrNotARepo", start, err)
		}
	}
}

func TestHasCommits(t *testing.T) {
	t.Run("EmptyRepo", func(t *testing.T) {
		root := initRepo(t)
		repo, err := DiscoverRepo(root)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.HasCommits()
		if err != nil {
			t.Fatalf("HasCommits: %v", err)
		}
		if got {
			t.Error("HasCommits = true for a fresh repository, want false")
		}
	})

	t.Run("AfterCommit", func(t *testing.T) {
		root := initRepo(t)
		commitAll(t, root)
		repo, err := DiscoverRepo(root)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.HasCommits()
		if err != nil {
			t.Fatalf("HasCommits: %v", err)
		}
		if !got {
			t.Error("HasCommits = false after a commit, want true")
		}
	})

	t.Run("PackedRefs", func(t *testing.T) {
		root := initRepo(t)
		commitAll(t, root)
		mustGit(t, root, "pack-refs", "--all")
		repo, err := DiscoverRepo(root)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.HasCommits()
		if err != nil {
			t.Fatalf("HasCommits: %v", err)
		}
		if !got {
			t.Error("HasCommits = false with packed refs, want true")
		}
	})

	t.Run("Worktree", func(t *testing.T) {
		main := initRepo(t)
		commitAll(t, main)
		wt := filepath.Join(t.TempDir(), "linked")
		mustGit(t, main, "worktree", "add", "-q", wt, "-b", "topic")

		repo, err := DiscoverRepo(wt)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.HasCommits()
		if err != nil {
			t.Fatalf("HasCommits: %v", err)
		}
		if !got {
			t.Error("HasCommits = false in a linked worktree, want true")
		}
	})
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  VersionInfo
		wantK bool
	}{
		{"plain", "git version 2.55.0", VersionInfo{Raw: "git version 2.55.0", Major: 2, Minor: 55}, true},
		{"apple suffix", "git version 2.39.2 (Apple Git-143)", VersionInfo{Raw: "git version 2.39.2 (Apple Git-143)", Major: 2, Minor: 39}, true},
		{"short", "git version 2.20", VersionInfo{Raw: "git version 2.20", Major: 2, Minor: 20}, true},
		{"major 3", "git version 3.0.1", VersionInfo{Raw: "git version 3.0.1", Major: 3, Minor: 0}, true},
		{"no version", "garbage", VersionInfo{}, false},
		{"empty", "", VersionInfo{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseVersion(tt.line)
			if ok != tt.wantK {
				t.Fatalf("ParseVersion(%q) ok = %v, want %v", tt.line, ok, tt.wantK)
			}
			if got != tt.want {
				t.Errorf("ParseVersion(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		major, minor int
		minMaj, min  int
		want         bool
	}{
		{2, 20, 2, 20, true},  // exactly the minimum
		{2, 19, 2, 20, false}, // just below
		{2, 55, 2, 20, true},
		{3, 0, 2, 20, true},
		{1, 9, 2, 20, false},
	}
	for _, tt := range tests {
		v := VersionInfo{Major: tt.major, Minor: tt.minor}
		if got := v.AtLeast(tt.minMaj, tt.min); got != tt.want {
			t.Errorf("Version(%d.%d).AtLeast(%d, %d) = %v, want %v",
				tt.major, tt.minor, tt.minMaj, tt.min, got, tt.want)
		}
	}
}

func TestVersionProbe(t *testing.T) {
	gitPath(t) // skip when git is absent
	info, err := Version()
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	if info.Raw == "" {
		t.Error("Raw is empty")
	}
	if !info.AtLeast(1, 0) {
		t.Errorf("parsed implausible version %d.%d from %q", info.Major, info.Minor, info.Raw)
	}
}

func TestRunner(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		gitPath(t)
		out, err := NewRunner("").Run("--version")
		if err != nil {
			t.Fatalf("Run(--version) failed: %v", err)
		}
		if !strings.Contains(string(out), "git version") {
			t.Errorf("output = %q, want it to contain %q", out, "git version")
		}
	})

	t.Run("GitError", func(t *testing.T) {
		root := initRepo(t)
		_, err := NewRunner(root).Run("rev-parse", "--verify", "no-such-ref")
		var gitErr *GitError
		if !errors.As(err, &gitErr) {
			t.Fatalf("error = %T (%v), want *GitError", err, err)
		}
		if gitErr.ExitCode == 0 {
			t.Error("ExitCode = 0, want non-zero")
		}
		if strings.TrimSpace(gitErr.Stderr) == "" {
			t.Error("Stderr is empty for a failed rev-parse")
		}
		if gitErr.Error() == "" {
			t.Error("Error() is empty")
		}
	})
}

package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smhanov/squint/internal/git/testfixture"
)

func TestStatusClean(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	st, err := Status(context.Background(), repo)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Branch != "main" {
		t.Errorf("Branch = %q, want main", st.Branch)
	}
	if st.Detached {
		t.Error("Detached = true on a clean checkout")
	}
	if st.Dirty {
		t.Error("Dirty = true for a clean tree")
	}
	if st.Shallow {
		t.Error("Shallow = true for a full clone")
	}
	if st.Ahead != 0 || st.Behind != 0 {
		t.Errorf("Ahead/Behind = %d/%d, want 0/0 without upstream", st.Ahead, st.Behind)
	}

	// OID matches HEAD.
	head := strings.TrimSpace(testfixture.Git(t, root, "rev-parse", "HEAD"))
	if st.OID != head {
		t.Errorf("OID = %q, want %q", st.OID, head)
	}
}

func TestStatusEmptyRepo(t *testing.T) {
	root := testfixture.New(t, testfixture.Empty)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	st, err := Status(context.Background(), repo)
	if err != nil {
		t.Fatalf("Status on an empty repository: %v", err)
	}
	if st.Branch != "main" {
		t.Errorf("Branch = %q, want main (unborn)", st.Branch)
	}
	if st.OID != "" {
		t.Errorf("OID = %q, want empty on an unborn branch", st.OID)
	}
	if st.Dirty {
		t.Error("Dirty = true for an empty repository")
	}
}

func TestStatusDirty(t *testing.T) {
	t.Run("ModifiedTrackedFile", func(t *testing.T) {
		root := testfixture.New(t, testfixture.Basic)
		// Touch a tracked file without committing — plain filesystem
		// noise, no git needed.
		if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		repo, _ := DiscoverRepo(root)
		st, err := Status(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Dirty {
			t.Error("Dirty = false after modifying a tracked file")
		}
	})

	t.Run("StagedFile", func(t *testing.T) {
		root := testfixture.New(t, testfixture.Basic)
		if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testfixture.Git(t, root, "add", "staged.txt")
		repo, _ := DiscoverRepo(root)
		st, err := Status(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Dirty {
			t.Error("Dirty = false with a staged file")
		}
	})

	t.Run("UntrackedFile", func(t *testing.T) {
		root := testfixture.New(t, testfixture.Basic)
		if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		repo, _ := DiscoverRepo(root)
		st, err := Status(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Dirty {
			t.Error("Dirty = false with an untracked file")
		}
	})
}

func TestStatusDetached(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	testfixture.Git(t, root, "checkout", "-q", "--detach", "HEAD~1")

	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Status(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Detached {
		t.Error("Detached = false after a detached checkout")
	}
	if st.Branch != "" {
		t.Errorf("Branch = %q, want empty when detached", st.Branch)
	}
	head := strings.TrimSpace(testfixture.Git(t, root, "rev-parse", "HEAD"))
	if st.OID != head {
		t.Errorf("OID = %q, want the detached position %q", st.OID, head)
	}
}

func TestStatusAheadBehind(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)

	// A local bare "remote" and an upstream-tracking push.
	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	testfixture.Git(t, origin, "init", "-q", "--bare")
	testfixture.Git(t, root, "remote", "add", "origin", origin)
	testfixture.Git(t, root, "push", "-q", "-u", "origin", "HEAD:refs/heads/main")

	// One local commit that origin lacks: ahead 1, behind 0.
	if err := os.WriteFile(filepath.Join(root, "local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testfixture.Git(t, root, "add", "local.txt")
	testfixture.Git(t, root, "commit", "-q", "-m", "local-only commit")

	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Status(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Errorf("Ahead/Behind = %d/%d, want 1/0", st.Ahead, st.Behind)
	}
}

func TestStatusShallow(t *testing.T) {
	root := testfixture.New(t, testfixture.Shallow)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	st, err := Status(context.Background(), repo)
	if err != nil {
		t.Fatalf("Status on a shallow clone: %v", err)
	}
	if !st.Shallow {
		t.Error("Shallow = false for a depth-1 clone")
	}
	if st.Branch != "main" {
		t.Errorf("Branch = %q, want main", st.Branch)
	}
}

// TestParseStatus feeds crafted porcelain=v2 output directly.
func TestParseStatus(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want RepoStatus
	}{
		{
			name: "empty output",
			in:   "",
			want: RepoStatus{},
		},
		{
			name: "clean branch",
			in:   "# branch.oid abc123\n# branch.head main\n",
			want: RepoStatus{Branch: "main", OID: "abc123"},
		},
		{
			name: "unborn branch",
			in:   "# branch.oid (initial)\n# branch.head master\n",
			want: RepoStatus{Branch: "master"},
		},
		{
			name: "detached",
			in:   "# branch.oid deadbee\n# branch.head (detached)\n",
			want: RepoStatus{Detached: true, OID: "deadbee"},
		},
		{
			name: "ahead and behind",
			in:   "# branch.oid abc\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +2 -7\n",
			want: RepoStatus{Branch: "main", OID: "abc", Ahead: 2, Behind: 7},
		},
		{
			name: "staged change is dirty",
			in:   "# branch.oid abc\n# branch.head main\n1 .M N... 100644 100644 100644 h1 h2 f.txt\n",
			want: RepoStatus{Branch: "main", OID: "abc", Dirty: true},
		},
		{
			name: "untracked entry is dirty",
			in:   "# branch.oid abc\n# branch.head main\n? new.txt\n",
			want: RepoStatus{Branch: "main", OID: "abc", Dirty: true},
		},
		{
			name: "unmerged entry is dirty",
			in:   "# branch.oid abc\n# branch.head main\nu U U N... h1 h2 h3 f.txt\n",
			want: RepoStatus{Branch: "main", OID: "abc", Dirty: true},
		},
		{
			name: "upstream header alone is not dirty",
			in:   "# branch.oid abc\n# branch.head main\n# branch.upstream origin/main\n",
			want: RepoStatus{Branch: "main", OID: "abc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStatus([]byte(tt.in))
			if got != tt.want {
				t.Errorf("parseStatus = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseAB(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"+0", 0}, {"+2", 2}, {"-7", 7}, {"+13", 13}, {"x", 0}, {"", 0},
	}
	for _, tt := range tests {
		if got := parseAB(tt.in); got != tt.want {
			t.Errorf("parseAB(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

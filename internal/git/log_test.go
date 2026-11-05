package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/squint-tui/squint/internal/git/testfixture"
)

// listCommits is ListCommits with default options, failing the test on
// error.
func listCommits(t *testing.T, root string, opts LogOpts) []Commit {
	t.Helper()
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatalf("DiscoverRepo: %v", err)
	}
	commits, err := ListCommits(context.Background(), repo, opts)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	return commits
}

func TestListCommitsBasic(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	commits := listCommits(t, root, LogOpts{})

	if len(commits) != 5 {
		t.Fatalf("got %d commits, want 5", len(commits))
	}

	// Newest first, with the pinned fixture subjects.
	wantSubjects := []string{
		"Add logo, drop notes, settle script mode",
		"Rename render.go to canvas.go",
		"Add renderer and run script",
		"Fix lane flicker on reload",
		"Initial commit",
	}
	for i, want := range wantSubjects {
		if commits[i].Subject != want {
			t.Errorf("commits[%d].Subject = %q, want %q", i, commits[i].Subject, want)
		}
	}

	// Full hashes are 40 hex characters; abbreviations are shorter and
	// prefix them.
	for i, c := range commits {
		if len(c.Hash) != 40 {
			t.Errorf("commits[%d].Hash = %q, want 40 hex characters", i, c.Hash)
		}
		if !strings.HasPrefix(c.Hash, c.Abbrev) || len(c.Abbrev) == 0 || len(c.Abbrev) >= len(c.Hash) {
			t.Errorf("commits[%d].Abbrev = %q is not a prefix of %q", i, c.Abbrev, c.Hash)
		}
	}

	// The parent chain: each commit's single parent is the previous one,
	// the root commit has none.
	if parents := commits[4].Parents; len(parents) != 0 {
		t.Errorf("root commit Parents = %v, want none", parents)
	}
	for i := range 4 {
		if parents := commits[i].Parents; len(parents) != 1 || parents[0] != commits[i+1].Hash {
			t.Errorf("commits[%d].Parents = %v, want [%s]", i, parents, commits[i+1].Hash)
		}
		if commits[i].Merge() {
			t.Errorf("commits[%d] (%s) reported Merge, want a linear history", i, commits[i].Subject)
		}
	}

	// Pinned timestamps: the fixture's n-th commit is epochBase + n
	// minutes (2025-01-01T00:00:00Z plus one minute per commit).
	base := time.Unix(1735689600, 0).UTC()
	for i, c := range commits {
		n := 5 - i // newest commit is the fifth
		want := base.Add(time.Duration(n) * time.Minute)
		if !c.At.Equal(want) {
			t.Errorf("commits[%d].At = %s, want %s", i, c.At, want)
		}
	}

	// Fixture identity.
	for _, c := range commits {
		if c.Author != "Squint Fixture" || c.Email != "fixture@squint.test" {
			t.Errorf("author = %q <%q>, want the pinned fixture identity", c.Author, c.Email)
		}
	}
}

func TestListCommitsMerge(t *testing.T) {
	root := testfixture.New(t, testfixture.Merges)
	commits := listCommits(t, root, LogOpts{})

	if len(commits) != 6 {
		t.Fatalf("got %d commits, want 6", len(commits))
	}
	merge := commits[0]
	if merge.Subject != "Merge branch 'feature'" {
		t.Fatalf("newest commit = %q, want the merge", merge.Subject)
	}
	if len(merge.Parents) != 2 {
		t.Fatalf("merge Parents = %v, want two", merge.Parents)
	}
	if !merge.Merge() {
		t.Error("Merge() = false for a two-parent commit")
	}

	// First parent is the pre-merge main tip, second the feature tip.
	var mainTip, featureTip Commit
	for _, c := range commits {
		switch c.Subject {
		case "Add main file":
			mainTip = c
		case "Add feature file":
			featureTip = c
		}
	}
	if mainTip.Hash == "" || featureTip.Hash == "" {
		t.Fatalf("branch tips not found in %v", commits)
	}
	if merge.Parents[0] != mainTip.Hash || merge.Parents[1] != featureTip.Hash {
		t.Errorf("merge Parents = %v, want [%s, %s]", merge.Parents, mainTip.Hash, featureTip.Hash)
	}
}

func TestListCommitsLongBody(t *testing.T) {
	root := testfixture.New(t, testfixture.LongMsg)
	commits := listCommits(t, root, LogOpts{})

	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	long := commits[0]
	if long.Subject != "Refactor the ingest pipeline" {
		t.Errorf("Subject = %q, want the pipeline subject", long.Subject)
	}

	// The body must survive verbatim: every paragraph, unicode runes,
	// punctuation, and the trailing footer — multi-paragraph LLM-style
	// messages are the core use case.
	for _, want := range []string{
		"The old pipeline re-read the whole buffer for every lane, which",
		"larger repositories.",
		"Second paragraph: keep the public API stable and swap the internals",
		"— lanes now stream, and the buffer is",
		"(¶ counts as unicode too: ✓).",
		"Third paragraph: footer notes such as review hints stay in the raw",
	} {
		if !strings.Contains(long.Body, want) {
			t.Errorf("Body misses %q\nfull body:\n%s", want, long.Body)
		}
	}
	if strings.Count(long.Body, "\n\n") < 2 {
		t.Errorf("Body = %q, want at least three paragraphs", long.Body)
	}
	if !strings.HasSuffix(long.Body, "never truncate them.\n") {
		t.Errorf("Body does not end with the footer + trailing newline: %q", long.Body)
	}
	// The body excludes the subject line and its separator blank line.
	if strings.Contains(long.Body, "Refactor the ingest pipeline") {
		t.Errorf("Body repeats the subject: %q", long.Body)
	}

	// The unicode author identity round-trips.
	if long.Author != "李雷" || long.Email != "lei@example.com" {
		t.Errorf("author = %q <%q>, want 李雷 <lei@example.com>", long.Author, long.Email)
	}
}

func TestListCommitsHead(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	commits := listCommits(t, root, LogOpts{})

	if !commits[0].IsHead {
		t.Error("newest commit not marked as HEAD")
	}
	head := 0
	for _, c := range commits {
		if c.IsHead {
			head++
		}
	}
	if head != 1 {
		t.Errorf("%d commits marked as HEAD, want exactly one", head)
	}
}

func TestListCommitsWindowAndFilters(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)

	t.Run("N", func(t *testing.T) {
		if got := listCommits(t, root, LogOpts{N: 2}); len(got) != 2 {
			t.Errorf("N=2 gave %d commits, want 2", len(got))
		}
		if got := listCommits(t, root, LogOpts{N: 99}); len(got) != 5 {
			t.Errorf("N=99 gave %d commits, want all 5", len(got))
		}
		if got := listCommits(t, root, LogOpts{N: 0}); len(got) != 5 {
			t.Errorf("unset N gave %d commits, want the default window (all 5)", len(got))
		}
	})

	t.Run("Paths", func(t *testing.T) {
		got := listCommits(t, root, LogOpts{Paths: []string{"src/app.go"}})
		if len(got) != 2 {
			t.Fatalf("src/app.go gave %d commits, want 2", len(got))
		}
		if got[0].Subject != "Fix lane flicker on reload" || got[1].Subject != "Initial commit" {
			t.Errorf("path filter subjects = [%q, %q], want the two src/app.go edits",
				got[0].Subject, got[1].Subject)
		}
	})

	t.Run("Since", func(t *testing.T) {
		// After the third fixture commit (epochBase + 3 minutes), the two
		// newest commits remain.
		cutoff := time.Unix(1735689600+4*60, 0).UTC().Format(time.RFC3339)
		got := listCommits(t, root, LogOpts{Since: cutoff})
		if len(got) != 2 {
			t.Fatalf("since %s gave %d commits, want 2", cutoff, len(got))
		}
		if got[0].Subject != "Add logo, drop notes, settle script mode" ||
			got[1].Subject != "Rename render.go to canvas.go" {
			t.Errorf("since filter subjects = [%q, %q]", got[0].Subject, got[1].Subject)
		}
	})

	t.Run("RevRange", func(t *testing.T) {
		got := listCommits(t, root, LogOpts{RevRange: "HEAD~2..HEAD"})
		if len(got) != 2 {
			t.Fatalf("HEAD~2..HEAD gave %d commits, want 2", len(got))
		}
	})
}

func TestListCommitsEmptyRepo(t *testing.T) {
	root := testfixture.New(t, testfixture.Empty)

	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := ListCommits(context.Background(), repo, LogOpts{})
	if err != nil {
		t.Fatalf("ListCommits on an empty repository: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("ListCommits on an empty repository = %v, want an empty list", commits)
	}
}

func TestListCommitsError(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ListCommits(context.Background(), repo, LogOpts{RevRange: "no-such-ref..also-bogus"})
	var gitErr *GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("error = %T (%v), want *GitError", err, err)
	}
	if gitErr.ExitCode != 128 {
		t.Errorf("ExitCode = %d, want 128 for a bad revision range", gitErr.ExitCode)
	}
}

// TestParseLog feeds crafted -z record bytes directly: separator layout,
// trailing NUL, empty records, and a body containing the field separator.
func TestParseLog(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Commit
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "single record with trailing NUL",
			in:   "abc\x1fabc123\x1f\x1fA U Thör\x1fa@x\x1f100\x1fdo it\x1f\x1e\x00",
			want: []Commit{{
				Hash: "abc", Abbrev: "abc123", Parents: nil,
				Author: "A U Thör", Email: "a@x",
				At:      time.Unix(100, 0).UTC(),
				Subject: "do it", Body: "",
			}},
		},
		{
			name: "two records, NUL between",
			in: "h1\x1fa1\x1fp1\x1fB\x1fb@x\x1f200\x1fs1\x1fb1\x1e\x00" +
				"h2\x1fa2\x1f\x1fC\x1fc@x\x1f300\x1fs2\x1f\x1e\x00",
			want: []Commit{
				{Hash: "h1", Abbrev: "a1", Parents: []string{"p1"}, Author: "B", Email: "b@x",
					At: time.Unix(200, 0).UTC(), Subject: "s1", Body: "b1"},
				{Hash: "h2", Abbrev: "a2", Parents: nil, Author: "C", Email: "c@x",
					At: time.Unix(300, 0).UTC(), Subject: "s2", Body: ""},
			},
		},
		{
			name: "empty record skipped",
			in:   "\x1e\x00h\x1fa\x1f\x1fN\x1fn@x\x1f5\x1fs\x1f\x1e\x00",
			want: []Commit{{
				Hash: "h", Abbrev: "a", Author: "N", Email: "n@x",
				At: time.Unix(5, 0).UTC(), Subject: "s", Body: "",
			}},
		},
		{
			name: "field separator inside body stays in the body",
			in:   "h\x1fa\x1f\x1fN\x1fn@x\x1f5\x1fs\x1fkeep\x1fthis\x1e\x00",
			want: []Commit{{
				Hash: "h", Abbrev: "a", Author: "N", Email: "n@x",
				At: time.Unix(5, 0).UTC(), Subject: "s", Body: "keep\x1fthis",
			}},
		},
		{
			name: "malformed records skipped leniently",
			in:   "nonsense-without-separators\x1e\x00h\x1fa\x1f\x1fN\x1fn@x\x1fnope\x1fs\x1f\x1e\x00",
			want: nil, // first record lacks fields, second has a bad epoch
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLog([]byte(tt.in))
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("parseLog = %+v, want %+v", got, tt.want)
			}
		})
	}
}

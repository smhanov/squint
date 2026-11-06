package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/smhanov/squint/internal/diff"
	"github.com/smhanov/squint/internal/git/testfixture"
)

// fixtureCommits lists the history of a fixture root, newest first.
func fixtureCommits(t *testing.T, root string) []Commit {
	t.Helper()
	return listCommits(t, root, LogOpts{})
}

// filesFor is FilesFor against the newest commit matching wantSubject.
func filesFor(t *testing.T, root, wantSubject string, mode MergeMode) []FileEntry {
	t.Helper()
	for _, c := range fixtureCommits(t, root) {
		if c.Subject == wantSubject {
			repo, err := DiscoverRepo(root)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := FilesFor(context.Background(), repo, c, mode)
			if err != nil {
				t.Fatalf("FilesFor(%q): %v", wantSubject, err)
			}
			return entries
		}
	}
	t.Fatalf("no commit %q found", wantSubject)
	return nil
}

func TestFilesForBasic(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)

	t.Run("RootCommit", func(t *testing.T) {
		got := filesFor(t, root, "Initial commit", MergeCombined)
		want := []FileEntry{
			{Status: 'A', Path: "README.md"},
			{Status: 'A', Path: "notes.txt"},
			{Status: 'A', Path: "src/app.go"},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("FilesFor(root) = %v, want %v", got, want)
		}
	})

	t.Run("Modified", func(t *testing.T) {
		got := filesFor(t, root, "Fix lane flicker on reload", MergeCombined)
		want := []FileEntry{{Status: 'M', Path: "src/app.go"}}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("FilesFor = %v, want %v", got, want)
		}
	})

	t.Run("Added", func(t *testing.T) {
		got := filesFor(t, root, "Add renderer and run script", MergeCombined)
		want := []FileEntry{
			{Status: 'A', Path: "src/renderer/render.go"},
			{Status: 'A', Path: "tools/run.sh"},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("FilesFor = %v, want %v", got, want)
		}
	})

	t.Run("Renamed", func(t *testing.T) {
		got := filesFor(t, root, "Rename render.go to canvas.go", MergeCombined)
		want := []FileEntry{{
			Status: 'R',
			Path:   "src/renderer/canvas.go",
			Orig:   "src/renderer/render.go",
		}}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("FilesFor = %v, want %v", got, want)
		}
	})

	t.Run("BinaryModeChangeAndDelete", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// Windows records no exec bit (core.fileMode is a no-op
			// there), so the mode change yields no diff entry.
			t.Skip("mode changes are unrepresentable on Windows")
		}
		got := filesFor(t, root, "Add logo, drop notes, settle script mode", MergeCombined)
		want := []FileEntry{
			{Status: 'A', Path: "assets/logo.bin"},
			{Status: 'D', Path: "notes.txt"},
			{Status: 'M', Path: "tools/run.sh"},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("FilesFor = %v, want %v", got, want)
		}
	})
}

// Submodule entries carry their gitlink sha for the files pane.
func TestFilesForSubmodule(t *testing.T) {
	root := testfixture.New(t, testfixture.Submodule)
	entries := filesFor(t, root, "Pin submodule at inner v1", MergeCombined)
	for _, e := range entries {
		if e.Path != "vendor/lib" {
			continue
		}
		if e.Submodule == "" {
			t.Fatal("vendor/lib has no gitlink sha")
		}
		if len(e.Submodule) != 40 {
			t.Errorf("Submodule = %q, want a full 40-char hash", e.Submodule)
		}
		return
	}
	t.Fatalf("vendor/lib missing from entries: %v", entries)
}

// TreeInfo reads modes, types, shas, and sizes in one batched call.
func TestTreeInfo(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := ListCommits(context.Background(), repo, LogOpts{N: 1})
	if err != nil {
		t.Fatal(err)
	}
	info, err := TreeInfo(context.Background(), repo, commits[0], []string{"assets/logo.bin", "no-such-path"})
	if err != nil {
		t.Fatal(err)
	}
	te, ok := info["assets/logo.bin"]
	if !ok {
		t.Fatal("assets/logo.bin missing from TreeInfo")
	}
	if te.Type != "blob" || te.Size <= 0 {
		t.Errorf("entry = %+v, want a blob with a positive size", te)
	}
	if _, ok := info["no-such-path"]; ok {
		t.Error("unknown path present in TreeInfo")
	}
}

func TestFilesForUnicodePaths(t *testing.T) {
	root := testfixture.New(t, testfixture.UnicodePaths)

	// Paths must round-trip raw: no C-style quoting, no escaped octal.
	got := filesFor(t, root, "Add unicode paths", MergeCombined)
	want := []FileEntry{
		{Status: 'A', Path: "folder with space/file name.txt"},
		{Status: 'A', Path: "документы/отчёт.md"},
		{Status: 'A', Path: "路径/文件.txt"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("FilesFor = %v, want %v", got, want)
	}

	got = filesFor(t, root, "Update unicode file", MergeCombined)
	want = []FileEntry{{Status: 'M', Path: "路径/文件.txt"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("FilesFor = %v, want %v", got, want)
	}
}

func TestFilesForMergeModes(t *testing.T) {
	root := testfixture.New(t, testfixture.Merges)

	t.Run("DefaultIsCombined", func(t *testing.T) {
		// The combined view only lists files that differ from every
		// parent — the conflict resolution of shared.txt.
		got := filesFor(t, root, "Merge branch 'feature'", MergeCombined)
		want := []FileEntry{{Status: 'M', Path: "shared.txt"}}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("combined FilesFor = %v, want %v", got, want)
		}
	})

	t.Run("FirstParent", func(t *testing.T) {
		// Against the first parent the merge carries everything the
		// feature side changed.
		got := filesFor(t, root, "Merge branch 'feature'", MergeFirstParent)
		want := []FileEntry{
			{Status: 'A', Path: "feature-only.txt"},
			{Status: 'M', Path: "shared.txt"},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("first-parent FilesFor = %v, want %v", got, want)
		}
	})
}

func TestFilesForError(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	bogus := Commit{Hash: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}
	_, err = FilesFor(context.Background(), repo, bogus, MergeCombined)
	var gitErr *GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("error = %T (%v), want *GitError", err, err)
	}
}

func TestDiffFor(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	commits := fixtureCommits(t, root)

	t.Run("PatchContent", func(t *testing.T) {
		out, err := DiffFor(context.Background(), repo, commits[3], DefaultDiffOpts) // "Fix lane flicker on reload"
		if err != nil {
			t.Fatalf("DiffFor: %v", err)
		}
		text := string(out)
		for _, want := range []string{"diff --git a/src/app.go", "@@ -1,3 +1,5 @@", `+	println("lane fixed")`} {
			if !strings.Contains(text, want) {
				t.Errorf("diff misses %q:\n%s", want, text)
			}
		}
		// --format= strips the message.
		if strings.Contains(text, "Fix lane flicker") {
			t.Errorf("diff leaks the commit message:\n%s", text)
		}
	})

	t.Run("RootCommitShowsFullPatch", func(t *testing.T) {
		out, err := DiffFor(context.Background(), repo, commits[4], DefaultDiffOpts) // "Initial commit"
		if err != nil {
			t.Fatalf("DiffFor on the root commit: %v", err)
		}
		text := string(out)
		if !strings.Contains(text, "new file mode") || !strings.Contains(text, "b/notes.txt") {
			t.Errorf("root diff misses added files:\n%s", text)
		}
	})

	t.Run("BinaryFile", func(t *testing.T) {
		out, err := DiffFor(context.Background(), repo, commits[0], DefaultDiffOpts)
		if err != nil {
			t.Fatalf("DiffFor: %v", err)
		}
		if !strings.Contains(string(out), "Binary files /dev/null and b/assets/logo.bin differ") {
			t.Errorf("binary file not marked as binary:\n%s", out)
		}
	})

	t.Run("ContextZero", func(t *testing.T) {
		out, err := DiffFor(context.Background(), repo, commits[3], DiffOpts{Context: 0})
		if err != nil {
			t.Fatalf("DiffFor: %v", err)
		}
		// With no context lines the patch body has no space-prefixed
		// lines; the hunk shrinks to one old line.
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, " ") {
				t.Errorf("Context=0 still shows a context line %q:\n%s", line, out)
			}
		}
		if !strings.Contains(string(out), "@@ -3 +3,3 @@") {
			t.Errorf("Context=0 hunk header not collapsed:\n%s", out)
		}
	})

	t.Run("IgnoreWhitespace", func(t *testing.T) {
		// Whitespace-only change: vanilla patch shows it, --ignore-all-space
		// collapses the hunk.
		wsRoot := testfixture.New(t, testfixture.Empty)
		testfixture.Git(t, wsRoot, "config", "core.filemode", "false") // avoid mode noise on some hosts
		if err := os.WriteFile(wsRoot+"/ws.txt", []byte("foo  bar\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testfixture.Git(t, wsRoot, "add", "-A")
		testfixture.Git(t, wsRoot, "commit", "-q", "-m", "one")
		if err := os.WriteFile(wsRoot+"/ws.txt", []byte("foo bar\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testfixture.Git(t, wsRoot, "add", "-A")
		testfixture.Git(t, wsRoot, "commit", "-q", "-m", "two")

		wsRepo, err := DiscoverRepo(wsRoot)
		if err != nil {
			t.Fatal(err)
		}
		wsCommits := fixtureCommits(t, wsRoot)

		def, err := DiffFor(context.Background(), wsRepo, wsCommits[0], DefaultDiffOpts)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(def), "-foo  bar") {
			t.Errorf("default diff misses the whitespace change:\n%s", def)
		}
		ig, err := DiffFor(context.Background(), wsRepo, wsCommits[0], DiffOpts{IgnoreWhitespace: true, Context: -1})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(ig), "@@") {
			t.Errorf("--ignore-all-space still shows a hunk:\n%s", ig)
		}
	})
}

func TestDiffForMergeModes(t *testing.T) {
	root := testfixture.New(t, testfixture.Merges)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	merge := fixtureCommits(t, root)[0]

	combined, err := DiffFor(context.Background(), repo, merge, DefaultDiffOpts)
	if err != nil {
		t.Fatalf("DiffFor combined: %v", err)
	}
	if !strings.Contains(string(combined), "diff --cc shared.txt") {
		t.Errorf("combined merge diff misses the --cc header:\n%s", combined)
	}
	if !strings.Contains(string(combined), "@@@") {
		t.Errorf("combined merge diff misses the @@ hunk markers:\n%s", combined)
	}

	first, err := DiffFor(context.Background(), repo, merge, DiffOpts{FirstParent: true, Context: -1})
	if err != nil {
		t.Fatalf("DiffFor first-parent: %v", err)
	}
	if !strings.Contains(string(first), "diff --git a/feature-only.txt") {
		t.Errorf("first-parent merge diff misses the feature file:\n%s", first)
	}
	if strings.Contains(string(first), "@@@") {
		t.Errorf("first-parent merge diff must use plain hunks:\n%s", first)
	}
}

func TestRangeDiff(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	commits := fixtureCommits(t, root)

	// Everything since the oldest commit: its parent position to HEAD.
	base := commits[4].Hash + "~0" // the oldest commit itself
	out, err := RangeDiff(context.Background(), repo, base, "HEAD", DefaultDiffOpts)
	if err != nil {
		t.Fatalf("RangeDiff: %v", err)
	}
	text := string(out)
	for _, want := range []string{
		"diff --git a/src/renderer/canvas.go", // the rename lands in the range
		"Binary files /dev/null and b/assets/logo.bin differ",
		"+++ /dev/null", // notes.txt deleted within the range
	} {
		if !strings.Contains(text, want) {
			t.Errorf("range diff misses %q:\n%s", want, text)
		}
	}

	// A no-op range produces no hunks.
	out, err = RangeDiff(context.Background(), repo, "HEAD", "HEAD", DefaultDiffOpts)
	if err != nil {
		t.Fatalf("RangeDiff(HEAD, HEAD): %v", err)
	}
	if strings.Contains(string(out), "@@") {
		t.Errorf("RangeDiff(HEAD, HEAD) has hunks:\n%s", out)
	}
}

func TestRangeDiffError(t *testing.T) {
	root := testfixture.New(t, testfixture.Basic)
	repo, err := DiscoverRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = RangeDiff(context.Background(), repo, "no-such-ref", "HEAD", DefaultDiffOpts)
	var gitErr *GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("error = %T (%v), want *GitError", err, err)
	}
}

// TestDiffForParsesCleanly runs every commit of the linear and merge
// fixtures through the full pipeline: DiffFor text goes through
// diff.ParseUnified and must produce one file per FilesFor entry, with
// matching paths. This pins the git layer and the pure parser together.
func TestDiffForParsesCleanly(t *testing.T) {
	for _, name := range []testfixture.Name{testfixture.Basic, testfixture.Merges} {
		t.Run(string(name), func(t *testing.T) {
			root := testfixture.New(t, name)
			repo, err := DiscoverRepo(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range fixtureCommits(t, root) {
				out, err := DiffFor(context.Background(), repo, c, DefaultDiffOpts)
				if err != nil {
					t.Fatalf("DiffFor(%q): %v", c.Subject, err)
				}
				files := diff.ParseUnified(out)

				entries, err := FilesFor(context.Background(), repo, c, MergeCombined)
				if err != nil {
					t.Fatalf("FilesFor(%q): %v", c.Subject, err)
				}

				if len(files) != len(entries) {
					t.Errorf("%q: parser found %d files, file list has %d:\n%s",
						c.Subject, len(files), len(entries), out)
					continue
				}
				for i := range entries {
					if files[i].Path != entries[i].Path {
						t.Errorf("%q: file %d parsed path %q, listed path %q",
							c.Subject, i, files[i].Path, entries[i].Path)
					}
				}
			}
		})
	}
}

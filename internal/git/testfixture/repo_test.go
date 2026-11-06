package testfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allNames is every fixture the package can build.
var allNames = []Name{
	Basic, Merges, LongMsg, UnicodePaths, Submodule, Shallow, Empty, ManyFiles,
}

// TestAllFixturesBuild smokes every fixture: it must build without dying
// (skips allowed for host-limited ones) and leave a usable repository.
func TestAllFixturesBuild(t *testing.T) {
	for _, name := range allNames {
		t.Run(string(name), func(t *testing.T) {
			root := New(t, name)
			if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
				t.Fatalf("fixture %s has no .git: %v", name, err)
			}
		})
	}
}

// wantBasicHeadPrefix pins the head commit of the basic fixture. Because
// every fixture input (identity, dates, config, content) is pinned, this
// hash must be identical across runs, platforms, and git versions; a
// mismatch means the fixture inputs drifted.
// wantBasicHeadPrefix pins the cross-session stability of the basic
// fixture's identity. Windows records no exec bit (core.fileMode is a
// no-op there), so the 100755 script entries hash differently and the
// fixture has one stable head hash per platform family.
const (
	wantBasicHeadPrefix        = "a916e6eac928" // darwin, linux
	wantBasicHeadPrefixWindows = "06b17885cc89" // windows
)

// TestBasicDeterminism rebuilds the basic fixture twice in one run and
// requires identical history, plus the pinned head hash prefix.
func TestBasicDeterminism(t *testing.T) {
	first := commitHashes(t, New(t, Basic))
	second := commitHashes(t, New(t, Basic))

	if len(first) == 0 {
		t.Fatal("basic fixture produced no commits")
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Errorf("rebuilding basic produced different hashes:\n first: %v\nsecond: %v",
			first, second)
	}
	want := wantBasicHeadPrefix
	if runtime.GOOS == "windows" {
		want = wantBasicHeadPrefixWindows
	}
	if !strings.HasPrefix(first[0], want) {
		t.Errorf("basic head hash = %s, want prefix %s", first[0], want)
	}
}

// commitHashes lists the fixture's full history, newest first.
func commitHashes(t *testing.T, root string) []string {
	t.Helper()
	out := Git(t, root, "log", "--format=%H")
	var hashes []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			hashes = append(hashes, line)
		}
	}
	return hashes
}

// TestEmptyHasNoCommits guards the empty fixture against accidental
// commits — the empty-state UI tests depend on it.
func TestEmptyHasNoCommits(t *testing.T) {
	root := New(t, Empty)
	// rev-list --all is the one counter that works on an unborn branch.
	out := strings.TrimSpace(Git(t, root, "rev-list", "--all", "--count"))
	if out != "0" {
		t.Errorf("empty fixture rev-list --all --count = %q, want 0", out)
	}
}

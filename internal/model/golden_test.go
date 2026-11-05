package model

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/git/testfixture"
)

// Golden frames: the full frame for each core fixture, rendered with a
// forced color profile and pinned clock, snapshotted under
// testdata/golden/. Rewrite with:
//
//	go test ./internal/model -run TestGoldenFrames -update
var updateGoldens = flag.Bool("update", false, "rewrite golden frames")

func TestGoldenFrames(t *testing.T) {
	type golden struct {
		name     testfixture.Name
		w, h     int
		ascii    bool
		interact func(*harness)
	}
	interact := func(h *harness) {
		// Interact a little: move to a mid-history commit so the frame
		// shows selection, files, and diff together.
		h.press("j")
		h.press("2")
		h.press("j")
		h.press("enter") // jump the detail pane to the second file
	}
	goldens := []golden{
		{testfixture.Basic, 120, 40, false, interact},
		{testfixture.Merges, 120, 40, false, interact},
		{testfixture.LongMsg, 120, 40, false, interact},
		// A second size pins the layout math against drift.
		{testfixture.Basic, 80, 24, false, interact},
		// The --ascii rendering path.
		{testfixture.Basic, 120, 40, true, interact},
	}
	for _, g := range goldens {
		name := fmt.Sprintf("%s-%dx%d", g.name, g.w, g.h)
		if g.ascii {
			name += "-ascii"
		}
		t.Run(name, func(t *testing.T) {
			h := sizedHarness(t, g.name, g.w, g.h, g.ascii)
			g.interact(h)
			frame := forceProfile(h.frame())
			path := filepath.Join("..", "..", "testdata", "golden", name+".txt")
			if *updateGoldens {
				if err := os.WriteFile(path, []byte(frame), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if got, want := frame, string(want); got != want {
				writeDiff(t, path, got, want)
			}
		})
	}
}

// goldenHarness differs from newHarness only in frame size and ASCII mode.
func sizedHarness(t *testing.T, name testfixture.Name, w, hgt int, ascii bool) *harness {
	t.Helper()
	root := testfixture.New(t, name)
	repo, err := git.DiscoverRepo(root)
	if err != nil {
		t.Fatalf("discover fixture repo: %v", err)
	}
	h := &harness{t: t, repo: repo}
	h.app = New(Config{
		RepoName:     "demo",
		HasCommits:   name != testfixture.Empty,
		Repo:         repo,
		Now:          func() time.Time { return pinnedNow },
		NoAnimations: true,
		ASCII:        ascii,
	})
	h.send(tea.WindowSizeMsg{Width: w, Height: hgt})
	h.exec(h.app.Init())
	return h
}

// forceProfile renders the frame through a fixed color profile, exactly
// what a 256-color terminal would display. Fixed profile = no environment
// sniffing = deterministic bytes.
func forceProfile(frame string) string {
	var buf bytes.Buffer
	w := colorprofile.Writer{Forward: &buf, Profile: colorprofile.ANSI256}
	_, _ = w.Write([]byte(frame))
	return buf.String()
}

// writeDiff reports a golden mismatch with a unified-ish excerpt and
// writes the got-frame next to the golden for inspection.
func writeDiff(t *testing.T, path, got, want string) {
	t.Helper()
	_ = os.WriteFile(path+".got", []byte(got), 0o644)
	gotLines := splitLines(got)
	wantLines := splitLines(want)
	for i := 0; i < max(len(gotLines), len(wantLines)); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("golden mismatch at row %d (saved %s.got):\n got: %q\nwant: %q", i, path, g, w)
		}
	}
	t.Fatal("golden mismatch")
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

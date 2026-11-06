package model

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/git/testfixture"
)

// Golden frames: the full frame for each core fixture, rendered with a
// forced color profile and pinned clock, snapshotted under
// testdata/golden/. Rewrite with:
//
//	go test ./internal/model -run TestGoldenFrames -update
var updateGoldens = flag.Bool("update", false, "rewrite golden frames")

// NO_COLOR: through an ASCII color profile — what a NO_COLOR terminal
// negotiates — the frame carries no color escapes at all (spec §7.5,
// AC3). Syntax highlighting rides the same styles, so it degrades with
// everything else.
func TestNoColorOutputClean(t *testing.T) {
	h := newHarness(t, testfixture.Basic)
	h.press("j")
	h.press("2")
	h.press("j")
	frame := forceProfileNamed(h.frame(), colorprofile.Ascii)
	for i, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "\x1b[3") || strings.Contains(line, "\x1b[9") ||
			strings.Contains(line, "\x1b[38;5;") || strings.Contains(line, "\x1b[1;3") {
			t.Fatalf("row %d carries a color escape under the ASCII profile: %q", i, line)
		}
	}
	if !strings.Contains(frame, "commits (") {
		t.Fatal("ASCII-profile frame lost its content")
	}
}

// forceProfileNamed renders the frame through an explicit color profile.
func forceProfileNamed(frame string, profile colorprofile.Profile) string {
	var buf bytes.Buffer
	w := colorprofile.Writer{Forward: &buf, Profile: profile}
	_, _ = w.Write([]byte(frame))
	return buf.String()
}

func TestGoldenFrames(t *testing.T) {
	type golden struct {
		name     testfixture.Name
		w, h     int
		ascii    bool
		suffix   string // distinguishes mode variants of one fixture+size
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
	selectContentCommit := func(h *harness) {
		// The "Fix lane flicker" commit edits a line in place: the best
		// material for paired and intraline rendering.
		h.press("j")
		h.press("j")
		h.press("j")
		h.press("3")
	}
	sideBySide := func(h *harness) {
		selectContentCommit(h)
		h.press("s") // side-by-side with word wrap (FR-6)
	}
	intraline := func(h *harness) {
		selectContentCommit(h)
		h.exec2(h.sendNoExec(key("i"))) // word-diff refetch (FR-8)
	}
	since := func(h *harness) {
		h.press("j") // second commit: the range to HEAD has content
		h.press("d") // cumulative since-diff (FR-10)
	}
	zoomed := func(h *harness) {
		interact(h)
		h.press("1") // focus commits, then zoom it (FR-16)
		h.press("z")
	}
	goldens := []golden{
		{name: testfixture.Basic, w: 120, h: 40, interact: interact},
		{name: testfixture.Merges, w: 120, h: 40, interact: interact},
		{name: testfixture.LongMsg, w: 120, h: 40, interact: interact},
		// A second size pins the layout math against drift.
		{name: testfixture.Basic, w: 80, h: 24, interact: interact},
		// The --ascii rendering path.
		{name: testfixture.Basic, w: 120, h: 40, ascii: true, interact: interact},
		// Side-by-side, intraline word-diff, since-diff, and zoom.
		{name: testfixture.Basic, w: 120, h: 40, suffix: "-side", interact: sideBySide},
		{name: testfixture.Basic, w: 120, h: 40, suffix: "-intraline", interact: intraline},
		{name: testfixture.Basic, w: 120, h: 40, suffix: "-since", interact: since},
		{name: testfixture.Basic, w: 120, h: 40, suffix: "-zoom", interact: zoomed},
	}
	for _, g := range goldens {
		name := fmt.Sprintf("%s-%dx%d", g.name, g.w, g.h)
		if g.ascii {
			name += "-ascii"
		}
		name += g.suffix
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

package model

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smhanov/squint/internal/git"
	"github.com/smhanov/squint/internal/git/testfixture"
)

// The Elm-style test harness. Messages go through Update; commands are
// executed and their messages fed back — but only data messages. The
// timer-driven messages (the 30s re-render tick and the spinner
// animation) are never processed, so tests stay hermetic and frames
// deterministic (spinner frozen on its first frame).

// pinnedNow is the fixed wall clock tests run against: one hour after the
// fixture commits, so relative ages differ per commit ("59m", "58m", …).
var pinnedNow = time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

// harness drives the root model without a terminal.
type harness struct {
	t    *testing.T
	app  App
	repo *git.Repo
}

// newHarness builds the named fixture and a sized, fully loaded model at
// 120×40.
func newHarness(t *testing.T, name testfixture.Name) *harness {
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
	})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.exec(h.app.Init())
	return h
}

// send feeds one message through Update and executes any resulting
// command.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	next, cmd := h.app.Update(msg)
	h.app = h.model(next)
	h.exec(cmd)
}

// sendNoExec feeds one message through Update without executing the
// returned command; the caller keeps it for later (race simulations).
func (h *harness) sendNoExec(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	next, cmd := h.app.Update(msg)
	h.app = h.model(next)
	return cmd
}

// exec runs a command, feeding data messages back through Update
// recursively (tea.Batch unwraps into its sub-commands).
func (h *harness) exec(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			h.exec(c)
		}
		return
	}
	if !isDataMsg(msg) {
		return // tick/spinner animation: never processed in tests
	}
	next, cmd := h.app.Update(msg)
	h.app = h.model(next)
	h.exec(cmd)
}

func (h *harness) model(m tea.Model) App {
	h.t.Helper()
	app, ok := m.(App)
	if !ok {
		h.t.Fatalf("Update returned %T, want model.App", m)
	}
	return app
}

// isDataMsg reports whether a message changes data state (as opposed to
// driving an animation).
func isDataMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case commitsLoadedMsg, filesLoadedMsg, diffLoadedMsg, sinceLoadedMsg, statusLoadedMsg:
		return true
	}
	return false
}

// key builds a KeyPressMsg from its String() form ("j", "ctrl+d", "pgdn",
// "shift+tab", …) the way the runtime would deliver it.
func key(s string) tea.KeyPressMsg {
	switch s {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdn":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	if ctrl, ok := strings.CutPrefix(s, "ctrl+"); ok && len(ctrl) == 1 {
		return tea.KeyPressMsg{Code: rune(ctrl[0]), Mod: tea.ModCtrl}
	}
	r := []rune(s)
	if len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
	return tea.KeyPressMsg{Text: s}
}

// press sends a keystroke through the model.
func (h *harness) press(s string) { h.send(key(s)) }

// frame returns the current rendered content.
func (h *harness) frame() string { return h.app.View().Content }

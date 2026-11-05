package model

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

func update(t *testing.T, m App, msg tea.Msg) App {
	t.Helper()
	next, _ := m.Update(msg)
	updated, ok := next.(App)
	if !ok {
		t.Fatalf("Update returned %T, want model.App", next)
	}
	return updated
}

func TestViewBeforeFirstSize(t *testing.T) {
	app := New(Config{RepoName: "demo", HasCommits: true})

	v := app.View()
	if v.Content != "" {
		t.Errorf("initial View content = %q, want empty (renders nothing before first size)", v.Content)
	}
	if !v.AltScreen {
		t.Error("AltScreen = false, want true")
	}
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want MouseModeCellMotion", v.MouseMode)
	}
}

func TestNoMouseConfig(t *testing.T) {
	app := New(Config{RepoName: "demo", HasCommits: true, NoMouse: true})
	if v := app.View(); v.MouseMode != tea.MouseModeNone {
		t.Errorf("MouseMode = %v with NoMouse, want MouseModeNone", v.MouseMode)
	}
}

func TestZeroSizeRendersNothing(t *testing.T) {
	app := New(Config{RepoName: "demo", HasCommits: true})
	app = update(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})

	// A zero-sized resize report must not produce a broken frame.
	app = update(t, app, tea.WindowSizeMsg{Width: 0, Height: 0})
	if v := app.View(); v.Content != "" {
		t.Errorf("View after zero-size msg = %q, want empty", v.Content)
	}
}

func TestOKState(t *testing.T) {
	app := New(Config{RepoName: "demo", HasCommits: true})
	app = update(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})

	v := app.View()
	if !strings.Contains(v.Content, "squint") {
		t.Errorf("content %q does not mention squint", v.Content)
	}
	if !strings.Contains(v.Content, "demo") {
		t.Errorf("content %q does not mention the repo name", v.Content)
	}
	if !v.AltScreen {
		t.Error("AltScreen = false in ok state, want true")
	}
}

func TestEmptyRepoState(t *testing.T) {
	app := New(Config{RepoName: "fresh", HasCommits: false})
	app = update(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})

	v := app.View()
	if !strings.Contains(v.Content, "No commits yet") {
		t.Errorf("content %q does not show the empty-state message", v.Content)
	}
}

func TestTooSmallState(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{"narrow", 79, 40},
		{"short", 100, 23},
		{"tiny", 10, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(Config{RepoName: "demo", HasCommits: true})
			app = update(t, app, tea.WindowSizeMsg{Width: tt.width, Height: tt.height})

			v := app.View()
			if !strings.Contains(v.Content, "too small") {
				t.Errorf("content %q at %dx%d does not report a too-small terminal", v.Content, tt.width, tt.height)
			}
			if !strings.Contains(v.Content, "80") || !strings.Contains(v.Content, "24") {
				t.Errorf("content %q does not mention the 80x24 minimum", v.Content)
			}
		})
	}
}

func TestQuitKeys(t *testing.T) {
	app := New(Config{RepoName: "demo", HasCommits: true})
	app = update(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})

	keys := []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	}
	for _, key := range keys {
		next, cmd := app.Update(key)
		if cmd == nil {
			t.Errorf("key %q: cmd = nil, want a quit command", key.String())
			continue
		}
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Errorf("key %q: cmd produced %T, want tea.QuitMsg", key.String(), msg)
		}
		if _, ok := next.(App); !ok {
			t.Errorf("key %q: Update returned %T, want model.App", key.String(), next)
		}
	}

	// Unbound keys produce no command.
	next, cmd := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if cmd != nil {
		t.Errorf("key j: cmd = %v, want nil", cmd)
	}
	if _, ok := next.(App); !ok {
		t.Errorf("key j: Update returned %T, want model.App", next)
	}
}

// Package model holds the Bubble Tea models that make up the squint
// interface. The root model owns focus routing and layout sizing.
package model

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// minWidth and minHeight are the smallest usable terminal dimensions.
const (
	minWidth  = 80
	minHeight = 24
)

// state is the current phase of the shell.
type state int

const (
	stateNoSize    state = iota // before a usable terminal size is known
	stateOK                     // repository with commits: placeholder shell
	stateEmptyRepo              // repository without commits
	stateTooSmall               // terminal below the minimum size
)

// Config carries the CLI settings the interface needs.
type Config struct {
	// RepoName is the base name of the discovered repository root.
	RepoName string
	// HasCommits is false when the repository has no commits yet.
	HasCommits bool
	// SideBySide starts the diff viewer in side-by-side mode.
	SideBySide bool
	// NoMouse disables mouse capture.
	NoMouse bool
	// ASCII requests ASCII-only fallback rendering.
	ASCII bool
}

// App is the root model.
type App struct {
	cfg   Config
	state state

	width, height int

	titleStyle lipgloss.Style
	dimStyle   lipgloss.Style
}

// New returns the root model for the given configuration.
func New(cfg Config) App {
	return App{
		cfg:        cfg,
		state:      stateNoSize,
		titleStyle: lipgloss.NewStyle().Bold(true),
		dimStyle:   lipgloss.NewStyle().Faint(true),
	}
}

// Init implements tea.Model. Nothing is loaded yet.
func (a App) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		switch {
		case msg.Width <= 0 || msg.Height <= 0:
			// The very first size report can be zero-sized; render nothing
			// rather than a broken frame.
			a.state = stateNoSize
		case msg.Width < minWidth || msg.Height < minHeight:
			a.state = stateTooSmall
		case !a.cfg.HasCommits:
			a.state = stateEmptyRepo
		default:
			a.state = stateOK
		}
		return a, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return a, func() tea.Msg { return tea.Quit() }
		}
		return a, nil
	}
	return a, nil
}

// View implements tea.Model.
func (a App) View() tea.View {
	v := tea.NewView(a.content())
	v.AltScreen = true
	if !a.cfg.NoMouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// content renders the current state. It reads state only.
func (a App) content() string {
	var b strings.Builder
	switch a.state {
	case stateNoSize:
		// No usable size yet: render nothing.
		return ""

	case stateTooSmall:
		fmt.Fprintf(&b, "%s\n\n", a.titleStyle.Render("Terminal too small"))
		fmt.Fprintf(&b, "squint needs at least %d columns by %d rows.\n", minWidth, minHeight)
		fmt.Fprintf(&b, "This terminal is %d by %d. Enlarge the window and try again.\n", a.width, a.height)

	case stateEmptyRepo:
		fmt.Fprintf(&b, "%s\n\n", a.titleStyle.Render("squint · "+a.cfg.RepoName))
		fmt.Fprint(&b, "No commits yet.\n\n")
		fmt.Fprintf(&b, "%s\n", a.dimStyle.Render("Make the first commit and squint will show it here."))

	default:
		fmt.Fprintf(&b, "%s\n", a.titleStyle.Render("squint · "+a.cfg.RepoName))
		fmt.Fprint(&b, "\nCommit list and diff viewer are not wired up yet.\n\n")
		fmt.Fprintf(&b, "%s\n", a.dimStyle.Render("q quit · ctrl+c quit"))
	}
	return b.String()
}

// Package model holds the Bubble Tea models that make up the squint
// interface. The root model owns focus routing, layout sizing, the
// selected commit hash, and the generation counter that guards every
// asynchronous git result (spec §5.1).
package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/ui"
)

// minWidth and minHeight are the smallest usable terminal dimensions.
const (
	minWidth  = 80
	minHeight = 24
	// statusRows is the height of the status bar at the bottom.
	statusRows = 2
)

// tickInterval is how often relative ages recompute (spec §5.1: pure view,
// timestamps re-render on a tick). Tests may shorten it.
var tickInterval = 30 * time.Second

// state is the current phase of the interface.
type state int

const (
	stateNoSize    state = iota // before a usable terminal size is known
	stateOK                     // repository with commits: three panes
	stateEmptyRepo              // repository without commits
	stateTooSmall               // terminal below the minimum size
)

// paneLayout is the derived geometry of the three panes; all values are
// outer sizes including borders.
type paneLayout struct {
	leftW, rightW             int
	commitsH, filesH, detailH int
}

// layoutFor derives the pane geometry for a terminal size. It reports
// false when the terminal is below the minimum (spec §7.1: 80×24).
func layoutFor(width, height int) (paneLayout, bool) {
	if width < minWidth || height < minHeight {
		return paneLayout{}, false
	}
	h := height - statusRows // rows available to the panes
	leftW := width / 2       // left pane defaults to 50% width
	filesH := clamp(h/3, 4, 10)
	return paneLayout{
		leftW:    leftW,
		rightW:   width - leftW,
		commitsH: h,
		filesH:   filesH,
		detailH:  h - filesH,
	}, true
}

func clamp(v, lo, hi int) int { return min(hi, max(lo, v)) }

// Config carries the CLI settings the interface needs.
type Config struct {
	// RepoName is the base name of the discovered repository root.
	RepoName string
	// HasCommits is false when the repository has no commits yet.
	HasCommits bool
	// Repo is the discovered repository; git reads run against it. When
	// nil (placeholder/test setups) no git commands are issued.
	Repo *git.Repo
	// N is the bounded history window (git.DefaultLogN when unset).
	N int
	// Since restricts history by date (git --since syntax).
	Since string
	// RevRange is an optional revision range such as main..feature.
	RevRange string
	// Paths restricts history to these pathspecs.
	Paths []string
	// SideBySide starts the diff viewer in side-by-side mode.
	SideBySide bool
	// NoMouse disables mouse capture.
	NoMouse bool
	// ASCII requests ASCII-only fallback rendering.
	ASCII bool
	// Now overrides the wall clock; tests pin it for deterministic ages.
	Now func() time.Time
	// NoAnimations disables the re-render tick and the spinner animation
	// chain. Tests set it so no timer command is ever armed; frames stay
	// hermetic and the spinner freezes on its first frame.
	NoAnimations bool
}

// App is the root model. The selected commit is tracked by hash, never by
// index, so reloads can re-derive the cursor (spec §5.1 cursor stability).
type App struct {
	cfg   Config
	repo  *git.Repo
	state state

	width, height int
	lay           paneLayout

	// gen increments on every selection change, refresh, and diff-option
	// refetch; async file/diff messages that arrive with an older
	// generation are dropped (spec §5.1 stale-result guard). The commit
	// list and status loads are repo-global, not selection-scoped, so
	// they carry globalSeq instead: refreshing bumps globalSeq without
	// invalidating the selection, and moving the selection never drops a
	// pending list result.
	gen       int
	globalSeq int

	// Commit list state.
	commits           []git.Commit
	commitIndex       map[string]int // hash → list position, rebuilt per load
	selectedHash      string
	cursor            int
	commitsLoading    bool
	commitsLoadedOnce bool
	commitsErr        string
	commitsView       ui.Scroller

	// nowFn is the wall clock; tests pin it for deterministic ages.
	nowFn func() time.Time

	// Files pane state.
	filesHash  string // hash the loaded file list belongs to
	files      []git.FileEntry
	fileCursor int
	filesErr   string
	filesView  ui.Scroller

	// Detail pane state (message + diff).
	detail detailPane

	// Status bar state.
	status        git.RepoStatus
	statusLoading bool
	statusErr     string

	focus    paneFocus
	help     bool
	pendingG bool // first "g" of the gg top sequence

	spinner spinner.Model
}

// New returns the root model for the given configuration.
func New(cfg Config) App {
	if cfg.N <= 0 {
		cfg.N = git.DefaultLogN
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	sp := spinner.New()
	if cfg.ASCII {
		sp.Spinner = spinner.Line
	} else {
		sp.Spinner = spinner.MiniDot
	}
	a := App{
		cfg:         cfg,
		repo:        cfg.Repo,
		state:       stateNoSize,
		nowFn:       now,
		spinner:     sp,
		commitIndex: map[string]int{},
		commitsView: ui.NewScroller(),
		filesView:   ui.NewScroller(),
		detail:      newDetailPane(),
	}
	if cfg.Repo != nil {
		// First loads start with Init; the flags only drive the spinners.
		a.commitsLoading = true
		a.statusLoading = true
	}
	return a
}

// Init starts the commit-list and status loads. The 30-second re-render
// tick and the spinner animation arm on the first window-size message.
func (a App) Init() tea.Cmd {
	if a.repo == nil {
		return nil
	}
	return tea.Batch(a.loadCommitsCmd(), a.loadStatusCmd())
}

// ExitCode reports the process exit code implied by the final state:
// 1 when quitting from the too-small error screen, 0 otherwise (spec
// §8, NFR-8).
func (a App) ExitCode() int {
	if a.state == stateTooSmall {
		return 1
	}
	return 0
}

// Update implements tea.Model.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return a.updateWindowSize(msg)

	case tea.KeyPressMsg:
		return a.updateKey(msg)

	case commitsLoadedMsg:
		return a.commitsLoaded(msg)

	case filesLoadedMsg:
		return a.filesLoaded(msg)

	case diffLoadedMsg:
		return a.diffLoaded(msg)

	case statusLoadedMsg:
		return a.statusLoaded(msg)

	case tickMsg:
		a.rebuildCommitsLines() // relative ages
		a.rebuildDetailLines()
		if a.cfg.NoAnimations {
			return a, nil // tests: never arm a timer command
		}
		return a, tickCmd()

	case spinner.TickMsg:
		if !a.anyLoading() || a.cfg.NoAnimations {
			return a, nil // nothing in flight: let the animation chain die
		}
		sm, cmd := a.spinner.Update(msg)
		a.spinner = sm
		return a, cmd
	}
	return a, nil
}

func (a App) updateWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	first := a.state == stateNoSize
	a.width, a.height = msg.Width, msg.Height
	if a.width <= 0 || a.height <= 0 {
		// The very first size report can be zero-sized; render nothing
		// rather than a broken frame.
		a.state = stateNoSize
		return a, nil
	}
	a.applyState()
	a.relayout()
	if first && a.repo != nil && !a.cfg.NoAnimations {
		return a, tea.Batch(tickCmd(), a.spinner.Tick)
	}
	return a, nil
}

// applyState recomputes the coarse state from size, repository, and load
// results.
func (a *App) applyState() {
	switch {
	case a.width <= 0 || a.height <= 0:
		a.state = stateNoSize
	case a.width < minWidth || a.height < minHeight:
		a.state = stateTooSmall
	case !a.cfg.HasCommits:
		a.state = stateEmptyRepo
	case a.repo != nil && a.commitsLoadedOnce && len(a.commits) == 0:
		a.state = stateEmptyRepo
	default:
		a.state = stateOK
	}
}

// laidOut reports whether pane geometry is known (a usable size arrived).
func (a App) laidOut() bool { return a.lay.leftW > 0 && a.lay.rightW > 0 }

// relayout derives pane geometry and rebuilds every pane's rendered lines
// for the new widths. Rendering happens here, in Update, so View stays
// pure (spec §5.1).
func (a *App) relayout() {
	lay, ok := layoutFor(a.width, a.height)
	a.lay = lay
	if !ok {
		return
	}
	a.commitsView.SetWidth(lay.leftW - 2)
	a.commitsView.SetHeight(lay.commitsH - 2)
	a.filesView.SetWidth(lay.rightW - 2)
	a.filesView.SetHeight(lay.filesH - 2)
	a.detail.view.SetWidth(lay.rightW - 2)
	a.detail.view.SetHeight(lay.detailH - 2)
	a.rebuildCommitsLines()
	a.rebuildFilesLines()
	a.rebuildDetailLines()
}

func (a App) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Global keys (spec §10.1) work in every pane, state, and overlay.
	switch key {
	case "q", "ctrl+c":
		return a, func() tea.Msg { return tea.Quit() }
	case "?":
		a.help = !a.help
		return a, nil
	case "esc":
		a.help = false
		a.pendingG = false
		return a, nil
	}
	if a.help || a.state != stateOK {
		return a, nil
	}

	switch key {
	case "tab", "shift+tab":
		a.focus = a.focus.cycle(key == "shift+tab")
		return a, nil
	case "r":
		return a.refresh()
	}
	if f, ok := focusDirect(key); ok {
		a.focus = f
		return a, nil
	}

	switch a.focus {
	case focusCommits:
		return a.commitsKey(key)
	case focusFiles:
		return a.filesKey(key)
	default:
		return a.detailKey(key)
	}
}

// refresh re-runs the commit list and status loads. The selection survives
// by hash: after the list lands, the cursor re-derives from selectedHash
// (spec §5.1, FR-12). Stale pane data is re-fetched, and a diff refetch
// left dirty by an in-flight viewer option change is re-issued too,
// because its message was dropped when the generation moved.
func (a App) refresh() (tea.Model, tea.Cmd) {
	if a.repo == nil {
		return a, nil
	}
	a.gen++
	a.globalSeq++
	a.commitsLoading = true
	a.statusLoading = true
	cmds := []tea.Cmd{a.loadCommitsCmd(), a.loadStatusCmd()}
	if a.needsFanout() {
		if f := a.startFanout(); f != nil {
			cmds = append(cmds, f)
		}
	} else if a.detail.dirtyOpts && a.selectedHash != "" && a.cursor < len(a.commits) {
		cmds = append(cmds, a.loadDiffCmd(a.commits[a.cursor]), a.spinKick())
	}
	return a, tea.Batch(cmds...)
}

// needsFanout reports whether the panes are missing data for the selected
// commit. Because hashes are immutable, a refresh that re-derives the same
// selection does not re-fetch its files or diff.
func (a App) needsFanout() bool {
	if a.repo == nil || a.selectedHash == "" {
		return false
	}
	return a.filesHash != a.selectedHash || a.detail.hash != a.selectedHash
}

// startFanout starts the files and diff loads for the current selection.
// The detail header renders immediately from the commit already in the
// list; only the diff body waits on git. Content belonging to a different
// commit is dropped, never shown half-overlapping the new header.
//
// It takes a pointer receiver on purpose: the state changes must survive
// in the caller's model, which is exactly what the returned command is
// attributed to.
func (a *App) startFanout() tea.Cmd {
	if !a.needsFanout() || a.cursor >= len(a.commits) {
		return nil
	}
	commit := a.commits[a.cursor]
	a.fileCursor = 0
	a.filesErr = ""
	a.detail.err = ""
	if a.detail.commit.Hash != commit.Hash {
		a.detail.dropContent()
	}
	a.detail.commit = commit
	a.rebuildFilesLines()
	a.rebuildDetailLines()
	return tea.Batch(a.loadFilesCmd(commit), a.loadDiffCmd(commit), a.spinKick())
}

// selectAt moves the selection to index i, tracking it by hash. Every
// actual change bumps the generation, killing any loads for the previous
// selection. It reports whether the selection changed.
func (a *App) selectAt(i int) bool {
	if len(a.commits) == 0 {
		return false
	}
	i = clamp(i, 0, len(a.commits)-1)
	a.cursor = i
	h := a.commits[i].Hash
	if h == a.selectedHash {
		return false
	}
	a.selectedHash = h
	a.gen++
	a.pendingG = false
	a.rebuildCommitsLines()
	return true
}

func (a App) commitsLoaded(msg commitsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != a.globalSeq {
		return a, nil // stale: a newer list is already on its way
	}
	a.commitsLoading = false
	if msg.err != nil {
		a.commitsErr = msg.err.Error()
		a.rebuildCommitsLines()
		return a, nil
	}
	a.commitsErr = ""
	a.commits = msg.commits
	a.commitIndex = make(map[string]int, len(a.commits))
	for i, c := range a.commits {
		a.commitIndex[c.Hash] = i
	}
	a.commitsLoadedOnce = true

	switch {
	case len(a.commits) == 0:
		// An unborn branch or an emptied window: the empty screen.
		a.selectedHash = ""
		a.cursor = 0
		a.files = nil
		a.filesHash = ""
		a.fileCursor = 0
		a.detail.reset()
	case a.selectedHash == "":
		a.cursor = 0
		a.selectedHash = a.commits[0].Hash
	default:
		// Cursor stability: re-derive the index from the stored hash; if
		// the commit vanished, keep the nearest position.
		i, ok := a.commitIndex[a.selectedHash]
		if !ok {
			i = min(a.cursor, len(a.commits)-1)
			a.selectedHash = a.commits[i].Hash
		}
		a.cursor = i
	}
	a.rebuildCommitsLines()
	a.applyState()
	if len(a.commits) == 0 {
		a.rebuildDetailLines()
		return a, nil
	}
	return a, a.startFanout()
}

func (a App) filesLoaded(msg filesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != a.gen || msg.hash != a.selectedHash {
		return a, nil
	}
	a.filesErr = ""
	if msg.err != nil {
		a.filesErr = msg.err.Error()
		a.files = nil
	} else {
		a.files = msg.files
	}
	a.filesHash = msg.hash
	a.fileCursor = 0
	a.rebuildFilesLines()
	return a, nil
}

func (a App) diffLoaded(msg diffLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != a.gen || msg.hash != a.selectedHash {
		return a, nil
	}
	a.detail.apply(msg.hash, msg.diffs, msg.err)
	a.rebuildDetailLines()
	return a, nil
}

func (a App) statusLoaded(msg statusLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != a.globalSeq {
		return a, nil
	}
	a.statusLoading = false
	if msg.err != nil {
		a.statusErr = msg.err.Error()
		return a, nil
	}
	a.statusErr = ""
	a.status = msg.status
	return a, nil
}

// now returns the configured clock.
func (a App) now() time.Time { return a.nowFn() }

// scrollPercent is the focused pane's scroll percentage for the hints bar.
func (a App) scrollPercent() int {
	switch a.focus {
	case focusFiles:
		return percentPosition(a.fileCursor, len(a.files))
	case focusDetail:
		return int(a.detail.view.Percent() * 100)
	default:
		return percentPosition(a.cursor, len(a.commits))
	}
}

// statusInfo collects what status bar row 1 renders.
func (a App) statusInfo() ui.StatusInfo {
	info := ui.StatusInfo{
		Repo:     a.cfg.RepoName,
		Branch:   a.status.Branch,
		Detached: a.status.Detached,
		Ahead:    a.status.Ahead,
		Behind:   a.status.Behind,
		Dirty:    a.status.Dirty,
		Shallow:  a.status.Shallow,
		NCommits: len(a.commits),
		Loading:  a.anyLoading(),
		Spinner:  a.spinner.View(),
		ASCII:    a.cfg.ASCII,
	}
	if a.status.Detached && len(a.status.OID) >= abbrevLen {
		info.DetachedAbbr = a.status.OID[:abbrevLen]
	}
	return info
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

// content renders the current frame. It reads state only.
func (a App) content() string {
	switch a.state {
	case stateNoSize:
		// No usable size yet: render nothing.
		return ""
	case stateTooSmall:
		return a.tooSmallView()
	case stateEmptyRepo:
		return a.emptyView()
	}
	if a.help {
		return a.helpOverlay()
	}
	main := a.panesView()
	hints := hintsFor(a.focus)
	if a.statusErr != "" {
		// The status load failed: surface it where the hints would be.
		hints = ui.StyleDel.Render("git error: " + a.statusErr)
	}
	bar1 := ui.StatusBar(a.statusInfo(), a.width)
	bar2 := ui.HintsBar(hints, a.scrollPercent(), a.width)
	return lipgloss.JoinVertical(lipgloss.Left, main, bar1, bar2)
}

// panesView composes the three-pane layout: commits on the left, files
// and detail stacked on the right (spec §7.1).
func (a App) panesView() string {
	filesTitle := fmt.Sprintf("files (Δ %d)", len(a.files))
	if a.cfg.ASCII {
		filesTitle = fmt.Sprintf("files (%d)", len(a.files))
	}
	left := ui.Pane{
		Title:   fmt.Sprintf("commits (%d)", len(a.commits)),
		Focused: a.focus == focusCommits,
		ASCII:   a.cfg.ASCII,
	}.Render(a.commitsView.View(), a.lay.leftW, a.lay.commitsH)
	files := ui.Pane{
		Title:   filesTitle,
		Focused: a.focus == focusFiles,
		ASCII:   a.cfg.ASCII,
	}.Render(a.filesView.View(), a.lay.rightW, a.lay.filesH)
	detail := ui.Pane{
		Title:   "detail",
		Focused: a.focus == focusDetail,
		ASCII:   a.cfg.ASCII,
	}.Render(a.detail.view.View(), a.lay.rightW, a.lay.detailH)
	right := lipgloss.JoinVertical(lipgloss.Left, files, detail)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// tooSmallView renders the too-small error state.
func (a App) tooSmallView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", ui.StyleBold.Render("Terminal too small"))
	fmt.Fprintf(&b, "squint needs at least %d columns by %d rows.\n", minWidth, minHeight)
	fmt.Fprintf(&b, "This terminal is %d by %d. Enlarge the window and try again.\n", a.width, a.height)
	fmt.Fprintf(&b, "\n%s", ui.StyleFaint.Render("Press q to quit."))
	return b.String()
}

// emptyView renders the empty-repository state.
func (a App) emptyView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", ui.StyleBold.Render("squint · "+a.cfg.RepoName))
	fmt.Fprint(&b, "No commits yet.\n\n")
	fmt.Fprintf(&b, "%s", ui.StyleFaint.Render("Make the first commit and squint will show it here."))
	return b.String()
}

# squint — Requirements Specification & Implementation Guide

| | |
|---|---|
| **Project** | squint — review what your LLM just committed, in a terminal UI |
| **Name** | *squint* — because sometimes you have to squint at what the machine committed. Prior art: `squint-cljs/squint` (ClojureScript dialect, unrelated domain) |
| **Version** | 0.2 (draft) |
| **Date** | 2026-11-05 |
| **Status** | Pre-implementation |
| **License** | MIT |
| **Language** | Go |
| **UI framework** | Charm Bracelet Bubble Tea |

---

## 1. Overview

**squint answers one question: what did the LLM just change?**

It shows the last few commits on the current branch and the relevant parts of
the source code that changed — commit list, changed files, and diffs in one
screen, navigable by keyboard and mouse.

The motivating workflow: an LLM coding agent has been working on a branch,
making commits as it goes. You want to drop into the repo, see those commits
(few to a few dozen, mostly linear), open one, and read the code it touched —
with syntax highlighting, hunk-by-hunk. That is the entire product.

Everything a general git client does — staging, committing, rebasing, ref
management, searching, blaming, push/pull — is out of scope (§2). squint is a
**read-only viewer**.

### Design pillars

1. **One screen, three panes.** Commits on the left; changed files and the
   diff on the right. Selecting a commit shows its code changes immediately.
2. **Zero-friction navigation.** Vim-style keys *and* full mouse support
   (wheel, click-to-select, click-file-to-jump-to-hunk, draggable splits).
3. **Readable code.** Syntax-highlighted diffs, side-by-side mode, word-level
   intraline highlighting — tuned for reading LLM edits.
4. **Always current.** Watches the repository; when the agent commits again,
   the list updates by itself.
5. **Instant.** Bounded history window (default 50 commits); no loading
   spinners for normal repos.

---

## 2. Goals and non-goals

### Goals (v1.0)

- List the most recent commits on the current branch (default last 50).
- Select a commit → see full message, metadata, changed-file list, and diff.
- Select a file → diff scrolls to that file's first hunk.
- Diff viewer: unified + side-by-side, syntax highlighting, intraline
   word-diff, hunk/file navigation, wrap, whitespace toggle.
- Cumulative "everything since this commit" diff (one keypress).
- Auto-refresh when the repository changes.
- Mouse support; keyboard help overlay; copy commit hash.
- Single static binary; macOS, Linux, Windows.

### Non-goals (v1.0 and beyond, unless re-specced)

- **Any write operation**: no checkout, branch/tag create/delete, reset,
  revert, cherry-pick, stash, push/pull/fetch.
- Staging or committing workflow (use lazygit/gitui for that).
- Multi-lane commit graph rendering. History is a single-column list;
  merge commits are indicated, not drawn.
- Search, filtering, grep, pickaxe, file-history or line-range tracing.
- Refs/reflog/blame/blob/stash views.
- Mark-and-diff or arbitrary A/B commit comparison (only the built-in
  "since selected commit" cumulative diff).
- Config file, user themes, custom keybindings, command palette.
- Working-tree (uncommitted) diff view.
- Pager mode (reading stdin), submodule contents, partial-clone blob
  fetching, bare-repo support.

---

## 3. Research summary (what we borrow, from whom)

- **gitk** — the three-region layout (history / detail+diff / file list) and
  click-file→focus-diff-hunk interaction.
- **tig** — view split with Tab cycling; simple, fast log browsing.
- **lazygit** — panel model with per-context key hints in a status bar;
  colored one-line commit rows; the `/`-free simplicity of its graph pane.
- **gitui** — context-sensitive help (`?`); never block the UI on git.
- **Bubble Tea ecosystem** — Elm architecture (Model/Init/Update/View),
  `tea.Cmd` for all git I/O, Bubbles components, Lip Gloss styling/layout,
  BubbleZone for mouse hit-testing, `teatest`/`vhs` for testing/demos.
- **lazygit #583 (libgit2 discussion)** — shell out to the `git` binary;
  fork/exec overhead is negligible next to the actual git work, and git
  itself handles packs, shallows, and SHA-256 repos correctly. go-git is
  not used.

---

## 4. Technology stack & dependencies

| Concern | Choice | Notes |
|---|---|---|
| Language | Go ≥ 1.25 | Two most recent Go majors supported |
| TUI runtime | `charm.land/bubbletea/v2` | v1 import path (`github.com/charmbracelet/bubbletea`) is the fallback if v2 churn blocks us |
| Components | `charm.land/bubbles/v2` | viewport, spinner |
| Styling/layout | `charm.land/lipgloss/v2` | borders, joins, adaptive color degradation |
| Mouse zones | BubbleZone | per-pane hit-testing |
| Syntax highlight | `github.com/alecthomas/chroma/v2` | diff content highlighting |
| FS watching | `github.com/fsnotify/fsnotify` | + polling fallback (auto-refresh) |

No CGO. No configuration files. Only runtime dependency: the `git` binary
(≥ 2.20 required).

---

## 5. System architecture

### 5.1 Elm-architecture mapping

```
┌─────────────── tea.Program (alt screen, mouse cell motion) ──────────────┐
│  Model (state)              Update (event loop)           View (render)  │
│  ┌────────────────┐  Msg   ┌───────────────────┐  Cmd   ┌─────────────┐  │
│  │ focus, layout  │◀──────▶│ key / mouse msg   │───────▶│ lipgloss    │  │
│  │ commits []     │        │ tea.WindowSize    │        │ composition │  │
│  │ selection      │        │ commitsLoadedMsg  │        │ of panes    │  │
│  │ files, diff    │        │ filesLoadedMsg    │        └─────────────┘  │
│  │ gen counter    │        │ diffLoadedMsg     │                         │
│  └────────────────┘        │ refreshMsg        │                         │
│         ▲                  └───────────────────┘                         │
│         └───────────── result Msg ─── exec git (goroutine)               │
└───────────────────────────────────────────────────────────────────────────┘
```

Rules:

- **`Update` never blocks.** All git I/O happens in `tea.Cmd` functions
  returning `Msg` values.
- **Stale-result guard.** Every async msg carries a generation counter;
  `Update` drops msgs whose gen ≠ the model's current gen. This prevents
  races when the user moves the selection quickly or a refresh lands mid-read.
- **Cursor stability.** The selected commit is stored as a *hash*, not an
  index. After any reload the index is re-derived from the hash; if the
  commit vanished, select the nearest by position.
- **Pure view.** `View()` reads state only; relative timestamps recompute on
  a 30s tick msg.

### 5.2 Package layout

```
squint/
├── cmd/squint/main.go            # CLI parsing, tea.NewProgram bootstrap
├── internal/
│   ├── git/                    # ALL shell-out code lives here
│   │   ├── repo.go             # discovery, git-dir resolution, version probe
│   │   ├── log.go              # bounded commit listing (Append A)
│   │   ├── diff.go             # diff-tree, show/diff (unified + merges)
│   │   ├── status.go           # HEAD state, ahead/behind, dirty flag
│   │   └── exec.go             # runner: env, timeouts, -z discipline, errors
│   ├── model/                  # Bubble Tea models
│   │   ├── app.go              # root Model, focus routing, layout sizing
│   │   ├── commits.go          # commit-list pane
│   │   ├── files.go            # changed-file list pane
│   │   ├── detail.go           # message + diff pane
│   │   ├── help.go             # keymap overlay
│   │   └── keys.go             # fixed default keymap (documented, not remappable)
│   ├── diff/                   # diff parsing, hunks, side-by-side pairing (pure)
│   ├── watch/                  # fsnotify + debounce, polling fallback
│   └── ui/                     # statusbar, divider widgets
├── testdata/                   # fixture repos (scripts), golden frames
├── scripts/mkfixtures.sh       # deterministic fixture repos
├── .github/workflows/          # ci.yml, release.yml
├── Makefile                    # build, test, lint, fixtures
├── spec.md                     # this document
└── README.md
```

Dependency direction: `model` → (`git`, `diff`, `ui`); `diff` is pure and
unit-testable; **only `internal/git` executes the git binary.** squint never
runs a git command that mutates the repository.

### 5.3 Core data types (sketch)

```go
type Commit struct {
    Hash, Abbrev string
    Parents      []string // >1 means merge commit
    Author, Email string
    At           time.Time
    Subject, Body string  // full body kept; LLM messages are long
    IsHead       bool
}

type FileEntry struct {
    Status byte   // A/M/D/R/C/T (git name-status)
    Path   string
    Orig   string // previous path for R/C
}

type Hunk struct {
    Header                       string
    OldStart, OldCount           int
    NewStart, NewCount           int
    Lines                        []DiffLine
}
```

---

## 6. Git integration layer

### 6.1 Strategy: shell out to `git`

Execute the `git` binary; parse machine-friendly output. Rules for every
invocation (`internal/git/exec.go`):

- Resolve binary from PATH; probe `git --version` at startup (≥ 2.20).
- Environment: inherit, then force `GIT_PAGER=cat`, pass
  `--no-optional-locks`, and `-c core.quotePath=false` (raw unicode paths).
- Always parse `-z` (NUL-terminated) output; never parse human-formatted
  porcelain intended for terminals.
- 30s timeout on reads; stderr captured; non-zero exit returns typed
  `GitError{ExitCode, Stderr, Args}` surfaced in an error overlay.
- Args passed as `exec.Args` — never a shell string.

### 6.2 Command catalog

| Purpose | Command (abridged; exact formats in Appendix A) |
|---|---|
| Commit list | `git log -z -n <N> --format=<record> [<rev-range>] [-- <path>…]` (N default 50) |
| Commit files | `git diff-tree -z --no-commit-id --name-status -r -M <commit>` |
| Commit diff | `git show --format= --patch [-m\|--cc] <commit> [-- <path>]` |
| Cumulative diff | `git diff --patch <parent-of-selected> HEAD [--]` |
| HEAD state | `git status --porcelain=v2 --branch` (branch, ahead/behind, dirty) |

Merge commits default to the **combined diff** (`--cc`); `p` toggles
first-parent view (`--first-parent -m`… re-run with
`--diff-merges=first-parent`).

### 6.3 Caching

Small LRU caches, keyed and cleared on refresh generation bump: commit diff
(64 entries), file list (128). Commit list itself is re-read on refresh
(it's one cheap bounded `git log`).

### 6.4 Repository watching & refresh

- Watch `<gitdir>/HEAD`, `refs/` recursively, packed-refs, and the index via
  fsnotify; 250ms debounce; 2s polling fallback if fsnotify is unavailable.
- On change: bump generation, re-run the commit list + status; keep the
  selected commit stable (§5.1). New commits appear at the top
  automatically — the core "agent is still working" behavior.
- `r` = manual refresh.

---

## 7. User interface specification

### 7.1 Layout

```
┌─ commits (50) ────────────────────────────┬─ files (Δ 3) ─────────────┐
│ ● a1b2c3d 2m  Fix lane flicker on reload  │ M src/graph/lane.go      │
│ ● e4f5a6b 14m Refactor renderer           │ A src/graph/cell.go      │
│ ● f0e1d2c 1h  Add side-by-side mode       │ D src/legacy.go          │
│ ● 9876543 2h  WIP: streaming batches      ├─ detail ─────────────────┤
│ ● …                                       │ Fix lane flicker on reload│
│                                           │ Claude <claude@…> 2m ago │
│                                           │ ── src/graph/lane.go ────│
│                                           │ @@ -10,7 +10,9 @@        │
│                                           │ -old lane logic          │
│                                           │ +new lane logic          │
├───────────────────────────────────────────┴───────────────────────────┤
│ squint · myrepo · main ↑2 · ● dirty · 50 commits              j/k move │
│ ↑↓ move  enter diff  d since-diff  ?: help                         41%│
└───────────────────────────────────────────────────────────────────────┘
```

- Left pane (default 50% width): commit list — single vertical rail with
  `●` nodes, abbrev hash, relative age, subject. Merge commits show `◆` and
  a `(merge)` marker; HEAD row is highlighted with `(HEAD)`.
- Right top: changed files of the selected commit with status letters.
- Right bottom: detail — full commit message (scrollable; LLM messages are
  often multi-paragraph), author line (who/what made the change — agent
  identities show here), then the diff.
- Selecting a file scrolls the diff to that file's first hunk.
- `z` zooms the focused pane (toggle); `tab`/`shift+tab` cycle panes;
  `1`/`2`/`3` focus panes directly.
- Divider draggable with mouse; 50/50 default, ratio kept for the session
  only (no persisted state).
- Minimum 80×24; below that, error message and clean exit.

### 7.2 Focus model

Exactly one pane has focus (highlighted border). Keys route to the focused
pane's keymap; global keys (quit, refresh, help, pane cycling) work
everywhere.

### 7.3 Diff viewer

- Unified by default; `s` toggles side-by-side (hunk pairing + aligned
  context + horizontal scroll).
- Syntax highlighting via chroma, per file extension (fallback mono).
- `i` toggles intraline word-diff (`--word-diff`) — small LLM edits read
  best this way. Should-priority.
- `w` cycles wrap none → word → character; `W` toggles
  `--ignore-all-space`; `+`/`-` context lines (3 default, 0–10).
- Hunk navigation `(`/`)`; file navigation `[`/`]`; `n/N` not applicable
  (no search) — removed.
- Binary files render `[binary · 412.3 KiB]`; diffs larger than
  3 MiB truncate with a `[diff truncated]` marker row.
- Deleted/added files show full content as removal/addition.

### 7.4 Status bar & help

- Row 1: `squint` · repo name · branch (or `HEAD detached at a1b2c3d`) ·
  ahead/behind · `● dirty` when the tree has changes · commit count.
- Row 2: context key hints for the focused pane; scroll percentage.
- `?` overlay: full keymap for the current pane + globals.

### 7.5 Color

- Fixed dark palette; automatic degradation via lipgloss profile
  (truecolor → 256 → 16 → mono). Honor `NO_COLOR`, `CLICOLOR_FORCE`.
- Status letters colored: `A` green, `M` yellow, `D` red, `R/C` blue.
- Selected row reverse video; HEAD row bold.

---

## 8. Command-line interface

```
squint [options] [<revision-range>] [--] [<path>…]
```

| Option | Behavior |
|---|---|
| `-n <count>` | History window (default 50) |
| `--since=<date>` | Restrict by date |
| `-C <path>` | Run as if started in `<path>` |
| `--side-by-side` | Start in side-by-side mode |
| `--no-mouse` | Disable mouse |
| `--ascii` | ASCII fallback rendering |
| `--version` `--help` | Standard |

A positional revision-range (e.g. `main..feature`) and pathspec pass through
to `git log`. Unknown git-log-style options pass through with a warning
overlay if git rejects them.

Exit codes: `0` ok · `1` usage · `2` not a git repo · `3` git failure ·
`130` interrupted.

---

## 9. Feature requirements

Priorities: **M** = must (1.0), **S** = should (1.0 if feasible).

| # | Requirement | Pri |
|---|---|---|
| FR-1 | Bounded commit list of current branch (default 50), single-rail rendering, HEAD highlight, relative ages | M |
| FR-2 | Keyboard navigation: line/half-page/page/top/bottom; mouse wheel & click-to-select | M |
| FR-3 | Selecting a commit loads file list + full message + metadata async; spinner while loading | M |
| FR-4 | Selecting a file scrolls diff to its first hunk | M |
| FR-5 | Unified diff viewer with hunks, context control, wrap, whitespace toggle | M |
| FR-6 | Side-by-side diff with alignment + horizontal scroll | S |
| FR-7 | Syntax highlighting | S |
| FR-8 | Intraline (word) diff toggle | S |
| FR-9 | Merge commits: combined diff default, first-parent toggle `p` | S |
| FR-10 | `d` cumulative diff: everything from selected commit's parent to HEAD | S |
| FR-11 | Auto-refresh on external repo changes; selection stays stable | M |
| FR-12 | `r` manual refresh | M |
| FR-13 | Copy abbrev/full hash (`y`/`Y`) via OSC 52 / pbcopy / clip | S |
| FR-14 | Help overlay `?` with current-pane keymap | M |
| FR-15 | Mouse: wheel scroll, click select, click file → hunk, drag divider | M |
| FR-16 | Zoom pane `z`; pane cycling `tab`/`shift+tab` | M |

---

## 10. Keybindings (fixed defaults)

### 10.1 Global

| Key | Action |
|---|---|
| `q` / `<c-c>` | Quit |
| `?` | Help overlay |
| `tab` / `<s-tab>` | Cycle panes |
| `1` `2` `3` | Focus commits / files / detail |
| `z` | Zoom focused pane |
| `r` | Refresh |
| `y` / `Y` | Copy short / full hash of selection |

### 10.2 Commits pane

| Key | Action |
|---|---|
| `j` `k` `<down>` `<up>` | Move selection |
| `<c-d>` `<c-u>` | Half page |
| `<c-f>` `<c-b>` `pgdn` `pgup` | Page |
| `g` `g` / `G` | Top / bottom |
| `enter` | Focus detail pane |
| `d` | Since-diff (cumulative to HEAD) |
| `p` | Toggle first-parent diff for merges |

### 10.3 Files pane

`j/k` move · `enter`/click focus diff at file · `[` `]` prev/next file.

### 10.4 Detail pane

`j/k` scroll · `(`/`)` hunk nav · `[`/`]` file nav · `s` side-by-side ·
`w` wrap · `W` ignore-whitespace · `i` intraline · `+`/`-` context.

---

## 11. Mouse behavior

| Gesture | Target | Action |
|---|---|---|
| Wheel | any pane | Scroll that pane |
| Click | commit row | Select + focus |
| Double-click | commit row | Focus detail (≡ `enter`) |
| Click | file row | Select file, diff scrolls to hunk |
| Click + drag | divider | Resize split (session only) |

Mouse on by default (`tea.WithMouseCellMotion`); `--no-mouse` disables.
Every mouse action has a key equivalent (shown in help).

---

## 12. Non-functional requirements

| ID | Requirement | Budget |
|---|---|---|
| NFR-1 | Startup → first paint (typical repo, 50 commits) | ≤ 150ms |
| NFR-2 | Key event → frame, warm | ≤ 16ms p99 |
| NFR-3 | Commit diff open (cold / warm) | ≤ 300ms / ≤ 100ms p95 |
| NFR-4 | RSS | ≤ 100MB |
| NFR-5 | Binary size | ≤ 15MB |
| NFR-6 | Platforms | macOS ≥ 12 (amd64/arm64), Linux (amd64/arm64, glibc+musl), Windows ≥ 10 (Windows Terminal) |
| NFR-7 | Terminals | truecolor (iTerm2, WT, Alacritty, kitty, foot, wezterm), 256-color, 16-color, mono; ASCII fallback auto |
| NFR-8 | Below 80×24 | Clean error, exit 1 |
| NFR-9 | Lint/vuln clean (`golangci-lint`, `govulncheck`) | enforced in CI |

---

## 13. Error handling & edge cases

| Case | Behavior |
|---|---|
| Not a git repo | Friendly error + hint, exit 2 |
| Empty repo (no commits) | Empty-state screen ("no commits yet") |
| Detached HEAD | Status shows `HEAD detached at <abbr>`; browsing unaffected |
| Branch with < N commits | List whatever exists; no padding |
| Shallow clone | Normal browse; `shallow` note in status bar |
| Submodule entry (mode 160000) | File row `⋗ path @<sha>`; gitlink change summary only |
| Binary file | `[binary · size]` row |
| Huge diff | Truncate at 3 MiB with marker |
| Rename/copy | `R old → new` with similarity % (`-M` on) |
| Unicode/space/quote paths | `-z` + `core.quotePath=false`; tested with `路径/文件.txt` |
| CRLF | Content passthrough; no normalization by squint |
| Merge commit | `◆` marker; combined diff default (FR-9) |
| External change mid-read | Generation guard drops stale msgs; refresh re-runs |
| Terminal resize | Relayout preserving selected row |

---

## 14. Testing strategy

1. **Unit (pure packages)** — `diff`: parser fixtures incl. binary, rename,
   mode-change, empty-file, unicode paths, huge-diff truncation.
2. **Integration (`internal/git`)** — script-built fixture repos
   (`scripts/mkfixtures.sh`, deterministic dates/authors via
   `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE`): `basic`, `merges`, `long-msg`
   (multi-paragraph LLM-style messages), `unicode-paths`, `submodule`,
   `shallow`, `empty`, `many-files`.
3. **Model tests** — `teatest`-driven `Update` loop: key sequences →
   expected selection/focus/layout transitions; stale-msg dropping via
   generation counter.
4. **Golden frame tests** — render `View()` per fixture at fixed size and
   forced color profile; snapshot testdata; changes reviewed explicitly.
5. **CI matrix** — Go {current, previous} × {linux-amd64, macos-arm64,
   windows-amd64} + lint/vet/govulncheck; goreleaser snapshot build.
6. **Manual matrix** — iTerm2, Terminal.app, WT, Alacritty, kitty, foot,
   tmux (mouse passthrough documented).
7. **Docs demo** — one `vhs` tape for the README GIF (select commit →
   file → hunk), regenerated on release.

---

## 15. Packaging & distribution

- GoReleaser: archives + checksums; Homebrew tap, scoop bucket;
  `go install` supported. Static binaries (CGO_ENABLED=0), musl build.
- README with the one workflow animated; `--help` text is the full user
  documentation (there is no config file to document).

---

## 16. Milestones & acceptance criteria

### M0 — Scaffold (≈ 3 days)
Repo layout, CI, lint, `cmd/squint` boots alt-screen, repo discovery, version
probe, error/empty states.
**AC:** `squint` in any dir shows repo-aware state or a clean error; CI green.

### M1 — Core viewer (≈ 1.5 weeks)
Commit list (FR-1/2), three-pane layout, async detail + file list
(FR-3/4), unified diff (FR-5), status bar, help overlay (FR-14), refresh
(FR-12).
**AC:** on `basic` + `merges` + `long-msg` fixtures: correct rendering
(golden-verified), navigation fluid, diff within NFR-3.

### M2 — Polish (≈ 1 week)
Mouse (FR-15), zoom/cycling (FR-16), side-by-side + highlighting +
intraline (FR-6/7/8), since-diff (FR-10), copy hash (FR-13), auto-refresh
(FR-11), packaging + demo.
**AC:** mouse checklist passes on 3 terminals; auto-refresh test commits
externally and list updates with selection stable; `go install` + brew work.

**Definition of done (v1.0):** all M-priority FRs complete, S-priority FRs
done or explicitly re-prioritized, NFR 1–9 green, README demo accurate.

---

## 17. Implementation guide (ordered notes)

1. **Build bottom-up:** `internal/git/repo.go` → `exec.go` → `log.go`
   (stream parsing tested before any UI) → `diff` package → Bubble Tea
   models. `diff` being pure makes it testable long before the UI exists.
2. **Bootstrap:** parse CLI → `tea.NewProgram(model, tea.WithAltScreen(),
   tea.WithMouseCellMotion())`; initial `tea.Batch(loadCommits, loadStatus)`.
3. **Stale-result guard:** selection changes bump the per-request gen; any
   async msg with an old gen is dropped in `Update`. This one pattern
   prevents nearly all selection-race bugs.
4. **Selection by hash:** after refresh, binary-search the hash; if missing,
   keep the nearest index. Never key UI state off slice indexes.
5. **Diff focus mapping:** while rendering the detail pane, record
   `[]fileSpan{startRow, endRow}`; file clicks/`enter` scroll to the span's
   first hunk. Build the index during render, not after.
6. **Bubble Tea gotchas:** never print to stdout (`tea.LogToFile` under
   `SQUINT_DEBUG=1`); handle the first zero-size `tea.WindowSizeMsg` by
   rendering nothing; construct lipgloss styles once at startup, never per
   row; use `strings.Builder` per frame.
7. **Windows:** VT processing handled by bubbletea; `-z` gives
   forward-slashed paths everywhere; resolve git via absolute path.
8. **PR sequence for M1:** (a) git exec + log + tests, (b) diff parser +
   tests, (c) app shell + commits pane, (d) detail + files panes,
   (e) navigation + status bar + help. Each lands green.

---

## 18. Risks & mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Bubble Tea v2 API churn mid-project | Rework | Pin versions; v1 escape hatch documented |
| Parsing git output drift across versions | Breakage | CI matrix against git 2.31/2.45/latest in containers; `-z` only; lenient parsers |
| Windows terminal quirks (mouse, VT) | Broken UX | Early Windows CI leg; auto-degrade to no-mouse mode on unsupported input |
| Scope creep toward general git client | Never ships | §2 non-goals are contractual; any new feature requires a spec revision |
| Huge LLM-generated diffs overwhelm viewer | Jank | 3 MiB truncation, line-count caps per hunk, rendering budget per frame |

---

## 19. Open questions

1. Default window: 50 commits right for agent review, or smaller (20)?
2. Should the status bar show the author identity per commit row (agent vs
   human) when they differ — e.g., a subtle `🤖` glyph? Cheap, useful for
   the LLM-review use case; decide in M1 user tests.
3. `d` since-diff semantics on merge commits: diff vs first parent, or
   exclude merges entirely (`git log --no-merges` range)? Leaning first
   parent.

---

## Appendix A — git format strings (normative)

**Commit record** (`internal/git/log.go`), record sep `%x1e`, field sep
`%x1f`, passed with `-z`:

```
git log -z -n <N> --format=%H%x1f%h%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s%x1f%b%x1e [<rev-range>] [-- <path>…]
```

Fields: hash, abbrev, parents, author name/email/epoch, subject, body.

**HEAD/state:** `git status --porcelain=v2 --branch`.

**File list:** `git diff-tree -z --no-commit-id --name-status -r -M <commit>`
(merges: `--cc` unless first-parent mode).

## Appendix B — References

- gitk layout inspiration — https://git-scm.com/docs/gitk
- tig manual — https://jonas.github.io/tig/doc/manual.html
- lazygit — https://github.com/jesseduffield/lazygit (incl. #583 shelling-out rationale)
- gitui — https://github.com/gitui-org/gitui
- Bubble Tea / Bubbles / Lip Gloss — https://github.com/charmbracelet/bubbletea

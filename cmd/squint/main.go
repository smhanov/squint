// Command squint is a terminal UI for reviewing recent commits and the code
// they changed.
//
// Usage:
//
//	squint [options] [<revision-range>] [--] [<path>…]
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/squint-tui/squint/internal/git"
	"github.com/squint-tui/squint/internal/model"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// Exit codes (see the usage text).
const (
	exitOK     = 0
	exitUsage  = 1
	exitError  = 2
	exitGit    = 3
	exitSignal = 130
)

const defaultHistoryWindow = 50

var (
	errHelp    = errors.New("help requested")
	errVersion = errors.New("version requested")
)

// options holds the parsed command line. The revision range and pathspec
// fields pass through to git log once history loading lands; they are
// captured here so the flag surface is complete from the start.
type options struct {
	count      int
	since      string
	dir        string
	sideBySide bool
	noMouse    bool
	ascii      bool
	help       bool
	showVer    bool
	revRange   string
	paths      []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses arguments, discovers the repository, and starts the program.
// It returns the process exit code.
func run(argv []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(argv)
	if err != nil {
		switch {
		case errors.Is(err, errHelp):
			printUsage(stdout)
			return exitOK
		case errors.Is(err, errVersion):
			fmt.Fprintf(stdout, "squint version %s\n", version)
			return exitOK
		default:
			fmt.Fprintf(stderr, "squint: %v\n\n", err)
			printUsage(stderr)
			return exitUsage
		}
	}

	// Run as if started in opts.dir: chdir before any discovery happens.
	if opts.dir != "" {
		if err := os.Chdir(opts.dir); err != nil {
			fmt.Fprintf(stderr, "squint: cannot change to %q: %v\n", opts.dir, err)
			return exitError
		}
	}

	repo, err := git.DiscoverRepo(".")
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "squint: not a git repository (or any parent directory).")
			fmt.Fprintln(stderr, "hint: cd into a repository first, or start with -C <path>.")
			return exitError
		}
		fmt.Fprintf(stderr, "squint: %v\n", err)
		return exitError
	}

	info, err := git.Version()
	if err != nil {
		fmt.Fprintf(stderr, "squint: %v\n", err)
		return exitError
	}
	if !info.AtLeast(git.MinMajor, git.MinMinor) {
		fmt.Fprintf(stderr, "squint: git %s is too old; squint needs git >= %d.%d\n",
			info.Raw, git.MinMajor, git.MinMinor)
		return exitError
	}

	hasCommits, err := repo.HasCommits()
	if err != nil {
		fmt.Fprintf(stderr, "squint: %v\n", err)
		return exitError
	}

	// Never write to stdout while the UI is running; debug logs go to a file.
	if os.Getenv("SQUINT_DEBUG") != "" {
		f, err := tea.LogToFile("squint-debug.log", "squint")
		if err != nil {
			fmt.Fprintf(stderr, "squint: cannot open debug log: %v\n", err)
		} else {
			defer f.Close() //nolint:errcheck // best-effort close on shutdown
		}
	}

	app := model.New(model.Config{
		RepoName:   filepath.Base(repo.Root),
		HasCommits: hasCommits,
		Repo:       repo,
		N:          opts.count,
		Since:      opts.since,
		RevRange:   opts.revRange,
		Paths:      opts.paths,
		SideBySide: opts.sideBySide,
		NoMouse:    opts.noMouse,
		ASCII:      opts.ascii,
	})

	var progOpts []tea.ProgramOption
	if opts.ascii {
		progOpts = append(progOpts, tea.WithColorProfile(colorprofile.Ascii))
	}

	p := tea.NewProgram(app, progOpts...)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintf(stderr, "squint: %v\n", err)
		return exitError
	}
	// Quitting from the too-small screen is a clean error exit (NFR-8).
	if fm, ok := final.(model.App); ok && fm.ExitCode() != exitOK {
		return exitUsage
	}
	return exitOK
}

// parseFlags parses the squint option surface. Git-log-style positionals
// (a revision range, and pathspecs after --) are captured for later
// pass-through. A parse failure returns a non-sentinel error describing the
// problem; errHelp and errVersion signal the informational flags.
func parseFlags(argv []string) (*options, error) {
	// The flag package treats a bare "--" as a terminator and consumes it,
	// so split on it first: everything after belongs to the pathspec list.
	var afterDashDash []string
	if i := slices.Index(argv, "--"); i >= 0 {
		afterDashDash = argv[i+1:]
		argv = argv[:i]
	}

	fs := flag.NewFlagSet("squint", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are formatted by the caller

	var opts options
	fs.IntVar(&opts.count, "n", defaultHistoryWindow, "history window (default 50)")
	fs.StringVar(&opts.since, "since", "", "restrict history by date")
	fs.StringVar(&opts.dir, "C", "", "run as if started in <path>")
	fs.BoolVar(&opts.sideBySide, "side-by-side", false, "start in side-by-side diff mode")
	fs.BoolVar(&opts.noMouse, "no-mouse", false, "disable mouse support")
	fs.BoolVar(&opts.ascii, "ascii", false, "ASCII fallback rendering")
	fs.BoolVar(&opts.showVer, "version", false, "print version and exit")
	fs.BoolVar(&opts.help, "help", false, "show help and exit")
	fs.BoolVar(&opts.help, "h", false, "show help and exit")

	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	if opts.help {
		return nil, errHelp
	}
	if opts.showVer {
		return nil, errVersion
	}
	if opts.count < 1 {
		return nil, fmt.Errorf("invalid -n %d: must be at least 1", opts.count)
	}

	// Anything left over before "--" is a revision range (first) and
	// optionally more pathspecs; after "--" is always pathspecs.
	rest := fs.Args()
	if len(rest) > 0 {
		opts.revRange = rest[0]
		opts.paths = append(opts.paths, rest[1:]...)
	}
	opts.paths = append(opts.paths, afterDashDash...)
	return &opts, nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

const usageText = `squint - review what your LLM just committed, in a terminal UI

Usage:
  squint [options] [<revision-range>] [--] [<path>…]

Options:
  -n <count>       history window (default 50)
  --since <date>   restrict history by date
  -C <path>        run as if started in <path>
  --side-by-side   start in side-by-side diff mode
  --no-mouse       disable mouse support
  --ascii          ASCII fallback rendering
  --version        print version and exit
  --help, -h       show this help and exit

A revision range (e.g. main..feature) and pathspecs after -- are passed
through to git log.

Exit codes:
  0    success
  1    usage error (bad flag or argument)
  2    not a git repository, or another runtime error
  3    git command failure
  130  interrupted
`

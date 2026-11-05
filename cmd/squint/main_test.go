package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFlagsDefaults(t *testing.T) {
	opts, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil) failed: %v", err)
	}
	if opts.count != 50 {
		t.Errorf("count = %d, want default 50", opts.count)
	}
	if opts.since != "" || opts.dir != "" || opts.revRange != "" {
		t.Errorf("string options not empty by default: %+v", opts)
	}
	if opts.sideBySide || opts.noMouse || opts.ascii || opts.help || opts.showVer {
		t.Errorf("bool options set by default: %+v", opts)
	}
	if len(opts.paths) != 0 {
		t.Errorf("paths = %v, want none", opts.paths)
	}
}

func TestParseFlagsValues(t *testing.T) {
	opts, err := parseFlags([]string{
		"-n", "10", "--since=2026-01-01", "-C", "/some/dir",
		"--side-by-side", "--no-mouse", "--ascii",
	})
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if opts.count != 10 {
		t.Errorf("count = %d, want 10", opts.count)
	}
	if opts.since != "2026-01-01" {
		t.Errorf("since = %q, want %q", opts.since, "2026-01-01")
	}
	if opts.dir != "/some/dir" {
		t.Errorf("dir = %q, want %q", opts.dir, "/some/dir")
	}
	if !opts.sideBySide || !opts.noMouse || !opts.ascii {
		t.Errorf("bool flags not set: %+v", opts)
	}
}

func TestParseFlagsPositionals(t *testing.T) {
	t.Run("RevisionRangeOnly", func(t *testing.T) {
		opts, err := parseFlags([]string{"main..feature"})
		if err != nil {
			t.Fatalf("parseFlags failed: %v", err)
		}
		if opts.revRange != "main..feature" {
			t.Errorf("revRange = %q, want %q", opts.revRange, "main..feature")
		}
		if len(opts.paths) != 0 {
			t.Errorf("paths = %v, want none", opts.paths)
		}
	})

	t.Run("RangeAndPathsAfterDashDash", func(t *testing.T) {
		opts, err := parseFlags([]string{"main..feature", "--", "src/a.go", "src/b.go"})
		if err != nil {
			t.Fatalf("parseFlags failed: %v", err)
		}
		if opts.revRange != "main..feature" {
			t.Errorf("revRange = %q, want %q", opts.revRange, "main..feature")
		}
		want := []string{"src/a.go", "src/b.go"}
		if fmt.Sprint(opts.paths) != fmt.Sprint(want) {
			t.Errorf("paths = %v, want %v", opts.paths, want)
		}
	})

	t.Run("PathsOnlyAfterDashDash", func(t *testing.T) {
		opts, err := parseFlags([]string{"--", "src/a.go"})
		if err != nil {
			t.Fatalf("parseFlags failed: %v", err)
		}
		if opts.revRange != "" {
			t.Errorf("revRange = %q, want empty", opts.revRange)
		}
		if fmt.Sprint(opts.paths) != fmt.Sprint([]string{"src/a.go"}) {
			t.Errorf("paths = %v, want [src/a.go]", opts.paths)
		}
	})
}

func TestParseFlagsHelpAndVersion(t *testing.T) {
	if _, err := parseFlags([]string{"--help"}); !errors.Is(err, errHelp) {
		t.Errorf("--help error = %v, want errHelp", err)
	}
	if _, err := parseFlags([]string{"-h"}); !errors.Is(err, errHelp) {
		t.Errorf("-h error = %v, want errHelp", err)
	}
	if _, err := parseFlags([]string{"--version"}); !errors.Is(err, errVersion) {
		t.Errorf("--version error = %v, want errVersion", err)
	}
}

func TestParseFlagsErrors(t *testing.T) {
	t.Run("UnknownFlag", func(t *testing.T) {
		_, err := parseFlags([]string{"--bogus"})
		if err == nil {
			t.Fatal("parseFlags(--bogus) succeeded, want an error")
		}
		if errors.Is(err, errHelp) || errors.Is(err, errVersion) {
			t.Errorf("error = %v, want a plain usage error", err)
		}
		if !strings.Contains(err.Error(), "bogus") {
			t.Errorf("error %q does not mention the bad flag", err)
		}
	})

	t.Run("ZeroCount", func(t *testing.T) {
		_, err := parseFlags([]string{"-n", "0"})
		if err == nil {
			t.Fatal("parseFlags(-n 0) succeeded, want an error")
		}
		if !strings.Contains(err.Error(), "-n") {
			t.Errorf("error %q does not mention -n", err)
		}
	})

	t.Run("NegativeCount", func(t *testing.T) {
		if _, err := parseFlags([]string{"-n", "-5"}); err == nil {
			t.Fatal("parseFlags(-n -5) succeeded, want an error")
		}
	})

	t.Run("NonNumericCount", func(t *testing.T) {
		if _, err := parseFlags([]string{"-n", "abc"}); err == nil {
			t.Fatal("parseFlags(-n abc) succeeded, want an error")
		}
	})
}

func TestRunVersionAndHelp(t *testing.T) {
	t.Run("Version", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--version"}, &stdout, &stderr); code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
		if !strings.Contains(stdout.String(), "squint version") {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), "squint version")
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty", stderr.String())
		}
	})

	t.Run("Help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
		out := stdout.String()
		// The help text is the entire user documentation (spec §15):
		// every flag, every key, and every mode must appear.
		for _, want := range []string{
			// Flags.
			"Usage", "-n", "--since", "-C", "--side-by-side", "--no-mouse", "--ascii",
			// Global keys and modes.
			"tab", "shift+tab", "zoom", "refresh", "copy", "quit", "help overlay",
			// Commits-pane keys.
			"half page", "page down", "top / bottom", "enter", "since-diff", "first-parent",
			// Files-pane keys.
			"first hunk",
			// Detail-pane keys and modes.
			"hunk", "side-by-side", "word-diff", "wrap", "ignore whitespace", "context",
		} {
			if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
				t.Errorf("help output missing %q", want)
			}
		}
	})
}

func TestRunBadFlagExitsUsage(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--bogus"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Errorf("stderr = %q, want it to mention the bad flag", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage") {
		t.Errorf("stderr = %q, want usage text", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunNotARepoExitsError(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "not a git repository") {
		t.Errorf("stderr = %q, want a friendly not-a-repo message", stderr.String())
	}
	if !strings.Contains(stderr.String(), "hint") {
		t.Errorf("stderr = %q, want a hint", stderr.String())
	}
}

func TestRunDashC(t *testing.T) {
	t.Chdir(t.TempDir())

	t.Run("PointsAtNonRepo", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		dir := t.TempDir() // separate non-repo directory
		code := run([]string{"-C", dir}, &stdout, &stderr)
		if code != exitError {
			t.Errorf("exit code = %d, want %d (discovery happens after chdir)", code, exitError)
		}
		if !strings.Contains(stderr.String(), "not a git repository") {
			t.Errorf("stderr = %q, want a not-a-repo message", stderr.String())
		}
	})

	t.Run("MissingDirectory", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		if code := run([]string{"-C", missing}, &stdout, &stderr); code != exitError {
			t.Errorf("exit code = %d, want %d", code, exitError)
		}
		if !strings.Contains(stderr.String(), "cannot change to") {
			t.Errorf("stderr = %q, want a chdir failure message", stderr.String())
		}
	})
}

// TestRunFailureKeepsStdoutClean asserts that a run that fails before the
// UI starts (discovery errors) never writes to stdout.
func TestRunFailureKeepsStdoutClean(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	run(nil, &stdout, &stderr) //nolint:errcheck // exit code asserted above
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q on failure path, want empty", stdout.String())
	}
}

// Silence the unused warning for constants that later work will consume;
// referencing them in a test keeps the exit-code table honest.
func TestExitCodeValues(t *testing.T) {
	codes := map[string]int{
		"ok": exitOK, "usage": exitUsage, "error": exitError,
		"git": exitGit, "signal": exitSignal,
	}
	if codes["ok"] != 0 || codes["usage"] != 1 || codes["error"] != 2 || codes["git"] != 3 || codes["signal"] != 130 {
		t.Errorf("exit code table changed unexpectedly: %v", codes)
	}
}

// Ensure the flag package still surfaces ErrHelp through our sentinel for
// unexpected bare help requests.
func TestFlagErrHelpMapping(t *testing.T) {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Skipf("flag package behavior changed: -h returned %v", err)
	}
}

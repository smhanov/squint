package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeFakeGit installs a fake git executable in a fresh directory and
// returns its path. Two script bodies are given: one for /bin/sh and one
// for cmd.exe batch files. The scripts ignore all arguments (the runner
// prepends its own flags before the subcommand).
func writeFakeGit(t *testing.T, shScript, batScript string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "git")
	script := shScript
	if runtime.GOOS == "windows" {
		path += ".bat"
		script = "@echo off\r\n" + strings.ReplaceAll(batScript, "\n", "\r\n")
	} else {
		script = "#!/bin/sh\n" + shScript
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeRunner returns a Runner bound to dir that executes the given fake
// git script, with the timeout replaced so tests stay fast.
func fakeRunner(t *testing.T, dir, shScript, batScript string, timeout time.Duration) *Runner {
	t.Helper()
	r := NewRunner(dir)
	r.bin = writeFakeGit(t, shScript, batScript)
	r.timeout = timeout
	return r
}

func TestRunnerFakeExitCode(t *testing.T) {
	tests := []struct {
		name     string
		sh       string
		bat      string
		wantCode int
	}{
		{
			name:     "exit 128 with stderr",
			sh:       "echo \"fatal: fake failure\" >&2\nexit 128\n",
			bat:      "echo fatal: fake failure 1>&2\nexit /b 128\n",
			wantCode: 128,
		},
		{
			name:     "exit 1 with stderr",
			sh:       "echo \"fatal: not a repo\" >&2\nexit 1\n",
			bat:      "echo fatal: not a repo 1>&2\nexit /b 1\n",
			wantCode: 1,
		},
		{
			name:     "silent failure",
			sh:       "exit 3\n",
			bat:      "exit /b 3\n",
			wantCode: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := fakeRunner(t, t.TempDir(), tt.sh, tt.bat, defaultTimeout)
			out, err := r.Run(context.Background(), "log", "-z", "-n", "5")
			if len(out) != 0 {
				t.Errorf("stdout = %q, want empty", out)
			}
			var gitErr *GitError
			if !errors.As(err, &gitErr) {
				t.Fatalf("error = %T (%v), want *GitError", err, err)
			}
			if gitErr.ExitCode != tt.wantCode {
				t.Errorf("ExitCode = %d, want %d", gitErr.ExitCode, tt.wantCode)
			}
			if gitErr.Timeout {
				t.Error("Timeout = true, want false")
			}
			if fmt.Sprint(gitErr.Args) != fmt.Sprint([]string{"log", "-z", "-n", "5"}) {
				t.Errorf("Args = %v, want the caller's arguments", gitErr.Args)
			}
			if tt.wantCode != 3 && !strings.Contains(gitErr.Stderr, "fatal") {
				t.Errorf("Stderr = %q, want the fake's message", gitErr.Stderr)
			}
			if gitErr.Error() == "" {
				t.Error("Error() is empty")
			}
		})
	}
}

func TestRunnerFakeTimeout(t *testing.T) {
	sh := "sleep 30\n"
	// batch has no sleep; one ping packet per second stalls well past
	// the 100ms deadline while keeping the orphaned grandchild (cmd.exe
	// is killed, ping.exe is not) short-lived.
	bat := "ping -n 2 127.0.0.1 >nul\n"
	r := fakeRunner(t, t.TempDir(), sh, bat, 100*time.Millisecond)
	// The orphaned ping.exe holds the fixture directory open until it
	// exits (~1s); wait it out so TempDir removal on Windows succeeds.
	t.Cleanup(func() { time.Sleep(3 * time.Second) })

	start := time.Now()
	_, err := r.Run(context.Background(), "log", "-z")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Run took %s, want the 100ms deadline to fire", elapsed)
	}
	var gitErr *GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("error = %T (%v), want *GitError", err, err)
	}
	if !gitErr.Timeout {
		t.Errorf("Timeout = false, want true (error: %v)", err)
	}
	if gitErr.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (killed, no exit status)", gitErr.ExitCode)
	}
	if !strings.Contains(gitErr.Stderr, "timed out") {
		t.Errorf("Stderr = %q, want a timeout description", gitErr.Stderr)
	}
}

func TestRunnerFakeStdout(t *testing.T) {
	// The runner must return stdout verbatim, byte-for-byte, because the
	// log and diff parsers depend on exact separator bytes. The batch
	// twin uses plain echo (set /p exits 1 under CI's cmd /c); its
	// trailing newline is trimmed on Windows only.
	r := fakeRunner(t, t.TempDir(),
		"printf 'alpha beta gamma'\n",
		"echo alpha beta gamma\n",
		defaultTimeout)
	out, err := r.Run(context.Background(), "version")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	got := string(out)
	if runtime.GOOS == "windows" {
		got = strings.TrimSpace(got)
	}
	if got != "alpha beta gamma" {
		t.Errorf("stdout = %q, want %q", got, "alpha beta gamma")
	}
}

func TestRunnerCanceledContext(t *testing.T) {
	gitPath(t) // any executable works; the context fails first
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewRunner("")
	_, err := r.Run(ctx, "--version")
	if err == nil {
		t.Fatal("Run with a canceled context succeeded, want an error")
	}
	var gitErr *GitError
	if errors.As(err, &gitErr) {
		t.Errorf("error = %v, want a plain context error for caller cancellation", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v does not wrap context.Canceled", err)
	}
}

func TestRunnerMissingBinary(t *testing.T) {
	r := NewRunner(t.TempDir())
	r.bin = filepath.Join(t.TempDir(), "does-not-exist")
	_, err := r.Run(context.Background(), "--version")
	if err == nil {
		t.Fatal("Run with a missing binary succeeded, want an error")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Errorf("error = %v, want a lookup failure rather than an exit status", err)
	}
}

// TestRunnerRealGitPrelude verifies against the real git binary that the
// runner's fixed prelude is accepted: --no-optional-locks and the
// core.quotePath override must not disturb ordinary commands.
func TestRunnerRealGitPrelude(t *testing.T) {
	root := initRepo(t)
	commitAll(t, root)

	r := NewRunner(root)
	if _, err := r.Run(context.Background(), "status", "--porcelain=v2", "--branch"); err != nil {
		t.Fatalf("status with prelude failed: %v", err)
	}

	// core.quotePath=false: non-ASCII paths must come back raw in -z output.
	mustGit(t, root, "-c", "user.name=T", "-c", "user.email=t@x",
		"commit", "--allow-empty", "-q", "-m", "second")
	out, err := r.Run(context.Background(), "log", "-z", "--format=%H%x1f%s%x1e")
	if err != nil {
		t.Fatalf("log with prelude failed: %v", err)
	}
	if !bytes.Contains(out, []byte("second\x1e")) {
		t.Errorf("log output %q misses the second commit record", out)
	}
}

package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// defaultTimeout bounds every git invocation; a hung git process must never
// freeze the UI.
const defaultTimeout = 30 * time.Second

// GitError reports a failed git invocation with its exit status and stderr,
// so callers can surface a useful error overlay.
type GitError struct {
	// ExitCode is the process exit status, or -1 when the invocation was
	// killed by the runner timeout and never exited on its own.
	ExitCode int
	// Stderr carries the captured standard error of the command.
	Stderr string
	// Args are the git arguments as invoked, without the runner's fixed
	// environment prelude.
	Args []string
	// Timeout is true when the command hit the runner deadline and was
	// killed instead of exiting by itself.
	Timeout bool
}

func (e *GitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("git %s: exit status %d", strings.Join(e.Args, " "), e.ExitCode)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// Runner executes the git binary in a fixed working directory.
type Runner struct {
	// dir is the working directory for commands; empty means the current
	// process directory.
	dir string
	// timeout bounds a single invocation.
	timeout time.Duration
	// bin overrides the git executable; empty resolves git from PATH.
	// Production runners leave it unset; tests install fake git binaries
	// through it.
	bin string
}

// NewRunner returns a Runner that runs git in dir.
func NewRunner(dir string) *Runner {
	return &Runner{dir: dir, timeout: defaultTimeout}
}

// gitBinary resolves the git executable once per process and caches the
// absolute path. An empty result means git was not found on PATH.
var gitBinary = sync.OnceValue(func() string {
	path, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	return path
})

// Run executes git with args and returns stdout. Stderr is captured and
// reported through GitError on failure. Arguments are passed verbatim as an
// argv slice; no shell is involved.
//
// The context bounds the invocation together with the runner's own timeout:
// a canceled parent context aborts the command immediately, a deadline that
// the runner or caller set returns a timed-out GitError.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := r.bin
	if bin == "" {
		bin = gitBinary()
	}
	if bin == "" {
		return nil, errors.New("git executable not found in PATH")
	}

	// Read-only discipline: no optional locks, raw (unquoted) unicode paths,
	// and never a pager.
	full := make([]string, 0, len(args)+3)
	full = append(full, "--no-optional-locks", "-c", "core.quotePath=false")
	full = append(full, args...)

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	// After the deadline the process is killed, but grand-children forked
	// by git may still hold the output pipes open. WaitDelay bounds that
	// wait so the invocation always returns close to its deadline.
	cmd.WaitDelay = time.Second

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	// Check the context first: when the deadline fires, CommandContext
	// kills the process and Output reports it as a signal exit, which must
	// surface as a timeout, not as an exit status.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		return nil, &GitError{
			ExitCode: -1,
			Stderr:   fmt.Sprintf("timed out after %s", r.timeout),
			Args:     args,
			Timeout:  true,
		}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, &GitError{
			ExitCode: exitErr.ExitCode(),
			Stderr:   stderr.String(),
			Args:     args,
		}
	}
	return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}

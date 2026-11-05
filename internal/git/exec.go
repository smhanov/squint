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
	ExitCode int
	Stderr   string
	Args     []string
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
	dir     string
	timeout time.Duration
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
func (r *Runner) Run(args ...string) ([]byte, error) {
	bin := gitBinary()
	if bin == "" {
		return nil, errors.New("git executable not found in PATH")
	}

	// Read-only discipline: no optional locks, raw (unquoted) unicode paths,
	// and never a pager.
	full := make([]string, 0, len(args)+3)
	full = append(full, "--no-optional-locks", "-c", "core.quotePath=false")
	full = append(full, args...)

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &GitError{
				ExitCode: exitErr.ExitCode(),
				Stderr:   stderr.String(),
				Args:     full,
			}
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), r.timeout)
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

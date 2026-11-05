package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotARepo is returned by DiscoverRepo when no repository is found.
var ErrNotARepo = errors.New("not a git repository")

// MinMajor and MinMinor define the oldest git release squint supports.
const (
	MinMajor = 2
	MinMinor = 20
)

// Repo describes a discovered git working tree.
type Repo struct {
	// Root is the absolute path of the working tree root.
	Root string
	// GitDir is the absolute path of the repository's .git directory. For
	// worktrees this is the worktree-private gitdir, not the shared .git.
	GitDir string
}

// DiscoverRepo walks up from start (a file path or directory) looking for a
// .git entry, exactly like git itself does. Both layouts are handled:
//
//   - a .git directory (a normal clone or init), and
//   - a .git file containing "gitdir: <path>" (a linked worktree).
//
// It returns ErrNotARepo wrapped with the start path if the filesystem root
// is reached without a match.
func DiscoverRepo(start string) (*Repo, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNotARepo, start, err)
	}

	for {
		if repo, ok := repoAt(dir); ok {
			return repo, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, fmt.Errorf("%w: %s", ErrNotARepo, start)
}

// repoAt reports whether dir contains a usable .git entry and returns the
// repository it describes.
func repoAt(dir string) (*Repo, bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return nil, false
	}
	if info.IsDir() {
		// A .git directory is a repository if it has a HEAD.
		if hasHEAD(dotGit) {
			return &Repo{Root: dir, GitDir: dotGit}, true
		}
		return nil, false
	}
	// A .git file is a worktree pointer: "gitdir: <path>".
	gitDir, ok := readGitFile(dotGit)
	if !ok || !hasHEAD(gitDir) {
		return nil, false
	}
	return &Repo{Root: dir, GitDir: gitDir}, true
}

// readGitFile parses a .git file and returns the absolute gitdir it points
// to. Relative paths are resolved against the directory containing the file.
func readGitFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	target, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", false
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target), true
}

// hasHEAD reports whether path looks like a git directory by checking for
// its HEAD file.
func hasHEAD(path string) bool {
	info, err := os.Stat(filepath.Join(path, "HEAD"))
	return err == nil && !info.IsDir()
}

// HasCommits reports whether the repository's current branch has any commits
// (that is, HEAD is not an unborn reference). It inspects repository files
// directly — no git process is started. Detached HEAD implies commits exist.
func (r *Repo) HasCommits() (bool, error) {
	data, err := os.ReadFile(filepath.Join(r.GitDir, "HEAD"))
	if err != nil {
		return false, fmt.Errorf("reading HEAD: %w", err)
	}
	line := strings.TrimSpace(string(data))
	ref, ok := strings.CutPrefix(line, "ref:")
	if !ok {
		// Raw object ID: HEAD is detached, so commits exist.
		return true, nil
	}
	ref = strings.TrimSpace(ref)

	// A linked worktree keeps its own HEAD but stores refs in the common
	// directory; check both locations, loose refs first.
	dirs := []string{r.GitDir}
	if common, ok := readCommonDir(r.GitDir); ok {
		dirs = append(dirs, common)
	}
	for _, dir := range dirs {
		if fileExists(filepath.Join(dir, filepath.FromSlash(ref))) {
			return true, nil
		}
		if packedHasRef(filepath.Join(dir, "packed-refs"), ref) {
			return true, nil
		}
	}
	return false, nil
}

// readCommonDir returns the common git directory for a linked worktree, as
// recorded in its commondir file. Relative paths are resolved against the
// gitdir.
func readCommonDir(gitDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return "", false
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(gitDir, line)
	}
	return filepath.Clean(line), true
}

// packedHasRef reports whether packed-refs at path contains ref.
func packedHasRef(path, ref string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		// Format: "<object-id> <refname>".
		if _, name, ok := strings.Cut(line, " "); ok && strings.TrimSpace(name) == ref {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// CommonDir returns the common git directory shared by all worktrees —
// the location of refs and packed-refs. For a normal repository this is
// GitDir itself. The value is resolved from the filesystem only; no git
// process is started.
func (r *Repo) CommonDir() string {
	if common, ok := readCommonDir(r.GitDir); ok {
		return common
	}
	return r.GitDir
}

// IsShallow reports whether the repository is a shallow clone (its git
// directory contains a shallow file). Linked worktrees keep the flag in
// the common directory.
func (r *Repo) IsShallow() bool {
	dirs := []string{r.GitDir}
	if common, ok := readCommonDir(r.GitDir); ok {
		dirs = append(dirs, common)
	}
	for _, dir := range dirs {
		if fileExists(filepath.Join(dir, "shallow")) {
			return true
		}
	}
	return false
}

// VersionInfo is the probed git release.
type VersionInfo struct {
	// Raw is the verbatim first line of `git --version`.
	Raw string
	// Major and Minor are the parsed version numbers.
	Major, Minor int
}

// AtLeast reports whether the probed version is major.minor or newer.
func (v VersionInfo) AtLeast(major, minor int) bool {
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}

// Version runs `git --version` in the current directory and parses the
// result. The parse is lenient: it finds the first "N.M" number token
// anywhere in the output, tolerating vendor suffixes such as
// "2.39.2 (Apple Git-143)".
func Version() (VersionInfo, error) {
	out, err := NewRunner("").Run(context.Background(), "--version")
	if err != nil {
		return VersionInfo{}, err
	}
	raw := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	info, ok := ParseVersion(raw)
	if !ok {
		return VersionInfo{}, fmt.Errorf("could not parse git version from %q", raw)
	}
	return info, nil
}

// ParseVersion extracts major and minor from a `git --version` output line.
func ParseVersion(line string) (VersionInfo, bool) {
	for _, field := range strings.Fields(line) {
		major, rest, found := strings.Cut(field, ".")
		if !found {
			continue
		}
		minor, _, _ := strings.Cut(rest, ".")
		m, err1 := strconv.Atoi(major)
		n, err2 := strconv.Atoi(minor)
		if err1 == nil && err2 == nil && m >= 0 && n >= 0 {
			return VersionInfo{Raw: line, Major: m, Minor: n}, true
		}
	}
	return VersionInfo{}, false
}

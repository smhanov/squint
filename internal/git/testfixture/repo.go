// Package testfixture builds deterministic git repositories for tests.
//
// Fixtures are created fresh in t.TempDir() with pinned authors, e-mail
// addresses, and timestamps, and with host git configuration excluded, so
// commits hash identically on every run, platform, and git version. The
// package is the only place in the repository that runs state-changing git
// commands; production code (internal/git) is read-only by contract.
//
// Fixture set (spec §14.2): basic, merges, long-msg, unicode-paths,
// submodule, shallow, empty, many-files.
package testfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Name identifies a fixture repository.
type Name string

// The fixture set. See the package documentation for what each contains.
const (
	Basic        Name = "basic"
	Merges       Name = "merges"
	LongMsg      Name = "long-msg"
	UnicodePaths Name = "unicode-paths"
	Submodule    Name = "submodule"
	Shallow      Name = "shallow"
	Empty        Name = "empty"
	ManyFiles    Name = "many-files"
)

// Fixture identity and timeline, pinned so that rebuilds reproduce the
// exact same commit objects.
const (
	// authorName and authorEmail are the fixture commit identity.
	authorName  = "Squint Fixture"
	authorEmail = "fixture@squint.test"

	// epochBase is 2025-01-01T00:00:00Z; the n-th commit of a fixture is
	// authored epochBase + n minutes, so ordering is stable everywhere.
	epochBase = int64(1735689600)

	// branchName is pinned so fixture refs do not depend on the host's
	// init.defaultBranch.
	branchName = "main"
)

// builder assembles one fixture repository.
type builder struct {
	t    *testing.T
	dir  string
	step int // commit counter for the pinned clock

	// author overrides for fixtures exercising unicode identities.
	authorName  string
	authorEmail string
}

// New builds the named fixture in a fresh directory and returns its root
// path. The empty fixture is a repository without commits.
func New(t *testing.T, name Name) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), string(name))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	b := &builder{t: t, dir: root, authorName: authorName, authorEmail: authorEmail}

	// The shallow fixture is a clone, not a fresh repository; cloning into
	// an existing directory requires it to be empty.
	if name != Shallow {
		b.git("init", "-q", ".")
		b.git("symbolic-ref", "HEAD", "refs/heads/"+branchName)
	}

	switch name {
	case Basic:
		b.basic()
	case Merges:
		b.merges()
	case LongMsg:
		b.longMsg()
	case UnicodePaths:
		b.unicodePaths()
	case Submodule:
		b.submodule()
	case Shallow:
		b.shallow()
	case Empty:
		// Intentionally no commits.
	case ManyFiles:
		b.manyFiles()
	default:
		t.Fatalf("unknown fixture %q", name)
	}
	return root
}

// Git runs a git command in dir under the fixture's deterministic
// environment (pinned identity, isolated configuration) and fails the test
// on error. It is exported so tests can adjust a fixture after building
// it — detaching HEAD, dirtying files, and so on. This is the single
// sanctioned entry point for mutating git commands in tests.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitEnv(t, dir, nil, args...)
}

// gitEnv is Git with extra environment variables layered on top.
func gitEnv(t *testing.T, dir string, extraEnv []string, args ...string) string {
	t.Helper()

	bin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found in PATH")
	}

	// Configuration hygiene: never inherit host settings (user identity,
	// gpg signing, autocrlf, hooks, aliases) — they would change blobs,
	// commit metadata, and therefore hashes.
	env := []string{
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + authorName,
		"GIT_COMMITTER_EMAIL=" + authorEmail,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_PAGER=cat",
	}
	env = append(env, extraEnv...)

	// -c flags must precede the subcommand.
	full := append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "core.autocrlf=false",
	}, args...)

	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// gitAllowFail runs git tolerating a non-zero exit, for commands whose
// failure is part of the fixture (a conflicted merge). Returns the output.
func (b *builder) gitAllowFail(args ...string) string {
	b.t.Helper()

	bin, err := exec.LookPath("git")
	if err != nil {
		b.t.Skip("git not found in PATH")
	}
	full := append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "core.autocrlf=false",
	}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = b.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+b.authorName,
		"GIT_AUTHOR_EMAIL="+b.authorEmail,
		"GIT_COMMITTER_NAME="+authorName,
		"GIT_COMMITTER_EMAIL="+authorEmail,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_PAGER=cat",
	)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func (b *builder) git(args ...string) string {
	b.t.Helper()
	return Git(b.t, b.dir, args...)
}

// commitAt stages every change and commits with an explicit pinned
// timestamp, returning the full hash.
func (b *builder) commitAt(msg string, stamp int64) string {
	b.t.Helper()
	at := fmt.Sprintf("@%d +0000", stamp)
	env := []string{
		"GIT_AUTHOR_DATE=" + at,
		"GIT_COMMITTER_DATE=" + at,
		"GIT_AUTHOR_NAME=" + b.authorName,
		"GIT_AUTHOR_EMAIL=" + b.authorEmail,
	}
	gitEnv(b.t, b.dir, env, "commit", "-q", "-m", msg)
	return b.headHash()
}

// commit stages everything and commits at the next pinned time. It returns
// the full commit hash.
func (b *builder) commit(msg string) string {
	b.t.Helper()
	b.step++
	b.git("add", "-A")
	return b.commitAt(msg, epochBase+int64(b.step)*60)
}

// commitStaged commits the index as-is (no staging step), for fixtures
// that prepare the index precisely.
func (b *builder) commitStaged(msg string) string {
	b.t.Helper()
	b.step++
	return b.commitAt(msg, epochBase+int64(b.step)*60)
}

// headHash returns the full hash of the current HEAD.
func (b *builder) headHash() string {
	b.t.Helper()
	return strings.TrimSpace(b.git("rev-parse", "HEAD"))
}

// write creates a file with content under the fixture root, making parent
// directories.
func (b *builder) write(path, content string) {
	b.t.Helper()
	full := filepath.Join(b.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		b.t.Fatal(err)
	}
}

// writeBinary creates a file that git must treat as binary (it contains a
// NUL byte).
func (b *builder) writeBinary(path string, content []byte) {
	b.t.Helper()
	full := filepath.Join(b.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		b.t.Fatal(err)
	}
}

// chmod adjusts a file's mode so a later commit records a mode change.
func (b *builder) chmod(path string, mode os.FileMode) {
	b.t.Helper()
	if err := os.Chmod(filepath.Join(b.dir, filepath.FromSlash(path)), mode); err != nil {
		b.t.Fatal(err)
	}
}

// basic: a short linear history covering every name-status letter —
// modify, add, rename (high similarity), binary add, mode change, delete.
func (b *builder) basic() {
	b.write("README.md", "# demo\n\nA small repository for squint tests.\n")
	b.write("notes.txt", "scratch notes\n")
	b.write("src/app.go", "package main\n\nfunc main() {}\n")
	b.commit("Initial commit")

	b.write("src/app.go", "package main\n\nfunc main() {\n\tprintln(\"lane fixed\")\n}\n")
	b.commit("Fix lane flicker on reload")

	b.write("src/renderer/render.go", "package renderer\n\nfunc Render() {}\n")
	b.write("tools/run.sh", "#!/bin/sh\nexec ./app\n")
	b.chmod("tools/run.sh", 0o755)
	b.commit("Add renderer and run script")

	b.git("mv", "src/renderer/render.go", "src/renderer/canvas.go")
	b.commit("Rename render.go to canvas.go")

	b.writeBinary("assets/logo.bin", []byte("BI\xff\x00NARY\x01\x02"))
	b.chmod("tools/run.sh", 0o644)
	if err := os.Remove(filepath.Join(b.dir, "notes.txt")); err != nil {
		b.t.Fatal(err)
	}
	b.commit("Add logo, drop notes, settle script mode")
}

// merges: two divergent branches whose conflicting edit forces a real
// merge commit. The conflict resolution makes shared.txt differ from both
// parents, so the combined view has content and the first-parent view
// differs from it.
func (b *builder) merges() {
	b.write("shared.txt", "line1\nline2\n")
	b.commit("Base")

	b.git("checkout", "-q", "-b", "feature")
	b.write("shared.txt", "line1\nfeature\n")
	b.commit("Feature edit")
	b.write("feature-only.txt", "feature\n")
	b.commit("Add feature file")

	b.git("checkout", "-q", branchName)
	b.write("shared.txt", "line1\nmainline\n")
	b.commit("Main edit")
	b.write("main-only.txt", "main\n")
	b.commit("Add main file")

	b.gitAllowFail("merge", "--no-commit", "--no-ff", "feature") // conflicts by design
	b.write("shared.txt", "line1\nmerged\n")
	b.git("add", "shared.txt")
	b.commitStaged("Merge branch 'feature'")
}

// longMsg: an LLM-style multi-paragraph commit message authored by a
// unicode identity.
func (b *builder) longMsg() {
	b.authorName = "李雷"
	b.authorEmail = "lei@example.com"

	b.write("pipeline.go", "package ingest\n\nfunc Run() {}\n")
	b.commit("Set up ingest package")

	b.write("pipeline.go", "package ingest\n\n// Run processes one lane.\nfunc Run() int { return 0 }\n")
	b.commit("Refactor the ingest pipeline\n\n" +
		"The old pipeline re-read the whole buffer for every lane, which\n" +
		"showed up as flicker on reload and made the renderer stall on\n" +
		"larger repositories.\n\n" +
		"Second paragraph: keep the public API stable and swap the internals\n" +
		"behind a small interface — lanes now stream, and the buffer is\n" +
		"reused across reloads (¶ counts as unicode too: ✓).\n\n" +
		"Third paragraph: footer notes such as review hints stay in the raw\n" +
		"body; the viewer must never truncate them.")
}

// unicodePaths: paths that git would quote or mangle without -z plus
// core.quotePath=false.
func (b *builder) unicodePaths() {
	b.write("路径/文件.txt", "unicode content\n")
	b.write("документы/отчёт.md", "# отчёт\n")
	b.write("folder with space/file name.txt", "spaces survive\n")
	b.commit("Add unicode paths")

	b.write("路径/文件.txt", "unicode content v2\n")
	b.commit("Update unicode file")
}

// submodule: a gitlink entry (mode 160000) registered via update-index, so
// the fixture needs no network, no .gitmodules, and no submodule checkout.
// A second commit bumps the gitlink to exercise submodule diffs.
func (b *builder) submodule() {
	// Build the inner repository in its own temp directory.
	inner := filepath.Join(b.t.TempDir(), "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		b.t.Fatal(err)
	}
	gitEnv(b.t, inner, nil, "init", "-q", ".")
	gitEnv(b.t, inner, nil, "symbolic-ref", "HEAD", "refs/heads/"+branchName)
	if err := os.WriteFile(filepath.Join(inner, "lib.go"), []byte("package lib\n"), 0o644); err != nil {
		b.t.Fatal(err)
	}
	gitEnv(b.t, inner, nil, "add", "-A")
	gitEnv(b.t, inner, []string{
		"GIT_AUTHOR_DATE=@1735689600 +0000", "GIT_COMMITTER_DATE=@1735689600 +0000",
	}, "commit", "-q", "-m", "inner v1")
	sha1 := strings.TrimSpace(gitEnv(b.t, inner, nil, "rev-parse", "HEAD"))

	// Register the gitlink without touching the worktree.
	b.git("update-index", "--add", "--cacheinfo", "160000,"+sha1+",vendor/lib")
	b.commitStaged("Pin submodule at inner v1")

	// Advance the inner repo and bump the gitlink.
	if err := os.WriteFile(filepath.Join(inner, "lib.go"), []byte("package lib\n\nfunc V2() {}\n"), 0o644); err != nil {
		b.t.Fatal(err)
	}
	gitEnv(b.t, inner, nil, "add", "-A")
	gitEnv(b.t, inner, []string{
		"GIT_AUTHOR_DATE=@1735689900 +0000", "GIT_COMMITTER_DATE=@1735689900 +0000",
	}, "commit", "-q", "-m", "inner v2")
	sha2 := strings.TrimSpace(gitEnv(b.t, inner, nil, "rev-parse", "HEAD"))

	b.git("update-index", "--cacheinfo", "160000,"+sha2+",vendor/lib")
	b.commitStaged("Bump submodule to inner v2")
}

// shallow: a depth-1 clone of a two-commit repository. Best-effort: hosts
// or git builds without local shallow support skip the test that needs it.
func (b *builder) shallow() {
	// Source repository with two commits, built in its own temp dir.
	src := filepath.Join(b.t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		b.t.Fatal(err)
	}
	gitEnv(b.t, src, nil, "init", "-q", ".")
	gitEnv(b.t, src, nil, "symbolic-ref", "HEAD", "refs/heads/"+branchName)
	if err := os.WriteFile(filepath.Join(src, "doc.txt"), []byte("v1\n"), 0o644); err != nil {
		b.t.Fatal(err)
	}
	gitEnv(b.t, src, nil, "add", "-A")
	gitEnv(b.t, src, []string{
		"GIT_AUTHOR_DATE=@1735689600 +0000", "GIT_COMMITTER_DATE=@1735689600 +0000",
	}, "commit", "-q", "-m", "first")
	if err := os.WriteFile(filepath.Join(src, "doc.txt"), []byte("v2\n"), 0o644); err != nil {
		b.t.Fatal(err)
	}
	gitEnv(b.t, src, nil, "add", "-A")
	gitEnv(b.t, src, []string{
		"GIT_AUTHOR_DATE=@1735689660 +0000", "GIT_COMMITTER_DATE=@1735689660 +0000",
	}, "commit", "-q", "-m", "second")

	// Shallow-clone into the (empty) fixture directory. Local clones need
	// a file:// URL for --depth to take effect.
	url := "file://" + filepath.ToSlash(src)
	if runtime.GOOS == "windows" && !strings.HasPrefix(url, "file:///") {
		url = "file:///" + strings.TrimPrefix(filepath.ToSlash(src), "/")
	}
	out := b.gitAllowFail("clone", "--depth", "1", "-q", url, b.dir)
	if _, err := os.Stat(filepath.Join(b.dir, ".git")); err != nil {
		b.t.Skipf("cannot create shallow clone on this host: %s", strings.TrimSpace(out))
	}
}

// manyFiles: a wide commit (60 new files) followed by a narrow one, for
// file-list and diff volume tests.
func (b *builder) manyFiles() {
	for i := range 60 {
		b.write(fmt.Sprintf("gen/file%03d.txt", i), fmt.Sprintf("content %d\n", i))
	}
	b.commit("Add generated files")

	for i := range 5 {
		b.write(fmt.Sprintf("gen/file%03d.txt", i), fmt.Sprintf("changed %d\n", i))
	}
	b.commit("Touch a few files")
}

// Package diff parses unified diffs into hunks and lines, and builds
// side-by-side pairings for the diff viewer.
//
// The package is pure: it performs no I/O and never invokes git. Input is
// raw diff text produced by the git layer; output is structured data for
// the TUI.
package diff

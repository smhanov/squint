// Package watch observes a repository for external changes and emits
// debounced refresh notifications.
//
// It combines filesystem notifications with a polling fallback so that the
// commit list stays current while another process (for example, a coding
// agent) commits.
package watch

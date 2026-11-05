package model

import (
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// copy.go implements y/Y (FR-13): copy the selected commit's abbreviated
// or full hash. The primary path is OSC 52 — bubbletea's SetClipboard
// emits the escape sequence through the renderer, which works across
// ssh and most modern terminals. Because OSC 52 support varies, a
// best-effort OS clipboard binary runs alongside it; whichever arrives
// first wins and the other is harmless.

// copyHash queues the selected commit's hash for copying. Abbreviated by
// default (y); full with Y.
func (a App) copyHash(full bool) tea.Cmd {
	if a.selectedHash == "" {
		return nil
	}
	hash := a.selectedHash
	if !full {
		hash = hash[:min(abbrevLen, len(hash))]
	}
	return copyCmd(hash)
}

// copyCmd batches the OSC 52 emission and the OS clipboard fallback.
func copyCmd(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), clipboardFallbackCmd(runtime.GOOS, text))
}

// clipboardBinary returns the OS-specific clipboard writer: its name and
// whether the platform has one. goos is a parameter so tests can cover
// every platform without switching hosts.
func clipboardBinary(goos string) (string, bool) {
	switch goos {
	case "darwin":
		return "pbcopy", true
	case "windows":
		return "clip", true
	case "linux":
		return "wl-copy", true
	default:
		return "", false
	}
}

// clipboardFallbackCmd writes text to the system clipboard through the
// platform binary. It is quiet by design: a missing binary (wl-copy on a
// GNOME/X11 box, clip under some Windows environments) is normal — the
// OSC 52 path has already handled the copy where supported.
func clipboardFallbackCmd(goos, text string) tea.Cmd {
	bin, ok := clipboardBinary(goos)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if _, err := exec.LookPath(bin); err != nil {
			return nil // no clipboard binary: nothing to fall back to
		}
		cmd := exec.Command(bin)
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run() // best-effort; OSC 52 remains the primary path
		return nil
	}
}

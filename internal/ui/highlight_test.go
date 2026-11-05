package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Lexer resolution: known extensions get a lexer; unknown files fall
// back to mono. The result is cached per filename.
func TestLexerFor(t *testing.T) {
	for _, name := range []string{"main.go", "app.ts", "script.py", "run.sh", "README.md"} {
		if lexerFor(name) == nil {
			t.Errorf("lexerFor(%q) = nil, want a lexer", name)
		}
	}
	for _, name := range []string{"data.unknownext", "logo.bin", ""} {
		if lexerFor(name) != nil {
			t.Errorf("lexerFor(%q) = %v, want nil (mono fallback)", name, lexerFor(name))
		}
	}
}

// Highlight colors recognizable tokens and leaves unknown files mono.
func TestHighlight(t *testing.T) {
	ResetHighlightCache()
	spent := 0

	// A Go line with a keyword, a string, and a comment.
	got := Highlight("x.go", `x := "s" // note`, &spent)
	if !strings.Contains(got, StyleString.Render(`"s"`)) {
		t.Errorf("string token not styled green: %q", got)
	}
	if !strings.Contains(got, StyleTag.Render(":=")) {
		t.Errorf("operator token not styled: %q", got)
	}
	if spent != 1 {
		t.Errorf("spent = %d, want 1 (one tokenization)", spent)
	}

	// The same line again: cache hit, no new tokenization.
	if again := Highlight("x.go", `x := "s" // note`, &spent); again != got {
		t.Error("cache returned a different rendering for the same line")
	}
	if spent != 1 {
		t.Errorf("spent after cache hit = %d, want 1", spent)
	}

	// Unknown file type: mono passthrough.
	if plain := Highlight("x.unknownext", `x := "s"`, &spent); plain != `x := "s"` {
		t.Errorf("unknown type rendered %q, want passthrough", plain)
	}

	// Empty filename: passthrough.
	if plain := Highlight("", "line", &spent); plain != "line" {
		t.Errorf("empty filename rendered %q, want passthrough", plain)
	}
}

// The budget caps tokenizations per render pass; over-budget lines stay
// mono but a fresh pass picks them up via the cache of earlier lines.
func TestHighlightBudget(t *testing.T) {
	ResetHighlightCache()
	old := highlightBudget
	highlightBudget = 2
	defer func() { highlightBudget = old }()

	spent := 0
	a := Highlight("x.py", "import os", &spent)
	b := Highlight("x.py", "import sys", &spent)
	if a == "import os" || b == "import sys" {
		t.Error("lines under the budget were not highlighted")
	}
	if spent != 2 {
		t.Fatalf("spent = %d, want 2", spent)
	}
	c := Highlight("x.py", "import json", &spent)
	if c != "import json" {
		t.Errorf("over-budget line was highlighted: %q", c)
	}

	// A later pass re-highlights the missed line (budget resets, cache
	// answers the first two for free).
	spent = 0
	_ = Highlight("x.py", "import os", &spent)
	_ = Highlight("x.py", "import sys", &spent)
	again := Highlight("x.py", "import json", &spent)
	if again == "import json" {
		t.Error("second pass did not pick up the previously skipped line")
	}
}

// Token styles degrade: every bucket style carries a color that lipgloss
// can drop entirely (NO_COLOR handled by the colorprofile writer).
func TestTokenStylesDegrade(t *testing.T) {
	for _, s := range []lipgloss.Style{StyleKeyword, StyleString, StyleNumber, StyleName, StyleFunc, StyleComment, StyleTag} {
		if got := s.Render("x"); !strings.Contains(got, "x") {
			t.Errorf("style render lost its text: %q", got)
		}
	}
}

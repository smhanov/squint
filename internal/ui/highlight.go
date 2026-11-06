package ui

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// highlight.go renders source lines with chroma syntax highlighting
// (spec FR-7). Colors flow through lipgloss styles — never raw ANSI — so
// the colorprofile writer keeps degrading them (truecolor → 256 → 16 →
// mono) and NO_COLOR strips them like every other squint color. Files
// without a matching lexer fall back to mono.

// HighlightBudget caps how many lines one render pass tokenizes. Beyond
// it lines stay mono; the per-line cache catches them on later passes.
const HighlightBudget = 4000

// highlightBudget mirrors HighlightBudget but is swappable in tests.
var highlightBudget = HighlightBudget

// highlightStyles maps chroma token categories onto the fixed squint
// palette (spec §7.5: ANSI-16, degradation via lipgloss).
var (
	StyleKeyword = lipgloss.NewStyle().Foreground(ColorMerge).Bold(true) // keywords: magenta, bold
	StyleString  = lipgloss.NewStyle().Foreground(ColorAdd)              // strings: green
	StyleNumber  = lipgloss.NewStyle().Foreground(ColorAhead)            // numbers: cyan
	StyleName    = lipgloss.NewStyle().Foreground(ColorRename)           // names/types: blue
	StyleFunc    = lipgloss.NewStyle().Foreground(ColorDot).Bold(true)   // function names: blue bold
	StyleComment = lipgloss.NewStyle().Faint(true)                       // comments: faint
	StyleTag     = lipgloss.NewStyle().Foreground(ColorDirty)            // operators: red
)

// tokenStyle buckets a chroma token type into a squint style.
func tokenStyle(t chroma.TokenType) lipgloss.Style {
	switch {
	case t.InCategory(chroma.Comment):
		return StyleComment
	case t.InCategory(chroma.Keyword):
		return StyleKeyword
	case t.InCategory(chroma.LiteralString):
		return StyleString
	case t.InCategory(chroma.LiteralNumber):
		return StyleNumber
	case t.InCategory(chroma.Name):
		switch {
		case t.InSubCategory(chroma.NameFunction):
			return StyleFunc
		default:
			return StyleName
		}
	case t.InCategory(chroma.Operator):
		return StyleTag
	case t.InCategory(chroma.Generic):
		return StyleFaint
	default:
		return lipgloss.NewStyle()
	}
}

// lexerCache caches lexers by filename; lexers.Match scans every
// registered pattern, far too slow to repeat per line.
var lexerCache sync.Map // string → chroma.Lexer (nil values = mono)

// lexerFor returns the chroma lexer for a filename, or nil for files no
// lexer claims (mono fallback).
func lexerFor(filename string) chroma.Lexer {
	if filename == "" {
		return nil
	}
	if v, ok := lexerCache.Load(filename); ok {
		l, _ := v.(chroma.Lexer)
		return l
	}
	l := lexers.Match(filename)
	lexerCache.Store(filename, l)
	return l
}

// lineCache caches rendered (highlighted) lines keyed by lexer name and
// input, so re-renders of the same diff — the 30s age tick, resizes,
// option toggles — skip chroma entirely. Cleared wholesale when it
// outgrows its cap. All rendering happens on the Update goroutine; the
// mutex only guards the (cheap) test resets.
var (
	lineCacheMu  sync.Mutex
	lineCache    = map[string]string{}
	lineCacheCap = 8192
)

// Highlight renders one source line with syntax colors for its file type.
// Unknown file types and lexer failures return the line unchanged.
// Budgeted: after highlightBudget tokenizations in this pass (tracked
// through *spent), the line is returned mono.
func Highlight(filename, line string, spent *int) string {
	lx := lexerFor(filename)
	if lx == nil {
		return line
	}
	key := lx.Config().Name + "\x00" + line
	lineCacheMu.Lock()
	if v, ok := lineCache[key]; ok {
		lineCacheMu.Unlock()
		return v
	}
	lineCacheMu.Unlock()
	if spent != nil {
		if *spent >= highlightBudget {
			return line
		}
		*spent++
	}
	// Tokenise(nil, …): passing a non-nil options struct trips a state
	// bug in chroma (verified on v2.27.0, the latest release as of
	// 2026-11); plain options are used deliberately. Revisit when a
	// chroma release fixes TokeniseOptions.
	it, err := lx.Tokenise(nil, line)
	if err != nil {
		return line
	}
	var b strings.Builder
	for _, tok := range it.Tokens() {
		if tok.Value == "" {
			continue
		}
		b.WriteString(tokenStyle(tok.Type).Render(tok.Value))
	}
	out := b.String()

	lineCacheMu.Lock()
	if len(lineCache) > lineCacheCap {
		lineCache = map[string]string{}
	}
	lineCache[key] = out
	lineCacheMu.Unlock()
	return out
}

// ResetHighlightCache empties the rendered-line cache (tests only).
func ResetHighlightCache() {
	lineCacheMu.Lock()
	lineCache = map[string]string{}
	lineCacheMu.Unlock()
}

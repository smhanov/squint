package diff

import (
	"fmt"
	"strings"
	"testing"
)

// segs renders intraline segments compactly for assertions.
func segs(ws []WordSeg) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = fmt.Sprintf("%d:%q", w.Kind, w.Text)
	}
	return strings.Join(parts, ",")
}

func TestWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // segs encoding; "" means nil
	}{
		{"noMarkers", "plain line", ""},
		{"addOnly", "keep {+new+} tail", `0:"keep ",1:"new",0:" tail"`},
		{"delOnly", "keep [-old-] tail", `0:"keep ",2:"old",0:" tail"`},
		{"both", "a [-x-]{+y+} b", `0:"a ",2:"x",1:"y",0:" b"`},
		{"adjacent", "[-x-]{+y+}", `2:"x",1:"y"`},
		{"wholeLineAdd", "{+added line+}", `1:"added line"`},
		{"wholeLineDel", "[-removed line-]", `2:"removed line"`},
		{"unterminatedAdd", "before {+open", `0:"before ",0:"{+open"`},
		{"unterminatedDel", "before [-open", `0:"before ",0:"[-open"`},
		{"emptyAddMarker", "{++}[-x-]", `2:"x"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Words(tc.in)
			if tc.want == "" {
				if got != nil {
					t.Errorf("Words(%q) = %s, want nil", tc.in, segs(got))
				}
				return
			}
			if s := segs(got); s != tc.want {
				t.Errorf("Words(%q) = %s, want %s", tc.in, s, tc.want)
			}
		})
	}
}

// The real --word-diff=plain shape: unchanged lines carry no prefix,
// changed lines carry inline markers, fully added lines are {+…+}.
func TestParseWordDiff(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"index 43c7d80..8a6707b 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,2 +1,3 @@",
		"alpha [-beta-]{+OMEGA+} gamma",
		"delta",
		"{+new line here+}",
	}, "\n") + "\n")

	files := ParseWordDiff(in)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	h := files[0].Hunks[0]
	if got, want := len(h.Lines), 3; got != want {
		t.Fatalf("got %d lines, want %d", got, want)
	}

	mixed := h.Lines[0]
	if mixed.Kind != Context || mixed.OldNo != 1 || mixed.NewNo != 1 {
		t.Errorf("mixed line = %+v, want Context 1/1", mixed)
	}
	if !mixed.HasWords() {
		t.Fatal("mixed line lost its word segments")
	}
	if s := segs(mixed.Words); !strings.Contains(s, `2:"beta"`) || !strings.Contains(s, `1:"OMEGA"`) {
		t.Errorf("mixed segments = %s, want del beta + add OMEGA", s)
	}

	ctx := h.Lines[1]
	if ctx.Kind != Context || ctx.OldNo != 2 || ctx.NewNo != 2 || ctx.HasWords() {
		t.Errorf("context line = %+v, want Context 2/2 without segments", ctx)
	}

	add := h.Lines[2]
	if add.Kind != Add || add.OldNo != 0 || add.NewNo != 3 {
		t.Errorf("added line = %+v, want Add with new number 3", add)
	}
}

// Hunk line counters stay consistent with the @@ header in word-diff
// form, including a pure removal.
func TestParseWordDiffCounters(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,2 @@",
		"keep",
		"[-gone entirely-]",
		"kept {+changed+}",
	}, "\n") + "\n")

	h := ParseWordDiff(in)[0].Hunks[0]
	if len(h.Lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(h.Lines))
	}
	del := h.Lines[1]
	if del.Kind != Del || del.OldNo != 2 || del.NewNo != 0 {
		t.Errorf("removed line = %+v, want Del old 2", del)
	}
	mixed := h.Lines[2]
	if mixed.Kind != Context || mixed.OldNo != 3 || mixed.NewNo != 2 {
		t.Errorf("mixed line = %+v, want Context old 3 new 2", mixed)
	}
}

// pairs renders paired rows compactly for assertions.
func pairsRender(pairs []SidePair) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		l, r := "·", "·"
		if p.Left != nil {
			l = fmt.Sprintf("%s:%s", p.Left.Kind, p.Left.Text)
		}
		if p.Right != nil {
			r = fmt.Sprintf("%s:%s", p.Right.Kind, p.Right.Text)
		}
		parts[i] = l + "|" + r
	}
	return strings.Join(parts, "\n")
}

func hunkWith(lines ...string) []DiffLine {
	in := strings.Join(append([]string{
		"diff --git a/x.txt b/x.txt", "--- a/x.txt", "+++ b/x.txt", "@@ -1,4 +1,4 @@",
	}, lines...), "\n")
	return ParseUnified([]byte(in + "\n"))[0].Hunks[0].Lines
}

func TestPairLines(t *testing.T) {
	t.Run("ContextOnly", func(t *testing.T) {
		lines := hunkWith(" a", " b")
		got := PairLines(lines)
		if len(got) != 2 || got[0].Left == nil || got[0].Right == nil {
			t.Errorf("PairLines(context) = %s", pairsRender(got))
		}
	})

	t.Run("EqualRunsPairByIndex", func(t *testing.T) {
		lines := hunkWith(" ctx", "-old1", "-old2", "+new1", "+new2", " ctx2")
		got := pairsRender(PairLines(lines))
		want := strings.Join([]string{
			"context:ctx|context:ctx",
			"del:old1|add:new1",
			"del:old2|add:new2",
			"context:ctx2|context:ctx2",
		}, "\n")
		if got != want {
			t.Errorf("PairLines =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("UnequalRunsPadShorterSide", func(t *testing.T) {
		lines := hunkWith("-a", "-b", "-c", "+x")
		got := pairsRender(PairLines(lines))
		want := strings.Join([]string{
			"del:a|add:x",
			"del:b|·",
			"del:c|·",
		}, "\n")
		if got != want {
			t.Errorf("PairLines =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("PureAdditionRun", func(t *testing.T) {
		lines := hunkWith("+n1", "+n2")
		got := pairsRender(PairLines(lines))
		want := strings.Join([]string{
			"·|add:n1",
			"·|add:n2",
		}, "\n")
		if got != want {
			t.Errorf("PairLines =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("PureRemovalRun", func(t *testing.T) {
		lines := hunkWith("-o1")
		got := pairsRender(PairLines(lines))
		if want := "del:o1|·"; got != want {
			t.Errorf("PairLines = %s, want %s", got, want)
		}
	})

	t.Run("InterleavedRunsSplitAtContext", func(t *testing.T) {
		lines := hunkWith("-a", "+x", " ctx", "-b", "+y")
		got := pairsRender(PairLines(lines))
		want := strings.Join([]string{
			"del:a|add:x",
			"context:ctx|context:ctx",
			"del:b|add:y",
		}, "\n")
		if got != want {
			t.Errorf("PairLines =\n%s\nwant\n%s", got, want)
		}
	})
}

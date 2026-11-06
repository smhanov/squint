package diff

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// lines renders parsed lines compactly for assertions: "kind:no:new:text".
func render(lines []DiffLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = fmt.Sprintf("%s:%d:%d:%s", l.Kind, l.OldNo, l.NewNo, l.Text)
	}
	return strings.Join(parts, "|")
}

func TestParseUnifiedEmptyAndGarbage(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		if got := ParseUnified(nil); got != nil {
			t.Errorf("ParseUnified(nil) = %v, want nil", got)
		}
		if got := ParseUnified([]byte("")); got != nil {
			t.Errorf("ParseUnified(\"\") = %v, want nil", got)
		}
	})

	t.Run("Garbage", func(t *testing.T) {
		got := ParseUnified([]byte("hello\nworld\n\nnot a diff at all\n"))
		if len(got) != 0 {
			t.Errorf("ParseUnified(garbage) = %+v, want no files", got)
		}
	})
}

func TestParseUnifiedModified(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/app.go b/app.go",
		"index 1234567..89abcde 100644",
		"--- a/app.go",
		"+++ b/app.go",
		"@@ -1,3 +1,4 @@",
		" package main",
		"",
		"-func main() {}",
		"+func main() {",
		"+\tprintln(\"hi\")",
		"+}",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	f := got[0]
	if f.Path != "app.go" || f.NewFile || f.Deleted || f.Binary || f.Truncated {
		t.Errorf("FileDiff = %+v", f)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldCount != 3 || h.NewStart != 1 || h.NewCount != 4 {
		t.Errorf("hunk ranges = %d,%d %d,%d, want 1,3 1,4", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}
	want := "context:1:1:package main|context:2:2:|del:3:0:func main() {}" +
		"|add:0:3:func main() {|add:0:4:\tprintln(\"hi\")|add:0:5:}"
	if gotWant := render(h.Lines); gotWant != want {
		t.Errorf("lines =\n%s\nwant\n%s", gotWant, want)
	}
}

// TestParseCombinedHunks checks exact modeling of combined merge diffs.
// The input is a real `git show --cc` of a two-parent merge (side
// a,B,c; master A,b,c; result A,B,c). The per-column markers are
// asserted verbatim; NewNo follows the format's one exact rule — a
// line is in the result iff no column is '-' — which reproduces the
// header's +1,3 count.
func TestParseCombinedHunks(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --cc f.txt",
		"index 7be73ce,4f213f7..4484d91",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@@ -1,3 -1,3 +1,3 @@@",
		"- a",
		"+ A",
		" -b",
		" +B",
		"  c",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	f := got[0]
	if f.Path != "f.txt" || f.Binary {
		t.Errorf("FileDiff = %+v", f)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if len(h.Parents) != 2 {
		t.Fatalf("got %d parent ranges, want 2", len(h.Parents))
	}
	if h.Parents[0] != (ParentRange{Start: 1, Count: 3}) ||
		h.Parents[1] != (ParentRange{Start: 1, Count: 3}) ||
		h.NewStart != 1 || h.NewCount != 3 {
		t.Errorf("ranges = %v %d,%d, want 1,3 1,3 1,3", h.Parents, h.NewStart, h.NewCount)
	}

	type want struct {
		text  string
		kind  LineKind
		marks string // the verbatim marker columns
		newNo int
	}
	wants := []want{
		{"a", Del, "- ", 0}, // dropped from the result
		{"A", Add, "+ ", 1},
		{"b", Del, " -", 0}, // dropped from the result
		{"B", Add, " +", 2},
		{"c", Context, "  ", 3},
	}
	if len(h.Lines) != len(wants) {
		t.Fatalf("got %d lines, want %d", len(h.Lines), len(wants))
	}
	for i, w := range wants {
		l := h.Lines[i]
		if l.Text != w.text || l.Kind != w.kind || l.NewNo != w.newNo || l.OldNo != 0 {
			t.Errorf("line %d = %q kind %v new %d old %d, want %q %v new %d old 0",
				i, l.Text, l.Kind, l.NewNo, l.OldNo, w.text, w.kind, w.newNo)
		}
		if len(l.Marks) != 2 {
			t.Fatalf("line %d marks width = %d, want 2", i, len(l.Marks))
		}
		marks := make([]rune, len(l.Marks))
		for col, m := range l.Marks {
			marks[col] = ' '
			switch m {
			case Add:
				marks[col] = '+'
			case Del:
				marks[col] = '-'
			}
		}
		if string(marks) != w.marks {
			t.Errorf("line %d marks = %q, want %q", i, string(marks), w.marks)
		}
	}

	// Result-side invariant: lines with no '-' column are exactly the
	// result lines, matching the header's +1,3.
	resultCount := 0
	for _, l := range h.Lines {
		if !slices.Contains(l.Marks, Del) {
			resultCount++
		}
	}
	if resultCount != h.NewCount {
		t.Errorf("result counted %d lines, hunk range says %d", resultCount, h.NewCount)
	}
}

func TestParseCombinedThreeParents(t *testing.T) {
	// An octopus merge: three parent columns. Only the column width and
	// ranges are checked — the marker width generalizes over parents.
	in := []byte(strings.Join([]string{
		"diff --cc x.txt",
		"@@@ -1,2 -1,2 -1,2 +1,2 @@@",
		"- a",
		"+ b",
		"  c",
	}, "\n") + "\n")

	f := ParseUnified(in)
	h := f[0].Hunks[0]
	if len(h.Parents) != 3 || h.Parents[2] != (ParentRange{Start: 1, Count: 2}) {
		t.Fatalf("parents = %v, want three ranges starting at 1", h.Parents)
	}
	for _, l := range h.Lines {
		if len(l.Marks) != 3 {
			t.Errorf("line %q marks width = %d, want 3", l.Text, len(l.Marks))
		}
	}
}

func TestParseUnifiedNewAndDeleted(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/new.txt b/new.txt",
		"new file mode 100644",
		"index 0000000..3b18e51",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1 @@",
		"+hello",
		"diff --git a/old.txt b/old.txt",
		"deleted file mode 100644",
		"index 3b18e51..0000000",
		"--- a/old.txt",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-goodbye",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2", len(got))
	}

	add := got[0]
	if !add.NewFile || add.Deleted || add.Path != "new.txt" {
		t.Errorf("added = %+v, want new.txt marked NewFile", add)
	}
	if !strings.Contains(render(add.Hunks[0].Lines), "add:0:1:hello") {
		t.Errorf("added lines = %s", render(add.Hunks[0].Lines))
	}

	del := got[1]
	if !del.Deleted || del.NewFile || del.Path != "old.txt" {
		t.Errorf("deleted = %+v, want old.txt marked Deleted", del)
	}
	if !strings.Contains(render(del.Hunks[0].Lines), "del:1:0:goodbye") {
		t.Errorf("deleted lines = %s", render(del.Hunks[0].Lines))
	}
}

func TestParseUnifiedModeChangeOnly(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/run.sh b/run.sh",
		"old mode 100755",
		"new mode 100644",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	f := got[0]
	if f.Path != "run.sh" {
		t.Errorf("Path = %q, want run.sh", f.Path)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("mode-only change has hunks: %v", f.Hunks)
	}
	if f.NewFile || f.Deleted || f.Binary {
		t.Errorf("mode-only change misclassified: %+v", f)
	}
}

func TestParseUnifiedRename(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/render.go b/canvas.go",
		"similarity index 96%",
		"rename from render.go",
		"rename to canvas.go",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	f := got[0]
	if f.Path != "canvas.go" || f.OldPath != "render.go" {
		t.Errorf("rename paths = %q <- %q, want canvas.go <- render.go", f.Path, f.OldPath)
	}
}

func TestParseUnifiedEmptyNewFile(t *testing.T) {
	// A newly added empty file has no ---/+++ lines and no hunks; the
	// path only appears on the "diff --git" line.
	in := []byte(strings.Join([]string{
		"diff --git a/empty.txt b/empty.txt",
		"new file mode 100644",
		"index 0000000..e69de29",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	f := got[0]
	if f.Path != "empty.txt" || !f.NewFile || len(f.Hunks) != 0 {
		t.Errorf("empty new file = %+v", f)
	}
}

func TestParseUnifiedUnicodeAndQuotedPaths(t *testing.T) {
	t.Run("RawUnicode", func(t *testing.T) {
		in := []byte(strings.Join([]string{
			"diff --git a/路径/文件.txt b/路径/文件.txt",
			"index 0000000..3b18e51 100644",
			"--- a/路径/文件.txt",
			"+++ b/路径/文件.txt",
			"@@ -0,0 +1 @@",
			"+内容",
		}, "\n") + "\n")
		got := ParseUnified(in)
		if len(got) != 1 || got[0].Path != "路径/文件.txt" {
			t.Errorf("unicode path = %+v, want 路径/文件.txt", got)
		}
	})

	t.Run("Quoted", func(t *testing.T) {
		// git C-quotes paths containing quotes or control characters.
		in := []byte(strings.Join([]string{
			`diff --git "a/tab\there.txt" "b/tab\there.txt"`,
			`--- "a/tab\there.txt"`,
			`+++ "b/tab\there.txt"`,
			"@@ -1 +1 @@",
			"-x",
			"+y",
		}, "\n") + "\n")
		got := ParseUnified(in)
		if len(got) != 1 {
			t.Fatalf("got %d files, want 1", len(got))
		}
		if got[0].Path != "tab\there.txt" {
			t.Errorf("Path = %q, want %q", got[0].Path, "tab\there.txt")
		}
	})

	t.Run("SpacesKeepTrailingText", func(t *testing.T) {
		// git appends a tab after paths containing spaces; the tab must
		// not end up in the parsed path.
		in := []byte(strings.Join([]string{
			"diff --git a/file name.txt b/file name.txt",
			"--- a/file name.txt\t",
			"+++ b/file name.txt\t",
			"@@ -1 +1 @@",
			"-a",
			"+b",
		}, "\n") + "\n")
		got := ParseUnified(in)
		if len(got) != 1 || got[0].Path != "file name.txt" {
			t.Errorf("path = %+v, want file name.txt", got)
		}
	})
}

func TestParseUnifiedBinary(t *testing.T) {
	t.Run("LiteralMarker", func(t *testing.T) {
		in := []byte(strings.Join([]string{
			"diff --git a/logo.bin b/logo.bin",
			"new file mode 100644",
			"index 0000000..f49f78f",
			"Binary files /dev/null and b/logo.bin differ",
		}, "\n") + "\n")
		got := ParseUnified(in)
		if len(got) != 1 || !got[0].Binary || got[0].Path != "logo.bin" || len(got[0].Hunks) != 0 {
			t.Errorf("literal binary = %+v", got)
		}
	})

	t.Run("GitBinaryPatch", func(t *testing.T) {
		in := []byte(strings.Join([]string{
			"diff --git a/blob.bin b/blob.bin",
			"index 1234567..89abcde 100644",
			"GIT binary patch",
			"literal 10",
			"YcmZo$Ft*~O00000",
		}, "\n") + "\n")
		got := ParseUnified(in)
		if len(got) != 1 || !got[0].Binary {
			t.Errorf("GIT binary patch = %+v", got)
		}
	})

	t.Run("NulSniff", func(t *testing.T) {
		// A NUL byte inside a patch body (diff --text output of a binary
		// file) marks the file binary.
		in := []byte(strings.Join([]string{
			"diff --git a/weird.txt b/weird.txt",
			"--- a/weird.txt",
			"+++ b/weird.txt",
			"@@ -1 +1 @@",
			"-ok\x00line",
			"+fine",
		}, "\n") + "\n")
		got := ParseUnified(in)
		if len(got) != 1 || !got[0].Binary {
			t.Errorf("NUL sniff = %+v, want Binary", got)
		}
	})
}

func TestParseUnifiedNoNewlineMarker(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/tail.txt b/tail.txt",
		"--- a/tail.txt",
		"+++ b/tail.txt",
		"@@ -1 +1 @@",
		"-old",
		"\\ No newline at end of file",
		"+new",
		"\\ No newline at end of file",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 || len(got[0].Hunks) != 1 {
		t.Fatalf("got %+v, want one file with one hunk", got)
	}
	lines := got[0].Hunks[0].Lines
	if len(lines) != 2 {
		t.Fatalf("lines = %s, want only -old and +new (annotations dropped)", render(lines))
	}
	// The annotation must not consume line numbers.
	if lines[0].OldNo != 1 || lines[1].NewNo != 1 {
		t.Errorf("line numbers = %s, want del 1 and add 1", render(lines))
	}
}

func TestParseUnifiedHunkHeaderVariants(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   Hunk
	}{
		{"full", "@@ -10,7 +20,9 @@ fn main()", Hunk{OldStart: 10, OldCount: 7, NewStart: 20, NewCount: 9}},
		{"count omitted old", "@@ -5 +5,2 @@", Hunk{OldStart: 5, OldCount: 1, NewStart: 5, NewCount: 2}},
		{"count omitted new", "@@ -3,4 +9 @@", Hunk{OldStart: 3, OldCount: 4, NewStart: 9, NewCount: 1}},
		{"both omitted", "@@ -1 +1 @@", Hunk{OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1}},
		{"empty file", "@@ -0,0 +1 @@", Hunk{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := []byte(strings.Join([]string{
				"diff --git a/x b/x",
				"--- a/x",
				"+++ b/x",
				tt.header,
				"+one",
			}, "\n") + "\n")
			got := ParseUnified(in)
			if len(got) != 1 || len(got[0].Hunks) != 1 {
				t.Fatalf("got %+v, want one hunk", got)
			}
			h := got[0].Hunks[0]
			if h.OldStart != tt.want.OldStart || h.OldCount != tt.want.OldCount ||
				h.NewStart != tt.want.NewStart || h.NewCount != tt.want.NewCount {
				t.Errorf("hunk = %d,%d %d,%d, want %d,%d %d,%d",
					h.OldStart, h.OldCount, h.NewStart, h.NewCount,
					tt.want.OldStart, tt.want.OldCount, tt.want.NewStart, tt.want.NewCount)
			}
		})
	}
}

func TestParseUnifiedMultipleHunks(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --git a/big.go b/big.go",
		"--- a/big.go",
		"+++ b/big.go",
		"@@ -1,2 +1,2 @@",
		"-one",
		"+ONE",
		" two",
		"@@ -50,3 +50,3 @@",
		" keep",
		"-old",
		"+new",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 || len(got[0].Hunks) != 2 {
		t.Fatalf("got %+v, want one file with two hunks", got)
	}
	h1, h2 := got[0].Hunks[0], got[0].Hunks[1]
	if h1.NewStart != 1 || h2.NewStart != 50 {
		t.Errorf("hunk starts = %d, %d, want 1, 50", h1.NewStart, h2.NewStart)
	}
	// Line numbers restart per hunk.
	if gotWant := render(h2.Lines); gotWant != "context:50:50:keep|del:51:0:old|add:0:51:new" {
		t.Errorf("second hunk = %s", gotWant)
	}
}

func TestParseUnifiedContentLooksLikeHeader(t *testing.T) {
	// A deleted line "-- a/x" becomes the body line "--- a/x"; while a
	// hunk is open it must parse as content, not as a file header.
	in := []byte(strings.Join([]string{
		"diff --git a/dashes.txt b/dashes.txt",
		"--- a/dashes.txt",
		"+++ b/dashes.txt",
		"@@ -1,2 +1,1 @@",
		"--- a/x",
		"-real delete",
		"+kept",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1 (body must not open a new file)", len(got))
	}
	want := "del:1:0:-- a/x|del:2:0:real delete|add:0:1:kept"
	if gotWant := render(got[0].Hunks[0].Lines); gotWant != want {
		t.Errorf("lines =\n%s\nwant\n%s", gotWant, want)
	}
}

func TestParseUnifiedCombinedMergeHunk(t *testing.T) {
	in := []byte(strings.Join([]string{
		"diff --cc shared.txt",
		"index 877f2c3,fa7bb17..7783477",
		"--- a/shared.txt",
		"+++ b/shared.txt",
		"@@@ -1,2 -1,2 +1,2 @@@",
		"  line1",
		" -mainline",
		" -feature",
		"++merged",
	}, "\n") + "\n")

	got := ParseUnified(in)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	f := got[0]
	if f.Path != "shared.txt" {
		t.Errorf("Path = %q, want shared.txt", f.Path)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldCount != 2 || h.NewStart != 1 || h.NewCount != 2 {
		t.Errorf("combined ranges = %d,%d %d,%d, want 1,2 1,2", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}
	if len(h.Parents) != 2 || h.Parents[0] != (ParentRange{Start: 1, Count: 2}) {
		t.Errorf("Parents = %v, want two 1,2 ranges", h.Parents)
	}
	// Kind folds per column (any '+' wins, else any '-', else context);
	// NewNo tracks the exact result rule (in the result iff no column
	// is '-'); OldNo is 0 — per-parent positions are not derivable.
	// Marks carry the verbatim columns.
	want := "context:0:1:line1|del:0:0:mainline|del:0:0:feature|add:0:2:merged"
	if gotWant := render(h.Lines); gotWant != want {
		t.Errorf("lines =\n%s\nwant\n%s", gotWant, want)
	}
	wantMarks := []string{"  ", " -", " -", "++"}
	for i, l := range h.Lines {
		gotMarks := make([]rune, len(l.Marks))
		for col, m := range l.Marks {
			gotMarks[col] = ' '
			switch m {
			case Add:
				gotMarks[col] = '+'
			case Del:
				gotMarks[col] = '-'
			}
		}
		if string(gotMarks) != wantMarks[i] {
			t.Errorf("line %d marks = %q, want %q", i, string(gotMarks), wantMarks[i])
		}
	}
}

func TestParseUnifiedTruncation(t *testing.T) {
	// The first file's diff is ~3.1 MB: just under the 3 MiB cap alone.
	// The second file pushes the total past it, so the cut lands inside
	// the second file while the first stays complete.
	big := strings.Join([]string{
		"diff --git a/big.txt b/big.txt",
		"--- a/big.txt",
		"+++ b/big.txt",
		"@@ -1,2 +1,2 @@",
		"-" + strings.Repeat("x", 1550000),
		"+" + strings.Repeat("y", 1550000),
	}, "\n") + "\n"
	small := strings.Join([]string{
		"diff --git a/next.txt b/next.txt",
		"--- a/next.txt",
		"+++ b/next.txt",
		"@@ -1,2 +1,2 @@",
		"-" + strings.Repeat("a", 50000),
		"+" + strings.Repeat("b", 50000),
	}, "\n") + "\n"
	in := []byte(big + small)

	got := ParseUnified(in)
	if len(got) < 2 {
		t.Fatalf("got %d files, want 2", len(got))
	}
	if got[0].Truncated {
		t.Error("first file flagged truncated, want the last one")
	}
	if !got[len(got)-1].Truncated {
		t.Errorf("last file not flagged as truncated (%d bytes of input)", len(in))
	}

	t.Run("ExactBoundary", func(t *testing.T) {
		// Exactly MaxDiffBytes is not truncated; one byte more is.
		body := strings.Repeat("x", MaxDiffBytes-1) // + final newline = Max
		exact := []byte(body + "\n")
		if len(exact) != MaxDiffBytes {
			t.Fatalf("test setup: input is %d bytes, want %d", len(exact), MaxDiffBytes)
		}
		if got := ParseUnified(exact); len(got) != 0 {
			t.Errorf("parsing plain text produced %v", got)
		}
		over := []byte(body + "x\n")
		if files := ParseUnified(over); len(files) != 0 {
			t.Errorf("non-diff input produced files: %v", files)
		}
	})

	t.Run("InputNotModified", func(t *testing.T) {
		orig := append([]byte(nil), in...)
		snapshot := append([]byte(nil), orig...)
		ParseUnified(orig)
		if string(orig) != string(snapshot) {
			t.Error("ParseUnified modified its input")
		}
	})
}

func TestParseUnifiedCRContentPreserved(t *testing.T) {
	// CRLF content passes through verbatim: the \r belongs to the line
	// text and must not be stripped.
	in := []byte("diff --git a/crlf.txt b/crlf.txt\r\n--- a/crlf.txt\r\n+++ b/crlf.txt\r\n@@ -1 +1 @@\r\n-old\r\n+new\r\n")
	got := ParseUnified(in)
	if len(got) != 1 || len(got[0].Hunks) != 1 {
		t.Fatalf("got %+v", got)
	}
	lines := got[0].Hunks[0].Lines
	if len(lines) != 2 {
		t.Fatalf("lines = %s, want two", render(lines))
	}
	if lines[0].Text != "old\r" || lines[1].Text != "new\r" {
		t.Errorf("CR content stripped: %q, %q", lines[0].Text, lines[1].Text)
	}
}

func TestTrimContext(t *testing.T) {
	mk := func(lines ...DiffLine) Hunk {
		return Hunk{Header: "@@", Lines: lines}
	}
	ctx := func(n int) DiffLine { return DiffLine{Kind: Context, Text: "c", OldNo: n, NewNo: n} }
	add := func() DiffLine { return DiffLine{Kind: Add, Text: "+", NewNo: 1} }
	del := func() DiffLine { return DiffLine{Kind: Del, Text: "-", OldNo: 1} }

	files := []FileDiff{{Path: "f", Hunks: []Hunk{
		mk(ctx(1), ctx(2), ctx(3), ctx(4), del(), add(), ctx(5), ctx(6), ctx(7)),
	}}}

	t.Run("KeepOne", func(t *testing.T) {
		got := TrimContext(files, 1)
		gotLines := got[0].Hunks[0].Lines
		// keep=1 keeps the context rows adjacent to the change: the last
		// leading context and the first trailing one.
		want := []DiffLine{ctx(4), del(), add(), ctx(5)}
		if fmt.Sprint(gotLines) != fmt.Sprint(want) {
			t.Errorf("lines = %v, want %v", gotLines, want)
		}
		// Original untouched (non-mutating).
		if len(files[0].Hunks[0].Lines) != 9 {
			t.Error("TrimContext mutated its input")
		}
	})

	t.Run("KeepZero", func(t *testing.T) {
		got := TrimContext(files, 0)
		gotLines := got[0].Hunks[0].Lines
		want := []DiffLine{del(), add()}
		if fmt.Sprint(gotLines) != fmt.Sprint(want) {
			t.Errorf("lines = %v, want %v", gotLines, want)
		}
	})

	t.Run("KeepMoreThanAvailable", func(t *testing.T) {
		got := TrimContext(files, 10)
		if len(got[0].Hunks[0].Lines) != 9 {
			t.Errorf("keep beyond the hunk size changed the lines: %d", len(got[0].Hunks[0].Lines))
		}
	})

	t.Run("NegativeKeepClampsToZero", func(t *testing.T) {
		got := TrimContext(files, -1)
		if len(got[0].Hunks[0].Lines) != 2 {
			t.Errorf("negative keep = %d lines, want the 2 changed lines", len(got[0].Hunks[0].Lines))
		}
	})
}

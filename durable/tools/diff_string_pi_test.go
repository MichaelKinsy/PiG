package tools

import (
	"strconv"
	"strings"
	"testing"
)

// pi: packages/durable/src/tools/edit-diff.ts

func numberedLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("l" + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

// generateDiffString (edit-diff.ts:377-486): a removed line is "-<old number> text", an added line "+<new number> text", and context
// is " <number> text" with the numbers right-aligned to the widest line number. Context longer than 4 lines on a side of a change is
// elided with " <blank> ..." and the line numbers skip the elided lines. firstChangedLine is the first change's line in the new file,
// or 0 (Pi's undefined) when nothing changed.
func TestGenerateDiffStringMatchesPi(t *testing.T) {
	replaceLine := func(content string, line int, text string) string {
		lines := strings.Split(content, "\n")
		lines[line-1] = text
		return strings.Join(lines, "\n")
	}
	twenty := numberedLines(20)
	cases := []struct {
		name      string
		old, new  string
		wantDiff  string
		wantFirst int
	}{
		{"one line replaced", "a\nb\nc\n", "a\nB\nc\n", " 1 a\n-2 b\n+2 B\n 3 c", 2},
		{"no change", "a\nb\n", "a\nb\n", "", 0},
		{"line appended", "a\n", "a\nb\n", " 1 a\n+2 b", 2},
		{
			"long context on both sides of one change", twenty, replaceLine(twenty, 10, "X"),
			strings.Join([]string{
				"    ...", "  6 l6", "  7 l7", "  8 l8", "  9 l9", "-10 l10", "+10 X", " 11 l11", " 12 l12", " 13 l13", " 14 l14", "    ...",
			}, "\n"), 10,
		},
		{
			"two changes with a long unchanged run between them", twenty, replaceLine(replaceLine(twenty, 1, "X"), 20, "Y"),
			strings.Join([]string{
				"- 1 l1", "+ 1 X", "  2 l2", "  3 l3", "  4 l4", "  5 l5", "    ...", " 16 l16", " 17 l17", " 18 l18", " 19 l19", "-20 l20", "+20 Y",
			}, "\n"), 1,
		},
	}
	for _, c := range cases {
		diff, first := GenerateDiffString(c.old, c.new)
		if diff != c.wantDiff || first != c.wantFirst {
			t.Errorf("%s:\n got %q first=%d\nwant %q first=%d", c.name, diff, first, c.wantDiff, c.wantFirst)
		}
	}
}

// generateUnifiedPatch (edit-diff.ts:366-371): jsdiff createTwoFilesPatch with the path as both file names, FILE_HEADERS_ONLY (no Index/=== lines)
// and 4 lines of context.
func TestGenerateUnifiedPatchMatchesPi(t *testing.T) {
	got := GenerateUnifiedPatch("f.txt", "a\nb\n", "a\nB\n")
	want := "--- f.txt\n+++ f.txt\n@@ -1,2 +1,2 @@\n a\n-b\n+B\n"
	if got != want {
		t.Fatalf("patch = %q, want %q", got, want)
	}
	twenty := numberedLines(20)
	got = GenerateUnifiedPatch("f.txt", twenty, strings.Replace(twenty, "l10\n", "X\n", 1))
	want = "--- f.txt\n+++ f.txt\n@@ -6,9 +6,9 @@\n l6\n l7\n l8\n l9\n-l10\n+X\n l11\n l12\n l13\n l14\n"
	if got != want {
		t.Fatalf("patch with 4 lines of context = %q, want %q", got, want)
	}
}

package tools

import (
	"strings"
	"testing"
)

// generateUnifiedPatch (edit-diff.ts:366-371, jsdiff structuredPatch with 4 lines of context): the unchanged lines after the last change end the hunk at 4 lines even when fewer than 2*4 remain
// (the same hunk `diff -U4` writes); unchanged lines between two changes stay in one hunk up to 2*4 and split into two hunks above that.
// mutation-checked: `<=` for `<` in `i < len(diff)-2` (the last real chunk joins the hunk whole) fails it.
func TestGenerateUnifiedPatchEndsAHunkWithFourLinesOfTrailingContext(t *testing.T) {
	sixteen := numberedLines(16)
	got := GenerateUnifiedPatch("f.txt", sixteen, strings.Replace(sixteen, "l10\n", "X\n", 1))
	want := "--- f.txt\n+++ f.txt\n@@ -6,9 +6,9 @@\n l6\n l7\n l8\n l9\n-l10\n+X\n l11\n l12\n l13\n l14\n"
	if got != want {
		t.Errorf("trailing context of 6 lines: patch = %q, want %q", got, want)
	}
	// Two changes with 8 unchanged lines between them stay in one hunk; with 9 they split.
	thirty := numberedLines(30)
	edit := func(lines ...string) string {
		text := thirty
		for _, l := range lines {
			text = strings.Replace(text, l+"\n", "X"+l+"\n", 1)
		}
		return text
	}
	if got := GenerateUnifiedPatch("f.txt", thirty, edit("l10", "l19")); strings.Count(got, "@@ -") != 1 {
		t.Errorf("8 unchanged lines between changes split the hunk:\n%s", got)
	}
	if got := GenerateUnifiedPatch("f.txt", thirty, edit("l10", "l20")); strings.Count(got, "@@ -") != 2 {
		t.Errorf("9 unchanged lines between changes kept one hunk:\n%s", got)
	}
}

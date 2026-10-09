package tools

import "testing"

// Expected values were measured by running Pi's generateDiffString and generateUnifiedPatch (edit-diff.ts, optional
// contextLines parameter, default 4) from the published @earendil-works/pi-coding-agent edit-diff.js on this input.
func TestEditDiffContextLinesParameterMatchesUpstream(t *testing.T) {
	const old = "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	const updated = "a\nb\nc\nD\ne\nf\ng\nh\nI\nI2\nj\nk\nl\n"
	for _, tc := range []struct {
		contextLines    int
		diff, patch     string
		firstChangedRow int
	}{
		{0, "    ...\n- 4 d\n+ 4 D\n    ...\n- 9 i\n+ 9 I\n+10 I2\n    ...", "--- f.txt\n+++ f.txt\n@@ -4,1 +4,1 @@\n-d\n+D\n@@ -9,1 +9,2 @@\n-i\n+I\n+I2\n", 4},
		{1, "    ...\n  3 c\n- 4 d\n+ 4 D\n  5 e\n    ...\n  8 h\n- 9 i\n+ 9 I\n+10 I2\n 10 j\n    ...", "--- f.txt\n+++ f.txt\n@@ -3,3 +3,3 @@\n c\n-d\n+D\n e\n@@ -8,3 +8,4 @@\n h\n-i\n+I\n+I2\n j\n", 4},
		{2, "    ...\n  2 b\n  3 c\n- 4 d\n+ 4 D\n  5 e\n  6 f\n  7 g\n  8 h\n- 9 i\n+ 9 I\n+10 I2\n 10 j\n 11 k\n    ...", "--- f.txt\n+++ f.txt\n@@ -2,10 +2,11 @@\n b\n c\n-d\n+D\n e\n f\n g\n h\n-i\n+I\n+I2\n j\n k\n", 4},
		{10, "  1 a\n  2 b\n  3 c\n- 4 d\n+ 4 D\n  5 e\n  6 f\n  7 g\n  8 h\n- 9 i\n+ 9 I\n+10 I2\n 10 j\n 11 k\n 12 l", "--- f.txt\n+++ f.txt\n@@ -1,12 +1,13 @@\n a\n b\n c\n-d\n+D\n e\n f\n g\n h\n-i\n+I\n+I2\n j\n k\n l\n", 4},
	} {
		result := GenerateDiffString(old, updated, tc.contextLines)
		diff, first := result.Diff, firstLine(result)
		if diff != tc.diff || first != tc.firstChangedRow {
			t.Errorf("GenerateDiffString contextLines=%d = (%q, %d), want (%q, %d)", tc.contextLines, diff, first, tc.diff, tc.firstChangedRow)
		}
		if patch := GenerateUnifiedPatch("f.txt", old, updated, tc.contextLines); patch != tc.patch {
			t.Errorf("GenerateUnifiedPatch contextLines=%d = %q, want %q", tc.contextLines, patch, tc.patch)
		}
	}
	// Omitting the parameter is upstream's default of 4.
	diff4, first4 := GenerateDiffString(old, updated, 4), GenerateDiffString(old, updated)
	if diff4.Diff != first4.Diff || firstLine(diff4) != firstLine(first4) || GenerateUnifiedPatch("f.txt", old, updated) != GenerateUnifiedPatch("f.txt", old, updated, 4) {
		t.Error("the default context is not 4")
	}
}

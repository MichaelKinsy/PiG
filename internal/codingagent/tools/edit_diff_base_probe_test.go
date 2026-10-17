package tools

import "testing"

// edit.ts applyEditsToNormalizedContent: once any edit needs fuzzy matching, every edit is located in the fuzzy-normalized content (baseContent), so an edit
// whose oldText exists verbatim in the original, trailing spaces included, is still matched at its position in the normalized text, where those spaces are
// gone. Probing the second pass against the original content instead would match it at original coordinates and apply it to the wrong text.
//
// mutation-checked: building the second probe's content from the original instead of baseContent fails it.
func TestApplyEditsLocatesEveryEditInTheNormalizedContentOnceOneNeedsFuzzyMatching(t *testing.T) {
	content := "alpha  \nbeta \u201cq\u201d\ngamma\n"
	result, err := applyEditsToNormalizedContent(content, []editEntry{
		{OldText: "gamma", NewText: "G"},
		{OldText: "beta \"q\"", NewText: "B"},
		{OldText: "alpha  ", NewText: "A"},
	}, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := "A\nB\nG\n"; result.newContent != want {
		t.Fatalf("newContent = %q, want %q", result.newContent, want)
	}
}

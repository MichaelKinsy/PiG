package tools

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// edit.ts applyEditsToNormalizedContent matches every edit against the same content, and fuzzyFindText/countOccurrences normalize the content for each one
// (normalizeForFuzzyMatch(content)). The result of that normalization depends only on the content, so a call with E edits needs it once per distinct content,
// not 2E times: a 167 KB file with a dozen edits spent most of the call re-normalizing it.
//
// mutation-checked: normalizing in fuzzyFindText or countOccurrences per call, instead of the shared view, multiplies the allocation by the number of edits.
func TestApplyEditsNormalizesTheContentOncePerCallNotOncePerEdit(t *testing.T) {
	var content strings.Builder
	for i := range 4000 {
		fmt.Fprintf(&content, "func item%04d() string { return \u201csmart quoted %04d\u201d }  \n", i, i)
	}
	text := content.String()
	var edits []editEntry
	for i := range 24 {
		n := i * 150
		edits = append(edits, editEntry{
			OldText: fmt.Sprintf("func item%04d() string { return \"smart quoted %04d\" }", n, n),
			NewText: fmt.Sprintf("func item%04d() string { return \"edited %04d\" }", n, n),
		})
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	result, err := applyEditsToNormalizedContent(text, edits, "f.go")
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.newContent, "edited 3450") {
		t.Fatal("the edits were not applied")
	}
	if allocated, budget := after.TotalAlloc-before.TotalAlloc, uint64(len(text))*20; allocated > budget {
		t.Fatalf("%d edits on %d bytes allocated %d bytes, want at most %d (a few normalizations of the content, not one per edit)", len(edits), len(text), allocated, budget)
	}
}

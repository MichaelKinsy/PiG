package prompts

import (
	"slices"
	"testing"
)

func TestGuidelinesUseJavaScriptWhitespace(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/system-prompt.ts:89 uses String.trim: BOM is whitespace, NEL is not.
	got := guidelinesFor([]string{"custom"}, map[string][]string{"custom": {"\ufeffkeep\ufeff", "keep", " \u0085 "}}, nil)
	want := []string{"keep", "\u0085", "Be concise in your responses", "Show file paths clearly when working with files"}
	if !slices.Equal(got, want) {
		t.Fatalf("guidelines = %q, want %q", got, want)
	}
}

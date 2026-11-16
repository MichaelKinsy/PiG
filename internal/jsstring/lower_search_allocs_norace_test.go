//go:build !race

package jsstring

import "testing"

// An ASCII id or name is searched without its lowercase copy.
func TestLowerSearchesDoNotAllocateForASCII(t *testing.T) {
	if allocs := testing.AllocsPerRun(100, func() {
		_ = LowerContains("Claude Opus 4.5", "opus") && LowerEquals("GPT-4O", "gpt-4o")
	}); allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}

// An ASCII text is searched for an ASCII needle without the UTF-16 copy of either: the model resolver asks it once per catalog model.
func TestIndexOfDoesNotAllocateForASCII(t *testing.T) {
	if allocs := testing.AllocsPerRun(100, func() {
		_ = IndexOf("openai/gpt-4o-mini", "/", 6)
	}); allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}

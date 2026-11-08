package durable

import "testing"

// A model named twice keeps its first position and takes the last override (the documented rule of Set and NewCompactionOverrides).
func TestCompactionOverridesSetReplacesInPlace(t *testing.T) {
	first, second := 1, 2
	set := NewCompactionOverrides(
		CompactionModelOverride{Model: "a/one", Override: CompactionPolicyOverride{ReserveTokens: &first}},
		CompactionModelOverride{Model: "b/two"},
	)
	set.Set("a/one", CompactionPolicyOverride{ReserveTokens: &second})
	if set.Len() != 2 {
		t.Fatalf("len = %d, want 2", set.Len())
	}
	var order []string
	for model := range set.All() {
		order = append(order, model)
	}
	if order[0] != "a/one" || order[1] != "b/two" {
		t.Fatalf("order = %v, want [a/one b/two]", order)
	}
	if got := set.Get("a/one").ReserveTokens; got == nil || *got != 2 {
		t.Fatalf("a/one reserveTokens = %v, want 2", got)
	}
}

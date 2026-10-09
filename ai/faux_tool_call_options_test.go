package ai

import (
	"strings"
	"testing"
)

// upstream: packages/ai/src/providers/faux.ts:62 fauxToolCall(name, arguments_, options: { id?: string } = {}): the id is
// options.id when set, else randomId("tool").
func TestFauxToolCallTakesAnOptionsObject(t *testing.T) {
	args := map[string]any{"q": "x"}
	if got := FauxToolCall("search", args, &FauxToolCallOptions{ID: "call-1"}); got.ID != "call-1" || got.Name != "search" || got.Type != FauxContentToolCall {
		t.Errorf("explicit id: %+v", got)
	}
	for name, options := range map[string]*FauxToolCallOptions{"nil options": nil, "empty options": {}} {
		got := FauxToolCall("search", args, options)
		if !strings.HasPrefix(got.ID, "tool:") {
			t.Errorf("%s: id %q, want a random tool:... id", name, got.ID)
		}
		if other := FauxToolCall("search", args, options); other.ID == got.ID {
			t.Errorf("%s: two calls drew the same id %q", name, got.ID)
		}
	}
}

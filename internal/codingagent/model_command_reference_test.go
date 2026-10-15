package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// /model <term> resolves the term with findExactModelReferenceMatch (interactive-mode.ts findExactModelMatch, core/model-resolver.ts:88). The rows are
// what that function answered for the same models when they were recorded from the installed Pi 1.1.0: case-insensitive with JavaScript's
// toLowerCase, a spaced "provider / id", a unique bare id, no match for an ambiguous id, and U+0085 is not trimmed.
func TestModelCommandReferenceResolutionMatchesPi(t *testing.T) {
	items := []tui.ModelSelectorItem{
		{Provider: "openai", ID: "gpt-4o"}, {Provider: "anthropic", ID: "claude-3"}, {Provider: "zai", ID: "glm-5"}, {Provider: "local", ID: "glm-5"},
		{Provider: "İstanbul", ID: "İ-1"}, {Provider: "openrouter", ID: "anthropic/claude-3"},
	}
	for _, tc := range []struct{ term, want string }{
		{"openai/gpt-4o", "openai/gpt-4o"}, {" OPENAI/GPT-4O ", "openai/gpt-4o"}, {"openai / gpt-4o", "openai/gpt-4o"}, {"gpt-4o", "openai/gpt-4o"},
		{"glm-5", ""}, {"zai/glm-5", "zai/glm-5"}, {"İstanbul/İ-1", "İstanbul/İ-1"}, {"istanbul/i-1", ""}, {"ISTANBUL/İ-1", ""},
		{"claude-3", "anthropic/claude-3"}, {"anthropic/claude-3", "anthropic/claude-3"}, {"openrouter/anthropic/claude-3", "openrouter/anthropic/claude-3"},
		{"anthropic/claude-3 ", "anthropic/claude-3"}, {"\u0085gpt-4o", ""}, {"/gpt-4o", ""}, {"openai/", ""}, {"nothing", ""}, {"", ""},
	} {
		got, ok := resolveModelFromItems(tc.term, items)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("resolveModelFromItems(%q) = %q, %v; Pi %q", tc.term, got, ok, tc.want)
		}
	}
}

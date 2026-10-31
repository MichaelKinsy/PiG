package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi b9e3eef6 (#11495): packages/coding-agent/test/system-prompt-updates.test.ts "does not patch the prompt when the active tools are only reordered".
// Setting the same active tools in another order keeps the order the tools were active in, so the prompt's tools and rules sections do not change and no system message records an update.
func TestReorderedActiveToolsDoNotPatchThePrompt(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{
		boundaryTool{name: "zeta", label: "Zeta", description: "Zeta", text: "done"},
		boundaryTool{name: "alpha", label: "Alpha", description: "Alpha", text: "done"},
	}}, fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0))
	if err := h.session.Prompt(t.Context(), "one"); err != nil {
		t.Fatal(err)
	}
	before := h.session.ActiveToolNames()
	if len(before) < 2 {
		t.Fatalf("active tools = %v, want at least two to reorder", before)
	}
	reversed := slices.Clone(before)
	slices.Reverse(reversed)
	h.session.SetActiveToolsByName(reversed)
	if got := h.session.ActiveToolNames(); !slices.Equal(got, before) {
		t.Fatalf("active tools after reordering = %v, want %v", got, before)
	}
	if err := h.session.Prompt(t.Context(), "two"); err != nil {
		t.Fatal(err)
	}
	systems := 0
	for _, message := range h.session.Messages() {
		if message.System != nil {
			systems++
		}
	}
	if systems != 1 {
		t.Fatalf("system messages = %d, want 1", systems)
	}
}

// Pi b9e3eef6 (#11495): tools that stay active keep their current order and newly active tools are appended in the requested order.
func TestSetActiveToolsKeepsActiveOrderAndAppendsNewTools(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{
		boundaryTool{name: "zeta", label: "Zeta", description: "Zeta", text: "done"},
		boundaryTool{name: "alpha", label: "Alpha", description: "Alpha", text: "done"},
		boundaryTool{name: "mu", label: "Mu", description: "Mu", text: "done"},
	}})
	h.session.SetActiveToolsByName([]string{"zeta", "alpha"})
	h.session.SetActiveToolsByName([]string{"mu", "alpha", "zeta"})
	if got, want := h.session.ActiveToolNames(), []string{"zeta", "alpha", "mu"}; !slices.Equal(got, want) {
		t.Fatalf("active tools = %v, want %v", got, want)
	}
	h.session.SetActiveToolsByName([]string{"mu", "unknown", "zeta"})
	if got, want := h.session.ActiveToolNames(), []string{"zeta", "mu"}; !slices.Equal(got, want) {
		t.Fatalf("active tools after dropping one = %v, want %v", got, want)
	}
}

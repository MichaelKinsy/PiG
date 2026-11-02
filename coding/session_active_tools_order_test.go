package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

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

package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:_rebuildSystemPrompt replaces the options object when active tools change; an older borrowed object keeps its prior selection.
func TestSessionPromptOptionsFollowActiveToolRebuild(t *testing.T) {
	h := newModelExtensionHarness(t, []bool{false}, "", true, extension.Extension{}, nil)
	before := h.session.GetSystemPromptOptions()
	original := slices.Clone(before.SelectedTools)
	h.session.SetActiveToolsByName([]string{"read"})
	after := h.session.GetSystemPromptOptions()
	if after == before || !slices.Equal(after.SelectedTools, []string{"read"}) {
		t.Fatalf("options not replaced after activation: same=%t tools=%v", after == before, after.SelectedTools)
	}
	if !slices.Equal(before.SelectedTools, original) {
		t.Fatalf("borrowed prior options changed: %v, want %v", before.SelectedTools, original)
	}
}

// H1 preflight uses the same live base inputs without moving the event back under the Session run lock.
func TestPreparedPromptReadsCurrentBaseOptions(t *testing.T) {
	var seen extension.BuildSystemPromptOptions
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		seen = args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
		return nil, nil
	}}}}
	h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil)
	h.session.SetActiveToolsByName([]string{"read"})
	if _, err := h.session.PreparePrompt(t.Context(), BuildUserContent("probe", nil)); err != nil {
		t.Fatal(err)
	}
	if seen.Cwd != h.services.CWD() || !slices.Equal(seen.SelectedTools, []string{"read"}) {
		t.Fatalf("preflight lost current base inputs: %+v", seen)
	}
}

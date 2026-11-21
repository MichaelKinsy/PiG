package coding

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestFreshAndCloneToolOrderMatchesPi verifies that a fresh Session, its Clone,
// and the session after Reload all send tools in Pi's order:
// [built-ins, extension tools, caller's tools]
// (upstream agent-session.ts:3660-3666, 3702, 3733-3736:
// allCustomTools = [...registeredTools, ...this._customTools], then appended
// after initialActiveToolNames).
func TestFreshAndCloneToolOrderMatchesPi(t *testing.T) {
	t.Parallel()

	extTool := extension.RegisteredTool{
		Definition: extension.ToolDefinition{
			Name:        "ext_probe",
			Label:       "Ext Probe",
			Description: "extension tool",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
	callerTool := extension.ToolDefinition{
		Name:        "caller_probe",
		Label:       "Caller Probe",
		Description: "caller custom tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}

	// 1. Unit check at createSessionToolRegistry level.
	t.Run("createSessionToolRegistry", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatalf("CreateAgentSessionServices: %v", err)
		}
		defer services.Close()
		runner := inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir)
		_, activeTools, err := createSessionToolRegistry(services, SessionOptions{
			Runner:      runner,
			CustomTools: []extension.ToolDefinition{callerTool},
		})
		if err != nil {
			t.Fatalf("createSessionToolRegistry: %v", err)
		}
		got := make([]string, len(activeTools))
		for i, tool := range activeTools {
			got[i] = tool.Name()
		}
		want := []string{"read", "bash", "edit", "write", "ext_probe", "caller_probe"}
		if !slices.Equal(got, want) {
			t.Fatalf("createSessionToolRegistry active tools = %v, want %v", got, want)
		}
	})

	// 2. Provider request boundary: fresh, clone, and after reload.
	t.Run("provider request tools order", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatalf("CreateAgentSessionServices: %v", err)
		}
		configureFauxAuth(t, services)
		defer services.Close()

		var recorded [][]string
		provider := &scriptedProvider{
			wrap: true,
			responses: []scriptedResponse{
				func(messages []ai.Message) *ai.AssistantMessage {
					schemas := ai.GetCurrentTools(ai.TranscriptMessages[ai.Message](messages))
					names := make([]string, len(schemas))
					for i, s := range schemas {
						names[i] = s.Name
					}
					recorded = append(recorded, names)
					return &ai.AssistantMessage{
						Provider:   "faux",
						Model:      "faux-1",
						Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}},
						StopReason: ai.StopReasonStop,
					}
				},
			},
		}
		captureToolNames := func(t *testing.T, session *Session, prompt string) []string {
			t.Helper()
			before := len(recorded)
			if err := session.Prompt(context.Background(), prompt); err != nil {
				t.Fatalf("Prompt(%q): %v", prompt, err)
			}
			if len(recorded) <= before {
				t.Fatalf("expected provider request for %q", prompt)
			}
			return recorded[len(recorded)-1]
		}
		runner := inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir)
		session, err := NewSession(services, SessionOptions{
			Model:       &ai.Model{ID: "faux-1", Provider: provider},
			Runner:      runner,
			CustomTools: []extension.ToolDefinition{callerTool},
			NoSession:   true,
		})
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		defer func() { _ = session.Close() }()

		want := []string{"read", "bash", "edit", "write", "ext_probe", "caller_probe"}
		// Clone before prompting so the clone starts from the registry rather than replaying a prior turn's transcript.
		cloned, err := session.Clone()
		if err != nil {
			t.Fatalf("Clone: %v", err)
		}
		defer func() { _ = cloned.Close() }()

		freshGot := captureToolNames(t, session, "fresh")
		if !slices.Equal(freshGot, want) {
			t.Errorf("fresh request tools = %v, want %v", freshGot, want)
		}

		cloneGot := captureToolNames(t, cloned, "clone")
		if !slices.Equal(cloneGot, want) {
			t.Errorf("clone request tools = %v, want %v", cloneGot, want)
		}
		if !slices.Equal(freshGot, cloneGot) {
			t.Errorf("fresh != clone: fresh=%v clone=%v", freshGot, cloneGot)
		}

		// Refresh the runner (the reload path) and verify the session keeps Pi's order.
		session.ReplaceRunner(inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir))
		reloadGot := captureToolNames(t, session, "reload")
		if !slices.Equal(reloadGot, want) {
			t.Errorf("reload request tools = %v, want %v", reloadGot, want)
		}
	})
}

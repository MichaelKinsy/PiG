package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A tool that is registered again under its name, with a definition that differs only in the function that runs it, runs the new function after the refresh: the Session reuses the tool it wrapped only for the registration it wrapped.
//
// upstream: loader.ts:289-301 (registerTool replaces extension.tools[name]), agent-session.ts:3448 (_refreshToolRegistry)
func TestRegisteringAToolAgainReplacesWhatTheSessionRuns(t *testing.T) {
	ext := extension.Extension{Path: "again"}
	ext.InitializeToolRegistry()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true, SkipBuiltinTools: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	register := func(text string) {
		definition := registryTool("same", "Same", "Same", "Same")
		definition.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		definition.Execute = func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}, nil
		}
		ext.SetRegisteredTool(extension.RegisteredTool{Definition: definition})
		if err := session.RefreshTools(); err != nil {
			t.Fatal(err)
		}
	}
	run := func() string {
		t.Helper()
		for _, tool := range session.Tools() {
			if tool.Name() == "same" {
				result, err := tool.Execute(t.Context(), "call", json.RawMessage(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				return result.Text()
			}
		}
		t.Fatal("tool same is not in the session")
		return ""
	}
	register("first")
	if got := run(); got != "first" {
		t.Fatalf("first registration ran %q", got)
	}
	register("second")
	if got := run(); got != "second" {
		t.Fatalf("a registration with a new function ran %q, want second", got)
	}
}

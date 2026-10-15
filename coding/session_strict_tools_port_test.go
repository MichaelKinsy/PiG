package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestBuiltinStrictOptOutPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/builtin-tool-strict-mode.test.ts:34
	t.Run("preserves explicit opt-outs when wrapping definitions for execution", func(t *testing.T) {
		strict := []string{"read", "bash", "powershell", "edit", "write"}
		for _, tool := range tools.CreateAllTools(t.TempDir(), nil) {
			if !slices.Contains(strict, tool.Name()) {
				continue
			}
			definition, err := toolDefinition(tool)
			if err != nil {
				t.Fatal(err)
			}
			// Builtin rendering is a separate Go component layer. Non-nil callbacks
			// make preservation through the definition wrapper observable here.
			definition.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
				return tui.NewText("call")
			}
			definition.RenderResult = func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
				return tui.NewText("result")
			}
			override := definition
			override.ConstrainedSampling = json.RawMessage(`false`)
			wrapped, err := newBridgeTool(extension.RegisteredTool{Definition: override})
			if err != nil {
				t.Fatal(err)
			}
			if wrapped.Schema().ConstrainedSampling != nil {
				t.Fatalf("%s did not preserve false", tool.Name())
			}
			for name, pair := range map[string][2]any{"execute": {wrapped.def.Execute, definition.Execute}, "prepareArguments": {wrapped.def.PrepareArguments, definition.PrepareArguments}, "renderCall": {wrapped.def.RenderCall, definition.RenderCall}, "renderResult": {wrapped.def.RenderResult, definition.RenderResult}} {
				if reflect.ValueOf(pair[0]).Pointer() != reflect.ValueOf(pair[1]).Pointer() {
					t.Fatalf("%s changed %s", tool.Name(), name)
				}
			}
			if !slices.Equal(wrapped.def.PromptGuidelines, definition.PromptGuidelines) {
				t.Fatal("guidelines changed")
			}
			var metadata map[string]string
			if err := json.Unmarshal(definition.ConstrainedSampling, &metadata); err != nil || metadata["type"] != "json_schema" || metadata["strict"] != "prefer" {
				t.Fatalf("original strict metadata = %s, %v", definition.ConstrainedSampling, err)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/builtin-tool-strict-mode.test.ts:49 (all three activeTools rows).
	for _, active := range [][]string{{}, {"read"}, {"read", "bash", "powershell", "edit", "write"}} {
		t.Run("allows extensions to re-register tools without strict sampling: "+string(mustToolNamesJSON(t, active)), func(t *testing.T) {
			strict := []string{"read", "bash", "powershell", "edit", "write"}
			session := newRegistryPortSession(t, active, SessionOptions{}, nil, func(session *Session, registered map[string]extension.RegisteredTool) {
				for _, name := range strict {
					definition, ok := session.GetToolDefinition(name)
					if !ok {
						t.Fatal("missing " + name)
					}
					definition.ConstrainedSampling = json.RawMessage(`false`)
					registerPortTool(registered, definition)
				}
				session.SetActiveToolsByName(active)
			})
			prompt := session.systemPrompt()
			bindRegistryPort(t, session)
			if !slices.Equal(session.ActiveToolNames(), active) || session.systemPrompt() != prompt {
				t.Fatalf("active %v, prompt changed=%v", session.ActiveToolNames(), session.systemPrompt() != prompt)
			}
			for _, name := range strict {
				definition, ok := session.GetToolDefinition(name)
				if !ok || string(definition.ConstrainedSampling) != "false" {
					t.Fatalf("%s definition = %+v", name, definition)
				}
			}
			for _, tool := range session.agent.Tools() {
				if tool.Schema().ConstrainedSampling != nil {
					t.Fatalf("%s is still strict", tool.Name())
				}
			}
			if slices.Contains(active, "read") {
				if err := os.WriteFile(filepath.Join(session.CWD(), "sample.txt"), []byte("still works"), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, tool := range session.agent.Tools() {
					if tool.Name() == "read" {
						result, err := tool.Execute(t.Context(), "read-test", json.RawMessage(`{"path":"sample.txt"}`), nil)
						if err != nil || result.IsError || result.Text() != "still works" || len(result.Images()) != 0 {
							t.Fatalf("result = %+v, %v", result, err)
						}
					}
				}
			}
		})
	}
}
func mustToolNamesJSON(t *testing.T, names []string) []byte {
	t.Helper()
	data, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

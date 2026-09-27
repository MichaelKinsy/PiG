package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestUIBridge_Snapshot_ReadsCallbacks(t *testing.T) {
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{
		GetActiveTools: func() []string { return []string{"read", "write"} },
		GetAllTools: func() []ToolInfo {
			return []ToolInfo{{Name: "read"}, {Name: "bash", Description: "Run bash", Parameters: json.RawMessage(`{"type":"object"}`), PromptGuidelines: []string{"g"}, SourceInfo: map[string]any{"path": "<builtin:bash>"}}}
		},
		GetCommands: func() []CommandInfo {
			return []CommandInfo{{Name: "help", Description: "Help", Source: "extension", SourceInfo: map[string]any{"path": "/ext.ts"}}}
		},
		GetThinkingLevel:   func() string { return "high" },
		IsIdle:             func() bool { return false },
		HasPendingMessages: func() bool { return true },
		GetSystemPrompt:    func() string { return "sp" },
		GetContextUsage: func() *extension.ContextUsage {
			tokens := 10
			pct := 10
			return &extension.ContextUsage{Tokens: &tokens, ContextWindow: 100, Percent: &pct}
		},
		GetFlag: func(_, name string) any {
			if name == "feature" {
				return true
			}
			return nil
		},
	})

	state := b.Snapshot([]string{"feature", "missing"}, 0, false)
	if len(state.ActiveTools) != 2 || state.ActiveTools[0] != "read" {
		t.Errorf("ActiveTools = %v", state.ActiveTools)
	}
	// The Node runtime answers pi.getAllTools() and pi.getCommands() from
	// this replica, so it carries the whole ToolInfo and SlashCommandInfo.
	if raw, _ := json.Marshal(state.AllTools); string(raw) != `[{"name":"read","description":"","parameters":null,"sourceInfo":null},{"name":"bash","description":"Run bash","parameters":{"type":"object"},"promptGuidelines":["g"],"sourceInfo":{"path":"\u003cbuiltin:bash\u003e"}}]` {
		t.Errorf("AllTools = %s", raw)
	}
	if raw, _ := json.Marshal(state.Commands); string(raw) != `[{"name":"help","description":"Help","source":"extension","sourceInfo":{"path":"/ext.ts"}}]` {
		t.Errorf("Commands = %s", raw)
	}
	if state.ThinkingLevel != "high" {
		t.Errorf("ThinkingLevel = %q", state.ThinkingLevel)
	}
	if state.IsIdle != false || !state.HasPendingMessages {
		t.Errorf("idle/pending = %v/%v", state.IsIdle, state.HasPendingMessages)
	}
	if state.SystemPrompt != "sp" {
		t.Errorf("SystemPrompt = %q", state.SystemPrompt)
	}
	if state.ContextUsage == nil || state.ContextUsage.Tokens != 10 {
		t.Errorf("ContextUsage = %+v", state.ContextUsage)
	}
	if raw, ok := state.Flags["feature"]; !ok || string(raw) != "true" {
		t.Errorf("Flags[feature] = %s ok=%v", string(raw), ok)
	}
	if _, ok := state.Flags["missing"]; ok {
		t.Errorf("missing flag should be absent")
	}
}

func TestUIBridge_Snapshot_NilActions(t *testing.T) {
	b := NewUIBridge(func() {})
	state := b.Snapshot(nil, 0, false)
	if state == nil {
		t.Fatal("Snapshot returned nil")
		return
	}
	if !state.IsIdle {
		t.Errorf("IsIdle default = false, want true")
	}
	if !state.HasUI {
		t.Errorf("HasUI default = false, want true")
	}
}

func TestStatePayload_RoundTrip(t *testing.T) {
	state := &StatePayload{
		ActiveTools:   []string{"a"},
		ThinkingLevel: "medium",
		IsIdle:        true,
		ContextUsage:  &extensionContextUsageDTO{Tokens: 1, ContextWindow: 2, Percent: 0.5},
		Flags:         map[string]json.RawMessage{"x": json.RawMessage(`"y"`)},
		HasUI:         true,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got StatePayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ThinkingLevel != "medium" || got.ContextUsage == nil || got.ContextUsage.Tokens != 1 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

// Node extensions read ToolInfo from the state replica, which is exactly
// upstream's shape. The getAllTools host call the Go, Rust and Python SDKs
// use also carries PiG's per-tool source, which their ToolInfo exposed
// before sourceInfo existed.
func TestGetAllToolsHostCallKeepsTheSDKSourceField(t *testing.T) {
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{GetAllTools: func() []ToolInfo {
		return []ToolInfo{{Name: "lookup", Description: "Look up", Parameters: json.RawMessage(`{"type":"object"}`), SourceInfo: map[string]any{"path": "/ext.ts"}, Source: "mcp:docs"}}
	}})
	result, err := b.HandleCall("ext", &CallPayload{Method: "getAllTools"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"tools":[{"name":"lookup","description":"Look up","parameters":{"type":"object"},"sourceInfo":{"path":"/ext.ts"},"source":"mcp:docs"}]}`; string(result.Result) != want {
		t.Errorf("getAllTools result = %s, want %s", result.Result, want)
	}
	state, _ := json.Marshal(b.Snapshot(nil, 0, false).AllTools)
	if want := `[{"name":"lookup","description":"Look up","parameters":{"type":"object"},"sourceInfo":{"path":"/ext.ts"}}]`; string(state) != want {
		t.Errorf("replicated allTools = %s, want %s", state, want)
	}
}

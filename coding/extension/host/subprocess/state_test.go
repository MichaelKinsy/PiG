package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestUIBridge_Snapshot_ReadsCallbacks(t *testing.T) {
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{
		GetActiveTools:     func() []string { return []string{"read", "write"} },
		GetAllTools:        func() []ToolInfo { return []ToolInfo{{Name: "read"}, {Name: "bash"}} },
		GetCommands:        func() []CommandInfo { return []CommandInfo{{Name: "help"}} },
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
	if len(state.AllTools) != 2 || state.AllTools[1] != "bash" {
		t.Errorf("AllTools = %v", state.AllTools)
	}
	if len(state.Commands) != 1 || state.Commands[0] != "help" {
		t.Errorf("Commands = %v", state.Commands)
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

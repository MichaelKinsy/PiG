package subprocess

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
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
			pct := 10.0
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
	if state.ContextUsage == nil || state.ContextUsage.Tokens == nil || *state.ContextUsage.Tokens != 10 {
		t.Errorf("ContextUsage = %+v", state.ContextUsage)
	}
	if raw, ok := state.Flags["feature"]; !ok || string(raw) != "true" {
		t.Errorf("Flags[feature] = %s ok=%v", string(raw), ok)
	}
	// A null resets the SDK replica even when this was its last configured flag.
	if raw, ok := state.Flags["missing"]; !ok || string(raw) != "null" {
		t.Errorf("missing flag reset = %s ok=%v", raw, ok)
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
	if state.HasUI {
		t.Errorf("HasUI default = true, want false (runner.ts:578-580)")
	}
}

func TestStatePayload_RoundTrip(t *testing.T) {
	tokens, percent := 1, 0.5
	state := &StatePayload{
		ActiveTools:   []string{"a"},
		ThinkingLevel: "medium",
		IsIdle:        true,
		ContextUsage:  &extensionContextUsageDTO{Tokens: &tokens, ContextWindow: 2, Percent: &percent},
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
	if got.ThinkingLevel != "medium" || got.ContextUsage == nil || got.ContextUsage.Tokens == nil || *got.ContextUsage.Tokens != 1 {
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

// The host call and the replicated state both carry a tool's exposure, namespace and annotations, which Node extensions read as pi.getAllTools() and the Go, Rust and Python SDKs decode.
// upstream: types.ts:2063 (ToolInfo)
func TestGetAllToolsCarriesExposureNamespaceAndAnnotations(t *testing.T) {
	hint := true
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{GetAllTools: func() []ToolInfo {
		return []ToolInfo{{
			Name: "lookup", Description: "Look up", Parameters: json.RawMessage(`{"type":"object"}`), SourceInfo: map[string]any{"path": "/ext.ts"},
			Exposure: extension.ToolExposureDeferred, Namespace: &extension.ToolNamespace{Name: "docs"}, Annotations: &extension.ToolAnnotations{ReadOnlyHint: &hint},
		}}
	}})
	result, err := b.HandleCall("ext", &CallPayload{Method: "getAllTools"})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"lookup","description":"Look up","parameters":{"type":"object"},"sourceInfo":{"path":"/ext.ts"},"exposure":"deferred","namespace":{"name":"docs"},"annotations":{"readOnlyHint":true}}]`
	var call struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(result.Result, &call); err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, "getAllTools tools", call.Tools, want)
	state, _ := json.Marshal(b.Snapshot(nil, 0, false).AllTools)
	assertJSONEqual(t, "replicated allTools", state, want)
}

func assertJSONEqual(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// A Node extension sees upstream's member order through Object.keys and JSON.stringify: exposure, namespace and annotations come after promptGuidelines and before sourceInfo, in both the host call and the replicated state. The host call appends the SDKs' deprecated source member, which the Node runtime drops.
// upstream: agent-session.ts:1452-1461 (getAllTools object literal)
func TestGetAllToolsKeepsUpstreamMemberOrder(t *testing.T) {
	hint := true
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{GetAllTools: func() []ToolInfo {
		return []ToolInfo{{
			Name: "lookup", Description: "Look up", Parameters: json.RawMessage(`{"type":"object"}`), PromptGuidelines: []string{"Use it."},
			SourceInfo: map[string]any{"path": "/ext.ts"}, Exposure: extension.ToolExposureDeferred,
			Namespace: &extension.ToolNamespace{Name: "docs"}, Annotations: &extension.ToolAnnotations{ReadOnlyHint: &hint}, Source: "ext",
		}}
	}})
	result, err := b.HandleCall("ext", &CallPayload{Method: "getAllTools"})
	if err != nil {
		t.Fatal(err)
	}
	var call struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(result.Result, &call); err != nil || len(call.Tools) != 1 {
		t.Fatalf("tools = %s (%v)", result.Result, err)
	}
	upstream := []string{"name", "description", "parameters", "promptGuidelines", "exposure", "namespace", "annotations", "sourceInfo"}
	if got, want := objectKeys(t, call.Tools[0]), append(slices.Clone(upstream), "source"); !slices.Equal(got, want) {
		t.Errorf("getAllTools members = %v, want %v", got, want)
	}
	state, _ := json.Marshal(b.Snapshot(nil, 0, false).AllTools[0])
	if got := objectKeys(t, state); !slices.Equal(got, upstream) {
		t.Errorf("replicated allTools members = %v, want %v", got, upstream)
	}
}

func objectKeys(t *testing.T, object json.RawMessage) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(object))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// A session narrowed to no tools by SetActiveTools([]) is a value on the wire, not absent state: both the getActiveTools
// reply and the replicated snapshot carry [], never null, which every SDK rejects as a missing field.
func TestActiveToolsNarrowedToNoneIsAnEmptyListOnTheWire(t *testing.T) {
	for name, getter := range map[string]func() []string{
		"empty": func() []string { return []string{} },
		"nil":   func() []string { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			b := NewUIBridge(func() {})
			b.SetActions(&HostCallbacks{GetActiveTools: getter})
			result, err := b.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "getActiveTools"})
			if err != nil {
				t.Fatal(err)
			}
			if got := string(result.Result); got != `{"tools":[]}` {
				t.Errorf("getActiveTools reply = %s, want {\"tools\":[]}", got)
			}
			raw, err := json.Marshal(b.Snapshot(nil, 0, false))
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				ActiveTools []string `json:"activeTools"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"activeTools":[]`)) || decoded.ActiveTools == nil {
				t.Errorf("snapshot = %s, want activeTools []", raw)
			}
		})
	}
}

// SetActiveTools([]) is a deny-all selection: the host must hand its action an empty list, which the Session
// applies as "no tools", never a value that reads as "unchanged".
func TestSetActiveToolsEmptyListReachesTheHostAsEmpty(t *testing.T) {
	var got []string
	called := false
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{SetActiveTools: func(names []string) { called, got = true, names }})
	if _, err := b.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "setActiveTools", Args: json.RawMessage(`{"tools":[]}`)}); err != nil {
		t.Fatal(err)
	}
	if !called || got == nil || len(got) != 0 {
		t.Errorf("SetActiveTools called=%v with %#v, want a call with an empty non-nil list", called, got)
	}
}

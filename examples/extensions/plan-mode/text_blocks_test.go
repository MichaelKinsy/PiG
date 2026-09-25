package planmode

import (
	"encoding/json"
	"reflect"
	"testing"
)

// index.ts:getTextContent joins only text blocks with newlines, including empty blocks.
func TestAssistantTextPreservesBlockBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message map[string]any
		text    string
		ok      bool
	}{
		{"plan", assistantBlocks("Plan:", "1. Inspect the implementation"), "Plan:\n1. Inspect the implementation", true},
		{"split marker", assistantBlocks("[DONE:", "1]"), "[DONE:\n1]", true},
		{"empty block", assistantBlocks("first", "", "last"), "first\n\nlast", true},
		{"no text", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "text": "ignored"}}}, "", true},
		{"string content", map[string]any{"role": "assistant", "content": "[DONE:1]"}, "", false},
		{"user", map[string]any{"role": "user", "content": []any{}}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, ok := assistantText(tc.message)
			if text != tc.text || ok != tc.ok {
				t.Fatalf("assistantText = (%q, %v), want (%q, %v)", text, ok, tc.text, tc.ok)
			}
		})
	}
}

func assistantBlocks(texts ...string) map[string]any {
	blocks := []any{map[string]any{"type": "thinking", "thinking": "ignored"}}
	for _, text := range texts {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	return map[string]any{"role": "assistant", "content": blocks, "stopReason": "stop"}
}

func TestPlanModeExtractsPlanAcrossTextBlocks(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "edit"})
	host.invokeCommand("plan")
	host.clearEffects()
	host.invokeEvent("agent_end", map[string]any{"messages": []any{assistantBlocks("Plan:", "1. Inspect the implementation")}})
	if calls := host.callsFor("ui.select"); len(calls) != 1 {
		t.Fatalf("plan dialogs = %v, want one", calls)
	}
	if got := lastSavedState(t, host).Todos; !reflect.DeepEqual(got, []TodoItem{{Step: 1, Text: "Inspect the implementation"}}) {
		t.Fatalf("extracted todos = %+v", got)
	}
}

func TestPlanModeDoesNotCompleteSplitDoneMarker(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "edit"})
	host.selectValue = "Execute the plan (track progress)"
	host.invokeCommand("plan")
	host.invokeEvent("agent_end", map[string]any{"messages": []any{assistantMessage("Plan:\n1. Inspect the implementation")}})
	host.clearEffects()
	host.invokeEvent("turn_end", map[string]any{"message": assistantBlocks("[DONE:", "1]")})
	if state := lastSavedState(t, host); len(state.Todos) != 1 || state.Todos[0].Completed {
		t.Fatalf("split marker changed completion: %+v", state)
	}
	host.invokeEvent("agent_end", map[string]any{"messages": []any{}})
	if calls := host.callsFor("sendMessage"); len(calls) != 0 {
		t.Fatalf("split marker emitted completion: %v", calls)
	}
	// A whole marker in a later text block must still complete the plan.
	host.invokeEvent("turn_end", map[string]any{"message": assistantBlocks("Finished.", "[DONE:1]")})
	if state := lastSavedState(t, host); !state.Todos[0].Completed {
		t.Fatalf("whole marker did not complete step: %+v", state)
	}
}

func TestPlanModeResumePreservesTextBlockBoundaries(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "edit"})
	host.entries = []json.RawMessage{
		json.RawMessage(`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"[DONE:1]"}]}}`),
		json.RawMessage(`{"type":"custom_message","customType":"plan-mode-execute"}`),
		json.RawMessage(`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"[DONE:"},{"type":"text","text":"1]"},{"type":"text","text":"[DONE:2]"}]}}`),
		json.RawMessage(`{"type":"custom","customType":"plan-mode","data":{"enabled":false,"executing":true,"todos":[{"step":1,"text":"Inspect the implementation","completed":false},{"step":2,"text":"Verify the result","completed":false}]}}`),
	}
	host.invokeEvent("session_start", map[string]any{})
	// turn_end persists the rebuilt state through the real registered handler.
	host.invokeEvent("turn_end", map[string]any{"message": assistantMessage("No new markers.")})
	state := lastSavedState(t, host)
	if len(state.Todos) != 2 || state.Todos[0].Completed || !state.Todos[1].Completed {
		t.Fatalf("rebuilt completion = %+v, want only step 2 complete", state.Todos)
	}
}

func lastSavedState(t *testing.T, host *extensionHost) planModeState {
	t.Helper()
	calls := host.callsFor("appendEntry")
	if len(calls) == 0 {
		t.Fatal("no persisted state")
	}
	raw, err := json.Marshal(calls[len(calls)-1].args["data"])
	if err != nil {
		t.Fatal(err)
	}
	var state planModeState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

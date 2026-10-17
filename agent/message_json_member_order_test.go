package agent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A provider builds its output as role, content, api, provider, model, usage, stopReason, timestamp and assigns responseId, responseModel and errorMessage afterwards (packages/ai/src/api/openai-completions.ts output literal and :564-566; the catch block's errorMessage), and the session file and get_entries keep that order.
func TestAssistantMessageJSONKeepsProviderMemberOrder(t *testing.T) {
	message := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, API: ai.APIOpenAICompletions, Provider: "custom", ModelID: "m", Usage: &ai.Usage{}, StopReason: ai.StopReasonError, Timestamp: 1, ResponseID: "chatcmpl-1", ErrorMessage: "boom"}}
	got, err := message.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"assistant","content":[{"type":"text","text":"ok"}],"api":"openai-completions","provider":"custom","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","timestamp":1,"responseId":"chatcmpl-1","errorMessage":"boom"}`
	if string(got) != want {
		t.Fatalf("assistant message JSON:\n got %s\nwant %s", got, want)
	}
}

// JSON.stringify writes a message's diagnostics array whenever the member is present, empty or not (types.ts:577 `diagnostics?:`); only an absent member is
// left out. Both assistant shapes keep an explicit empty array.
func TestAssistantMessageJSONKeepsAnEmptyDiagnosticsArray(t *testing.T) {
	for _, errorFirst := range []bool{false, true} {
		message := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{}, API: ai.APIOpenAICompletions, Provider: "custom", ModelID: "m", Usage: &ai.Usage{}, StopReason: ai.StopReasonError, Timestamp: 1, ErrorMessage: "boom", ErrorBeforeTimestamp: errorFirst, Diagnostics: []ai.AssistantMessageDiagnostic{}}}
		got, err := message.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), `"diagnostics":[]`) {
			t.Errorf("errorBeforeTimestamp=%v: diagnostics [] dropped: %s", errorFirst, got)
		}
		message.Assistant.Diagnostics = nil
		if got, _ := message.MarshalJSON(); strings.Contains(string(got), "diagnostics") {
			t.Errorf("errorBeforeTimestamp=%v: absent diagnostics written: %s", errorFirst, got)
		}
	}
}

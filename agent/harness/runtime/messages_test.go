package runtime

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// runtimeAssistantMessage keeps every response field of the provider carrier,
// including the agent loop's thinkingLevel (upstream ai/src/types.ts:557), so
// a message that round-trips through the agent carrier is unchanged.
func TestRuntimeAssistantMessageKeepsEveryResponseField(t *testing.T) {
	endTurn := true
	message := ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, API: ai.APIOpenAIResponses,
		Provider: "openai", Model: "gpt", ResponseModel: "gpt-2", ResponseID: "resp-1",
		ProviderThinkingLevel: "medium", ThinkingLevel: ai.ThinkingHigh,
		Diagnostics: []ai.AssistantMessageDiagnostic{{}}, Usage: ai.Usage{Input: 1, Output: 2, TotalTokens: 3},
		StopReason: ai.StopReasonError, Deferred: &ai.DeferredHandle{}, ErrorMessage: "boom", RawStopReason: "raw",
		EndTurn: &endTurn, Timestamp: 42,
	}
	fields := reflect.TypeFor[ai.AssistantMessage]()
	value := reflect.ValueOf(message)
	for index := range fields.NumField() {
		if fields.Field(index).IsExported() && value.Field(index).IsZero() {
			t.Fatalf("fixture leaves ai.AssistantMessage.%s zero; set it so the round trip checks it", fields.Field(index).Name)
		}
	}

	converted := runtimeAssistantMessage(&message)

	if got := converted.LLMMessage(); !reflect.DeepEqual(got, message) {
		t.Fatalf("round trip = %+v, want %+v", got, message)
	}
}

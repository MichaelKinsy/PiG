package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// live.ts:51 types the in-flight partial as JsonRepresentation<AssistantMessage>: LiveGeneration.Message is the AssistantMessage itself, so the
// stored JSON of a half-built response decodes to it and encodes back to the same JSON.
func TestLiveGenerationMessageIsTheAssistantMessage(t *testing.T) {
	const stored = `{"attempt":2,"message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"par"},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a"}}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":7}}`
	var generation LiveGeneration
	if err := json.Unmarshal([]byte(stored), &generation); err != nil {
		t.Fatal(err)
	}
	if generation.Message == nil || len(generation.Message.Content) != 3 || generation.Message.StopReason != ai.StopReasonToolUse {
		t.Fatalf("decoded message %+v", generation.Message)
	}
	if call, ok := generation.Message.Content[2].(ai.ToolCall); !ok || call.Name != "read" {
		t.Fatalf("third block %#v, want the read tool call", generation.Message.Content[2])
	}
	encoded, err := json.Marshal(generation)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(stored), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if gm, wm := got["message"].(map[string]any), want["message"].(map[string]any); gm["role"] != wm["role"] || gm["stopReason"] != wm["stopReason"] || len(gm["content"].([]any)) != 3 {
		t.Fatalf("encoded %s, want the stored message back", encoded)
	}
	var bare LiveGeneration
	if err := json.Unmarshal([]byte(`{"attempt":1}`), &bare); err != nil || bare.Message != nil {
		t.Fatalf("a generation without a message has none: %v %+v", err, bare.Message)
	}
}

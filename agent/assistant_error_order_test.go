package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The AgentMessage encoder writes the key order the assistant message was built in (faux.ts: errorMessage before timestamp; a
// provider's catch block: timestamp first), a decoded record keeps the order it lists, and the order survives the conversion to
// and from ai.AssistantMessage.
// mutation-checked: an encoder, decoder or conversion that drops ErrorBeforeTimestamp fails a row.
func TestAgentMessageKeyOrderFollowsTheConstructionOrder(t *testing.T) {
	for name, first := range map[string]bool{"errorMessage before timestamp": true, "timestamp before errorMessage": false} {
		t.Run(name, func(t *testing.T) {
			message := AgentMessage{Assistant: &AssistantMessage{
				Role: RoleAssistant, Content: []ai.AssistantContentBlock{}, API: "faux", Provider: "faux", ModelID: "m",
				StopReason: ai.StopReasonError, ErrorMessage: "503 service unavailable", ErrorBeforeTimestamp: first, Timestamp: 5,
			}}
			data, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			e, ts := strings.Index(string(data), `"errorMessage"`), strings.Index(string(data), `"timestamp"`)
			if (e < ts) != first {
				t.Fatalf("errorMessage before timestamp = %v, want %v: %s", e < ts, first, data)
			}
			var decoded AgentMessage
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Assistant == nil || decoded.Assistant.ErrorBeforeTimestamp != first {
				t.Fatalf("decoded order flag = %+v, want %v", decoded.Assistant, first)
			}
			again, _ := json.Marshal(decoded)
			if !bytes.Equal(again, data) {
				t.Fatalf("round trip changed the record:\n got %s\nwant %s", again, data)
			}
			converted := decoded.Assistant.LLMMessage()
			if converted.ErrorBeforeTimestamp != first {
				t.Fatalf("ai conversion lost the order: %v", converted.ErrorBeforeTimestamp)
			}
		})
	}
}

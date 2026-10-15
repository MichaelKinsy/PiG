package agent

import (
	"encoding/json"
	"testing"
)

// A session line or an RPC message carries a lone surrogate as the escape `\ud83d`, and JSON.parse then JSON.stringify gives the same text back. Decoding an assistant message and encoding it again keeps the escape in the text, the tool-call arguments and the tool result, where it used to turn into U+FFFD in the arguments.
func TestAgentMessageRoundTripKeepsALoneSurrogate(t *testing.T) {
	for name, wire := range map[string]string{
		"assistant":  `{"role":"assistant","content":[{"type":"text","text":"a\ud83db"},{"type":"toolCall","id":"1","name":"x","arguments":{"k":"a\ud83db","l":["\ude00"]}}],"api":"x","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}`,
		"toolResult": `{"role":"toolResult","toolCallId":"1","toolName":"x","content":[{"type":"text","text":"a\ud83db"}],"details":{"k":"a\ud83db"},"isError":false,"timestamp":1}`,
	} {
		var message AgentMessage
		if err := json.Unmarshal([]byte(wire), &message); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != wire {
			t.Errorf("%s round trip:\n got %s\nwant %s", name, got, wire)
		}
	}
}

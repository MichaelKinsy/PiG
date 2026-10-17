package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// get_messages writes session.messages with JSON.stringify (rpc-mode.ts get_messages), and Pi holds each message as JSON.parse returned it from the session line, so a message Pi did not write keeps its member order and the members PiG does not model. Each line is its own JSON.stringify(JSON.parse(line)).
func TestRPCGetMessagesWritesEachMessageAsItWasParsed(t *testing.T) {
	lines := []string{
		`{"timestamp":1,"meta":{"z":1,"a":[2.5,"<&>"]},"content":[{"text":"hi","type":"text"}],"role":"user"}`,
		`{"role":"assistant","content":[{"type":"text","text":"ok","textSignature":"s"}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","errorMessage":"","timestamp":2}`,
		`{"toolName":"read","role":"toolResult","toolCallId":"c","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":3,"note":"x"}`,
		`{"timestamp":4,"content":"sys","role":"system","zz":1}`,
		`{"customType":"k","role":"custom","content":"c","display":true,"timestamp":5}`,
	}
	messages := make([]agent.AgentMessage, len(lines))
	for i, line := range lines {
		if err := json.Unmarshal([]byte(line), &messages[i]); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	data, err := rpcGetMessagesData(messages)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	writeJSONLine(&got, data)
	want := `{"messages":[` + strings.Join(lines, ",") + "]}\n"
	if got.String() != want {
		t.Fatalf("get_messages data:\n got %swant %s", got.String(), want)
	}
}

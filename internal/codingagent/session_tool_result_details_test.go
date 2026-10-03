package codingagent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi's session manager writes each entry with JSON.stringify (session-manager.ts appendFileSync), so a toolResult
// message whose tool returned details: null is stored with "details":null and one without details has no key, and a
// resumed session reads both back unchanged.
func TestSessionToolResultDetailsRoundTripAsPi(t *testing.T) {
	wires := []string{
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":null,"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c2","toolName":"probe","content":[],"isError":false,"timestamp":2}`,
	}
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-details-null", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(mkAssistantMsg("calling")); err != nil {
		t.Fatal(err)
	}
	for _, wire := range wires {
		var message agent.AgentMessage
		if err := json.Unmarshal([]byte(wire), &message); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for index, wire := range wires {
		if line := lines[len(lines)-len(wires)+index]; !strings.HasSuffix(line, `"message":`+wire+`}`) {
			t.Fatalf("session line\n got %s\nwant message %s", line, wire)
		}
	}
	loaded, err := NewSessionManagerWithDir(sm.cwd, sm.sessionDir).Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	context := loaded.BuildContext(nil)
	if len(context) != 1+len(wires) {
		t.Fatalf("resumed context %d messages, want %d", len(context), 1+len(wires))
	}
	for index, wire := range wires {
		encoded, err := json.Marshal(context[1+index])
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != wire {
			t.Fatalf("resumed message\n got %s\nwant %s", encoded, wire)
		}
	}
}

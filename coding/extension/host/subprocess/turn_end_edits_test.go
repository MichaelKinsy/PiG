package subprocess

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// turnEndReply sends a turn_end event holding the assistant message and one tool result to a handler that answers with reply, and returns the message, the tool result and the handler's outcome.
func turnEndReply(t *testing.T, reply string) (*agent.AssistantMessage, extension.TurnEndEvent, any, error) {
	t.Helper()
	hostEnd, peer := net.Pipe()
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("turn_end", 1)
	assistant := &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "first"}}, Timestamp: 1}
	event := extension.TurnEndEvent{
		Type:          "turn_end",
		Message:       agent.AgentMessage{Assistant: assistant},
		ToolResults:   []extension.ToolResultMessage{{Role: "toolResult", ToolCallID: "c", ToolName: "t", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool"}}, Timestamp: 1}},
		BoundaryState: &extension.BoundaryState{},
	}
	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := handler(event, t.Context())
		done <- outcome{result, err}
	}()
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(reply)}})
	got := <-done
	_ = peer.Close()
	return assistant, event, got.result, got.err
}

func assistantText(t *testing.T, message *agent.AssistantMessage) string {
	t.Helper()
	text, err := json.Marshal(message.Content)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// The reply of a turn_end handler carries the messages it edited beside the boundary members: the assistant message at 0 and the tool results after it.
func TestTurnEndReplyEditsReachTheTurnsMessages(t *testing.T) {
	assistant, event, result, err := turnEndReply(t, `{"_pigBoundaryEntries":[],"_pigBoundaryResult":{"continue":true},"_pigEventEdits":[`+
		`{"at":0,"message":{"role":"assistant","content":[{"type":"text","text":"edited"}],"timestamp":1}},`+
		`{"at":1,"message":{"role":"toolResult","toolCallId":"c","toolName":"t","content":[{"type":"text","text":"tool edited"}],"isError":false,"timestamp":1}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := assistantText(t, assistant); !strings.Contains(got, "edited") {
		t.Errorf("the assistant message holds %s, want the edit", got)
	}
	if text, _ := json.Marshal(event.ToolResults[0].Content); !strings.Contains(string(text), "tool edited") {
		t.Errorf("the tool result holds %s, want the edit", text)
	}
	if raw, ok := result.(json.RawMessage); !ok || string(raw) != `{"continue":true}` {
		t.Errorf("the boundary result is %#v, want the handler's result", result)
	}
}

// A malformed member is a refused reply, an edit that does not fit refuses the whole reply, and no message changes.
func TestTurnEndReplyRefusesMalformedEditsAndAppliesNone(t *testing.T) {
	good := `{"at":0,"message":{"role":"assistant","content":[{"type":"text","text":"edited"}],"timestamp":1}}`
	for name, edits := range map[string]string{
		"not a list":              `{}`,
		"a string":                `"x"`,
		"an element that is null": `[null]`,
		"no position":             `[{"message":{"role":"assistant","content":[],"timestamp":1}}]`,
		"no message":              `[{"at":0}]`,
		"a message that is null":  `[{"at":0,"message":null}]`,
		"an unknown member":       `[{"at":0,"message":{"role":"assistant","content":[],"timestamp":1},"extra":1}]`,
		"past the tool results":   `[` + good + `,{"at":2,"message":{"role":"toolResult","toolCallId":"c","toolName":"t","content":[],"isError":false,"timestamp":1}}]`,
		"another role":            `[{"at":0,"message":{"role":"user","content":"x","timestamp":1}}]`,
		"named twice":             `[` + good + `,` + good + `]`,
	} {
		assistant, _, _, err := turnEndReply(t, `{"_pigBoundaryEntries":[],"_pigBoundaryResult":null,"_pigEventEdits":`+edits+`}`)
		if err == nil {
			t.Errorf("%s: the reply was not refused", name)
		}
		if got := assistantText(t, assistant); strings.Contains(got, "edited") {
			t.Errorf("%s: the assistant message holds %s after a refused reply", name, got)
		}
	}
}

// The boundary decode ignores members it does not name: a reply with another member is read for its boundary members as before.
func TestTurnEndReplyIgnoresAMemberItDoesNotName(t *testing.T) {
	_, _, _, err := turnEndReply(t, `{"_pigBoundaryEntries":[],"_pigBoundaryResult":null,"_pigUnknown":1}`)
	if err != nil {
		t.Fatal(err)
	}
}

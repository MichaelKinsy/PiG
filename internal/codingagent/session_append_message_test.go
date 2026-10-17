package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// session-manager.ts:1204 appendMessage(message: Message | CustomMessage | BashExecutionMessage) appends a "message" entry as a child of the
// current leaf and returns its id: an LLM message, and a bashExecution message, both through the one method.
func TestAppendMessageTakesLLMAndBashExecutionMessages(t *testing.T) {
	session := NewSession("append-message", t.TempDir())
	user := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
	first, err := session.AppendMessage(user)
	if err != nil {
		t.Fatal(err)
	}
	code := 3
	second, err := session.AppendMessage(BashExecutionMessage{Command: "make test", Output: "boom\n", ExitCode: &code, Timestamp: 2})
	if err != nil {
		t.Fatal(err)
	}
	third, err := session.AppendMessage(&user)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || second == third {
		t.Fatalf("ids repeat: %s %s %s", first, second, third)
	}
	entries := session.GetBranch()
	if len(entries) != 3 || entries[0].Base().ID != first || entries[1].Base().ID != second || entries[2].Base().ID != third {
		t.Fatalf("branch = %v, want the three appended entries in order", entries)
	}
	if entries[1].Base().ParentID == nil || *entries[1].Base().ParentID != first {
		t.Fatalf("the bash entry is not a child of the previous leaf: %+v", entries[1].Base())
	}
	var stored struct {
		Type    string `json:"type"`
		Message struct {
			Role     string `json:"role"`
			Command  string `json:"command"`
			Output   string `json:"output"`
			ExitCode *int   `json:"exitCode"`
		} `json:"message"`
	}
	if err := json.Unmarshal(entries[1].Raw(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Type != "message" || stored.Message.Role != "bashExecution" || stored.Message.Command != "make test" || stored.Message.Output != "boom\n" || stored.Message.ExitCode == nil || *stored.Message.ExitCode != 3 {
		t.Fatalf("stored bash entry = %s", entries[1].Raw())
	}
	if session.GetLeafID() == nil || *session.GetLeafID() != third {
		t.Fatalf("leaf = %v, want %s", session.GetLeafID(), third)
	}
	if role := (BashExecutionMessage{}).MessageRole(); role != "bashExecution" {
		t.Fatalf("MessageRole = %q", role)
	}
	if role := user.MessageRole(); role != "user" {
		t.Fatalf("AgentMessage.MessageRole = %q", role)
	}
}

type foreignSessionMessage struct{}

func (foreignSessionMessage) MessageRole() string { return "foreign" }

// A SessionMessage outside the union has nothing to store: it is an error and no entry is appended.
func TestAppendMessageRejectsAMessageOutsideTheUnion(t *testing.T) {
	session := NewSession("append-message-foreign", t.TempDir())
	if _, err := session.AppendMessage(foreignSessionMessage{}); err == nil || !strings.Contains(err.Error(), "unsupported message") {
		t.Fatalf("err = %v, want unsupported message", err)
	}
	var none *agent.AgentMessage
	if _, err := session.AppendMessage(none); err == nil {
		t.Fatal("a nil *AgentMessage was appended")
	}
	if got := len(session.GetEntries()); got != 0 {
		t.Fatalf("%d entries after the rejected appends", got)
	}
}

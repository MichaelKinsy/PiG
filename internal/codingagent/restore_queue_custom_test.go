package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi #9886 (D135): the dequeue action moves the queued user messages to the editor and leaves the custom messages an extension queued with sendMessage({ deliverAs }) in their queues.
func TestRestoreQueuedMessagesToEditorKeepsQueuedCustomMessages(t *testing.T) {
	m := statusBorderMode(t, false)
	user := func(text string) agent.AgentMessage {
		return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
	}
	custom := func(customType string) agent.AgentMessage {
		return agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": customType, "content": "BACKGROUND-RESULT", "display": true}}
	}
	m.agent.Steer(custom("steer-notify"))
	m.agent.Steer(user("steer text"))
	m.agent.FollowUp(user("follow text"))
	m.agent.FollowUp(custom("follow-notify"))

	if n := m.restoreQueuedMessagesToEditor(false); n != 2 {
		t.Fatalf("restored %d messages, want the 2 user messages", n)
	}
	if got := m.editor.Text(); got != "steer text\n\nfollow text" {
		t.Fatalf("editor = %q", got)
	}
	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 1 || steering[0].Custom["customType"] != "steer-notify" {
		t.Errorf("steering queue = %+v, want only the custom message", steering)
	}
	if len(followUps) != 1 || followUps[0].Custom["customType"] != "follow-notify" {
		t.Errorf("follow-up queue = %+v, want only the custom message", followUps)
	}
}

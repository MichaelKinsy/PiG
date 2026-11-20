package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The interactive mode matches message_end to the block message_start opened by its own state (evCurrentBlock), never by the address of the message, so the message_end event of a Session that publishes the view of the message (agent.AgentMessage.EventView) finishes the block with the message as it was published, whatever a later handler does to the message the transcript holds.
func TestInteractiveModeFinishesTheStreamingBlockWithTheViewTheSessionPublished(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg(""))
	prepareAssistantEventTest(t, m)
	msg := assistantMsg("", ai.TextContent{Text: "streaming"})
	m.handleAgentEvent(agent.MessageStartEvent{Message: msg.EventView()})
	block := m.evCurrentBlock
	if block == nil {
		t.Fatal("message_start opened no block")
	}
	msg.Assistant.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "final"}}
	published := msg.EventView()
	// A later turn_end handler edits the transcript's message in place.
	agent.ReplaceMessageInPlace(msg, assistantMsg("", ai.TextContent{Text: "edited later"}))
	if published.Identity() != msg.Identity() || published.Assistant == msg.Assistant {
		t.Fatalf("the view has identity %v and pointer %p, the message %v and %p", published.Identity(), published.Assistant, msg.Identity(), msg.Assistant)
	}
	m.handleAgentEvent(agent.MessageEndEvent{Message: published})
	if m.evCurrentBlock != nil {
		t.Error("message_end left the streaming block open")
	}
	if got, want := assistantLines(block), []string{"\x1b]133;A\x07", "\x1b]133;B\x07\x1b]133;C\x07 final"}; !slices.Equal(got, want) {
		t.Errorf("the block after message_end: %q, want %q", got, want)
	}
}

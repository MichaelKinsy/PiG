package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// interactive-mode.ts:3378-3383 (steer) and :4493-4498 (follow-up): a message submitted while the agent streams is shown in the pending
// area only. Pi shows no status line for it, so the chat does not gain "Queued steering message" or "Queued follow-up message".
func TestQueuedMessagesShowNoStatusLine(t *testing.T) {
	m, term := newTickRenderProbe(t, "regular")
	m.handleAgentEvent(agent.AgentStartEvent{})
	m.steerMessageWithImages("check the logs", nil)
	m.followUpMessageWithImages("then summarize", nil)
	m.tuiInst.Render()
	screen := widthx.StripAnsi(term.take())
	for _, want := range []string{"Steering: check the logs", "Follow-up: then summarize"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("pending area lacks %q:\n%s", want, screen)
		}
	}
	for _, unwanted := range []string{"Queued steering message", "Queued follow-up message"} {
		if strings.Contains(screen, unwanted) {
			t.Errorf("the chat shows %q, which Pi never shows:\n%s", unwanted, screen)
		}
	}
}

// queuingSessionHandle builds the queued messages of a Session, which marks their images for normalization at delivery.
type queuingSessionHandle struct {
	InteractiveSessionHandle
	built []string
}

func (h *queuingSessionHandle) QueuedUserMessage(text string, images []ai.ImageContent) agent.AgentMessage {
	h.built = append(h.built, text)
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
}

// pi main 4cf69332c (#11782; D249): the interactive mode queues streaming Enter, Alt+Enter, extension sendUserMessage and the
// compaction-queued flush on the Agent itself, so it builds the message through the Session that normalizes its images at delivery.
func TestInteractiveQueuedMessagesAreBuiltBySessionForImageNormalization(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	handle := &queuingSessionHandle{}
	m.opts.SessionHandle = handle
	m.handleAgentEvent(agent.AgentStartEvent{})
	image := ai.ImageContent{MimeType: "image/png", Data: "AA=="}
	m.steerMessageWithImages("steer", []ai.ImageContent{image})
	m.followUpMessageWithImages("follow up", []ai.ImageContent{image})
	m.queueCompactionMessageForActiveTurn(compactionQueuedMessage{text: "flushed", images: []ai.ImageContent{image}, mode: compactionQueueSteer})
	if want := []string{"steer", "follow up", "flushed"}; !slices.Equal(handle.built, want) {
		t.Fatalf("messages built by the Session = %v, want %v", handle.built, want)
	}
	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 2 || len(followUps) != 1 {
		t.Fatalf("queued on the agent: %d steering, %d follow-up", len(steering), len(followUps))
	}
}

func (h *queuingSessionHandle) Inner() *Session { return nil }

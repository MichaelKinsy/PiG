package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/agent-session.ts getLastAssistantText (4347): the last assistant message that is not an aborted one without content,
// every text block joined, trimmed with String.prototype.trim, undefined when that is empty. RPC get_last_assistant_text and /copy read it.
func TestSessionLastAssistantText(t *testing.T) {
	assistant := func(stop ai.StopReason, blocks ...ai.AssistantContentBlock) agent.AgentMessage {
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: blocks, StopReason: stop}}
	}
	text := func(value string) ai.AssistantContentBlock { return ai.TextContent{Text: value} }
	user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}}
	for _, tc := range []struct {
		name     string
		messages []agent.AgentMessage
		want     *string
	}{
		{"none", []agent.AgentMessage{user}, nil},
		{"joined blocks", []agent.AgentMessage{assistant(ai.StopReasonStop, text("a "), ai.ThinkingContent{Thinking: "t"}, text(" b"))}, new("a  b")},
		{"trimmed", []agent.AgentMessage{assistant(ai.StopReasonStop, text("\ufeff\n  answer \u00a0\t"))}, new("answer")},
		{"whitespace only", []agent.AgentMessage{assistant(ai.StopReasonStop, text(" \n"))}, nil},
		{"empty errored message is the last one", []agent.AgentMessage{assistant(ai.StopReasonStop, text("earlier")), assistant(ai.StopReasonError)}, nil},
		{"aborted without content is skipped", []agent.AgentMessage{assistant(ai.StopReasonStop, text("earlier")), assistant(ai.StopReasonAborted)}, new("earlier")},
		{"aborted with content counts", []agent.AgentMessage{assistant(ai.StopReasonStop, text("earlier")), assistant(ai.StopReasonAborted, text("partial"))}, new("partial")},
		{"U+0085 is not trimmed", []agent.AgentMessage{assistant(ai.StopReasonStop, text("\u0085x\u0085"))}, new("\u0085x\u0085")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newModelExtensionHarness(t, []bool{false}, "", true, extension.Extension{}, nil)
			h.session.Agent().SetMessages(tc.messages)
			got := h.session.LastAssistantText()
			switch {
			case got == nil && tc.want != nil:
				t.Fatalf("LastAssistantText = nil, want %q", *tc.want)
			case got != nil && tc.want == nil:
				t.Fatalf("LastAssistantText = %q, want nil", *got)
			case got != nil && *got != *tc.want:
				t.Fatalf("LastAssistantText = %q, want %q", *got, *tc.want)
			}
		})
	}
}

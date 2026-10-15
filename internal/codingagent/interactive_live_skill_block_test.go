package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A /skill: command submits a <skill> block. Pi's message_start(user) handler goes through addMessageToChat, which parses the block
// and shows a collapsible "[skill] name" component followed by the trailing user message (interactive-mode.ts:3501-3505, 3933-3952);
// PiG built the collapsible component only when rebuilding the transcript and printed the raw XML while the turn ran. Found by the
// skills journey (frame diff).
func TestLiveUserSkillBlockRendersAsSkillInvocation(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	block := "<skill name=\"greeter\" location=\"/x/SKILL.md\">\nReferences are relative to /x.\n\n# Greeter\n\nGreet warmly.\n</skill>\n\nsay hello to Ada"
	m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{User: &agent.UserMessage{
		Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: block}}, Timestamp: time.Now().UnixMilli(),
	}}})
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(chat, "[skill] greeter") || strings.Contains(chat, "<skill") {
		t.Fatalf("live skill block is not a collapsed skill invocation:\n%s", chat)
	}
	if !strings.Contains(chat, "say hello to Ada") {
		t.Fatalf("the trailing user message is missing:\n%s", chat)
	}
}

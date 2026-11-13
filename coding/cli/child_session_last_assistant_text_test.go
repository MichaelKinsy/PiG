package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// Pi ede360e4d (#12119): a child session's getLastAssistantText (the op an extension's AgentSession calls, and the lastAssistantText of
// its state) is the same AgentSession.getLastAssistantText, so a turn that ended in a provider error without text is skipped there too.
func TestChildSessionLastAssistantTextSkipsATurnWithoutAnAnswer(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	session.Agent().SetMessages([]agent.AgentMessage{
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "Here is the answer."}}, StopReason: ai.StopReasonStop}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonError, ErrorMessage: "400 invalid_request_error"}},
	})
	child := &childSession{session: session}
	got, err := child.Op(t.Context(), "getLastAssistantText", nil)
	if err != nil || string(got) != `"Here is the answer."` {
		t.Fatalf("op getLastAssistantText = %s, %v; want the previous answer", got, err)
	}
	if text := child.State().LastAssistantText; text == nil || *text != "Here is the answer." {
		t.Fatalf("state lastAssistantText = %v, want the previous answer", text)
	}
}

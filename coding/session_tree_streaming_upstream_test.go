package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/tree-during-streaming.test.ts:7
func TestUpstreamTreeDuringStreamingRejectsNavigationWithoutChangingActiveLeaf(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	target, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "first"}}, Timestamp: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var navigationErr error
	var leafUnchanged bool
	h.provider.responses = []scriptedResponse{func([]ai.Message) *ai.AssistantMessage {
		active := *h.session.LeafID()
		_, navigationErr = h.session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: false})
		leafUnchanged = active != target && h.session.LeafID() != nil && *h.session.LeafID() == active
		return sessionTestMessage("faux", "response", ai.StopReasonStop, "")
	}}
	if _, err := h.session.Send(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	if navigationErr == nil || navigationErr.Error() != "Wait for the current response to finish before navigating the session tree." {
		t.Fatalf("navigation error = %v", navigationErr)
	}
	if !leafUnchanged {
		t.Fatal("navigation changed the active leaf")
	}
}

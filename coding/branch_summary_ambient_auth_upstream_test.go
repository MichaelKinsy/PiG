package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6324-branch-summary-ambient-auth.test.ts:15
func TestBranchSummaryAmbientAuthWithoutAPIKeyUpstream(t *testing.T) {
	services := newTestServices(t)
	stored, err := services.Auth().List(t.Context())
	if err != nil || len(stored) != 0 {
		t.Fatalf("auth must be unconfigured: %#v,%v", stored, err)
	}
	calls := 0
	provider := &runtimeTestProvider{id: "ambient-summary", stream: func(_ context.Context, _ ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls++
		if opts.APIKey != "" {
			t.Errorf("summary fabricated API key %q", opts.APIKey)
		}
		message := runtimeTestMessage("ambient-summary", "test-model", "branch summary text", ai.StopReasonStop)
		message.Usage = ai.Usage{Input: 1, Output: 1, TotalTokens: 2, Cost: ai.UsageCost{Total: 0.25}}
		stream := ai.NewAssistantMessageEventStream()
		// The Go stream validates its start/terminal protocol; the fixture's terminal message and assertions are unchanged.
		if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
			return nil, err
		}
		if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
			return nil, err
		}
		return stream, nil
	}}
	session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	var target string
	for _, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "first branch"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "first reply"}}}},
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "abandoned branch work"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "abandoned reply"}}}},
	} {
		id, err := session.inner.AppendMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		if target == "" {
			target = id
		}
	}
	result, err := session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
	if err != nil || result.Cancelled || calls != 1 || result.SummaryEntry == nil {
		t.Fatalf("navigation=%#v,%v calls=%d", result, err, calls)
	}
	if result.SummaryEntry.Type != "branch_summary" || !strings.Contains(result.SummaryEntry.Summary, "branch summary text") || result.SummaryEntry.Usage == nil || result.SummaryEntry.Usage.Cost.Total != 0.25 {
		t.Fatalf("summary=%#v", result.SummaryEntry)
	}
}

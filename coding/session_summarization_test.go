package coding

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// These responses must never become persisted summaries (compaction.ts
// getSummarizationFailure and the subsequent tool-call guard).
func TestSummarizationRejectsIncompleteResponses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response scriptedResponse
		want     string
	}{
		{"length", fauxReply("partial", ai.StopReasonLength, 0), "failed: generation hit the token cap and the summary is incomplete"},
		{"error without message", fauxError(""), "failed: Unknown error"},
		{"tool", fauxToolCall("read"), "attempted to call a tool"},
	} {
		for _, split := range []bool{false, true} {
			name := "history"
			label := "Summarization"
			if split {
				name = "prefix"
				label = "Turn prefix summarization"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				provider := &scriptedProvider{responses: []scriptedResponse{tc.response}}
				messages := []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "summarize this"}}}}}
				prep := compaction.CompactionPreparation{FirstKeptEntryID: "keep", Settings: compaction.CompactionSettings{ReserveTokens: 1000}}
				if split {
					prep.IsSplitTurn = true
					prep.TurnPrefixMessages = messages
				} else {
					prep.MessagesToSummarize = messages
				}
				_, err := compaction.Compact(context.Background(), prep, fakeModelWithProvider(provider), modelCompleter{}, nil, "", "", nil, "")
				if err == nil || err.Error() != label+" "+tc.want {
					t.Fatalf("error = %v, want %s %s", err, label, tc.want)
				}
			})
		}
	}
}

// Ports packages/coding-agent/test/suite/regressions/7048-compaction-truncated-summary.test.ts.
func TestCompactDoesNotPersistLengthLimitedSummary(t *testing.T) {
	provider := &scriptedProvider{responses: []scriptedResponse{fauxReply("partial summar", ai.StopReasonLength, 0)}}
	sess, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: fakeModelWithProvider(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i := range 3 {
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: fmt.Sprintf("q%d", i)}}}},
			{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("a%d", i)}}}},
		} {
			if _, err := sess.inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	sess.refreshContext()

	if _, err := sess.CompactResult(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "generation hit the token cap") {
		t.Fatalf("CompactResult error = %v, want token-cap failure", err)
	}
	for _, entry := range sess.inner.Entries() {
		if entry.Base.Type == "compaction" {
			t.Fatalf("length-limited summary persisted as entry %q", entry.Base.ID)
		}
	}
}

type pricedSummaryProvider struct {
	options []ai.StreamOptions
}

func (*pricedSummaryProvider) ID() string   { return "priced-summary" }
func (*pricedSummaryProvider) Close() error { return nil }
func (p *pricedSummaryProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.options = append(p.options, options)
	message := sessionTestMessage(p.ID(), "priced summary", ai.StopReasonStop, "")
	message.Usage = ai.Usage{Input: 100, Output: 10}
	message.Usage.Cost.Input = float64(message.Usage.Input) * options.ModelCost.Input / 1_000_000
	message.Usage.Cost.Output = float64(message.Usage.Output) * options.ModelCost.Output / 1_000_000
	message.Usage.Cost.Total = message.Usage.Cost.Input + message.Usage.Cost.Output
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

// Summary requests retain the selected model's pricing so provider-finalized
// compaction usage records its cost.
func TestSessionSummarizationPreservesModelCost(t *testing.T) {
	provider := &pricedSummaryProvider{}
	model := fakeModelWithProvider(provider)
	model.Capabilities.InputCostPer1M = 3
	model.Capabilities.OutputCostPer1M = 15
	sess, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i := range 3 {
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: fmt.Sprintf("q%d", i)}}}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("a%d", i)}}}},
		} {
			if _, err := sess.inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	sess.refreshContext()

	result, err := sess.CompactResult(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || result.Usage.Cost.Total <= 0 {
		t.Fatalf("summary usage = %#v, want priced cost", result.Usage)
	}
	for _, options := range provider.options {
		if options.ModelCost.Input != 3 || options.ModelCost.Output != 15 {
			t.Fatalf("summary model cost = %#v", options.ModelCost)
		}
	}
}

type summaryRoutingProvider struct {
	options  []ai.StreamOptions
	requests [][]ai.Message
}

func (*summaryRoutingProvider) ID() string   { return "summary-routing" }
func (*summaryRoutingProvider) Close() error { return nil }
func (p *summaryRoutingProvider) Stream(_ context.Context, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.options = append(p.options, options)
	p.requests = append(p.requests, request.Messages())
	message := sessionTestMessage(p.ID(), "complete summary", ai.StopReasonStop, "")
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

// Upstream compaction passes the session thinking level and no routing session
// (each summary gets a fresh one); the bug report summary also forwards the
// session ID. Neither writes prompt cache (agent-session.ts
// _runDefaultCompaction, summarizeForBugReport).
func TestSessionSummarizationRoutingAndThinkingLevel(t *testing.T) {
	provider := &summaryRoutingProvider{}
	model := fakeModelWithProvider(provider)
	model.Capabilities.MaxThinking = ai.ThinkingHigh
	sess, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i := range 3 {
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: fmt.Sprintf("q%d", i)}}}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("a%d", i)}}}},
		} {
			if _, err := sess.inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	sess.agent.SetMessages(sess.inner.BuildContext(nil))
	if err := sess.SetThinkingLevel(ai.ThinkingMedium); err != nil {
		t.Fatal(err)
	}

	if err := sess.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.SummarizeForBugReport(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(provider.options) < 2 {
		t.Fatalf("summary requests = %d, want compaction and bug report", len(provider.options))
	}
	compactions, bug := provider.options[:len(provider.options)-1], provider.options[len(provider.options)-1]
	seen := map[string]bool{sess.ID(): true}
	for _, options := range compactions {
		if options.SessionID == "" || seen[options.SessionID] {
			t.Fatalf("compaction routing ID = %q, want a fresh ID per request (session %q)", options.SessionID, sess.ID())
		}
		seen[options.SessionID] = true
	}
	if bug.SessionID != sess.ID() {
		t.Fatalf("bug report routing ID = %q, want %q", bug.SessionID, sess.ID())
	}
	for i, options := range provider.options {
		if options.Thinking != ai.ThinkingMedium || options.Env["PI_CACHE_RETENTION"] != "none" || options.CacheRetention != ai.CacheRetentionNone {
			t.Fatalf("summary options = thinking %q cache env %q retention %q", options.Thinking, options.Env["PI_CACHE_RETENTION"], options.CacheRetention)
		}
		if len(ai.GetCurrentTools(provider.requests[i])) != 0 {
			t.Fatalf("summary request %d exposed session tools", i)
		}
	}
}

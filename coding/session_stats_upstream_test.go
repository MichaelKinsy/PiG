package coding

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func statsUser(text string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: timestamp}}
}

func statsAssistant(text string, tokens int, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "anthropic-messages", Provider: "anthropic", ModelID: "claude-sonnet-4-5", Usage: &ai.Usage{Input: tokens, TotalTokens: tokens}, StopReason: ai.StopReasonStop, Timestamp: timestamp}}
}

func appendStatsMessage(t *testing.T, s *Session, message agent.AgentMessage) string {
	t.Helper()
	id, err := s.inner.AppendMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func newStatsUpstreamSession(t *testing.T) *Session {
	t.Helper()
	services := newTestServices(t)
	if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("anthropic", "claude-sonnet-4-5")
	if model == nil {
		t.Fatal("upstream fixture model is absent")
	}
	// The upstream fixture constructs AgentSession directly around an empty manager, not through SDK initialization that appends model/thinking metadata.
	session, err := NewSession(services, SessionOptions{existing: icodingagent.NewSession("stats", services.CWD()), Model: model, NoSession: true, SkipBuiltinTools: true, SystemPrompt: "You are a helpful assistant.", ThinkingLevel: ai.ThinkingHigh})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	return session
}

func TestUpstreamAgentSessionStatsContext(t *testing.T) {
	model, ok := ai.LookupModelExact("anthropic/claude-sonnet-4-5")
	if !ok {
		t.Fatal("upstream fixture model is absent")
	}
	window := model.ContextWindow
	for _, tc := range []struct {
		name                string
		compact, post, zero bool
		total, tokens       int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:101
		{name: "exposes the current context usage alongside token totals", total: 200, tokens: 200},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:119
		{name: "reports unknown current context usage immediately after compaction", compact: true, total: 375000},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:142
		{name: "uses post-compaction usage for current context instead of stale kept usage", compact: true, post: true, total: 400000, tokens: 25000},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:293
		{name: "ignores zero-usage messages when checking for post-compaction context usage", compact: true, post: true, zero: true, total: 400000, tokens: 25000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStatsUpstreamSession(t)
			if !tc.compact {
				appendStatsMessage(t, s, statsUser("hello", 1))
				appendStatsMessage(t, s, statsAssistant("hi", 200, 2))
			} else {
				appendStatsMessage(t, s, statsUser("first", 1))
				appendStatsMessage(t, s, statsAssistant("response1", 180000, 2))
				kept := appendStatsMessage(t, s, statsUser("second", 3))
				appendStatsMessage(t, s, statsAssistant("response2", 195000, 4))
				if _, err := s.inner.AppendCompaction("summary", kept, 195000, nil, false, nil); err != nil {
					t.Fatal(err)
				}
				appendStatsMessage(t, s, statsUser("third", 5))
				if tc.post {
					appendStatsMessage(t, s, statsAssistant("response3", 25000, 6))
				}
				if tc.zero {
					appendStatsMessage(t, s, statsUser("continue", 7))
					appendStatsMessage(t, s, statsAssistant("partial", 0, 8))
				}
			}
			s.agent.SetMessages(s.inner.BuildContext(nil))
			stats := s.GetSessionStats()
			usage := stats.ContextUsage
			if stats.Tokens.Input != tc.total {
				t.Fatalf("input = %d, want %d", stats.Tokens.Input, tc.total)
			}
			if !reflect.DeepEqual(usage, s.ContextUsage()) || usage == nil || usage.ContextWindow != window {
				t.Fatalf("context usage = %+v", usage)
			}
			if tc.compact && !tc.post {
				if usage.Tokens != nil || usage.Percent != nil {
					t.Fatalf("stale usage = %+v", usage)
				}
				return
			}
			if usage.Tokens == nil || usage.Percent == nil {
				t.Fatalf("unknown usage = %+v", usage)
			}
			if tc.zero {
				if *usage.Tokens <= tc.tokens {
					t.Fatalf("context = %d, want > %d", *usage.Tokens, tc.tokens)
				}
			} else if *usage.Tokens != tc.tokens || *usage.Percent != float64(tc.tokens)/float64(window)*100 {
				t.Fatalf("context = %d (%g%%)", *usage.Tokens, *usage.Percent)
			}
		})
	}
}

func TestUpstreamAgentSessionStatsUsage(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:166
		{"includes branch summary usage in session totals", "branch"},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:188
		{"includes compaction usage in session totals", "compaction"},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:244
		{"includes tool result usage in session totals", "tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStatsUpstreamSession(t)
			usage := &ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: .1, Output: .2, CacheRead: .3, CacheWrite: .4, Total: 1}}
			var err error
			switch tc.kind {
			case "branch":
				_, err = s.inner.BranchWithSummary(nil, "summary", nil, false, usage)
			case "compaction":
				kept := appendStatsMessage(t, s, statsUser("hello", 1))
				_, err = s.inner.AppendCompaction("summary", kept, 100, nil, false, usage)
			case "tool":
				appendStatsMessage(t, s, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "tool-call-1", ToolName: "test_tool", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool result"}}, Usage: usage, Timestamp: 1}})
			}
			if err != nil {
				t.Fatal(err)
			}
			s.agent.SetMessages(s.inner.BuildContext(nil))
			stats := s.GetSessionStats()
			if stats.Tokens != (SessionStatsTokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Total: 100}) || stats.Cost != 1 {
				t.Fatalf("stats = %+v", stats)
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:211
func TestUpstreamStatsIncludesCacheWarmingUsageExactlyOnceWithoutAddingMessages(t *testing.T) {
	s := newStatsUpstreamSession(t)
	usage := ai.Usage{Input: 2, Output: 1, CacheRead: 97, TotalTokens: 100, Cost: ai.UsageCost{Input: .001, Output: .002, CacheRead: .007, Total: .01}}
	entry, err := s.inner.AppendUsage("cache_warm", "anthropic", "claude-sonnet-4-5", usage, "extension override")
	if err != nil {
		t.Fatal(err)
	}
	entries := s.inner.GetEntries()
	if len(entries) == 0 {
		t.Fatal("usage was not appended")
	}
	var first icodingagent.UsageEntry
	if err := json.Unmarshal(entries[0].Raw(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "usage" || first.Kind != "cache_warm" || first.Note != "extension override" {
		t.Fatalf("first entry = %+v, appended entry = %+v", first, entry)
	}
	stats := s.GetSessionStats()
	if stats.Tokens != (SessionStatsTokens{Input: 2, Output: 1, CacheRead: 97, Total: 100}) || stats.TotalMessages != 0 || len(s.inner.BuildContext(nil)) != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	want := []icodingagent.SessionUsageBreakdown{{Key: "anthropic/claude-sonnet-4-5", Cost: .01, Tokens: 100}}
	if got := s.inner.Accounting().UsageBreakdown; !reflect.DeepEqual(got, want) {
		t.Fatalf("breakdown = %+v, want %+v", got, want)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/agent-session-stats.test.ts:268
func TestUpstreamStatsGroupsToolAndSummaryUsageSeparatelyFromModelAttributedUsage(t *testing.T) {
	s := newStatsUpstreamSession(t)
	root := appendStatsMessage(t, s, statsUser("hello", 1))
	msg := statsAssistant("response", 100, 2)
	msg.Assistant.Usage.Cost.Total = .5
	appendStatsMessage(t, s, msg)
	usage := func(cost float64) *ai.Usage {
		return &ai.Usage{Input: 100, TotalTokens: 100, Cost: ai.UsageCost{Total: cost}}
	}
	appendStatsMessage(t, s, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "tool-call-1", ToolName: "test_tool", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool result"}}, Usage: usage(1), Timestamp: 1}})
	if _, err := s.inner.AppendCompaction("summary", root, 100, nil, false, usage(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.inner.BranchWithSummary(nil, "branch summary", nil, false, usage(3)); err != nil {
		t.Fatal(err)
	}
	want := []icodingagent.SessionUsageBreakdown{{Key: "Tools/summaries", Cost: 6, Tokens: 300}, {Key: "anthropic/claude-sonnet-4-5", Cost: .5, Tokens: 100}}
	if got := s.inner.Accounting().UsageBreakdown; !reflect.DeepEqual(got, want) {
		t.Fatalf("breakdown = %+v, want %+v", got, want)
	}
}

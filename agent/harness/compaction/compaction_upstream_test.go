// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func mockUsage(input, output, read, write int) *ai.Usage {
	return &ai.Usage{Input: input, Output: output, CacheRead: read, CacheWrite: write, TotalTokens: input + output + read + write}
}
func user(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}}
}
func assistant(text string, usage ...*ai.Usage) agent.AgentMessage {
	u := mockUsage(100, 50, 0, 0)
	if len(usage) > 0 {
		u = usage[0]
	}
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "anthropic-messages", Provider: "anthropic", ModelID: "claude-sonnet-4-5", Usage: u, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}}
}

type entryFactory struct{ next int }

func (f *entryFactory) message(message agent.AgentMessage, parent ...string) session.Entry {
	e := session.Entry{ID: fmt.Sprintf("entry-%d", f.next), Seq: int64(f.next + 1), Timestamp: time.Now().UnixMilli(), Type: session.EntryTypeMessage, Message: message}
	f.next++
	if len(parent) > 0 {
		e.ParentID = new(parent[0])
	}
	return e
}
func (f *entryFactory) compact(summary string, parent *string, tail ...agent.AgentMessage) session.Entry {
	e := f.message(agent.AgentMessage{})
	e.Type = session.EntryTypeCompaction
	e.ParentID = parent
	e.Summary = summary
	e.TokensBefore = 1234
	e.RetainedTail = append([]agent.AgentMessage{}, tail...)
	return e
}
func (f *entryFactory) custom(kind string, parent ...string) session.Entry {
	e := f.message(agent.AgentMessage{}, parent...)
	e.Type = session.EntryTypeCustom
	e.CustomType = kind
	return e
}
func preparation(t *testing.T, entries []session.Entry, settings CompactionSettings) *CompactionPreparation {
	t.Helper()
	p, err := PrepareCompaction(entries, settings)
	if err != nil || p == nil {
		t.Fatalf("preparation=%+v error=%v", p, err)
	}
	return p
}
func messageRole(message agent.AgentMessage) string {
	switch {
	case message.User != nil:
		return "user"
	case message.Assistant != nil:
		return "assistant"
	case message.ToolResult != nil:
		return "toolResult"
	case message.System != nil:
		return "system"
	default:
		role, _ := message.Custom["role"].(string)
		return role
	}
}

func TestHarnessCompactionUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:156
	t.Run("calculates total context tokens from usage", func(t *testing.T) {
		if got := CalculateContextTokens(*mockUsage(1000, 500, 200, 100)); got != 1800 {
			t.Fatal(got)
		}
		if got := CalculateContextTokens(*mockUsage(0, 0, 0, 0)); got != 0 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:161
	t.Run("checks compaction threshold", func(t *testing.T) {
		settings := CompactionSettings{Enabled: true, ReserveTokens: 10000, KeepRecentTokens: 20000}
		if !ShouldCompact(95000, 100000, settings) || ShouldCompact(89000, 100000, settings) {
			t.Fatal("wrong threshold")
		}
		settings.Enabled = false
		if ShouldCompact(95000, 100000, settings) {
			t.Fatal("disabled compaction")
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:172
	t.Run("finds cut point based on token differences", func(t *testing.T) {
		var f entryFactory
		var entries []session.Entry
		parent := ""
		for i := range 10 {
			u := f.message(user(fmt.Sprintf("User %d", i)))
			if parent != "" {
				u.ParentID = new(parent)
			}
			a := f.message(assistant(fmt.Sprintf("Assistant %d", i), mockUsage(0, 100, (i+1)*1000, 0)), u.ID)
			entries = append(entries, u, a)
			parent = a.ID
		}
		cut := FindCutPoint(entries, 0, len(entries), 2500)
		if cut.FirstKeptEntryIndex < 0 || cut.FirstKeptEntryIndex >= len(entries) || entries[cut.FirstKeptEntryIndex].Type != session.EntryTypeMessage {
			t.Fatal(cut)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:190
	t.Run("covers cut-point and turn-start edge cases", func(t *testing.T) {
		var f entryFactory
		first := f.custom("first")
		second := f.custom("second", first.ID)
		want := CutPointResult{0, -1, false}
		if got := FindCutPoint([]session.Entry{first, second}, 0, 2, 1); got != want {
			t.Fatal(got)
		}
		branch := f.message(agent.AgentMessage{}, second.ID)
		branch.Type = session.EntryTypeBranchSummary
		branch.FromID = new("branch")
		branch.Summary = "branch summary"
		if FindTurnStartIndex([]session.Entry{first, branch}, 1, 0) != 1 || FindTurnStartIndex([]session.Entry{first, second}, 1, 0) != -1 {
			t.Fatal("wrong turn start")
		}
		if FindCutPoint([]session.Entry{first, branch}, 0, 2, 1).FirstKeptEntryIndex != 0 {
			t.Fatal("metadata boundary moved")
		}
		tool := f.message(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call-1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool output"}}, Timestamp: time.Now().UnixMilli()}})
		if got := FindCutPoint([]session.Entry{tool}, 0, 1, 1); got != want {
			t.Fatal(got)
		}
		u := f.message(user("user"))
		c := f.compact("summary", &u.ID)
		a := f.message(assistant("assistant"), c.ID)
		if got := FindCutPoint([]session.Entry{u, c, a}, 0, 3, 1).FirstKeptEntryIndex; got != 2 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:235
	t.Run("estimates tokens and context usage across supported message roles", func(t *testing.T) {
		var f entryFactory
		usage := mockUsage(10, 5, 3, 2)
		a := assistant("assistant", usage)
		thinking := assistant("assistant", usage)
		thinking.Assistant.Content = []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "thinking"}, ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "file.ts"}}}
		custom := agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "note", "content": "custom text", "display": true, "timestamp": time.Now().UnixMilli()}}
		tool := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call-1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool text"}, ai.ImageContent{MimeType: "image/png", Data: "abc"}}, Timestamp: time.Now().UnixMilli()}}
		bash := agent.AgentMessage{Custom: map[string]any{"role": "bashExecution", "command": "npm run check", "output": "ok", "exitCode": 0, "cancelled": false, "truncated": false, "timestamp": time.Now().UnixMilli()}}
		branch := agent.AgentMessage{Custom: map[string]any{"role": "branchSummary", "summary": "branch", "fromId": "x", "timestamp": time.Now().UnixMilli()}}
		summary := agent.AgentMessage{Custom: map[string]any{"role": "compactionSummary", "summary": "compact", "tokensBefore": 123, "timestamp": time.Now().UnixMilli()}}
		var plain agent.AgentMessage
		if err := json.Unmarshal([]byte(`{"role":"user","content":"plain user","timestamp":1}`), &plain); err != nil {
			t.Fatal(err)
		}
		for _, message := range []agent.AgentMessage{plain, thinking, custom, bash, branch, summary} {
			if EstimateTokens(message) <= 0 {
				t.Fatalf("zero estimate for %s", messageRole(message))
			}
		}
		if EstimateTokens(tool) <= 1000 {
			t.Fatal("image not counted")
		}
		if EstimateTokens(agent.AgentMessage{Custom: map[string]any{"role": "unknown", "timestamp": time.Now().UnixMilli()}}) != 0 {
			t.Fatal("unknown role counted")
		}
		if GetLastAssistantUsage([]session.Entry{f.message(user("user")), f.message(a)}) != usage {
			t.Fatal("usage identity changed")
		}
		aborted := a.Clone()
		aborted.Assistant.StopReason = ai.StopReasonAborted
		failure := a.Clone()
		failure.Assistant.StopReason = ai.StopReasonError
		if GetLastAssistantUsage([]session.Entry{f.message(aborted), f.message(failure)}) != nil {
			t.Fatal("failed usage counted")
		}
		if GetLastAssistantUsage([]session.Entry{f.message(user("user")), f.message(a), f.message(assistant("partial", mockUsage(0, 0, 0, 0)))}) != usage {
			t.Fatal("zero usage hid valid usage")
		}
		if EstimateContextTokens([]agent.AgentMessage{user("no usage")}).LastUsageIndex != nil {
			t.Fatal("expected null usage index")
		}
		estimate := EstimateContextTokens([]agent.AgentMessage{a, user("tail")})
		if estimate.UsageTokens != 20 || estimate.LastUsageIndex == nil || *estimate.LastUsageIndex != 0 {
			t.Fatal(estimate)
		}
		estimate = EstimateContextTokens([]agent.AgentMessage{user("Hello"), a, user("continue"), assistant("Partial thinking", mockUsage(0, 0, 0, 0))})
		if estimate.UsageTokens != 20 || estimate.LastUsageIndex == nil || *estimate.LastUsageIndex != 1 || estimate.TrailingTokens <= 0 || estimate.Tokens != 20+estimate.TrailingTokens {
			t.Fatal(estimate)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:326
	t.Run("builds session context with a compaction entry", func(t *testing.T) {
		var f entryFactory
		u1 := f.message(user("1"))
		a1 := f.message(assistant("a"), u1.ID)
		u2 := f.message(user("2"), a1.ID)
		a2 := f.message(assistant("b"), u2.ID)
		c := f.compact("Summary of 1,a,2,b", &a2.ID, user("2"), assistant("b"))
		u3 := f.message(user("3"), c.ID)
		a3 := f.message(assistant("c"), u3.ID)
		loaded, err := session.BuildSessionContext(t.Context(), []session.Entry{u1, a1, u2, a2, c, u3, a3}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var roles []string
		for _, message := range loaded {
			roles = append(roles, messageRole(message))
		}
		if len(loaded) != 5 || loaded[0].Custom["role"] != "compactionSummary" || !slices.Equal(roles, []string{"compactionSummary", "user", "assistant", "user", "assistant"}) {
			t.Fatal(roles)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:349
	t.Run("prepares compaction using latest summary as previousSummary", func(t *testing.T) {
		var f entryFactory
		u1 := f.message(user("user msg 1"))
		a1 := f.message(assistant("assistant msg 1"), u1.ID)
		u2 := f.message(user("user msg 2"), a1.ID)
		a2 := f.message(assistant("assistant msg 2", mockUsage(5000, 1000, 0, 0)), u2.ID)
		c := f.compact("First summary", &a2.ID)
		u3 := f.message(user("user msg 3"), c.ID)
		a3 := f.message(assistant("assistant msg 3", mockUsage(8000, 2000, 0, 0)), u3.ID)
		entries := []session.Entry{u1, a1, u2, a2, c, u3, a3}
		p := preparation(t, entries, DefaultCompactionSettings)
		loaded, err := session.BuildSessionContext(t.Context(), entries, nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.PreviousSummary != "First summary" || len(p.RetainedTail) == 0 || p.TokensBefore != EstimateContextTokens(loaded).Tokens {
			t.Fatalf("preparation=%+v", p)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:367
	t.Run("carries previous compaction retained tail into next preparation", func(t *testing.T) {
		var f entryFactory
		retainedUser, retainedAssistant := user("retained user"), assistant("retained assistant")
		c := f.compact("previous summary", nil, retainedUser, retainedAssistant)
		u := f.message(user("new user"), c.ID)
		a := f.message(assistant("new assistant"), u.ID)
		p := preparation(t, []session.Entry{c, u, a}, CompactionSettings{Enabled: true, ReserveTokens: 100, KeepRecentTokens: 1})
		all := slices.Concat(p.MessagesToSummarize, p.TurnPrefixMessages, p.RetainedTail)
		if p.PreviousSummary != "previous summary" || !reflect.DeepEqual(all, []agent.AgentMessage{retainedUser, retainedAssistant, u.Message, a.Message}) {
			t.Fatalf("preparation=%+v messages=%+v", p, all)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:389
	t.Run("prepares split-turn compaction with prior file-operation details", func(t *testing.T) {
		var f entryFactory
		u1 := f.message(user("user msg 1"))
		message := assistant("assistant msg 1")
		message.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "tool-1", Name: "write", Arguments: ai.JsonObject{"path": "written.ts"}}}
		a1 := f.message(message, u1.ID)
		c := f.compact("First summary", &a1.ID)
		var details session.JsonValue = map[string]any{"readFiles": []any{"old-read.ts"}, "modifiedFiles": []any{"old-edit.ts", "written.ts"}}
		c.Details = &details
		u2 := f.message(user("large turn"), c.ID)
		a2 := f.message(assistant("large assistant message"), u2.ID)
		p := preparation(t, []session.Entry{u1, a1, c, u2, a2}, CompactionSettings{Enabled: true, ReserveTokens: 100, KeepRecentTokens: 1})
		if p.PreviousSummary != "First summary" || !p.IsSplitTurn || len(p.TurnPrefixMessages) != 1 || p.TurnPrefixMessages[0].User == nil {
			t.Fatal(p)
		}
		for _, path := range []string{"old-edit.ts", "written.ts"} {
			if _, ok := p.FileOps.Edited[path]; !ok {
				t.Fatalf("missing edited %s", path)
			}
		}
		if _, ok := p.FileOps.Read["old-read.ts"]; !ok {
			t.Fatal("missing prior read")
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:417
	t.Run("does not prepare when nothing valid to compact", func(t *testing.T) {
		var f entryFactory
		for _, entries := range [][]session.Entry{{f.compact("already compacted", nil)}, {}} {
			p, err := PrepareCompaction(entries, DefaultCompactionSettings)
			if err != nil || p != nil {
				t.Fatalf("preparation=%+v err=%v", p, err)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:423
	t.Run("serializes conversation with truncated tool results", func(t *testing.T) {
		message := ai.ToolResultMessage{ToolCallID: "tc1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("x", 5000)}}, Timestamp: time.Now().UnixMilli()}
		result := SerializeConversation([]ai.Message{message})
		if !strings.Contains(result, "[Tool result]:") || !strings.Contains(result, "[... 3000 more characters truncated]") {
			t.Fatal(result)
		}
	})
}

// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type upstreamCompactionBuilder struct {
	t       *testing.T
	entries []codingagent.SessionEntry
}

func (b *upstreamCompactionBuilder) add(kind string, fields map[string]any) codingagent.SessionEntry {
	b.t.Helper()
	id := fmt.Sprintf("test-id-%d", len(b.entries))
	var parent *string
	if len(b.entries) > 0 {
		parent = new(b.entries[len(b.entries)-1].Base.ID)
	}
	fields["type"] = kind
	fields["id"] = id
	fields["parentId"] = parent
	fields["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry := mustSessionEntry(b.t, fields)
	b.entries = append(b.entries, entry)
	return entry
}
func piUsage(input, output, read, write int) *ai.Usage {
	return &ai.Usage{Input: input, Output: output, CacheRead: read, CacheWrite: write, TotalTokens: input + output + read + write}
}
func piUser(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}}
}
func piAssistant(text string, usage *ai.Usage) agent.AgentMessage {
	if usage == nil {
		usage = piUsage(100, 50, 0, 0)
	}
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "anthropic-messages", Provider: "anthropic", ModelID: "claude-sonnet-4-5", Usage: usage, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}}
}
func (b *upstreamCompactionBuilder) message(m agent.AgentMessage) codingagent.SessionEntry {
	return b.add("message", map[string]any{"message": m})
}
func (b *upstreamCompactionBuilder) user(text string) codingagent.SessionEntry {
	return b.message(piUser(text))
}
func (b *upstreamCompactionBuilder) assistant(text string, usage *ai.Usage) codingagent.SessionEntry {
	return b.message(piAssistant(text, usage))
}
func (b *upstreamCompactionBuilder) compact(summary, kept string) codingagent.SessionEntry {
	return b.add("compaction", map[string]any{"summary": summary, "firstKeptEntryId": kept, "tokensBefore": 10000})
}
func largeUpstreamSession(t *testing.T) []codingagent.SessionEntry {
	t.Helper()
	data, err := os.ReadFile("../../../.upstream/v0.87.1/packages/coding-agent/test/fixtures/large-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "large.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := codingagent.NewSessionManagerWithDir(t.TempDir(), t.TempDir()).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return s.Entries()
}

func TestCodingCompactionUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:194
	t.Run("should calculate total context tokens from usage", func(t *testing.T) {
		if got := agent.CalculateContextTokens(*piUsage(1000, 500, 200, 100)); got != 1800 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:199
	t.Run("should handle zero values", func(t *testing.T) {
		if got := agent.CalculateContextTokens(*piUsage(0, 0, 0, 0)); got != 0 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:206
	t.Run("should find the last non-aborted assistant message usage", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("Hello")
		b.assistant("Hi", piUsage(100, 50, 0, 0))
		b.user("How are you?")
		b.assistant("Good", piUsage(200, 100, 0, 0))
		if u := GetLastAssistantUsage(b.entries); u == nil || u.Input != 200 {
			t.Fatal(u)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:219
	t.Run("should skip aborted messages", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("Hello")
		b.assistant("Hi", piUsage(100, 50, 0, 0))
		b.user("How are you?")
		m := piAssistant("Aborted", piUsage(300, 150, 0, 0))
		m.Assistant.StopReason = ai.StopReasonAborted
		b.message(m)
		if u := GetLastAssistantUsage(b.entries); u == nil || u.Input != 100 {
			t.Fatal(u)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:237
	t.Run("should skip all-zero assistant usage", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("Hello")
		b.assistant("Hi", piUsage(100, 50, 0, 0))
		b.user("continue")
		b.assistant("Partial", piUsage(0, 0, 0, 0))
		if u := GetLastAssistantUsage(b.entries); u == nil || u.Input != 100 {
			t.Fatal(u)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:250
	t.Run("should return undefined if no assistant messages", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("Hello")
		if u := GetLastAssistantUsage(b.entries); u != nil {
			t.Fatal(u)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:257
	t.Run("uses the last non-zero assistant usage as the context anchor", func(t *testing.T) {
		e := EstimateContextTokens([]agent.AgentMessage{piUser("Hello"), piAssistant("Hi", piUsage(100, 50, 0, 0)), piUser("continue"), piAssistant("Partial thinking", piUsage(0, 0, 0, 0))})
		if e.UsageTokens != 150 || e.LastUsageIndex != 1 || e.TrailingTokens <= 0 || e.Tokens != 150+e.TrailingTokens {
			t.Fatal(e)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:275
	t.Run("should return true when context exceeds threshold", func(t *testing.T) {
		s := CompactionSettings{Enabled: true, ReserveTokens: 10000, KeepRecentTokens: 20000}
		if !ShouldCompact(95000, 100000, s) || ShouldCompact(89000, 100000, s) {
			t.Fatal("threshold")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:286
	t.Run("should return false when disabled", func(t *testing.T) {
		if ShouldCompact(95000, 100000, CompactionSettings{Enabled: false, ReserveTokens: 10000, KeepRecentTokens: 20000}) {
			t.Fatal("disabled")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:298
	t.Run("should find cut point based on actual token differences", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		for i := range 10 {
			b.user(fmt.Sprintf("User %d", i))
			b.assistant(fmt.Sprintf("Assistant %d", i), piUsage(0, 100, (i+1)*1000, 0))
		}
		r := FindCutPoint(b.entries, 0, len(b.entries), 2500)
		e := b.entries[r.FirstKeptEntryIndex]
		m, ok := e.AsMessage()
		if !ok || (m.Message.Role() != "user" && m.Message.Role() != "assistant") {
			t.Fatal(e)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:318
	t.Run("should return startIndex if no valid cut points in range", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.assistant("a", nil)
		if r := FindCutPoint(b.entries, 0, 1, 1000); r.FirstKeptEntryIndex != 0 {
			t.Fatal(r)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:324
	t.Run("should keep everything if all messages fit within budget", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("1")
		b.assistant("a", piUsage(0, 50, 500, 0))
		b.user("2")
		b.assistant("b", piUsage(0, 50, 1000, 0))
		if r := FindCutPoint(b.entries, 0, 4, 50000); r.FirstKeptEntryIndex != 0 {
			t.Fatal(r)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:336
	t.Run("should indicate split turn when cutting at assistant message", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("Turn 1")
		b.assistant("A1", piUsage(0, 100, 1000, 0))
		b.user("Turn 2")
		for i, usage := range []int{5000, 8000, 10000} {
			b.assistant(fmt.Sprintf("A2-%d", i+1), piUsage(0, 100, usage, 0))
		}
		r := FindCutPoint(b.entries, 0, len(b.entries), 3000)
		m, _ := b.entries[r.FirstKeptEntryIndex].AsMessage()
		if m.Message.Role() == "assistant" && (!r.IsSplitTurn || r.TurnStartIndex != 2) {
			t.Fatal(r)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:358
	t.Run("should budget context-visible custom message entries", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("hi")
		b.assistant("hello", nil)
		b.add("custom_message", map[string]any{"customType": "test", "content": strings.Repeat("x", 4000), "display": true})
		b.assistant("ok", nil)
		if r := FindCutPoint(b.entries, 0, 4, 1); r != (CutPointResult{FirstKeptEntryIndex: 3, IsSplitTurn: true, TurnStartIndex: 2}) {
			t.Fatal(r)
		}
		if r := FindCutPoint(b.entries, 0, 4, 2); r != (CutPointResult{FirstKeptEntryIndex: 2, IsSplitTurn: false, TurnStartIndex: -1}) {
			t.Fatal(r)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:378
	t.Run("should fall back to latest valid cut point before oversized trailing tool results", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		oldUser := b.user("old history")
		oldAssistant := b.assistant("old answer", nil)
		currentUser := b.user("read the large file")
		call := piAssistant("", nil)
		call.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "big.txt"}}}
		call.Assistant.StopReason = ai.StopReasonToolUse
		callEntry := b.message(call)
		b.message(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call-1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("x", 8000)}}, Timestamp: time.Now().UnixMilli()}})
		r := FindCutPoint(b.entries, 0, len(b.entries), 1000)
		if r != (CutPointResult{FirstKeptEntryIndex: 3, IsSplitTurn: true, TurnStartIndex: 2}) {
			t.Fatal(r)
		}
		settings := DefaultCompactionSettings
		settings.KeepRecentTokens = 1000
		prep := PrepareCompaction(b.entries, settings)
		a, _ := oldUser.AsMessage()
		c, _ := oldAssistant.AsMessage()
		u, _ := currentUser.AsMessage()
		if prep == nil || prep.FirstKeptEntryID != callEntry.Base.ID || !reflect.DeepEqual(prep.MessagesToSummarize, []agent.AgentMessage{a.Message, c.Message}) || !reflect.DeepEqual(prep.TurnPrefixMessages, []agent.AgentMessage{u.Message}) {
			t.Fatalf("prep=%+v", prep)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:415
	t.Run("should load all messages when no compaction", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("1")
		b.assistant("a", nil)
		b.user("2")
		b.assistant("b", nil)
		ctx := codingagent.BuildSessionContext(b.entries)
		if len(ctx.Messages) != 4 || ctx.ThinkingLevel != "off" || !reflect.DeepEqual(ctx.Model, &codingagent.SessionContextModel{Provider: "anthropic", ModelID: "claude-sonnet-4-5"}) {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:429
	t.Run("should handle single compaction", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("1")
		b.assistant("a", nil)
		u2 := b.user("2")
		b.assistant("b", nil)
		b.compact("Summary of 1,a,2,b", u2.Base.ID)
		b.user("3")
		b.assistant("c", nil)
		m := codingagent.BuildSessionContext(b.entries).Messages
		if len(m) != 5 || m[0].Role() != "compactionSummary" || !strings.Contains(m[0].Custom["summary"].(string), "Summary of 1,a,2,b") {
			t.Fatal(m)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:448
	t.Run("should handle multiple compactions only latest matters", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		u1 := b.user("1")
		b.assistant("a", nil)
		b.compact("First summary", u1.Base.ID)
		b.user("2")
		b.assistant("b", nil)
		u3 := b.user("3")
		b.assistant("c", nil)
		b.compact("Second summary", u3.Base.ID)
		b.user("4")
		b.assistant("d", nil)
		m := codingagent.BuildSessionContext(b.entries).Messages
		if len(m) != 5 || !strings.Contains(m[0].Custom["summary"].(string), "Second summary") {
			t.Fatal(m)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:471
	t.Run("should keep all messages when firstKeptEntryId is first entry", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		u1 := b.user("1")
		b.assistant("a", nil)
		b.compact("First summary", u1.Base.ID)
		b.user("2")
		b.assistant("b", nil)
		if m := codingagent.BuildSessionContext(b.entries).Messages; len(m) != 5 {
			t.Fatal(m)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:485
	t.Run("should track model and thinking level changes", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("1")
		b.add("model_change", map[string]any{"provider": "openai", "modelId": "gpt-4"})
		b.assistant("a", nil)
		b.add("thinking_level_change", map[string]any{"thinkingLevel": "high"})
		ctx := codingagent.BuildSessionContext(b.entries)
		if ctx.ThinkingLevel != "high" || !reflect.DeepEqual(ctx.Model, &codingagent.SessionContextModel{Provider: "anthropic", ModelID: "claude-sonnet-4-5"}) {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:501
	t.Run("does not treat system messages as conversation history", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.add("message", map[string]any{"message": map[string]any{"role": "system", "content": "", "sections": map[string]any{"preamble": "current prompt"}, "timestamp": time.Now().UnixMilli()}})
		u := b.user("one long turn")
		a := b.assistant("assistant suffix", nil)
		settings := DefaultCompactionSettings
		settings.KeepRecentTokens = 1
		prep := PrepareCompaction(b.entries, settings)
		um, _ := u.AsMessage()
		if prep == nil || prep.FirstKeptEntryID != a.Base.ID || !prep.IsSplitTurn || len(prep.MessagesToSummarize) != 0 || !reflect.DeepEqual(prep.TurnPrefixMessages, []agent.AgentMessage{um.Message}) {
			t.Fatalf("prep=%+v", prep)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:524
	t.Run("should skip repeated compactions when kept messages still fit", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user("user msg 1 (summarized by compaction1)")
		b.assistant("assistant msg 1", nil)
		u2 := b.user("user msg 2 - kept by compaction1")
		b.assistant("assistant msg 2", nil)
		b.user("user msg 3 - kept by compaction1")
		b.assistant("assistant msg 3", piUsage(5000, 1000, 0, 0))
		b.compact("First summary", u2.Base.ID)
		b.user("user msg 4 (new after compaction1)")
		b.assistant("assistant msg 4", piUsage(8000, 2000, 0, 0))
		if prep := PrepareCompaction(b.entries, DefaultCompactionSettings); prep != nil {
			t.Fatalf("prep=%+v", prep)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:541
	t.Run("should re-summarize previously kept messages when recent window moves past them", func(t *testing.T) {
		b := upstreamCompactionBuilder{t: t}
		b.user(strings.Repeat("user msg 1 (summarized by compaction1)", 4))
		b.assistant(strings.Repeat("assistant msg 1", 4), nil)
		u2 := b.user(strings.Repeat("user msg 2 - kept by compaction1 ", 12))
		b.assistant(strings.Repeat("assistant msg 2 ", 12), nil)
		b.user(strings.Repeat("user msg 3 - kept by compaction1 ", 12))
		b.assistant(strings.Repeat("assistant msg 3 ", 12), piUsage(5000, 1000, 0, 0))
		b.compact("First summary", u2.Base.ID)
		b.user(strings.Repeat("user msg 4 (new after compaction1) ", 12))
		b.assistant(strings.Repeat("assistant msg 4 ", 12), piUsage(8000, 2000, 0, 0))
		settings := DefaultCompactionSettings
		settings.KeepRecentTokens = 100
		prep := PrepareCompaction(b.entries, settings)
		if prep == nil {
			t.Fatal("missing preparation")
		}
		text := SerializeConversation(convertToLlm(prep.MessagesToSummarize))
		if !strings.Contains(text, "user msg 2 - kept by compaction1") || !strings.Contains(text, "user msg 3 - kept by compaction1") || strings.Contains(text, "First summary") || prep.PreviousSummary != "First summary" {
			t.Fatal(text, prep.PreviousSummary)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:572
	t.Run("should parse the large session", func(t *testing.T) {
		entries := largeUpstreamSession(t)
		messages := 0
		for _, e := range entries {
			if e.Base.Type == "message" {
				messages++
			}
		}
		if len(entries) <= 100 || messages <= 100 {
			t.Fatalf("entries=%d messages=%d", len(entries), messages)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:580
	t.Run("should find cut point in large session", func(t *testing.T) {
		entries := largeUpstreamSession(t)
		r := FindCutPoint(entries, 0, len(entries), DefaultCompactionSettings.KeepRecentTokens)
		m, ok := entries[r.FirstKeptEntryIndex].AsMessage()
		if !ok || m.Message.Role() != "user" && m.Message.Role() != "assistant" {
			t.Fatal(r)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:590
	t.Run("should load session correctly", func(t *testing.T) {
		ctx := codingagent.BuildSessionContext(largeUpstreamSession(t))
		if len(ctx.Messages) <= 100 || ctx.Model == nil {
			t.Fatalf("messages=%d model=%v", len(ctx.Messages), ctx.Model)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:604
	t.Run("should generate a compaction result for the large session", func(t *testing.T) {
		entries := largeUpstreamSession(t)
		prep := PrepareCompaction(entries, DefaultCompactionSettings)
		if prep == nil {
			t.Fatal("missing preparation")
		}
		result, err := Compact(t.Context(), *prep, &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}}, &fakeCompleter{response: strings.Repeat("Detailed fixture summary. ", 8)}, nil, "", "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Summary) <= 100 || result.FirstKeptEntryID == "" || result.TokensBefore <= 0 {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction.test.ts:624
	t.Run("should produce valid session after compaction", func(t *testing.T) {
		entries := largeUpstreamSession(t)
		loaded := codingagent.BuildSessionContext(entries)
		prep := PrepareCompaction(entries, DefaultCompactionSettings)
		if prep == nil {
			t.Fatal("missing preparation")
		}
		result, err := Compact(t.Context(), *prep, &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}}, &fakeCompleter{response: strings.Repeat("Detailed fixture summary. ", 8)}, nil, "", "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(codingagent.CompactionEntry{SessionEntryBase: codingagent.SessionEntryBase{Type: "compaction", ID: "compaction-test-id", ParentID: new(entries[len(entries)-1].Base.ID), Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}, Summary: result.Summary, FirstKeptEntryID: result.FirstKeptEntryID, TokensBefore: result.TokensBefore})
		if err != nil {
			t.Fatal(err)
		}
		var base codingagent.SessionEntryBase
		if err := json.Unmarshal(raw, &base); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, codingagent.NewSessionEntry(raw, base))
		reloaded := codingagent.BuildSessionContext(entries)
		if len(reloaded.Messages) >= len(loaded.Messages) || reloaded.Messages[0].Role() != "compactionSummary" || !strings.Contains(reloaded.Messages[0].Custom["summary"].(string), result.Summary) {
			t.Fatal("invalid compacted context")
		}
	})
}

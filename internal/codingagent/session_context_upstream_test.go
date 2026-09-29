// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func contextFixtureEntry(t *testing.T, id, parent, kind string, fields map[string]any) SessionEntry {
	t.Helper()
	var parentID *string
	if parent != "" {
		parentID = &parent
	}
	base := SessionEntryBase{Type: kind, ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"}
	body := map[string]any{"type": kind, "id": id, "parentId": parentID, "timestamp": base.Timestamp}
	maps.Copy(body, fields)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return NewSessionEntry(raw, base)
}
func contextFixtureMessage(t *testing.T, id, parent, role, text string) SessionEntry {
	t.Helper()
	message := map[string]any{"role": role, "content": text, "timestamp": 1}
	if role == "assistant" {
		message["content"] = []any{map[string]any{"type": "text", "text": text}}
		message["api"] = "anthropic-messages"
		message["provider"] = "anthropic"
		message["model"] = "claude-test"
		message["usage"] = &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
		message["stopReason"] = "stop"
	}
	return contextFixtureEntry(t, id, parent, "message", map[string]any{"message": message})
}
func contextFixtureCompaction(t *testing.T, id, parent, summary, kept string) SessionEntry {
	t.Helper()
	return contextFixtureEntry(t, id, parent, "compaction", map[string]any{"summary": summary, "firstKeptEntryId": kept, "tokensBefore": 1000})
}
func contextTexts(messages []agent.AgentMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.User != nil:
			out = append(out, extractUserText(message))
		case message.Assistant != nil:
			out = append(out, message.Assistant.Content[0].(ai.TextContent).Text)
		default:
			summary, _ := message.Custom["summary"].(string)
			out = append(out, summary)
		}
	}
	return out
}
func contextRoles(messages []agent.AgentMessage) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, m.Role())
	}
	return out
}

func TestBuildSessionContextUpstream(t *testing.T) {
	msg := func(id, parent, role, text string) SessionEntry {
		return contextFixtureMessage(t, id, parent, role, text)
	}
	compact := func(id, parent, summary, kept string) SessionEntry {
		return contextFixtureCompaction(t, id, parent, summary, kept)
	}
	thinking := func(id, parent, level string) SessionEntry {
		return contextFixtureEntry(t, id, parent, "thinking_level_change", map[string]any{"thinkingLevel": level})
	}
	branch := func(id, parent, summary, from string) SessionEntry {
		return contextFixtureEntry(t, id, parent, "branch_summary", map[string]any{"summary": summary, "fromId": from})
	}
	custom := func(id, parent, typ string, data any) SessionEntry {
		return contextFixtureEntry(t, id, parent, "custom", map[string]any{"customType": typ, "data": data})
	}
	requireTexts := func(t *testing.T, ctx SessionContext, want []string) {
		t.Helper()
		if got := contextTexts(ctx.Messages); !reflect.DeepEqual(got, want) {
			t.Fatalf("texts=%v want=%v", got, want)
		}
	}
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:71
	t.Run("empty entries returns empty context", func(t *testing.T) {
		ctx := BuildSessionContext(nil)
		if ctx.Messages == nil || len(ctx.Messages) != 0 || ctx.ThinkingLevel != "off" || ctx.Model != nil {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:78
	t.Run("single user message", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello")})
		if !reflect.DeepEqual(contextRoles(ctx.Messages), []string{"user"}) {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:85
	t.Run("simple conversation", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello"), msg("2", "1", "assistant", "hi there"), msg("3", "2", "user", "how are you"), msg("4", "3", "assistant", "great")})
		if !reflect.DeepEqual(contextRoles(ctx.Messages), []string{"user", "assistant", "user", "assistant"}) {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:97
	t.Run("tracks thinking level changes", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello"), thinking("2", "1", "high"), msg("3", "2", "assistant", "thinking hard")})
		if ctx.ThinkingLevel != "high" || len(ctx.Messages) != 2 {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:108
	t.Run("tracks model from assistant message", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello"), msg("2", "1", "assistant", "hi")})
		if !reflect.DeepEqual(ctx.Model, &SessionContextModel{Provider: "anthropic", ModelID: "claude-test"}) {
			t.Fatal(ctx.Model)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:114
	t.Run("tracks model from model change entry", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello"), contextFixtureEntry(t, "2", "1", "model_change", map[string]any{"provider": "openai", "modelId": "gpt-4"}), msg("3", "2", "assistant", "hi")})
		if !reflect.DeepEqual(ctx.Model, &SessionContextModel{Provider: "anthropic", ModelID: "claude-test"}) {
			t.Fatal(ctx.Model)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:127
	t.Run("includes summary before kept messages", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "first"), msg("2", "1", "assistant", "response1"), msg("3", "2", "user", "second"), msg("4", "3", "assistant", "response2"), compact("5", "4", "Summary of first two turns", "3"), msg("6", "5", "user", "third"), msg("7", "6", "assistant", "response3")})
		requireTexts(t, ctx, []string{"Summary of first two turns", "second", "response2", "third", "response3"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:148
	t.Run("handles compaction keeping from first message", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "first"), msg("2", "1", "assistant", "response"), compact("3", "2", "Empty summary", "1"), msg("4", "3", "user", "second")})
		requireTexts(t, ctx, []string{"Empty summary", "first", "response", "second"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:162
	t.Run("multiple compactions uses latest", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "a"), msg("2", "1", "assistant", "b"), compact("3", "2", "First summary", "1"), msg("4", "3", "user", "c"), msg("5", "4", "assistant", "d"), compact("6", "5", "Second summary", "4"), msg("7", "6", "user", "e")})
		requireTexts(t, ctx, []string{"Second summary", "c", "d", "e"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:179
	t.Run("buildContextEntries returns compaction-aware entries including custom entries", func(t *testing.T) {
		entries := []SessionEntry{msg("1", "", "user", "first"), custom("2", "1", "old-state", map[string]any{"hidden": true}), msg("3", "2", "assistant", "response1"), custom("4", "3", "kept-card", map[string]any{"title": "Kept"}), msg("5", "4", "user", "second"), compact("6", "5", "Summary", "4"), custom("7", "6", "after-card", map[string]any{"title": "After"}), msg("8", "7", "assistant", "response2")}
		if got := upstreamEntryIDs(BuildContextEntries(entries)); !reflect.DeepEqual(got, []string{"6", "4", "5", "7", "8"}) {
			t.Fatal(got)
		}
		if roles := contextRoles(BuildSessionContext(entries).Messages); !reflect.DeepEqual(roles, []string{"compactionSummary", "user", "assistant"}) {
			t.Fatal(roles)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:196
	t.Run("keeps settings from the full path after compaction", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "first"), thinking("2", "1", "high"), msg("3", "2", "assistant", "response1"), msg("4", "3", "user", "second"), compact("5", "4", "Summary", "4")})
		if ctx.ThinkingLevel != "high" || !reflect.DeepEqual(contextRoles(ctx.Messages), []string{"compactionSummary", "user"}) {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:212
	t.Run("follows path to specified leaf", func(t *testing.T) {
		entries := []SessionEntry{msg("1", "", "user", "start"), msg("2", "1", "assistant", "response"), msg("3", "2", "user", "branch A"), msg("4", "2", "user", "branch B")}
		requireTexts(t, BuildSessionContext(entries, new("3")), []string{"start", "response", "branch A"})
		requireTexts(t, BuildSessionContext(entries, new("4")), []string{"start", "response", "branch B"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:232
	t.Run("includes branch summary in path", func(t *testing.T) {
		entries := []SessionEntry{msg("1", "", "user", "start"), msg("2", "1", "assistant", "response"), msg("3", "2", "user", "abandoned path"), branch("4", "2", "Summary of abandoned work", "3"), msg("5", "4", "user", "new direction")}
		requireTexts(t, BuildSessionContext(entries, new("5")), []string{"start", "response", "Summary of abandoned work", "new direction"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:247
	t.Run("complex tree with multiple branches and compaction", func(t *testing.T) {
		entries := []SessionEntry{msg("1", "", "user", "start"), msg("2", "1", "assistant", "r1"), msg("3", "2", "user", "q2"), msg("4", "3", "assistant", "r2"), compact("5", "4", "Compacted history", "3"), msg("6", "5", "user", "q3"), msg("7", "6", "assistant", "r3"), msg("8", "3", "user", "wrong path"), msg("9", "8", "assistant", "wrong response"), branch("10", "3", "Tried wrong approach", "9"), msg("11", "10", "user", "better approach")}
		requireTexts(t, BuildSessionContext(entries, new("7")), []string{"Compacted history", "q2", "r2", "q3", "r3"})
		requireTexts(t, BuildSessionContext(entries, new("11")), []string{"start", "r1", "q2", "Tried wrong approach", "better approach"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:289
	t.Run("uses last entry when leafId not found", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello"), msg("2", "1", "assistant", "hi")}, new("nonexistent"))
		if len(ctx.Messages) != 2 {
			t.Fatal(ctx)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/build-context.test.ts:295
	t.Run("handles orphaned entries gracefully", func(t *testing.T) {
		ctx := BuildSessionContext([]SessionEntry{msg("1", "", "user", "hello"), msg("2", "missing", "assistant", "orphan")}, new("2"))
		if len(ctx.Messages) != 1 {
			t.Fatal(ctx)
		}
	})
}

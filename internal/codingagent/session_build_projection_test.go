// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"reflect"
	"slices"
	"testing"
)

// upstream: packages/coding-agent/src/core/session-manager.ts:543-583 (buildSessionProjection(entries, leafId?) and buildSessionContext, which returns the projection's messages, thinkingLevel and model) with the leaf rules of buildSessionPath (build-context.test.ts "handles branches" and "uses explicit leafId"): the projection follows the branch that ends at the leaf, not every entry.
func TestBuildSessionProjectionFollowsTheLeafBranch(t *testing.T) {
	msg := func(id, parent, role, text string) SessionEntry {
		return contextFixtureMessage(t, id, parent, role, text)
	}
	thinking := func(id, parent, level string) SessionEntry {
		return contextFixtureEntry(t, id, parent, "thinking_level_change", map[string]any{"thinkingLevel": level})
	}
	modelChange := func(id, parent string) SessionEntry {
		return contextFixtureEntry(t, id, parent, "model_change", map[string]any{"provider": "openai", "modelId": "gpt-4"})
	}
	// 1 -> 2 -> 3 (high thinking) -> 4, and a sibling branch 2 -> 5 (low thinking) -> 6 (model change).
	entries := []SessionEntry{
		msg("1", "", "user", "hello"),
		msg("2", "1", "assistant", "hi"),
		msg("3", "2", "user", "left question"),
		thinking("t1", "3", "high"),
		msg("4", "t1", "assistant", "left answer"),
		msg("5", "2", "user", "right question"),
		thinking("t2", "5", "low"),
		modelChange("m1", "t2"),
		msg("6", "m1", "assistant", "right answer"),
	}
	ids := func(projection SessionProjection) []string {
		out := make([]string, len(projection.Entries))
		for i, entry := range projection.Entries {
			out[i] = entry.SourceEntry.Base().ID
		}
		return out
	}
	str := func(s string) *string { return &s }
	for _, row := range []struct {
		name     string
		leaf     *string
		wantIDs  []string
		wantText []string
		thinking string
		model    *SessionContextModel
	}{
		{"default leaf is the last entry", LastLeaf(), []string{"1", "2", "5", "t2", "m1", "6"}, []string{"hello", "hi", "right question", "right answer"}, "low", &SessionContextModel{Provider: "anthropic", ModelID: "claude-test"}},
		{"explicit leaf selects its branch", str("4"), []string{"1", "2", "3", "t1", "4"}, []string{"hello", "hi", "left question", "left answer"}, "high", &SessionContextModel{Provider: "anthropic", ModelID: "claude-test"}},
		{"model change is the branch model", str("m1"), []string{"1", "2", "5", "t2", "m1"}, []string{"hello", "hi", "right question"}, "low", &SessionContextModel{Provider: "openai", ModelID: "gpt-4"}},
		{"unknown leaf falls back to the last entry", str("missing"), []string{"1", "2", "5", "t2", "m1", "6"}, []string{"hello", "hi", "right question", "right answer"}, "low", &SessionContextModel{Provider: "anthropic", ModelID: "claude-test"}},
		{"nil leaf is an empty branch", nil, []string{}, []string{}, "off", nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			projection := BuildSessionProjection(entries, row.leaf, nil)
			if got := ids(projection); !reflect.DeepEqual(got, row.wantIDs) {
				t.Errorf("entries = %v, want %v", got, row.wantIDs)
			}
			if got := contextTexts(projection.Messages); !reflect.DeepEqual(got, row.wantText) {
				t.Errorf("messages = %v, want %v", got, row.wantText)
			}
			if projection.ThinkingLevel != row.thinking || !reflect.DeepEqual(projection.Model, row.model) {
				t.Errorf("settings = %q, %+v; want %q, %+v", projection.ThinkingLevel, projection.Model, row.thinking, row.model)
			}
			context := BuildSessionContext(entries, row.leaf, nil)
			if !reflect.DeepEqual(contextTexts(context.Messages), row.wantText) {
				t.Errorf("buildSessionContext messages = %v, want the projection's %v", contextTexts(context.Messages), row.wantText)
			}
			if context.ThinkingLevel != projection.ThinkingLevel || !reflect.DeepEqual(context.Model, projection.Model) {
				t.Errorf("buildSessionContext settings differ from the projection's")
			}
		})
	}
}

// upstream: packages/coding-agent/src/core/session-manager.ts:381-388 and :543 (buildEntryIndex returns the caller's byId when one is
// passed; buildSessionProjection(entries, leafId, byId) walks that index instead of indexing entries). SessionManager passes its own
// index so the walk does not rebuild one. An index that holds a different entry under an id is what the walk follows.
func TestBuildSessionProjectionWalksTheCallersIndexInsteadOfRebuildingOne(t *testing.T) {
	msg := func(id, parent, role, text string) SessionEntry {
		return contextFixtureMessage(t, id, parent, role, text)
	}
	entries := []SessionEntry{
		msg("1", "", "user", "hello"),
		msg("2", "1", "assistant", "hi"),
		msg("3", "2", "user", "question"),
	}
	index := map[string]SessionEntry{
		"1": entries[0],
		"2": msg("2", "1", "assistant", "from the index"),
		"3": entries[2],
	}
	got := BuildSessionContext(entries, LastLeaf(), index)
	if texts := contextTexts(got.Messages); !slices.Equal(texts, []string{"hello", "from the index", "question"}) {
		t.Fatalf("messages with the caller's index = %q, want the index's entry for id 2", texts)
	}
	rebuilt := BuildSessionContext(entries, LastLeaf(), nil)
	if texts := contextTexts(rebuilt.Messages); !slices.Equal(texts, []string{"hello", "hi", "question"}) {
		t.Fatalf("messages without an index = %q, want the entries' own walk", texts)
	}
}

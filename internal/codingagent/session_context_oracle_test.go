package codingagent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// contextTrees are session entry trees (JSON entries, parent links) that exercise what buildSessionContext decides: branch walking, compaction and the entries it keeps, branch summaries, custom messages, context edits, and the model and thinking settings on the path.
func contextTrees() [][]string {
	ts := `"timestamp":"2026-01-01T00:00:00.000Z"`
	msg := func(id, parent, role, text string) string {
		switch role {
		case "assistant":
			return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,%s,"message":{"role":"assistant","content":[{"type":"text","text":%q}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}}`, id, parent, ts, text)
		case "toolResult":
			return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,%s,"message":{"role":"toolResult","toolCallId":"t","toolName":"x","content":[{"type":"text","text":%q}],"isError":false,"timestamp":1}}`, id, parent, ts, text)
		case "system":
			return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,%s,"message":{"role":"system","content":%q,"timestamp":1}}`, id, parent, ts, text)
		}
		return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,%s,"message":{"role":"user","content":%q,"timestamp":1}}`, id, parent, ts, text)
	}
	p := func(id string) string { return `"` + id + `"` }
	null := "null"
	compaction := func(id, parent, first string) string {
		return fmt.Sprintf(`{"type":"compaction","id":%q,"parentId":%s,%s,"summary":"sum","firstKeptEntryId":%q,"tokensBefore":100}`, id, parent, ts, first)
	}
	return [][]string{
		{},
		{msg("1", null, "user", "a")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b"), msg("3", p("2"), "user", "c")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b"), msg("3", p("1"), "user", "branch")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b"), msg("3", p("2"), "user", "c"), compaction("4", p("3"), "2"), msg("5", p("4"), "user", "d")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b"), msg("3", p("2"), "user", "c"), compaction("4", p("3"), "9"), msg("5", p("4"), "user", "d")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "system", "sys"), msg("3", p("2"), "user", "c"), compaction("4", p("3"), "2"), msg("5", p("4"), "user", "d")},
		{msg("1", null, "user", "a"), compaction("2", p("1"), "1"), msg("3", p("2"), "user", "b"), compaction("4", p("3"), "1"), msg("5", p("4"), "user", "c")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "user", "b"), compaction("3", p("2"), "2"), msg("4", p("3"), "user", "c"), compaction("5", p("4"), "3"), msg("6", p("5"), "user", "d")},
		{msg("1", null, "user", "a"), `{"type":"branch_summary","id":"2","parentId":"1",` + ts + `,"fromId":"1","summary":"branch sum"}`, msg("3", p("2"), "user", "b")},
		{msg("1", null, "user", "a"), `{"type":"custom_message","id":"2","parentId":"1",` + ts + `,"customType":"c","content":"hello","display":true,"details":{"k":1}}`, `{"type":"custom_message","id":"3","parentId":"2",` + ts + `,"customType":"c","content":[{"type":"text","text":"blocks"}],"display":false}`},
		{`{"type":"model_change","id":"1","parentId":null,` + ts + `,"provider":"p","modelId":"m1"}`, `{"type":"thinking_level_change","id":"2","parentId":"1",` + ts + `,"thinkingLevel":"high"}`, msg("3", p("2"), "assistant", "x"), `{"type":"model_change","id":"4","parentId":"3",` + ts + `,"provider":"q","modelId":"m2"}`, msg("5", p("4"), "user", "y")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b")},
		{`{"type":"thinking_level_change","id":"1","parentId":null,` + ts + `,"thinkingLevel":"low"}`, msg("2", p("1"), "assistant", "b"), `{"type":"thinking_level_change","id":"3","parentId":"2",` + ts + `,"thinkingLevel":"medium"}`},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b"), `{"type":"context_edit","id":"3","parentId":"2",` + ts + `,"targetId":"2","replacement":null}`, msg("4", p("3"), "user", "c")},
		{msg("1", null, "user", "a"), msg("2", p("1"), "assistant", "b"), `{"type":"context_edit","id":"3","parentId":"2",` + ts + `,"targetId":"2","replacement":{"content":"edited"}}`, `{"type":"context_edit","id":"4","parentId":"3",` + ts + `,"targetId":"1","replacement":{"content":[{"type":"text","text":"blocks"}]}}`},
		{msg("1", null, "user", "a"), msg("2", p("1"), "toolResult", "tr"), `{"type":"context_edit","id":"3","parentId":"2",` + ts + `,"targetId":"2","replacement":{"content":"edited tool"}}`},
		{msg("1", null, "user", "a"), compaction("2", p("1"), "1"), `{"type":"context_edit","id":"3","parentId":"2",` + ts + `,"targetId":"2","replacement":null}`},
		{msg("1", null, "user", "a"), `{"type":"custom","id":"2","parentId":"1",` + ts + `,"customType":"x","data":1}`, `{"type":"label","id":"3","parentId":"2",` + ts + `,"targetId":"1","label":"l"}`, `{"type":"session_info","id":"4","parentId":"3",` + ts + `,"name":"n"}`, msg("5", p("4"), "user", "b")},
		{msg("1", p("9"), "user", "orphan"), msg("2", p("1"), "user", "child")},
	}
}

// TestBuildSessionContextMatchesPi runs buildSessionContext of Pi 1.0.4 and BuildSessionContext over the same entry trees and leaves (the last entry, null, each id, an unknown id).
func TestBuildSessionContextMatchesPi(t *testing.T) {
	trees := contextTrees()
	type request struct {
		Tree    int      `json:"tree"`
		Entries []string `json:"-"`
		Leaf    *string  `json:"leaf"`
		Default bool     `json:"default"`
	}
	var requests []request
	for i, tree := range trees {
		requests = append(requests, request{Tree: i, Default: true}, request{Tree: i, Leaf: nil})
		for _, id := range []string{"1", "2", "3", "4", "5", "6", "nope"} {
			requests = append(requests, request{Tree: i, Leaf: &id})
		}
		_ = tree
	}
	wire := make([]map[string]any, 0, len(trees))
	for _, tree := range trees {
		entries := make([]json.RawMessage, len(tree))
		for i, e := range tree {
			entries[i] = json.RawMessage(e)
		}
		wire = append(wire, map[string]any{"entries": entries})
	}
	var want []map[string]any
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
emit(input.requests.map((r) => {
	const entries = input.trees[r.tree].entries;
	try {
		const ctx = r.default ? mod.buildSessionContext(entries) : mod.buildSessionContext(entries, r.leaf);
		return { ctx };
	} catch (e) { return { error: String(e.message) }; }
}));`, map[string]any{"trees": wire, "requests": requests}, &want)
	failures := 0
	for n, r := range requests {
		var entries []SessionEntry
		for _, raw := range trees[r.Tree] {
			var base SessionEntryBase
			if err := json.Unmarshal([]byte(raw), &base); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, sessionentry.DecodeSessionEntry(json.RawMessage(raw)))
		}
		var ctx SessionContext
		if r.Default {
			ctx = BuildSessionContext(entries, LastLeaf(), nil)
		} else {
			ctx = BuildSessionContext(entries, r.Leaf, nil)
		}
		encoded, err := json.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		w := want[n]
		if w["ctx"] == nil {
			t.Errorf("tree %d leaf %v: Pi threw %v", r.Tree, ptrString(r.Leaf), w["error"])
			continue
		}
		if !reflect.DeepEqual(got, w["ctx"]) {
			g, _ := json.Marshal(got)
			x, _ := json.Marshal(w["ctx"])
			if failures++; failures > 4 {
				continue
			}
			t.Errorf("tree %d default=%v leaf %v:\n  Pig %s\n  Pi  %s", r.Tree, r.Default, ptrString(r.Leaf), strings.TrimSpace(string(g)), strings.TrimSpace(string(x)))
		}
	}
}

func ptrString(s *string) string {
	if s == nil {
		return "nil"
	}
	return *s
}

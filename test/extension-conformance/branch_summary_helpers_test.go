package extensionconformance

import (
	"context"
	"encoding/json"
	"maps"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type branchCase struct {
	name  string
	input map[string]any
	// want is a member of Pi's report the case pins, so a mistake every SDK shares cannot pass: "totalTokens" (a number) or "roles" (the message roles in order).
	wantTokens *float64
	wantRoles  []any
	wantIDs    []any
}

// TestConformance_BranchSummaryHelpersAgreeAcrossSDKs: Pi exports prepareBranchEntries and collectEntriesForBranchSummary (core/compaction/branch-summarization.ts,
// index.ts). The Node SDK runs Pi's own code, so its report is the reference: every case below runs in each SDK and must equal it, timestamps included (they
// go through the host's Date.parse in the other SDKs).
func TestConformance_BranchSummaryHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	cases := branchCases()
	run := func(t *testing.T, h *harness, c map[string]any) map[string]any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "branch_probe", string(args)) {
			t.Fatal("fixture has no branch_probe command")
		}
		var reported string
		pollUntilConformance(t, "branch_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "branch:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(markLoneSurrogates(reported)), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got
	}

	var reference []map[string]any
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		for _, c := range cases {
			reference = append(reference, run(t, h, c.input))
		}
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) != len(cases) {
		t.Fatalf("the Node reference ran %d of %d cases", len(reference), len(cases))
	}
	for i, c := range cases {
		if c.wantTokens != nil && reference[i]["totalTokens"] != *c.wantTokens {
			t.Fatalf("reference for %s: totalTokens = %v, want %v", c.name, reference[i]["totalTokens"], *c.wantTokens)
		}
		if c.wantRoles != nil {
			roles := []any{}
			for _, m := range reference[i]["messages"].([]any) {
				roles = append(roles, m.(map[string]any)["role"])
			}
			if !reflect.DeepEqual(roles, c.wantRoles) {
				t.Fatalf("reference for %s: roles = %v, want %v", c.name, roles, c.wantRoles)
			}
		}
		if c.wantIDs != nil {
			ids := []any{}
			for _, e := range reference[i]["entries"].([]any) {
				ids = append(ids, e.(map[string]any)["id"])
			}
			if !reflect.DeepEqual(ids, c.wantIDs) {
				t.Fatalf("reference for %s: entry ids = %v, want %v", c.name, ids, c.wantIDs)
			}
		}
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for i, c := range cases {
				if got := run(t, h, c.input); !reflect.DeepEqual(got, reference[i]) {
					t.Errorf("%s: %.400v, Node (Pi) reports %.400v", c.name, got, reference[i])
				}
			}
		})
	}
}

func branchCases() []branchCase {
	type m = map[string]any
	entry := func(id, parent, kind string, members m) m {
		e := m{"id": id, "type": kind, "timestamp": "2026-03-04T05:06:07.890Z"}
		if parent != "" {
			e["parentId"] = parent
		} else {
			e["parentId"] = nil
		}
		maps.Copy(e, members)
		return e
	}
	user := func(id, parent string, text any) m {
		return entry(id, parent, "message", m{"message": m{"role": "user", "content": text, "timestamp": 1}})
	}
	call := func(name string, arguments any) m {
		return m{"type": "toolCall", "id": "c", "name": name, "arguments": arguments}
	}
	assistant := func(id, parent string, blocks ...any) m {
		return entry(id, parent, "message", m{"message": m{"role": "assistant", "content": blocks, "timestamp": 2}})
	}
	prepare := func(name string, budget int, entries ...m) branchCase {
		list := make([]any, len(entries))
		for i, e := range entries {
			list[i] = e
		}
		return branchCase{name: name, input: m{"fn": "prepare", "entries": list, "budget": budget}}
	}
	// lone cases hold an unpaired surrogate, which only a JavaScript string can hold: the SDKs read their entries as JSON text, and the report marks each one.
	lone := func(name string, budget int, entries ...m) branchCase {
		c := prepare(name, budget, entries...)
		c.input["lone"] = true
		return c
	}
	collect := func(name string, old any, target string, entries ...m) branchCase {
		list := make([]any, len(entries))
		for i, e := range entries {
			list[i] = e
		}
		return branchCase{name: name, input: m{"fn": "collect", "entries": list, "old": old, "target": target}}
	}
	text := func(s string) m { return m{"type": "text", "text": s} }
	tokens := func(n float64) *float64 { return &n }

	every := []m{
		user("u1", "", "hello there"),
		assistant("a1", "u1", text("reading"), call("read", m{"path": "a.go"}), call("read", m{"path": "b.go"}), call("write", m{"path": "w.go", "content": "x"}), call("edit", m{"path": "a.go"})),
		entry("t1", "a1", "message", m{"message": m{"role": "toolResult", "toolCallId": "c", "toolName": "read", "content": []any{text(strings.Repeat("r", 400))}, "isError": false, "timestamp": 3}}),
		entry("cm1", "t1", "custom_message", m{"customType": "note", "content": "custom text", "display": true, "details": m{"k": []any{1, 2}}}),
		entry("cm2", "cm1", "custom_message", m{"customType": "note2", "content": []any{text("blocks"), m{"type": "image", "data": "x", "mimeType": "image/png"}}, "display": false}),
		entry("bs1", "cm2", "branch_summary", m{"summary": "earlier branch", "fromId": "zz", "details": m{"readFiles": []any{"r1.go", "a.go"}, "modifiedFiles": []any{"m1.go", "w.go"}}}),
		entry("bs2", "bs1", "branch_summary", m{"summary": "by an extension", "fromId": "zz", "fromHook": true, "details": m{"readFiles": []any{"hook.go"}, "modifiedFiles": []any{"hookm.go"}}}),
		entry("co1", "bs2", "compaction", m{"summary": "compacted", "firstKeptEntryId": "a1", "tokensBefore": 1234}),
		entry("x1", "co1", "thinking_level_change", m{"thinkingLevel": "high"}),
		entry("x2", "x1", "model_change", m{"provider": "p", "modelId": "m"}),
		entry("x3", "x2", "custom", m{"customType": "c", "data": 1}),
		entry("x4", "x3", "label", m{"targetId": "u1", "label": "l"}),
		entry("x5", "x4", "session_info", m{"name": "n"}),
		assistant("a2", "x5", text("done")),
	}
	cases := []branchCase{
		prepare("every entry type, no budget", 0, every...),
		prepare("every entry type, a budget the newest entries fill", 40, every...),
		// The newest message is 36 characters (9 tokens) and the summary before it 40 (10): a budget of 11 stops at the summary, which still fits because 9 is
		// under 90% of 11 (9.9), and a budget of 10 does not, because 9 is not under 9.
		prepare("a summary the budget stops at still fits under 90%", 11, entry("k", "", "compaction", m{"summary": strings.Repeat("s", 40), "tokensBefore": 1}), user("u", "k", strings.Repeat("w", 36))),
		prepare("a summary the budget stops at is dropped at 90% or more", 10, entry("k", "", "compaction", m{"summary": strings.Repeat("s", 40), "tokensBefore": 1}), user("u", "k", strings.Repeat("w", 36))),
		prepare("a message the budget stops at is dropped", 10, user("u0", "", strings.Repeat("w", 40)), user("u", "u0", strings.Repeat("w", 36))),
		prepare("a budget smaller than the newest message", 1, every...),
		prepare("empty", 0),
		lone("an unpaired surrogate in a user message", 0, user("u", "", "x"), entry("m", "u", "message", m{"message": m{"role": "user", "content": json.RawMessage(`"a\ud800b\udc00c\ud83d\ude00\ude00\ud83d"`), "timestamp": 1}})),
		lone("unpaired surrogates in tool call paths are different files, and none is U+FFFD", 0, assistant("a", "", call("read", m{"path": json.RawMessage(`"f\ud83d.go"`)}), call("read", m{"path": json.RawMessage(`"f\ud83d.go"`)}), call("read", m{"path": json.RawMessage(`"f\ude00.go"`)}), call("read", m{"path": "f\ufffd.go"}), call("edit", m{"path": json.RawMessage(`"\udc00"`)}))),
		lone("unpaired surrogates in recorded files and a custom message", 0,
			entry("b", "", "branch_summary", m{"summary": json.RawMessage(`"s\udfff"`), "fromId": "f", "details": m{"readFiles": []any{json.RawMessage(`"\ud800"`), json.RawMessage(`"\ud800"`), json.RawMessage(`"\udc00"`), "\ufffd", "\ue000"}, "modifiedFiles": []any{json.RawMessage(`"m\ud800"`)}}}),
			entry("c", "b", "custom_message", m{"customType": "n", "content": []any{m{"type": "text", "text": json.RawMessage(`"t\udc01"`)}}, "display": true, "details": m{"k": json.RawMessage(`"\ud801"`)}})),
		lone("an unpaired surrogate beside a private-use character and a budget", 4, user("u", "", json.RawMessage(`"\ue000\ud800\ue001\udc00"`)), user("v", "u", "x")),
		prepare("only entries without content", 0, every[8:13]...),
		prepare("a tool result adds nothing, even with nested calls", 0, entry("t", "", "message", m{"message": m{"role": "toolResult", "content": []any{text("x")}, "nestedCalls": m{"calls": []any{m{"name": "read", "arguments": m{"path": "n1.go"}}, m{"name": "edit", "arguments": m{"path": "n2.go"}}, m{"name": "bash", "arguments": m{"path": "n3.go"}}, m{"name": "write", "arguments": "n4"}}}}})),
		prepare("tool calls with odd arguments", 0, assistant("a", "", call("read", m{"path": ""}), call("read", m{"path": 5}), call("read", "path"), call("read", []any{"path"}), call("grep", m{"path": "g.go"}), call("read", m{"path": "dup.go"}), call("read", m{"path": "dup.go"}))),
		prepare("recorded files that are not strings", 0, entry("b", "", "branch_summary", m{"summary": "s", "fromId": "f", "details": m{"readFiles": []any{1, 1.0, true, true, nil, nil, "1", m{"a": 1}, m{"a": 1}, []any{1}}, "modifiedFiles": []any{2, "2"}}})),
		prepare("recorded details that are not lists", 0, entry("b", "", "branch_summary", m{"summary": "s", "fromId": "f", "details": m{"readFiles": "x", "modifiedFiles": 5}}), entry("c", "b", "branch_summary", m{"summary": "s", "fromId": "f", "details": "str"}), entry("d", "c", "branch_summary", m{"summary": "s", "fromId": "f", "details": 0})),
		prepare("timestamps", 0,
			entry("t1", "", "compaction", m{"summary": "s", "tokensBefore": 1, "timestamp": "2020-01-02T03:04:05.678Z"}),
			entry("t2", "t1", "compaction", m{"summary": "s", "tokensBefore": 2, "timestamp": "2020-01-02"}),
			entry("t3", "t2", "compaction", m{"summary": "s", "tokensBefore": 3, "timestamp": "Tue, 05 Oct 2021 10:00:00 GMT"}),
			entry("t4", "t3", "compaction", m{"summary": "s", "tokensBefore": 4, "timestamp": "2021-10-05T10:00:00+05:30"}),
			entry("t5", "t4", "compaction", m{"summary": "s", "tokensBefore": 5, "timestamp": "not a date"}),
			entry("t6", "t5", "compaction", m{"summary": "s", "tokensBefore": 6, "timestamp": 1700000000000.5}),
			entry("t7", "t6", "compaction", m{"summary": "s", "tokensBefore": 7, "timestamp": 9e15}),
			entry("t8", "t7", "compaction", m{"summary": "s", "tokensBefore": 8, "timestamp": nil}),
			entry("t9", "t8", "compaction", m{"summary": "s", "tokensBefore": 9, "timestamp": true}),
			entry("t10", "t9", "compaction", m{"summary": "s", "tokensBefore": 10, "timestamp": -5}),
			entry("t11", "t10", "compaction", m{"summary": "s", "tokensBefore": 11, "timestamp": "2020-02-30T00:00:00Z"})),
		prepare("optional members of a created message that are absent", 0, m{"id": "c1", "type": "custom_message", "content": "c", "timestamp": "2020-01-01"}, m{"id": "c2", "type": "branch_summary", "summary": "s"}, m{"id": "c3", "type": "compaction", "summary": "s", "timestamp": "2020-01-01T00:00:00Z"}),
		prepare("a message entry with no message", 0, m{"id": "e", "type": "message"}),
		prepare("a message entry whose message is null", 0, m{"id": "e", "type": "message", "message": nil}),
		prepare("an unknown entry type", 0, m{"id": "e", "type": "something_new"}, user("u", "e", "x")),
		prepare("a very long message", 100, user("u", "", strings.Repeat("é", 5000)), user("v", "u", "short")),
	}
	cases[2].wantRoles, cases[2].wantTokens = []any{"compactionSummary", "user"}, tokens(19)
	cases[3].wantRoles, cases[3].wantTokens = []any{"user"}, tokens(9)
	cases[4].wantRoles, cases[4].wantTokens = []any{"user"}, tokens(9)
	cases[6].wantTokens = tokens(0)

	tree := []m{
		entry("r", "", "message", m{"message": m{"role": "user", "content": "root", "timestamp": 1}}),
		user("a1", "r", "a1"), user("a2", "a1", "a2"), user("a3", "a2", "a3"),
		user("b1", "a1", "b1"), entry("b2", "b1", "compaction", m{"summary": "s", "tokensBefore": 1}), user("b3", "b2", "b3"),
		user("c1", "a3", "c1"),
		user("s1", "", "second root"), user("s2", "s1", "s2"),
	}
	cc := []branchCase{
		collect("a sibling branch", "b3", "c1", tree...),
		collect("the target is an ancestor of the old leaf", "b3", "a1", tree...),
		collect("the old leaf is an ancestor of the target", "a1", "c1", tree...),
		collect("the old leaf is the target", "b3", "b3", tree...),
		collect("no common ancestor", "b3", "s2", tree...),
		collect("no old position", nil, "c1", tree...),
		collect("an empty old position", "", "c1", tree...),
		collect("an unknown old leaf", "nope", "c1", tree...),
		collect("an unknown target", "b3", "nope", tree...),
		collect("a compaction is not a boundary", "b3", "r", tree...),
		collect("an orphan's parent is missing", "o2", "c1", append([]m{user("o2", "gone", "o2")}, tree...)...),
	}
	cc[0].wantIDs = []any{"b1", "b2", "b3"}
	cc[1].wantIDs = []any{"b1", "b2", "b3"}
	cc[2].wantIDs = []any{}
	cc[4].wantIDs = []any{"r", "a1", "b1", "b2", "b3"}
	cc[5].wantIDs = []any{}
	cases = append(cases, cc...)

	random := rand.New(rand.NewPCG(11, 13))
	paths := []string{"a.go", "b.go", "c/d.go", "é.go", "", "x y"}
	kinds := []string{"message", "message", "message", "custom_message", "branch_summary", "compaction", "label", "thinking_level_change"}
	for i := range 120 {
		var entries []m
		parent := ""
		for j := range 1 + random.IntN(8) {
			id := "e" + strconv.Itoa(j)
			var e m
			switch kind := kinds[random.IntN(len(kinds))]; kind {
			case "message":
				switch random.IntN(3) {
				case 0:
					e = user(id, parent, strings.Repeat("w", random.IntN(300)))
				case 1:
					blocks := []any{}
					for range random.IntN(4) {
						if random.IntN(2) == 0 {
							blocks = append(blocks, text(strings.Repeat("t", random.IntN(200))))
						} else {
							blocks = append(blocks, call([]string{"read", "write", "edit", "bash"}[random.IntN(4)], m{"path": paths[random.IntN(len(paths))]}))
						}
					}
					e = assistant(id, parent, blocks...)
				default:
					e = entry(id, parent, "message", m{"message": m{"role": "toolResult", "content": []any{text("r")}}})
				}
			case "custom_message":
				e = entry(id, parent, kind, m{"customType": "n", "content": strings.Repeat("c", random.IntN(200)), "display": random.IntN(2) == 0})
			case "branch_summary":
				e = entry(id, parent, kind, m{"summary": strings.Repeat("s", random.IntN(300)), "fromId": "f", "fromHook": random.IntN(3) == 0, "details": m{"readFiles": []any{paths[random.IntN(len(paths))]}, "modifiedFiles": []any{paths[random.IntN(len(paths))]}}})
			case "compaction":
				e = entry(id, parent, kind, m{"summary": strings.Repeat("k", random.IntN(300)), "tokensBefore": random.IntN(1000)})
			default:
				e = entry(id, parent, kind, m{"label": "l"})
			}
			entries = append(entries, e)
			parent = id
		}
		cases = append(cases, prepare("random "+strconv.Itoa(i), []int{0, 0, 10, 50, 100, 400}[random.IntN(6)], entries...))
	}
	return cases
}

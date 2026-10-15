package extensionconformance

import (
	"context"
	"encoding/json"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// TestConformance_CompactionUsageHelpersAgreeAcrossSDKs: Pi exports calculateContextTokens, shouldCompact, getLastAssistantUsage and
// DEFAULT_COMPACTION_SETTINGS (coding-agent index.ts, core/compaction/compaction.ts). The Node SDK runs Pi's own code, so its answer is the reference: every case runs in each
// SDK and the result must equal the Node result, which this test also pins to values Pi's source implies.
func TestConformance_CompactionUsageHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	type tcase = map[string]any
	usage := func(input, output, cacheRead, cacheWrite, total int) tcase {
		return tcase{"input": input, "output": output, "cacheRead": cacheRead, "cacheWrite": cacheWrite, "totalTokens": total, "cost": tcase{"total": 0.5}}
	}
	assistant := func(stop string, u any) tcase {
		message := tcase{"role": "assistant", "content": []any{}, "stopReason": stop}
		if u != nil {
			message["usage"] = u
		}
		return tcase{"type": "message", "message": message}
	}
	user := tcase{"type": "message", "message": tcase{"role": "user", "content": "hi"}}
	other := tcase{"type": "compaction", "summary": "s"}
	cases := []tcase{
		{"fn": "defaults"},
		{"fn": "calc", "usage": usage(1, 2, 3, 4, 100)},
		{"fn": "calc", "usage": usage(1, 2, 3, 4, 0)},
		{"fn": "calc", "usage": usage(0, 0, 0, 0, 0)},
		{"fn": "calc", "usage": usage(0, 0, 0, 0, 7)},
		{"fn": "calc", "usage": usage(50000, 0, 120000, 0, 0)},
		{"fn": "should", "tokens": 83616, "window": 100000, "settings": tcase{"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 20000}},
		{"fn": "should", "tokens": 83617, "window": 100000, "settings": tcase{"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 20000}},
		{"fn": "should", "tokens": 83617, "window": 100000, "settings": tcase{"enabled": false, "reserveTokens": 16384, "keepRecentTokens": 20000}},
		{"fn": "should", "tokens": 0, "window": 100, "settings": tcase{"enabled": true, "reserveTokens": 0, "keepRecentTokens": 1}},
		{"fn": "should", "tokens": 1, "window": 100, "settings": tcase{"enabled": true, "reserveTokens": 100, "keepRecentTokens": 1}},
		{"fn": "should", "tokens": 0, "window": 100, "settings": tcase{"enabled": true, "reserveTokens": 200, "keepRecentTokens": 1}},
		{"fn": "should", "tokens": -1, "window": 100, "settings": tcase{"enabled": true, "reserveTokens": 200, "keepRecentTokens": 1}},
		{"fn": "last", "entries": []any{}},
		{"fn": "last", "entries": []any{user, other}},
		{"fn": "last", "entries": []any{user, assistant("stop", usage(1, 2, 3, 4, 10))}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), user}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), assistant("aborted", usage(9, 9, 9, 9, 36))}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), assistant("error", usage(9, 9, 9, 9, 36))}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), assistant("toolUse", usage(0, 0, 0, 0, 0))}},
		{"fn": "last", "entries": []any{assistant("length", usage(5, 6, 0, 0, 0)), other}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 1, 1, 1, 4)), assistant("stop", nil)}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 1, 1, 1, 4)), assistant("stop", nil), other, user}},
		{"fn": "last", "entries": []any{assistant("stop", usage(0, 0, 0, 0, 0))}},
		// Only an assistant message carries a valid usage: a user or tool result message that holds one is skipped.
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), tcase{"type": "message", "message": tcase{"role": "user", "content": "x", "usage": usage(9, 9, 9, 9, 36)}}}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), tcase{"type": "message", "message": tcase{"role": "toolResult", "usage": usage(9, 9, 9, 9, 36)}}}},
		{"fn": "last", "entries": []any{assistant("stop", usage(1, 2, 3, 4, 10)), tcase{"type": "custom", "message": tcase{"role": "assistant", "usage": usage(9, 9, 9, 9, 36)}}}},
		{"fn": "last", "entries": []any{assistant("stop", usage(2, 0, 0, 0, 0)), assistant("stop", tcase{"input": 5})}},
		{"fn": "last", "entries": []any{assistant("stop", usage(2, 0, 0, 0, 0)), tcase{"type": "message", "message": tcase{"role": "assistant", "usage": nil, "stopReason": "stop"}}}},
		// JavaScript's + reads a null token count as 0 and a missing one as NaN: a usage with a null component still counts, one with a missing component does not.
		{"fn": "last", "entries": []any{assistant("stop", usage(2, 0, 0, 0, 0)), assistant("stop", tcase{"input": 10, "output": 5, "cacheRead": nil, "cacheWrite": 0, "totalTokens": 0})}},
		{"fn": "last", "entries": []any{assistant("stop", usage(2, 0, 0, 0, 0)), assistant("stop", tcase{"input": nil, "output": nil, "cacheRead": nil, "cacheWrite": nil, "totalTokens": nil})}},
		{"fn": "last", "entries": []any{assistant("stop", usage(2, 0, 0, 0, 0)), assistant("stop", tcase{"input": 10, "output": 5, "cacheWrite": 0, "totalTokens": 0})}},
	}
	rng := rand.New(rand.NewPCG(3, 4))
	stops := []string{"stop", "length", "toolUse", "aborted", "error"}
	for range 60 {
		entries := make([]any, rng.IntN(7))
		for i := range entries {
			switch rng.IntN(4) {
			case 0:
				entries[i] = user
			case 1:
				entries[i] = other
			default:
				entries[i] = assistant(stops[rng.IntN(len(stops))], usage(rng.IntN(3), rng.IntN(3), rng.IntN(3), rng.IntN(3), rng.IntN(3)*rng.IntN(4)))
			}
		}
		cases = append(cases, tcase{"fn": "last", "entries": entries},
			tcase{"fn": "calc", "usage": usage(rng.IntN(1000), rng.IntN(1000), rng.IntN(1000), rng.IntN(1000), rng.IntN(2)*rng.IntN(5000))},
			tcase{"fn": "should", "tokens": rng.IntN(300) - 50, "window": rng.IntN(300), "settings": tcase{"enabled": rng.IntN(5) != 0, "reserveTokens": rng.IntN(150), "keepRecentTokens": 1}})
	}

	// estimateTokens: messages of every role Pi sizes, with strings that separate UTF-16 units from code points and bytes, escapes that change a JSON.stringify
	// length, and arguments whose numbers print differently from Go's, Rust's and Python's own formats.
	numbers := []any{0, 1, -1, 1.5, 0.1, 100, 1e21, 1.5e21, 1e-6, 1e-7, 1.5e-7, 123456789012345680000.0, 4294967296.5, math.Copysign(0, -1), 12345.678, 1e20, 5e-324, 9007199254740993, 1e300}
	pieces := []string{"a", "é", "日", "😀", "𝒜", "\"", "\\", "\n", "\t", "\r", "\b", "\f", "\u0001", "\u001f", "\u007f", "\u2028", "\u2029", "\ufeff", "/", " ", "x"}
	randomString := func() string {
		var b strings.Builder
		for range rng.IntN(12) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		return b.String()
	}
	var randomValue func(depth int) any
	randomValue = func(depth int) any {
		switch k := rng.IntN(8); {
		case k == 0:
			return nil
		case k == 1:
			return rng.IntN(2) == 0
		case k == 2 || k == 3:
			return numbers[rng.IntN(len(numbers))]
		case k == 4 || k == 5 || depth > 2:
			return randomString()
		case k == 6:
			array := make([]any, rng.IntN(4))
			for i := range array {
				array[i] = randomValue(depth + 1)
			}
			return array
		default:
			object := tcase{}
			for range rng.IntN(4) {
				object[randomString()] = randomValue(depth + 1)
			}
			return object
		}
	}
	randomBlocks := func() []any {
		blocks := make([]any, rng.IntN(5))
		for i := range blocks {
			switch rng.IntN(5) {
			case 0:
				blocks[i] = tcase{"type": "text", "text": randomString()}
			case 1:
				blocks[i] = tcase{"type": "thinking", "thinking": randomString()}
			case 2:
				blocks[i] = tcase{"type": "image", "data": "AAAA", "mimeType": "image/png"}
			default:
				blocks[i] = tcase{"type": "toolCall", "id": "c1", "name": randomString(), "arguments": tcase{"a": randomValue(0), randomString(): randomValue(0)}}
			}
		}
		return blocks
	}
	messages := []tcase{
		{"role": "user", "content": ""}, {"role": "user", "content": "abcde"}, {"role": "user", "content": "😀😀"},
		{"role": "user", "content": []any{tcase{"type": "image"}, tcase{"type": "text", "text": "abcd"}, tcase{"type": "text", "text": ""}}},
		{"role": "assistant", "content": []any{}}, {"role": "assistant", "content": []any{tcase{"type": "toolCall", "name": "read", "arguments": tcase{}}}},
		{"role": "toolResult", "content": []any{tcase{"type": "text", "text": "ok"}, tcase{"type": "image"}}}, {"role": "custom", "content": "custom text"},
		{"role": "bashExecution", "command": "ls", "output": "é日😀"}, {"role": "branchSummary", "summary": "sum😀"}, {"role": "compactionSummary", "summary": ""},
		{"role": "system", "content": "sys", "sections": tcase{"a": "12345", "b": ""}, "toolsAdded": []any{"read", tcase{"x": 1.5}}},
		{"role": "system", "content": []any{tcase{"type": "text", "text": "sys"}}, "toolsAdded": []any{}}, {"role": "system", "content": "s"},
	}
	for range 80 {
		messages = append(messages,
			tcase{"role": "assistant", "content": randomBlocks()}, tcase{"role": "user", "content": randomBlocks()}, tcase{"role": "toolResult", "content": randomBlocks()},
			tcase{"role": "bashExecution", "command": randomString(), "output": randomString()}, tcase{"role": "branchSummary", "summary": randomString()},
			tcase{"role": "system", "content": randomString(), "sections": tcase{"a": randomString()}, "toolsAdded": randomValue(1)})
	}
	for _, message := range messages {
		cases = append(cases, tcase{"fn": "estimate", "message": message})
	}

	// findCutPoint and findTurnStartIndex: entries of every kind the session holds, with sizes around the keep budget, so a boundary (>= versus >), the
	// cut that moves back over metadata entries, a split turn and a compaction boundary each show against Pi's own code.
	entryMessage := func(role string, text string) tcase {
		switch role {
		case "assistant":
			return tcase{"type": "message", "message": tcase{"role": "assistant", "content": []any{tcase{"type": "text", "text": text}}, "stopReason": "stop"}}
		case "toolResult":
			return tcase{"type": "message", "message": tcase{"role": "toolResult", "content": []any{tcase{"type": "text", "text": text}}}}
		case "bash":
			return tcase{"type": "message", "message": tcase{"role": "bashExecution", "command": text, "output": ""}}
		case "nocontent":
			return tcase{"type": "message", "message": tcase{"role": "user"}}
		}
		return tcase{"type": "message", "message": tcase{"role": "user", "content": text}}
	}
	metadata := []tcase{{"type": "model_change", "provider": "p", "modelId": "m"}, {"type": "thinking_level_change", "thinkingLevel": "high"}, {"type": "label", "targetId": "x", "label": "l"}, {"type": "session_info", "name": "n"}}
	randomEntry := func() tcase {
		text := strings.Repeat("x", rng.IntN(60))
		switch rng.IntN(11) {
		case 0, 1:
			return entryMessage("user", text)
		case 2, 3:
			return entryMessage("assistant", text)
		case 4:
			return entryMessage("toolResult", text)
		case 5:
			return entryMessage("bash", text)
		case 6:
			return metadata[rng.IntN(len(metadata))]
		case 7:
			return tcase{"type": "custom_message", "customType": "c", "content": text, "display": true}
		case 8:
			return tcase{"type": "branch_summary", "fromId": "a", "summary": text}
		case 9:
			return tcase{"type": "compaction", "summary": text, "firstKeptEntryId": "a", "tokensBefore": 10}
		}
		return entryMessage("nocontent", "")
	}
	fixedEntries := []any{
		entryMessage("user", strings.Repeat("a", 400)), entryMessage("assistant", strings.Repeat("b", 400)), entryMessage("toolResult", strings.Repeat("c", 400)),
		metadata[0], metadata[1], entryMessage("user", strings.Repeat("d", 400)), entryMessage("assistant", strings.Repeat("e", 400)),
	}
	for _, keep := range []int{0, 1, 100, 101, 199, 200, 201, 300, 301, 400, 10000} {
		cases = append(cases, tcase{"fn": "cut", "entries": fixedEntries, "start": 0, "end": len(fixedEntries), "keep": keep})
	}
	cases = append(cases,
		tcase{"fn": "cut", "entries": []any{}, "start": 0, "end": 0, "keep": 10},
		tcase{"fn": "cut", "entries": fixedEntries, "start": 2, "end": 5, "keep": 100},
		tcase{"fn": "cut", "entries": fixedEntries, "start": 3, "end": 5, "keep": 1},
		// Trailing metadata entries weigh nothing: a zero budget keeps walking to the last weighty entry instead of stopping at the end.
		tcase{"fn": "cut", "entries": fixedEntries, "start": 0, "end": 5, "keep": 0}, tcase{"fn": "cut", "entries": fixedEntries, "start": 0, "end": 4, "keep": 1},
		// An empty user message is a cut point that weighs nothing: with a zero budget the walk skips it and cuts at the weighty entry before.
		tcase{"fn": "cut", "entries": []any{entryMessage("user", strings.Repeat("a", 400)), entryMessage("assistant", strings.Repeat("b", 400)), entryMessage("user", "")}, "start": 0, "end": 3, "keep": 0},
		// Trailing tool results exceed the budget alone: no cut point lies at or after them, so the last one (their assistant call) is kept, not the first message.
		tcase{"fn": "cut", "entries": []any{entryMessage("user", "hi"), entryMessage("assistant", "call"), entryMessage("toolResult", strings.Repeat("r", 800))}, "start": 0, "end": 3, "keep": 100},
		tcase{"fn": "cut", "entries": []any{entryMessage("user", "hi"), entryMessage("assistant", "call"), entryMessage("toolResult", strings.Repeat("r", 800)), entryMessage("toolResult", strings.Repeat("r", 800))}, "start": 0, "end": 4, "keep": 100},
		tcase{"fn": "turn", "entries": fixedEntries, "index": 6, "start": 0}, tcase{"fn": "turn", "entries": fixedEntries, "index": 4, "start": 1},
		tcase{"fn": "turn", "entries": fixedEntries, "index": 2, "start": 2}, tcase{"fn": "turn", "entries": fixedEntries, "index": 0, "start": 0})
	// A message that is not an object is one role-less context message: it is no cut point and weighs nothing, but the cut does not move back over it as it
	// does over a metadata entry. (Pi throws on a null message.)
	for _, odd := range []any{5, "text", []any{1, 2}, true} {
		entries := []any{entryMessage("user", strings.Repeat("a", 400)), entryMessage("assistant", strings.Repeat("b", 400)), tcase{"type": "message", "message": odd},
			entryMessage("user", strings.Repeat("c", 400)), entryMessage("assistant", strings.Repeat("d", 400))}
		cases = append(cases, tcase{"fn": "cut", "entries": entries, "start": 0, "end": len(entries), "keep": 150}, tcase{"fn": "turn", "entries": entries, "index": 2, "start": 0})
	}
	for range 150 {
		entries := make([]any, 1+rng.IntN(12))
		for i := range entries {
			entries[i] = randomEntry()
		}
		start := rng.IntN(len(entries))
		end := start + rng.IntN(len(entries)-start+1)
		cases = append(cases, tcase{"fn": "cut", "entries": entries, "start": start, "end": end, "keep": rng.IntN(250)},
			tcase{"fn": "turn", "entries": entries, "index": rng.IntN(len(entries)), "start": start})
	}

	run := func(t *testing.T, h *harness, c tcase) map[string]any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "compact_usage_probe", string(args)) {
			t.Fatal("fixture has no compact_usage_probe command")
		}
		var reported string
		pollUntilConformance(t, "compact_usage_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "compact:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
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
			reference = append(reference, run(t, h, c))
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
	// Values Pi's source implies, so a mistake shared by every SDK cannot pass.
	pinned := map[int]any{
		0: tcase{"enabled": true, "reserveTokens": 16384.0, "keepRecentTokens": 20000.0},
		1: tcase{"tokens": 100.0}, 2: tcase{"tokens": 10.0}, 3: tcase{"tokens": 0.0}, 4: tcase{"tokens": 7.0}, 5: tcase{"tokens": 170000.0},
		6: tcase{"result": false}, 7: tcase{"result": true}, 8: tcase{"result": false},
	}
	for i, want := range pinned {
		if !reflect.DeepEqual(any(map[string]any(reference[i])), want) && !reflect.DeepEqual(reference[i], map[string]any(want.(tcase))) {
			t.Fatalf("reference case %d = %v, want %v", i, reference[i], want)
		}
	}
	if got := reference[17]["usage"]; got == nil {
		t.Fatal("reference getLastAssistantUsage skipped an assistant that stopped normally")
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
				got := run(t, h, c)
				if !reflect.DeepEqual(got, reference[i]) {
					args, _ := json.Marshal(c)
					t.Errorf("case %d %.120s: got %v, Node (Pi) reports %v", i, args, got, reference[i])
				}
			}
		})
	}
}

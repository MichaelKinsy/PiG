package extensionconformance

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// TestConformance_MessageConversionAgreesAcrossSDKs: Pi exports convertToLlm (core/messages.ts:148, index.ts) and formatDimensionNote
// (utils/image-resize.ts:115, index.ts). The Node SDK runs Pi's own code, so its answer is the reference: every case below runs in each SDK and must equal it.
// The corpus covers every role, JavaScript's truthiness and ToString of each JSON type in the template-literal fields, absent against null members,
// and toFixed(2) ties (0.125 is "0.13" in JavaScript; half-even formatting gives "0.12").
func TestConformance_MessageConversionAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	cases := messageConversionCases()
	run := func(t *testing.T, h *harness, c map[string]any) any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "convert_probe", string(args)) {
			t.Fatal("fixture has no convert_probe command")
		}
		var reported string
		pollUntilConformance(t, "convert_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "convert:"); ok {
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
		return got["result"]
	}

	var reference []any
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
	// Values Pi's code implies, so a mistake shared by every SDK cannot pass.
	for i, c := range cases {
		if want, ok := messageConversionPins[c["pin"]]; ok && !reflect.DeepEqual(reference[i], want) {
			t.Fatalf("reference %v for %v = %v, want %v", c["fn"], c, reference[i], want)
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
				if got := run(t, h, c); !reflect.DeepEqual(got, reference[i]) {
					t.Errorf("%v %v = %v, Node (Pi) reports %v", c["fn"], c, got, reference[i])
				}
			}
		})
	}
}

const (
	pinBash     = "bash"
	pinNote     = "note-tie"
	pinCustom   = "custom-string"
	pinSummary  = "branch-summary"
	pinCompact  = "compaction-summary"
	pinDropped  = "dropped"
	pinPassthru = "passthrough"
)

var messageConversionPins = map[any]any{
	pinBash:     []any{map[string]any{"role": "user", "timestamp": 7.0, "content": []any{map[string]any{"type": "text", "text": "Ran `ls -l`\n```\nout\n```\n\nCommand exited with code 2\n\n[Output truncated. Full output: /tmp/full]"}}}},
	pinNote:     "[Image: original 1x1, displayed at 8x4. Multiply coordinates by 0.13 to map to original image.]",
	pinCustom:   []any{map[string]any{"role": "user", "timestamp": 1.0, "content": []any{map[string]any{"type": "text", "text": "hi"}}}},
	pinSummary:  []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "The following is a summary of a branch that this conversation came back from:\n\n<summary>\nS</summary>"}}}},
	pinCompact:  []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "The conversation history before this point was compacted into the following summary:\n\n<summary>\nS\n</summary>"}}}},
	pinDropped:  []any{},
	pinPassthru: []any{map[string]any{"role": "toolResult", "toolCallId": "c", "extra": []any{1.0}}},
}

func messageConversionCases() []map[string]any {
	convert := func(pin string, messages ...any) map[string]any {
		if messages == nil {
			messages = []any{}
		}
		c := map[string]any{"fn": "convert", "messages": messages}
		if pin != "" {
			c["pin"] = pin
		}
		return c
	}
	note := func(pin string, original, originalHeight, width, height float64, resized bool) map[string]any {
		c := map[string]any{"fn": "note", "image": map[string]any{"originalWidth": original, "originalHeight": originalHeight, "width": width, "height": height, "wasResized": resized}}
		if pin != "" {
			c["pin"] = pin
		}
		return c
	}
	bash := func(fields map[string]any) map[string]any {
		m := map[string]any{"role": "bashExecution"}
		maps.Copy(m, fields)
		return m
	}
	cases := []map[string]any{
		convert(pinBash, bash(map[string]any{"command": "ls -l", "output": "out", "exitCode": 2.0, "cancelled": false, "truncated": true, "fullOutputPath": "/tmp/full", "timestamp": 7.0})),
		convert(pinCustom, map[string]any{"role": "custom", "customType": "x", "content": "hi", "display": true, "details": map[string]any{"a": 1.0}, "timestamp": 1.0}),
		convert(pinSummary, map[string]any{"role": "branchSummary", "summary": "S", "fromId": "e"}),
		convert(pinCompact, map[string]any{"role": "compactionSummary", "summary": "S", "tokensBefore": 5.0}),
		convert(pinDropped, map[string]any{"role": "nope"}, map[string]any{}, map[string]any{"role": 5.0}, map[string]any{"role": nil}, map[string]any{"role": []any{"user"}}, bash(map[string]any{"excludeFromContext": true, "command": "x"})),
		convert(pinPassthru, map[string]any{"role": "toolResult", "toolCallId": "c", "extra": []any{1.0}}),
		convert(""),
		convert("", 5.0, "user", true, []any{}, []any{map[string]any{"role": "user"}}),
		note(pinNote, 1, 1, 8, 4, true),
		note("", 1000, 500, 8, 4, false),
		note("", 1000, 500, 8, 4, true),
	}
	values := []any{nil, true, false, 0.0, 1.0, -1.0, 1.5, 0.1, 1e21, 1e-7, 123456789012345680000.0, "", "0", "text", "multi\nline `tick`", []any{}, []any{1.0, nil, []any{"a", nil, 2.5}}, map[string]any{}, map[string]any{"a": 1.0}}
	for _, v := range values {
		cases = append(cases,
			convert("", bash(map[string]any{"command": v, "output": "o"})),
			convert("", bash(map[string]any{"command": "c", "output": v})),
			convert("", bash(map[string]any{"command": "c", "output": "o", "exitCode": v})),
			convert("", bash(map[string]any{"command": "c", "output": "o", "exitCode": 1.0, "cancelled": v})),
			convert("", bash(map[string]any{"command": "c", "output": "o", "truncated": v, "fullOutputPath": "/p"})),
			convert("", bash(map[string]any{"command": "c", "output": "o", "truncated": true, "fullOutputPath": v})),
			convert("", bash(map[string]any{"command": "c", "output": "o", "excludeFromContext": v})),
			convert("", bash(map[string]any{"command": "c", "timestamp": v})),
			convert("", map[string]any{"role": "custom", "content": v, "timestamp": v}),
			convert("", map[string]any{"role": "branchSummary", "summary": v, "timestamp": v}),
			convert("", map[string]any{"role": "compactionSummary", "summary": v}),
			convert("", map[string]any{"role": "user", "content": v}),
		)
	}
	// Absent members: undefined in a template literal is "undefined", a missing content or timestamp is not in the JSON result.
	cases = append(cases,
		convert("", bash(nil)),
		convert("", bash(map[string]any{"command": "c"})),
		convert("", map[string]any{"role": "custom"}),
		convert("", map[string]any{"role": "branchSummary"}),
		convert("", map[string]any{"role": "compactionSummary"}),
	)
	// Dimensions: ties and near ties of toFixed(2), signs, zero and non-finite scales, the exponential form.
	for _, d := range [][4]float64{
		{1, 1, 8, 4}, {5, 5, 8, 4}, {3, 3, 8, 4}, {7, 7, 8, 4}, {1, 1, 16, 2}, {1005, 1, 1000, 1}, {2675, 1, 1000, 1}, {1, 1, 3, 3}, {2, 2, 3, 3}, {10, 10, 3, 3},
		{100.5, 50.25, 10.25, 5.125}, {0, 0, 5, 5}, {5, 5, 0, 0}, {0, 0, 0, 0}, {-5, 5, 0, 5}, {-1, 1, 1e6, 1}, {1, 1, 1e6, 1}, {1e21, 1, 1, 1}, {1e22, 1, 3, 1},
		{1e300, 1, 1, 1}, {1, 1, 5e-324, 1}, {123456789, 1, 7, 1}, {99.995, 1, 1, 1}, {0.995, 1, 1, 1}, {9.995, 1, 1, 1}, {0.005, 1, 1, 1}, {0.0049999, 1, 1, 1},
		{2, 1, -4, 1}, {-2, 1, -4, 1}, {0, 1, -4, 1}, {math.Copysign(0, -1), 1, 4, 1}, {1, 1, 1e-7, 1}, {4096, 2048, 1024, 512}, {8000, 6000, 2000, 1500},
	} {
		cases = append(cases, note("", d[0], d[1], d[2], d[3], true))
	}
	random := rand.New(rand.NewPCG(7, 11))
	for range 300 {
		a, b := float64(random.IntN(5000)), float64(1+random.IntN(64))
		cases = append(cases, note("", a/8, 3, b/8, 2, true), note("", a, 3, b, 2, true))
	}
	// Seeded mixes of every field, so a combination no single case names still agrees.
	roles := []string{"bashExecution", "custom", "branchSummary", "compactionSummary", "system", "user", "assistant", "toolResult", "other"}
	pick := func() any { return values[random.IntN(len(values))] }
	for range 400 {
		var messages []any
		for range 1 + random.IntN(4) {
			m := map[string]any{"role": roles[random.IntN(len(roles))]}
			for _, key := range []string{"command", "output", "exitCode", "cancelled", "truncated", "fullOutputPath", "excludeFromContext", "content", "summary", "timestamp", "details"} {
				if random.IntN(2) == 0 {
					m[key] = pick()
				}
			}
			messages = append(messages, m)
		}
		cases = append(cases, convert("", messages...))
	}
	return cases
}

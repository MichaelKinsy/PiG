package agent

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// proxy.ts processProxyEvent appends every delta to the block (`textContent.text += event.delta`, `partialJson += event.delta`) and re-parses the whole
// partialJson with parseStreamingJson for each toolcall_delta, so a call of N deltas costs N times its length. The converter's state for a block
// only grows, so its cost per delta must follow the delta, not the block. The converted message is unchanged.
//
// mutation-checked: `+=` on the content or on toolJSON, or SetStreamingArguments on the whole text, makes the allocation grow with deltas x length.
func TestProxyEventConverterCostPerDeltaFollowsTheDeltaNotTheBlock(t *testing.T) {
	const deltas, chunk = 2000, 40
	piece := strings.Repeat("x", chunk)
	for _, tc := range []struct {
		name   string
		start  ProxyAssistantMessageEvent
		first  string
		delta  func(i int) ProxyAssistantMessageEvent
		verify func(t *testing.T, content ai.AssistantContentBlock)
	}{
		{
			name:  "text",
			start: ProxyAssistantMessageEvent{Type: "text_start", ContentIndex: 0},
			delta: func(int) ProxyAssistantMessageEvent {
				return ProxyAssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: piece}
			},
			verify: func(t *testing.T, content ai.AssistantContentBlock) {
				if text, ok := content.(ai.TextContent); !ok || len(text.Text) != deltas*chunk {
					t.Fatalf("text content %#v", content)
				}
			},
		},
		{
			name:  "thinking",
			start: ProxyAssistantMessageEvent{Type: "thinking_start", ContentIndex: 0},
			delta: func(int) ProxyAssistantMessageEvent {
				return ProxyAssistantMessageEvent{Type: "thinking_delta", ContentIndex: 0, Delta: piece}
			},
			verify: func(t *testing.T, content ai.AssistantContentBlock) {
				if thinking, ok := content.(ai.ThinkingContent); !ok || len(thinking.Thinking) != deltas*chunk {
					t.Fatalf("thinking content %#v", content)
				}
			},
		},
		{
			name:  "tool call arguments",
			start: ProxyAssistantMessageEvent{Type: "toolcall_start", ContentIndex: 0, ID: "call", ToolName: "write"},
			first: `{"content":"`,
			delta: func(int) ProxyAssistantMessageEvent {
				return ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: piece}
			},
			verify: func(t *testing.T, content ai.AssistantContentBlock) {
				call, ok := content.(ai.ToolCall)
				if !ok || len(call.Arguments["content"].(string)) != deltas*chunk {
					t.Fatalf("tool call %#v", content)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
			for _, event := range []ProxyAssistantMessageEvent{{Type: "start"}, tc.start} {
				if _, err := converter.process(event); err != nil {
					t.Fatal(err)
				}
			}
			if tc.first != "" {
				if _, err := converter.process(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: tc.first}); err != nil {
					t.Fatal(err)
				}
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for i := range deltas {
				if _, err := converter.process(tc.delta(i)); err != nil {
					t.Fatal(err)
				}
			}
			runtime.ReadMemStats(&after)
			tc.verify(t, converter.partial.Content[0])
			if allocated, budget := after.TotalAlloc-before.TotalAlloc, uint64(deltas*chunk)*40; allocated > budget {
				t.Fatalf("%d deltas of %d bytes allocated %d bytes, want at most %d (the cost of the deltas, not of the block per delta)", deltas, chunk, allocated, budget)
			}
		})
	}
}

// Every toolcall_delta leaves the call with what proxy.ts computes from the whole text so far: parseStreamingJson(partialJson) || {} (proxy.ts:343), with
// partialJson the concatenated deltas. The converter reads only the bytes a delta adds, so the call after each delta must equal a parse of the whole prefix,
// whatever the split points, for objects that nest, escape, repeat keys, and for texts that never open an object. The text and thinking blocks hold the
// concatenation. A toolcall_start that reuses an index starts the text over.
//
// mutation-checked: a converter that parsed only the delta, dropped the first delta, or kept the previous call's text across toolcall_start fails it.
func TestProxyEventConverterToolArgumentsEqualTheWholeTextParseAfterEveryDelta(t *testing.T) {
	texts := []string{
		`{"path":"a.go","content":"line1\nline \"two\" é日本","edits":[{"o":1,"n":[true,null,2.5e3]},{"o":2}],"k":{"z":1,"a":{"b":[]}},"k":9}`,
		`{"a":1,"b":{"c":[1,2,{"d":"x"}]},"e":"y\\"}`,
		`  {"unterminated":"value that never closes, with , commas and } braces`,
		`[1,2,3]`, `null`, `"text"`, `plain words`, `{}`, ``,
	}
	rng := rand.New(rand.NewPCG(7, 9))
	for _, text := range texts {
		for range 40 {
			converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
			for round := range 2 { // the second call reuses the index and must not inherit the first
				if _, err := converter.process(ProxyAssistantMessageEvent{Type: "toolcall_start", ContentIndex: 0, ID: "c", ToolName: "t"}); err != nil {
					t.Fatal(err)
				}
				for sent := 0; sent < len(text); {
					next := min(len(text), sent+1+rng.IntN(9))
					if _, err := converter.process(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: text[sent:next]}); err != nil {
						t.Fatal(err)
					}
					sent = next
					want := ai.ToolCall{ID: "c", Name: "t"}
					want.SetStreamingArguments(text[:sent])
					want.DefaultFalsyArguments()
					got := converter.partial.Content[0].(ai.ToolCall)
					gotJSON, _ := json.Marshal(got.Arguments)
					wantJSON, _ := json.Marshal(want.Arguments)
					gotMembers, _ := got.ArgumentMembers()
					wantMembers, _ := want.ArgumentMembers()
					if !bytes.Equal(gotJSON, wantJSON) || !reflect.DeepEqual(gotMembers, wantMembers) {
						t.Fatalf("round %d after %q of %q: arguments %s %v, want %s %v", round, text[:sent], text, gotJSON, gotMembers, wantJSON, wantMembers)
					}
				}
			}
		}
	}
}

// Tool calls stream side by side (proxy.ts keeps partialJson on each block): a delta for one call must continue that call's parse, not restart it because
// the call before it was a different text.
//
// mutation-checked: one parser shared by every content index restarts at every interleaved delta and fails the allocation budget.
func TestProxyEventConverterInterleavedToolCallsEachContinueTheirOwnParse(t *testing.T) {
	const deltas, chunk = 1000, 40
	piece := strings.Repeat("y", chunk)
	converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
	for _, event := range []ProxyAssistantMessageEvent{
		{Type: "start"},
		{Type: "toolcall_start", ContentIndex: 0, ID: "a", ToolName: "write"},
		{Type: "toolcall_start", ContentIndex: 1, ID: "b", ToolName: "write"},
		{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"a":"`},
		{Type: "toolcall_delta", ContentIndex: 1, Delta: `{"b":"`},
	} {
		if _, err := converter.process(event); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range deltas {
		for index := range 2 {
			if _, err := converter.process(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: index, Delta: piece}); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.ReadMemStats(&after)
	for index, key := range []string{"a", "b"} {
		if call, ok := converter.partial.Content[index].(ai.ToolCall); !ok || len(call.Arguments[key].(string)) != deltas*chunk {
			t.Fatalf("tool call %d: %#v", index, converter.partial.Content[index])
		}
	}
	if allocated, budget := after.TotalAlloc-before.TotalAlloc, uint64(2*deltas*chunk)*40; allocated > budget {
		t.Fatalf("interleaved deltas allocated %d bytes, want at most %d", allocated, budget)
	}
}

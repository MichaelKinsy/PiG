//go:build !race && linux && !pig_strip_mistral_conversations

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// Each delta of a long assistant turn costs a constant allocation: the marginal cost of the deltas between n and 2n stays under its ratchet ceiling, and the bytes per delta do not grow with the length of the turn (a copy of the whole block per delta would double them). The builder path is the one every provider shares.
func TestStreamBuilderPerDeltaAllocationGate(t *testing.T) {
	const n = 1000
	shapes := []struct {
		id    string
		shape func(units int) longStreamShape
	}{
		{"ai.stream.text_delta", func(units int) longStreamShape { return longStreamShape{textDeltas: units} }},
		{"ai.stream.thinking_delta", func(units int) longStreamShape { return longStreamShape{thinkingDeltas: units} }},
		{"ai.stream.toolcall_delta", func(units int) longStreamShape {
			return longStreamShape{toolCalls: 1, argDeltas: units, argLines: units}
		}},
	}
	for _, shape := range shapes {
		t.Run(shape.id, func(t *testing.T) {
			run := func(units int) { shape.shape(units).drive(context.Background(), nil).stream.Result() }
			marginal := perfgate.CheckLinear(t, shape.id, n, 1.5, run)
			perfgate.Check(t, shape.id+".allocs", marginal.Allocs)
			perfgate.Check(t, shape.id+".bytes", marginal.Bytes)
		})
	}
}

// Each provider stream frame (one SSE delta record through the HTTP client, the decoder, the builder and the event stream to a consumer) costs constant bytes, and the cost per frame does not grow with the frames before it. The shapes are BenchmarkProviderStreamFrames'; TestStreamFrameAllocationCeilings gates the allocation count per frame.
func TestProviderStreamPerFrameAllocationGate(t *testing.T) {
	const n = 1000
	for _, shape := range streamFrameShapes {
		t.Run(shape.name, func(t *testing.T) {
			var payload atomic.Pointer[string]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, *payload.Load())
			}))
			defer server.Close()
			provider := shape.provider(server.URL)
			bodies := map[int]string{}
			run := func(frames int) {
				body, ok := bodies[frames]
				if !ok {
					body = shape.body(frames)
					bodies[frames] = body
				}
				payload.Store(&body)
				if events := streamFrameRun(t, provider, StreamOptions{}); events < shape.minEvents(frames) {
					t.Fatalf("%s: %d events from %d frames", shape.name, events, frames)
				}
			}
			for _, frames := range []int{n, 2 * n, 4 * n} {
				run(frames) // Build each body before measuring, so the measured runs allocate only the stream.
			}
			id := "ai.provider_frame." + shape.name
			marginal := perfgate.CheckLinear(t, id, n, 1.5, run)
			perfgate.Check(t, id+".bytes", marginal.Bytes)
		})
	}
}

// gateToolSchema is shaped like the built-in edit tool: an ordered object with nested properties, an array of objects and a required list, so it takes the ordered-marshal path a session's tool declarations take.
const gateToolSchema = `{"name":"edit","description":"Edit a single file using exact text replacement.","parameters":{"type":"object","required":["path","edits"],"properties":{"path":{"type":"string","description":"Path to the file to edit (relative or absolute)"},"edits":{"type":"array","description":"One or more targeted replacements.","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string","description":"Exact text for one targeted replacement."},"newText":{"type":"string","description":"Replacement text for this targeted edit."}},"additionalProperties":false}}},"additionalProperties":false},"promptGuidelines":["Use edit for precise changes."]}`

// Estimating context tokens encodes every declared tool twice per turn (JSON.stringify(tools).length in Pi's estimateContextTokens and the compaction estimate), so the ordered marshal of one declaration is per-turn cost.
func TestToolSchemaMarshalAllocationGate(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(gateToolSchema), &tool); err != nil {
		t.Fatal(err)
	}
	if tool.parameterOrder == nil {
		t.Fatal("the gate schema lost its key order")
	}
	tools := []ToolSchema{tool}
	cost := perfgate.Measure(func() { JSONStringifyLength(tools) })
	perfgate.Check(t, "ai.tool_schema_marshal.allocs", cost.Allocs)
	perfgate.Check(t, "ai.tool_schema_marshal.bytes", cost.Bytes)
}

// Every request reads its response body in chunks of up to 64 KiB, as undici's socket reads do. A chunk must cost the bytes it carries, not the read size: a short streamed response used to allocate a fresh 64 KiB buffer for each read, including the empty read at end of body, which made the read buffers the largest per-request allocation of a tool turn.
func TestProviderResponseAllocationGate(t *testing.T) {
	shape := streamFrameShapes[0]
	body := shape.body(3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	provider := shape.provider(server.URL)
	cost := perfgate.Measure(func() {
		if events := streamFrameRun(t, provider, StreamOptions{}); events < shape.minEvents(3) {
			t.Fatalf("%d events", events)
		}
	})
	perfgate.Check(t, "ai.provider_response.openai-completions.bytes", cost.Bytes)
}

// Every request re-encodes the whole history, as Pi's JSON.stringify of the request body does, so the cost of one history turn is paid on every later request. A history turn here is what a read tool turn adds: the prompt, the assistant's tool call, a 1.7 KB file and the reply.
func TestProviderRequestHistoryAllocationGate(t *testing.T) {
	shape := streamFrameShapes[0]
	body := shape.body(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	provider := shape.provider(server.URL)
	file := strings.Repeat("Benchmark fixture. ", 90)
	histories := map[int]TranscriptContext{}
	history := func(turns int) TranscriptContext {
		if built, ok := histories[turns]; ok {
			return built
		}
		messages := make([]Message, 0, 4*turns+1)
		for i := range turns {
			id := fmt.Sprintf("call_%d", i)
			messages = append(messages,
				UserMessage{Content: UserText(fmt.Sprintf("Turn %d. Read README.md and tell me in one sentence what it says.", i))},
				AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": "/work/README.md"}}}},
				ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: file}}},
				AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: "The file is a benchmark fixture."}}},
			)
		}
		messages = append(messages, UserMessage{Content: UserText("next")})
		built := NormalizeContext(Context{Messages: messages, Frozen: true})
		histories[turns] = built
		return built
	}
	run := func(turns int) {
		stream, err := provider.Stream(t.Context(), history(turns), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
	}
	for _, turns := range []int{50, 100, 200} {
		history(turns)
	}
	// The allocation count must stay linear in the history. The bytes are checked at one size only: encoding/json keeps or drops its pooled output buffer by how full the last one was, so between 100 and 200 turns a run sometimes regrows the whole buffer.
	marginal := perfgate.Marginal(50, run)
	double := perfgate.Marginal(100, run)
	t.Logf("perfgate: ai.request_history_turn marginal per unit at 50: %.1f allocs %.0f B; at 100: %.1f allocs %.0f B", marginal.Allocs, marginal.Bytes, double.Allocs, double.Bytes)
	if double.Allocs > 1.1*marginal.Allocs {
		t.Errorf("perfgate: ai.request_history_turn allocations are not linear: %.1f per turn after 50 turns, %.1f after 100", marginal.Allocs, double.Allocs)
	}
	perfgate.Check(t, "ai.request_history_turn.allocs", marginal.Allocs)
	perfgate.Check(t, "ai.request_history_turn.bytes", marginal.Bytes)
}

// A large file written through a tool call: providers open the call with empty arguments, then stream the arguments JSON a token (about 16 bytes) at a time. Each delta must cost the same whatever the length of the arguments before it (the gate compares the deltas after 3,200 and after 6,400, up to 200 KB), so a 200 KB file costs 12,800 constant deltas, not 12,800 parses of everything so far. The content is source code: spaces, braces and escaped newlines.
func TestToolCallArgumentsPerDeltaAllocationGate(t *testing.T) {
	const step = 16
	var source strings.Builder
	for n := 0; source.Len() < 200*1024; n++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", n, n%97, n)
	}
	encoded, err := json.Marshal(map[string]string{"path": "big/source.ts", "content": source.String()})
	if err != nil {
		t.Fatal(err)
	}
	arguments := string(encoded)
	run := func(deltas int) {
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "test", "model")
		runManagedBuilder(builder, func() {
			builder.start()
			builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call_0", name: "write"})
			for i := range deltas {
				builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: arguments[i*step : (i+1)*step]})
			}
			builder.endToolCall(0)
			builder.done(StopReasonToolUse, &Usage{Input: 1, Output: 2, TotalTokens: 3}, "")
		})
		builder.stream.Result()
	}
	marginal := perfgate.CheckLinear(t, "ai.stream.toolcall_arguments_delta", 3200, 1.5, run)
	perfgate.Check(t, "ai.stream.toolcall_arguments_delta.allocs", marginal.Allocs)
	perfgate.Check(t, "ai.stream.toolcall_arguments_delta.bytes", marginal.Bytes)
}

// Every turn's context carries the tool results before it, and a tool result read back from the session holds its details as raw JSON: a read of a long file keeps the shown text in its truncation object. NormalizeContext validates and copies each of them, and TranscriptContext.Messages copies them again. The cost per earlier tool result is two compact copies of its details: no encode, decode or tree of the text.
func TestContextToolResultDetailsAllocationGate(t *testing.T) {
	content, err := json.Marshal(strings.Repeat("export function step(input: number[]): number { return input.length; }\n", 700))
	if err != nil {
		t.Fatal(err)
	}
	details := json.RawMessage(`{"truncation":{"content":` + string(content) + `,"truncated":true,"truncatedBy":"lines","totalLines":6851,"totalBytes":250000,"outputLines":2000,"outputBytes":51200,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":2000,"maxBytes":51200}}`)
	run := func(results int) {
		messages := make([]Message, 0, results+1)
		for i := range results {
			messages = append(messages, ToolResultMessage{ToolCallID: fmt.Sprintf("call_%d", i), ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "text"}}, Details: details})
		}
		messages = append(messages, UserMessage{Content: UserText("next")})
		if copied := NormalizeContext(Context{Messages: messages}).Messages(); len(copied) != results+1 {
			t.Fatalf("%d messages", len(copied))
		}
	}
	marginal := perfgate.CheckLinear(t, "ai.context_tool_result_details", 20, 1.2, run)
	t.Logf("perfgate: ai.context_tool_result_details per %d-byte details: %.1f allocs %.0f B", len(details), marginal.Allocs, marginal.Bytes)
	perfgate.Check(t, "ai.context_tool_result_details.allocs", marginal.Allocs)
	perfgate.Check(t, "ai.context_tool_result_details.bytes_per_details_byte", marginal.Bytes/float64(len(details)))
}

// A grammar (custom) tool call streams its raw input a delta at a time, which the provider turns into JSON arguments. Each delta must cost the same whatever came before it: the input so far grows in place and the monotonic check compares the shared bytes without reading them, instead of copying and comparing all the input per delta. The gate compares the deltas after 1,600 and after 3,200, for the openai-responses custom_tool_call_input.delta events and the openai-completions custom tool call deltas.
func TestGrammarToolInputPerDeltaAllocationGate(t *testing.T) {
	const n = 1600
	var source strings.Builder
	for line := 0; source.Len() < 16*4*n; line++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", line, line%97, line)
	}
	text := source.String()
	piece := func(unit int) string {
		encoded, err := json.Marshal(text[16*unit : 16*unit+16])
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	shapes := []struct {
		id   string
		body func(units int) string
		run  func(body string)
	}{
		{
			id: "ai.stream.grammar_input_delta.openai-responses",
			body: func(units int) string {
				var body strings.Builder
				body.WriteString(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"apply_patch"}}` + "\n\n")
				for unit := range units {
					body.WriteString(`data: {"type":"response.custom_tool_call_input.delta","output_index":0,"delta":` + piece(unit) + "}\n\n")
				}
				body.WriteString(`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"apply_patch"}}` + "\n\n")
				body.WriteString(`data: {"type":"response.completed","response":{"status":"completed"}}` + "\n\n")
				return body.String()
			},
			run: func(body string) {
				builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-test")
				go processResponsesStream(t.Context(), newSSEDecoder(strings.NewReader(body)), builder, &OpenAIResponsesStreamOptions{model: &Model{ID: "gpt-test"}, GrammarToolInputProperties: map[string]string{"apply_patch": "patch"}})
				if result := builder.stream.Result(); result.StopReason != StopReasonToolUse {
					t.Fatalf("result = %+v", result)
				}
			},
		},
		{
			id: "ai.stream.grammar_input_delta.openai-completions",
			body: func(units int) string {
				var body strings.Builder
				body.WriteString(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_g","type":"custom","custom":{"name":"apply_patch","input":""}}]},"finish_reason":null}]}` + "\n\n")
				for unit := range units {
					body.WriteString(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"custom":{"input":` + piece(unit) + `}}]},"finish_reason":null}]}` + "\n\n")
				}
				body.WriteString(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n")
				return body.String()
			},
			run: func(body string) {
				builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "openai", "model")
				(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(body), builder, map[string]string{"apply_patch": "patch"})
				if result := builder.stream.Result(); result.StopReason != StopReasonToolUse {
					t.Fatalf("result = %+v", result)
				}
			},
		},
	}
	for _, shape := range shapes {
		t.Run(shape.id, func(t *testing.T) {
			bodies := map[int]string{}
			marginal := perfgate.CheckLinear(t, shape.id, n, 1.5, func(units int) {
				body, ok := bodies[units]
				if !ok {
					body = shape.body(units)
					bodies[units] = body
				}
				shape.run(body)
			})
			perfgate.Check(t, shape.id+".allocs", marginal.Allocs)
			perfgate.Check(t, shape.id+".bytes", marginal.Bytes)
		})
	}
}

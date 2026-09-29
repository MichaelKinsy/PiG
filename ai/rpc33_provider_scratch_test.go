package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The parser owns Read and its construction state on the same goroutine. Each
// frame is followed by a read handshake; this tests source suspension state,
// not when a forwarded event or Agent observes a shared message.
type providerScratchReader struct {
	frames     []string
	beforeRead func(int)
	index      int
}

func (r *providerScratchReader) Read(p []byte) (int, error) {
	if r.beforeRead != nil {
		r.beforeRead(r.index)
	}
	if r.index == len(r.frames) {
		return 0, io.EOF
	}
	frame := r.frames[r.index]
	n := copy(p, frame)
	if n != len(frame) {
		return 0, errors.New("scratch fixture frame exceeds read buffer")
	}
	r.index++
	return n, nil
}

func assertScratchJSON(t *testing.T, got any, want string) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected any
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("JSON = %s; want %s", data, want)
	}
}

func completionToolFrame(tools string, reason string) string {
	return `data: {"choices":[{"delta":{"tool_calls":` + tools + `},"finish_reason":` + reason + `}]}` + "\n\n"
}

func responseScratchFrame(kind string, fields string) string {
	return `data: {"type":"response.` + kind + `",` + fields + `}` + "\n\n"
}

func TestProviderScratchFunctionLifecycle(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:491-550,644-657
	// upstream: packages/ai/src/api/openai-responses-shared.ts:485-502,653-669,709-726
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			t.Parallel()
			builder := newAssistantStreamBuilder(t.Context(), api, "scratch-provider", "scratch-model")
			var frames, want []string
			if api == APIOpenAICompletions {
				frames = []string{
					completionToolFrame(`[{"id":"call-a","function":{"name":"read","arguments":""}}]`, `null`),
					completionToolFrame(`[{"id":"call-a","index":0,"function":{"arguments":"{\"path\":\"tar"}}]`, `null`),
					completionToolFrame(`[{"index":0,"function":{"arguments":"get\",\"count\":2,"}}]`, `"tool_calls"`),
					"data: [DONE]\n\n",
				}
				want = []string{
					`[{"type":"toolCall","id":"call-a","name":"read","arguments":{},"partialArgs":""}]`,
					`[{"type":"toolCall","id":"call-a","name":"read","arguments":{"path":"tar"},"partialArgs":"{\"path\":\"tar","streamIndex":0}]`,
					`[{"type":"toolCall","id":"call-a","name":"read","arguments":{"path":"target","count":2},"partialArgs":"{\"path\":\"target\",\"count\":2,","streamIndex":0}]`,
				}
			} else {
				frames = []string{
					responseScratchFrame("output_item.added", `"output_index":7,"item":{"type":"function_call","id":"item-a","call_id":"call-a","name":"read","arguments":"{\"path\":"}`),
					responseScratchFrame("function_call_arguments.delta", `"output_index":7,"delta":"\"tar"`),
					responseScratchFrame("function_call_arguments.done", `"output_index":7,"arguments":"{\"other\":true}"`),
					responseScratchFrame("output_item.done", `"output_index":7,"item":{"type":"function_call","id":"ignored","call_id":"ignored","name":"ignored","arguments":"","namespace":"final"}`),
					responseScratchFrame("completed", `"response":{"status":"completed"}`),
				}
				want = []string{
					`[{"type":"toolCall","id":"call-a|item-a","name":"read","arguments":{},"partialJson":"{\"path\":"}]`,
					`[{"type":"toolCall","id":"call-a|item-a","name":"read","arguments":{"path":"tar"},"partialJson":"{\"path\":\"tar"}]`,
					`[{"type":"toolCall","id":"call-a|item-a","name":"read","arguments":{"other":true},"partialJson":"{\"other\":true}"}]`,
					`[{"type":"toolCall","id":"call-a|item-a","name":"read","arguments":{"other":true},"namespace":"final"}]`,
				}
			}
			var retained AssistantMessage
			reader := &providerScratchReader{frames: frames, beforeRead: func(i int) {
				if i > 0 && i <= len(want) {
					assertScratchJSON(t, builder.partial.Content, want[i-1])
				}
				if i == 1 {
					retained = builder.partial.cloneMessage().(AssistantMessage)
				}
			}}
			if api == APIOpenAICompletions {
				p := &openAIProvider{cfg: OpenAIConfig{Model: "scratch-model"}}
				p.parseSSE(t.Context(), reader, builder, nil)
			} else {
				p := &openAIResponsesProvider{}
				p.parseResponsesSSE(t.Context(), reader, builder, nil)
			}
			assertScratchJSON(t, retained.Content, want[0])
			result := builder.stream.Result()
			if result.StopReason != StopReasonToolUse {
				t.Errorf("stopReason = %s", result.StopReason)
			}
			assertNoProviderScratch(t, result)
		})
	}
}

func assertNoProviderScratch(t *testing.T, message *AssistantMessage) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"partialArgs", "partialJson", "customInput", "streamIndex"} {
		if strings.Contains(string(data), `"`+field+`":`) {
			t.Errorf("terminal retained %s: %s", field, data)
		}
	}
}

func TestCompletionsStopStateBeforeSuspension(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:575-583
	for _, tc := range []struct {
		wire    string
		reason  StopReason
		message string
	}{
		{"tool_calls", StopReasonToolUse, ""},
		{"length", StopReasonLength, ""},
		{"content_filter", StopReasonError, "Provider finish_reason: content_filter"},
	} {
		t.Run(tc.wire, func(t *testing.T) {
			t.Parallel()
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "scratch-provider", "scratch-model")
			reader := &providerScratchReader{frames: []string{
				`data: {"choices":[{"delta":{},"finish_reason":"` + tc.wire + `"}]}` + "\n\n",
				"data: [DONE]\n\n",
			}, beforeRead: func(i int) {
				if i == 1 && (builder.partial.StopReason != tc.reason || builder.partial.ErrorMessage != tc.message) {
					t.Errorf("before next read: stop = %s, error = %q; want %s, %q", builder.partial.StopReason, builder.partial.ErrorMessage, tc.reason, tc.message)
				}
			}}
			(&openAIProvider{}).parseSSE(t.Context(), reader, builder, nil)
		})
	}
}

func TestCompletionsStopStateUsesFirstChoice(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:566 processes only choices[0].
	t.Parallel()
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "scratch-provider", "scratch-model")
	frames := `data: {"choices":[{"delta":{"content":"first"},"finish_reason":"stop"},{"delta":{"content":"ignored"},"finish_reason":"content_filter"}]}` + "\n\ndata: [DONE]\n\n"
	(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(frames), builder, nil)
	result := builder.stream.Result()
	if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
		t.Errorf("result stop = %s, error = %q", result.StopReason, result.ErrorMessage)
	}
	assertScratchJSON(t, result.Content, `[{"type":"text","text":"first"}]`)
}

func TestCompletionsStopStateRetainsEarlierErrorMessage(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:575-583,698 only assigns a nonempty mapped error.
	t.Parallel()
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "scratch-provider", "scratch-model")
	frames := completionToolFrame(`[]`, `"content_filter"`) + completionToolFrame(`[]`, `"stop"`) + "data: [DONE]\n\n"
	(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(frames), builder, nil)
	result := builder.stream.Result()
	if result.StopReason != StopReasonStop || result.ErrorMessage != "Provider finish_reason: content_filter" {
		t.Errorf("result stop = %s, error = %q", result.StopReason, result.ErrorMessage)
	}
}

func TestProviderScratchCustomLifecycle(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:425-470,503-550
	// upstream: packages/ai/src/api/openai-responses-shared.ts:505-529,670-677,728-739
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, ending := range []string{"success", "error", "abort"} {
			t.Run(string(api)+"/"+ending, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				builder := newAssistantStreamBuilder(ctx, api, "scratch-provider", "scratch-model")
				var frames, want []string
				if api == APIOpenAICompletions {
					frames = []string{
						completionToolFrame(`[{"id":"call","index":0,"custom":{"name":"grammar","input":""}}]`, `null`),
						completionToolFrame(`[{"index":0,"custom":{"input":"a\"\n"}}]`, `"tool_calls"`),
						"data: [DONE]\n\n",
					}
					want = []string{
						`[{"type":"toolCall","id":"call","name":"grammar","arguments":{"payload":""},"streamIndex":0,"customInput":{"property":"payload","jsonBuffer":{"input":"","started":false,"closed":false}}}]`,
						`[{"type":"toolCall","id":"call","name":"grammar","arguments":{"payload":"a\"\n"},"streamIndex":0,"customInput":{"property":"payload","jsonBuffer":{"input":"a\"\n","started":true,"closed":false}}}]`,
					}
				} else {
					frames = []string{
						responseScratchFrame("output_item.added", `"output_index":3,"item":{"type":"custom_tool_call","id":"item","call_id":"call","name":"grammar","input":"a"}`),
						responseScratchFrame("custom_tool_call_input.delta", `"output_index":3,"delta":"\"\n"`),
						responseScratchFrame("custom_tool_call_input.done", `"output_index":3,"input":"a\"\n"`),
						responseScratchFrame("output_item.done", `"output_index":3,"item":{"type":"custom_tool_call","id":"item","call_id":"call","name":"grammar","input":"a\"\n"}`),
						responseScratchFrame("completed", `"response":{"status":"completed"}`),
					}
					want = []string{
						`[{"type":"toolCall","id":"call|item","name":"grammar","arguments":{"payload":"a"},"customInput":{"property":"payload","jsonBuffer":{"input":"","started":false,"closed":false}}}]`,
						`[{"type":"toolCall","id":"call|item","name":"grammar","arguments":{"payload":"a\"\n"},"customInput":{"property":"payload","jsonBuffer":{"input":"a\"\n","started":true,"closed":false}}}]`,
						`[{"type":"toolCall","id":"call|item","name":"grammar","arguments":{"payload":"a\"\n"},"customInput":{"property":"payload","jsonBuffer":{"input":"a\"\n","started":true,"closed":true}}}]`,
						`[{"type":"toolCall","id":"call|item","name":"grammar","arguments":{"payload":"a\"\n"}}]`,
					}
				}
				if ending != "success" {
					frames = append(frames[:2], "data: {\"error\":{\"message\":\"broken\"}}\n\n")
					want = want[:2]
				}
				var retained AssistantMessage
				reader := &providerScratchReader{frames: frames, beforeRead: func(i int) {
					if i > 0 && i <= len(want) {
						assertScratchJSON(t, builder.partial.Content, want[i-1])
					}
					if i == 1 {
						retained = builder.partial.cloneMessage().(AssistantMessage)
					}
					if i == 2 && ending == "abort" {
						cancel()
					}
				}}
				if api == APIOpenAICompletions {
					(&openAIProvider{}).parseSSE(ctx, reader, builder, map[string]string{"grammar": "payload"})
				} else {
					(&openAIResponsesProvider{}).parseResponsesSSE(ctx, reader, builder, map[string]string{"grammar": "payload"})
				}
				assertScratchJSON(t, retained.Content, want[0])
				result := builder.stream.Result()
				assertNoProviderScratch(t, result)
				tool, ok := result.Content[0].(ToolCall)
				if !ok {
					t.Fatalf("content = %#v", result.Content)
				}
				assertScratchJSON(t, tool.Arguments, `{"payload":"a\"\n"}`)
				wantReason := map[string]StopReason{"success": StopReasonToolUse, "error": StopReasonError, "abort": StopReasonAborted}[ending]
				if result.StopReason != wantReason {
					t.Errorf("reason = %s; want %s", result.StopReason, wantReason)
				}
				if ending != "success" {
					for _, event := range collectBuilderEvents(builder.stream) {
						if event.EventType() == EventToolCallEnd {
							t.Error("failure synthesized toolcall_end")
						}
					}
				}
			})
		}
	}
}

func TestProviderScratchInterleavedTools(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:491-550
	// upstream: packages/ai/src/api/openai-responses-shared.ts:653-669,709-739
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			t.Parallel()
			builder := newAssistantStreamBuilder(t.Context(), api, "scratch-provider", "scratch-model")
			var frames []string
			if api == APIOpenAICompletions {
				frames = []string{
					completionToolFrame(`[{"index":8,"id":"a","function":{"name":"read","arguments":"{\"x\":1"}},{"id":"b","function":{"name":"write","arguments":"{\"y\":\"b"}}]`, `null`),
					completionToolFrame(`[{"id":"b","function":{"arguments":"eta\"}"}},{"index":8,"function":{"arguments":",\"bad\":nope}"}}]`, `"tool_calls"`),
					"data: [DONE]\n\n",
				}
			} else {
				frames = []string{
					responseScratchFrame("output_item.added", `"output_index":8,"item":{"type":"function_call","id":"ia","call_id":"a","name":"read","arguments":""}`),
					responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"function_call","id":"ib","call_id":"b","name":"write","arguments":""}`),
					responseScratchFrame("function_call_arguments.delta", `"output_index":8,"delta":"{\"x\":1"`),
					responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"{\"y\":\"b"`),
					responseScratchFrame("function_call_arguments.done", `"output_index":0,"arguments":"{\"y\":\"beta\"}"`),
					responseScratchFrame("function_call_arguments.delta", `"output_index":8,"delta":",\"bad\":nope}"`),
					responseScratchFrame("output_item.done", `"output_index":0,"item":{"type":"function_call","arguments":""}`),
					responseScratchFrame("output_item.done", `"output_index":8,"item":{"type":"function_call","arguments":""}`),
					responseScratchFrame("completed", `"response":{"status":"completed"}`),
				}
			}
			if api == APIOpenAICompletions {
				(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(strings.Join(frames, "")), builder, nil)
			} else {
				(&openAIResponsesProvider{}).parseResponsesSSE(t.Context(), strings.NewReader(strings.Join(frames, "")), builder, nil)
			}
			result := builder.stream.Result()
			assertNoProviderScratch(t, result)
			suffixA, suffixB := "", ""
			if api == APIOpenAIResponses {
				suffixA, suffixB = "|ia", "|ib"
			}
			assertScratchJSON(t, result.Content, `[{"type":"toolCall","id":"a`+suffixA+`","name":"read","arguments":{"x":1}},{"type":"toolCall","id":"b`+suffixB+`","name":"write","arguments":{"y":"beta"}}]`)
		})
	}
}

func TestProviderScratchFunctionFailureCleanup(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:701-715
	// upstream: packages/ai/src/api/openai-responses.ts:197-216
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, abort := range []bool{false, true} {
			t.Run(string(api)+map[bool]string{false: "/error", true: "/abort"}[abort], func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				builder := newAssistantStreamBuilder(ctx, api, "scratch-provider", "scratch-model")
				frames := []string{completionToolFrame(`[{"index":0,"id":"call","function":{"name":"read","arguments":"{\"path\":\"tar"}}]`, `null`)}
				if api == APIOpenAIResponses {
					frames = []string{
						responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"function_call","id":"item","call_id":"call","name":"read","arguments":""}`),
						responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"{\"path\":\"tar"`),
					}
				}
				frames = append(frames, "data: {\"error\":{\"message\":\"broken\"}}\n\n")
				reader := &providerScratchReader{frames: frames, beforeRead: func(i int) {
					if abort && i == len(frames)-1 {
						cancel()
					}
				}}
				if api == APIOpenAICompletions {
					(&openAIProvider{}).parseSSE(ctx, reader, builder, nil)
				} else {
					(&openAIResponsesProvider{}).parseResponsesSSE(ctx, reader, builder, nil)
				}
				result := builder.stream.Result()
				assertNoProviderScratch(t, result)
				assertScratchJSON(t, result.Content[0].(ToolCall).Arguments, `{"path":"tar"}`)
				for _, event := range collectBuilderEvents(builder.stream) {
					if event.EventType() == EventToolCallEnd {
						t.Error("failure synthesized toolcall_end")
					}
				}
			})
		}
	}
}

func TestResponsesScratchGrammarRejectsReplacements(t *testing.T) {
	// upstream: packages/ai/src/api/constrained-sampling.ts:appendGrammarToolInputJsonDelta
	for _, closeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-monotonic", true: "after-close"}[closeFirst], func(t *testing.T) {
			t.Parallel()
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "scratch-provider", "scratch-model")
			frames := []string{
				responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"custom_tool_call","id":"item","call_id":"call","name":"unknown"}`),
				responseScratchFrame("custom_tool_call_input.delta", `"output_index":0,"delta":"before"`),
			}
			if closeFirst {
				frames = append(frames, responseScratchFrame("custom_tool_call_input.done", `"output_index":0,"input":"before"`))
			}
			frames = append(frames, responseScratchFrame("custom_tool_call_input.done", `"output_index":0,"input":"after"`))
			(&openAIResponsesProvider{}).parseResponsesSSE(t.Context(), strings.NewReader(strings.Join(frames, "")), builder, nil)
			result := builder.stream.Result()
			if result.StopReason != StopReasonError {
				t.Fatalf("result = %#v", result)
			}
			assertNoProviderScratch(t, result)
			assertScratchJSON(t, result.Content, `[{"type":"toolCall","id":"call|item","name":"unknown","arguments":{"input":"before"}}]`)
		})
	}
}

func TestResponsesScratchCustomDoneInputAndNamespace(t *testing.T) {
	// upstream: packages/ai/src/api/openai-responses-shared.ts:728-739 uses ??, not ||, for final input.
	for _, tc := range []struct {
		name, doneFields string
		closeFirst       bool
		wantReason       StopReason
	}{
		{"explicit-empty", `"input":""`, false, StopReasonError},
		{"absent-input", `"namespace":"final"`, true, StopReasonToolUse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "scratch-provider", "scratch-model")
			frames := []string{
				responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"custom_tool_call","id":"item","call_id":"call","name":"unknown"}`),
				responseScratchFrame("custom_tool_call_input.delta", `"output_index":0,"delta":"before"`),
			}
			if tc.closeFirst {
				frames = append(frames, responseScratchFrame("custom_tool_call_input.done", `"output_index":0,"input":"before"`))
			}
			frames = append(frames, responseScratchFrame("output_item.done", `"output_index":0,"item":{"type":"custom_tool_call",`+tc.doneFields+`}`), responseScratchFrame("completed", `"response":{"status":"completed"}`))
			(&openAIResponsesProvider{}).parseResponsesSSE(t.Context(), strings.NewReader(strings.Join(frames, "")), builder, nil)
			result := builder.stream.Result()
			if result.StopReason != tc.wantReason {
				t.Errorf("stop = %s; want %s", result.StopReason, tc.wantReason)
			}
			assertNoProviderScratch(t, result)
			tool := result.Content[0].(ToolCall)
			if tc.closeFirst && tool.Namespace != "final" {
				t.Errorf("namespace = %q; want final", tool.Namespace)
			}
			assertScratchJSON(t, tool.Arguments, `{"input":"before"}`)
		})
	}
}

func TestCompletionsScratchGrammarEndOrder(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:425-470,680-682
	t.Parallel()
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "scratch-provider", "scratch-model")
	frames := completionToolFrame(`[{"index":1,"id":"a","custom":{"name":"first","input":"a"}},{"index":0,"id":"b","custom":{"name":"second","input":"b"}}]`, `"tool_calls"`) + "data: [DONE]\n\n"
	(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(frames), builder, nil)
	events := collectBuilderEvents(builder.stream)
	want := []AssistantEventType{EventStart, EventToolCallStart, EventToolCallDelta, EventToolCallStart, EventToolCallDelta, EventToolCallDelta, EventToolCallEnd, EventToolCallDelta, EventToolCallEnd, EventDone}
	if got := fauxUpstreamEventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = %v; want %v", got, want)
	}
	for _, i := range []int{5, 7} {
		delta := events[i].(ToolCallDeltaEvent)
		if delta.ContentIndex != (i-5)/2 || delta.Delta != `"}` {
			t.Errorf("closing delta = %#v", delta)
		}
	}
}

func BenchmarkProviderScratchPublication(b *testing.B) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		b.Run(string(api), func(b *testing.B) {
			var frames strings.Builder
			if api == APIOpenAICompletions {
				frames.WriteString(completionToolFrame(`[{"index":0,"id":"call","function":{"name":"read","arguments":"{\"path\":\""}}]`, `null`))
				for range 64 {
					frames.WriteString(completionToolFrame(`[{"index":0,"function":{"arguments":"fragment"}}]`, `null`))
				}
				frames.WriteString(completionToolFrame(`[{"index":0,"function":{"arguments":"\"}"}}]`, `"tool_calls"`))
				frames.WriteString("data: [DONE]\n\n")
			} else {
				frames.WriteString(responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"function_call","id":"item","call_id":"call","name":"read","arguments":""}`))
				frames.WriteString(responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"{\"path\":\""`))
				for range 64 {
					frames.WriteString(responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"fragment"`))
				}
				frames.WriteString(responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"\"}"`))
				frames.WriteString(responseScratchFrame("output_item.done", `"output_index":0,"item":{"type":"function_call"}`))
				frames.WriteString(responseScratchFrame("completed", `"response":{"status":"completed"}`))
			}
			input := frames.String()
			b.ReportAllocs()
			for b.Loop() {
				builder := newAssistantStreamBuilder(b.Context(), api, "scratch-provider", "scratch-model")
				if api == APIOpenAICompletions {
					(&openAIProvider{}).parseSSE(b.Context(), strings.NewReader(input), builder, nil)
				} else {
					(&openAIResponsesProvider{}).parseResponsesSSE(b.Context(), strings.NewReader(input), builder, nil)
				}
				for range builder.stream.Events(b.Context()) {
				}
				_ = builder.stream.Result()
			}
		})
	}
}

func TestProviderScratchThroughStream(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:378-379,644-657
	// upstream: packages/ai/src/api/openai-responses.ts:177-180
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, credential := range []string{"api-key", "oauth-callback"} {
			t.Run(string(api)+"/"+credential, func(t *testing.T) {
				t.Parallel()
				release := make(chan struct{})
				resume := sync.OnceFunc(func() { close(release) })
				first := completionToolFrame(`[{"id":"call","index":0,"function":{"name":"read","arguments":"{\"path\":\"tar"}}]`, `null`)
				last := completionToolFrame(`[{"index":0,"function":{"arguments":"get\"}"}}]`, `"tool_calls"`) + "data: [DONE]\n\n"
				want := `[{"type":"toolCall","id":"call","name":"read","arguments":{"path":"tar"},"partialArgs":"{\"path\":\"tar","streamIndex":0}]`
				if api == APIOpenAIResponses {
					first = responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"function_call","id":"item","call_id":"call","name":"read","arguments":""}`) + responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"{\"path\":\"tar"`)
					last = responseScratchFrame("output_item.done", `"output_index":0,"item":{"type":"function_call","arguments":"{\"path\":\"target\"}"}`) + responseScratchFrame("completed", `"response":{"status":"completed"}`)
					want = `[{"type":"toolCall","id":"call|item","name":"read","arguments":{"path":"tar"},"partialJson":"{\"path\":\"tar"}]`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.Header.Get("Authorization") != "Bearer scratch-key" {
						t.Error("request credential missing")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, first)
					w.(http.Flusher).Flush()
					<-release
					_, _ = io.WriteString(w, last)
				}))
				defer server.Close()
				defer resume()
				var keyFunc func(context.Context) (string, error)
				key := "scratch-key"
				if credential == "oauth-callback" {
					key = ""
					keyFunc = func(context.Context) (string, error) { return "scratch-key", nil }
				}
				var provider Provider
				if api == APIOpenAICompletions {
					provider = NewOpenAIProvider(OpenAIConfig{Model: "no-catalog-model", ProviderID: "custom-proxy", BaseURL: server.URL, APIKey: key, GetAPIKey: keyFunc})
				} else {
					provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "no-catalog-model", ProviderID: "custom-proxy", BaseURL: server.URL, APIKey: key, GetAPIKey: keyFunc})
				}
				defer func() { _ = provider.Close() }()
				stream, err := provider.Stream(t.Context(), piMessagesTestContext(), StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				sawDelta := false
				for event := range stream.Events(t.Context()) {
					if delta, ok := event.(ToolCallDeltaEvent); ok && !sawDelta {
						assertScratchJSON(t, delta.Partial.Observe().Content, want)
						sawDelta = true
						resume()
					}
				}
				if !sawDelta {
					t.Fatal("stream omitted toolcall_delta")
				}
				result := stream.Result()
				assertNoProviderScratch(t, result)
				assertScratchJSON(t, result.Content[0].(ToolCall).Arguments, `{"path":"target"}`)
			})
		}
	}
}

func TestSharedBuilderDoesNotInventProviderScratch(t *testing.T) {
	t.Parallel()
	builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "scratch-provider", "scratch-model")
	builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call", name: "read", argumentsDelta: `{"path":"x`})
	assertScratchJSON(t, builder.partial.Content, `[{"type":"toolCall","id":"call","name":"read","arguments":{"path":"x"}}]`)
	builder.done(StopReasonToolUse, nil, "")
}

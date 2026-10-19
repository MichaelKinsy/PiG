package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// sequentialToolCallArgumentDeltas is how many argument deltas each streamed call of a sequentialToolCallShape arrives in, between the one that opens its arguments and the one that closes them.
const sequentialToolCallArgumentDeltas = 4

// sequentialToolCallShape is one provider's response that streams calls tool calls one after another in a single assistant message, each call finished before the next one starts, as a model that issues many parallel calls does.
type sequentialToolCallShape struct {
	name     string
	provider func(baseURL string) Provider
	body     func(calls int) string
}

// sequentialToolCallValue is the one argument value of every streamed call: its argument text is {"v":"<value>"}.
func sequentialToolCallValue() string {
	var value strings.Builder
	for i := range sequentialToolCallArgumentDeltas {
		fmt.Fprintf(&value, "l%d ", i)
	}
	return value.String()
}

var sequentialToolCallShapes = []sequentialToolCallShape{
	{
		name: "openai-completions",
		provider: func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "k", Model: "gpt-bench", ProviderID: "openai"})
		},
		body: func(calls int) string {
			var body strings.Builder
			for call := range calls {
				fmt.Fprintf(&body, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":\"call_%d\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"v\\\":\\\"\"}}]}}]}\n\n", call, call)
				for i := range sequentialToolCallArgumentDeltas {
					fmt.Fprintf(&body, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"function\":{\"arguments\":\"l%d \"}}]}}]}\n\n", call, i)
				}
				fmt.Fprintf(&body, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"function\":{\"arguments\":\"\\\"}\"}}]}}]}\n\n", call)
			}
			body.WriteString("data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return body.String()
		},
	},
	{
		name: "openai-responses",
		provider: func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "k", Model: "gpt-bench", ProviderID: "openai"})
		},
		body: func(calls int) string {
			var body strings.Builder
			body.WriteString(sseRecords(`data: {"type":"response.created","response":{"id":"resp_1"}}`))
			full := `{"v":"` + sequentialToolCallValue() + `"}`
			for call := range calls {
				body.WriteString(sseRecords(fmt.Sprintf(`data: {"type":"response.output_item.added","output_index":%d,"item":{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":"echo","arguments":""}}`, call, call, call)))
				body.WriteString(sseRecords(fmt.Sprintf(`data: {"type":"response.function_call_arguments.delta","output_index":%d,"item_id":"fc_%d","delta":"{\"v\":\""}`, call, call)))
				for i := range sequentialToolCallArgumentDeltas {
					body.WriteString(sseRecords(fmt.Sprintf(`data: {"type":"response.function_call_arguments.delta","output_index":%d,"item_id":"fc_%d","delta":"l%d "}`, call, call, i)))
				}
				body.WriteString(sseRecords(fmt.Sprintf(`data: {"type":"response.function_call_arguments.delta","output_index":%d,"item_id":"fc_%d","delta":"\"}"}`, call, call)))
				body.WriteString(sseRecords(fmt.Sprintf(`data: {"type":"response.output_item.done","output_index":%d,"item":{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":"echo","arguments":%q}}`, call, call, call, full)))
			}
			body.WriteString(sseRecords(`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}}`))
			return body.String()
		},
	},
	{
		name: "anthropic",
		provider: func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "k", Model: "claude-bench"})
		},
		body: func(calls int) string {
			var body strings.Builder
			body.WriteString(sseRecords("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-bench\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}"))
			for call := range calls {
				body.WriteString(sseRecords(fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_%d\",\"name\":\"echo\",\"input\":{}}}", call, call)))
				body.WriteString(sseRecords(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"v\\\":\\\"\"}}", call)))
				for i := range sequentialToolCallArgumentDeltas {
					body.WriteString(sseRecords(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"l%d \"}}", call, i)))
				}
				body.WriteString(sseRecords(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"}\"}}", call)))
				body.WriteString(sseRecords(fmt.Sprintf("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}", call)))
			}
			body.WriteString(sseRecords(
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":7}}",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}",
			))
			return body.String()
		},
	},
	{
		// Gemini sends each function call whole, in one part, so its arguments never grow.
		name: "google",
		provider: func(url string) Provider {
			return NewGoogleProvider(GoogleConfig{BaseURL: url, APIKey: "k", Model: "gemini-2.5-flash", ProviderID: "google"})
		},
		body: func(calls int) string {
			var body strings.Builder
			for call := range calls {
				fmt.Fprintf(&body, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"call_%d\",\"name\":\"echo\",\"args\":{\"v\":%q}}}]}}]}\r\n\r\n", call, sequentialToolCallValue())
			}
			body.WriteString("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"\"}]},\"finishReason\":\"STOP\"}]}\r\n\r\n")
			return body.String()
		},
	},
}

// sequentialToolCallServer serves body to every request.
func sequentialToolCallServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
}

// sequentialToolCallStream starts one request of the shape's provider.
func sequentialToolCallStream(tb testing.TB, provider Provider) *AssistantMessageEventStream {
	tb.Helper()
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		tb.Fatal(err)
	}
	return stream
}

// sequentialToolCallStates is the arguments (as ArgumentsJSON writes them) an eager parse gives each prefix of the text every call streams, shortest first. A reader at an event sees the stream state at its own tick, which the producer may already have advanced past the event, so any prefix is a state a call may be read in.
func sequentialToolCallStates(t *testing.T) (text string, states []string) {
	t.Helper()
	text = `{"v":"` + sequentialToolCallValue() + `"}`
	for end := 0; end <= len(text); end++ {
		encoded, err := eagerToolCall(text[:end]).ArgumentsJSON()
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, string(encoded))
	}
	return text, states
}

// Whatever a reader reads of a message that streams many tool calls in a row, at any event, every call holds the arguments of an eager parse of a prefix of its text: a call never goes back to a shorter prefix, a call another follows holds its whole text, and the final message holds the strict parse. A reader that reads twice at one event, or rewrites the copy it owns, changes nothing a later read sees.
func TestSequentialToolCallsReadAsTheEagerParse(t *testing.T) {
	const calls = 7
	text, states := sequentialToolCallStates(t)
	for _, shape := range sequentialToolCallShapes {
		t.Run(shape.name, func(t *testing.T) {
			server := sequentialToolCallServer(shape.body(calls))
			defer server.Close()
			stream := sequentialToolCallStream(t, shape.provider(server.URL))
			longest := map[int]int{}
			events, reads := 0, 0
			// A failed check returns from the consumer's turn: a Fatal inside it would never release the stream.
			failed := false
			check := func(format string, args ...any) {
				t.Errorf(format, args...)
				failed = true
			}
			for _, event := range stream.ObservePublishedEvents(context.Background()) {
				events++
				partial := eventPartial(event)
				if partial == nil {
					continue
				}
				// Reads of one publication share the parse: read each event twice, through an owned copy the consumer then rewrites and through the projection.
				for range 2 {
					observed := partial.Observe()
					projected, _, _ := partial.ObserveProjection(nil)
					if len(projected) != len(observed.Content) {
						check("event %d: projection has %d blocks, observation %d", events, len(projected), len(observed.Content))
						return
					}
					for index, block := range observed.Content {
						call, ok := block.(ToolCall)
						if !ok {
							// Gemini ends its message with an empty text part.
							continue
						}
						encoded, err := call.ArgumentsJSON()
						if err != nil || call.deferredArguments != "" {
							check("event %d call %d: arguments %s (%v), deferred %q", events, index, encoded, err, call.deferredArguments)
							return
						}
						// The shortest prefix this state is the parse of that does not go back from the last read.
						prefix := -1
						for end := longest[index]; end < len(states); end++ {
							if states[end] == string(encoded) {
								prefix = end
								break
							}
						}
						if prefix < 0 {
							check("event %d (%T) call %d: arguments %s are not an eager parse of a prefix of %q from %d bytes on", events, event, index, encoded, text, longest[index])
							return
						}
						longest[index] = prefix
						if index < len(observed.Content)-1 && string(encoded) != states[len(text)] {
							check("event %d (%T) call %d: another call follows, but its arguments are %s, not the whole text's %s", events, event, index, encoded, states[len(text)])
							return
						}
						if !reflect.DeepEqual(projected[index].Arguments, call.Arguments) {
							check("event %d call %d: projection %#v, observation %#v", events, index, projected[index].Arguments, call.Arguments)
							return
						}
						reads++
						// The copy is the consumer's: rewriting it changes nothing a later read sees.
						if call.Arguments != nil {
							call.Arguments["v"] = "rewritten by the consumer"
							call.Arguments["added"] = []any{"x"}
						}
					}
				}
			}
			if failed {
				return
			}
			final := stream.Result()
			finished := 0
			for index, block := range final.Content {
				if call, ok := block.(ToolCall); ok {
					sameArguments(t, fmt.Sprintf("final call %d", index), call, eagerFinalToolCall(text))
					finished++
				}
			}
			if finished != calls {
				t.Fatalf("final message has %d tool calls, want %d", finished, calls)
			}
			if reads == 0 {
				t.Fatal("no event carried a tool call")
			}
		})
	}
}

// sseRecords joins server-sent event records, each ending in a blank line.
func sseRecords(records ...string) string {
	var body strings.Builder
	for _, record := range records {
		body.WriteString(record)
		body.WriteString("\n\n")
	}
	return body.String()
}

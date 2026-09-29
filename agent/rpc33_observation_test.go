package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/agent-loop.ts:408-453. A held sink retains live nested content but the stopReason copied before its await does not advance.
func TestAssistantHeldSinkObservesShallowMessage(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, shape := range []string{"empty", "text", "multiple", "tool", "cancel"} {
			t.Run(string(api)+"/"+shape, func(t *testing.T) {
				t.Parallel()
				// This deadline only bounds a broken admission/result handshake. No assertion depends on elapsed time.
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				release := make(chan struct{})
				releaseBody := sync.OnceFunc(func() { close(release) })
				defer releaseBody()
				body, wantContent := heldSinkFrames(api, shape)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				var provider ai.Provider
				if api == ai.APIOpenAICompletions {
					provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
				} else {
					provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
				}
				defer func() {
					if err := provider.Close(); err != nil {
						t.Error(err)
					}
				}()
				stream, err := provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("probe")}}}), ai.StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				var held AgentMessage
				var first, after []byte
				var result *ai.AssistantMessage
				var ended, persisted *AssistantMessage
				var order []string
				a := NewAgent(AgentOptions{
					OnEvent: func(ev AgentEvent) {
						switch ev := ev.(type) {
						case MessageStartEvent:
							order = append(order, "start")
							held = ev.Message
							first, err = json.Marshal(held)
							if err != nil {
								t.Error(err)
							}
							if shape == "cancel" {
								cancel()
							} else {
								releaseBody()
							}
							// Result must progress independently of the held Agent sink.
							result = stream.Result()
							after, err = json.Marshal(held)
							if err != nil {
								t.Error(err)
							}
							again, encodeErr := json.Marshal(held)
							if encodeErr != nil || !bytes.Equal(after, again) {
								t.Errorf("repeated observation changed: %s / %s, %v", after, again, encodeErr)
							}
						case MessageUpdateEvent:
							order = append(order, "update")
						case MessageEndEvent:
							order = append(order, "end")
							ended = ev.Message.Assistant
						}
					},
					OnMessagePersist: func(message AgentMessage) error { persisted = message.Assistant; return nil },
				})
				// Cancellation drains the provider terminal response just as Agent.abort does.
				message, _, consumeErr := a.consumeStream(context.WithoutCancel(ctx), stream, nil)
				if consumeErr != nil {
					t.Fatal(consumeErr)
				}
				if result == nil || held.Assistant == nil {
					t.Fatalf("missing held start/result: order=%v", order)
				}
				var before, observed AgentMessage
				if err := json.Unmarshal(first, &before); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(after, &observed); err != nil {
					t.Fatal(err)
				}
				if len(before.Assistant.Content) != 0 || before.Assistant.StopReason != ai.StopReasonPending {
					t.Errorf("start before first body = %s; want empty pending", first)
				}
				if observed.Assistant.StopReason != ai.StopReasonPending {
					t.Errorf("held copy stopReason=%s; want pending", observed.Assistant.StopReason)
				}
				// Both providers replace output.usage. The Agent spread retains the previous object, not the latest message's usage.
				if observed.Assistant.Usage.Output != 0 || (shape != "cancel" && result.Usage.Output != 2) {
					t.Errorf("shallow/final usage output=%d/%d; want 0/2 unless canceled", observed.Assistant.Usage.Output, result.Usage.Output)
				}
				actualContent, _ := json.Marshal(observed.Assistant.Content)
				if shape != "cancel" && !jsonEqual(actualContent, []byte(wantContent)) {
					t.Errorf("held content=%s; want %s", actualContent, wantContent)
				}
				wantStop := ai.StopReasonStop
				if shape == "tool" {
					wantStop = ai.StopReasonToolUse
				}
				if shape == "cancel" {
					wantStop = ai.StopReasonAborted
				}
				if message.StopReason != wantStop || result.StopReason != wantStop {
					t.Errorf("terminal stopReason=%s/%s; want %s", message.StopReason, result.StopReason, wantStop)
				}
				if result != stream.Result() {
					t.Error("terminal result identity changed")
				}
				if message != ended || ended != persisted {
					t.Error("message_end, transcript result and persistence must share identity")
				}
				if len(order) < 2 || order[0] != "start" || order[len(order)-1] != "end" {
					t.Errorf("lifecycle order=%v", order)
				}
				final, _ := json.Marshal(AgentMessage{Assistant: persisted})
				for _, scratch := range []string{"partialArgs", "partialJson", "streamIndex", "customInput"} {
					if bytes.Contains(final, []byte(scratch)) || bytes.Contains(after, []byte(scratch)) {
						t.Errorf("finalization retained %s: %s / %s", scratch, final, after)
					}
				}
				var persistedWire AgentMessage
				if err := json.Unmarshal(final, &persistedWire); err != nil {
					t.Fatal(err)
				}
				if shape != "cancel" && !reflect.DeepEqual(observed.Assistant.Content, persistedWire.Assistant.Content) {
					t.Errorf("held nested content differs from final: %s / %s", after, final)
				}
			})
		}
	}
}

// upstream: packages/agent/src/agent-loop.ts:421-453; packages/ai/src/api/openai-completions.ts:425-470; packages/ai/src/api/openai-responses-shared.ts:653-739.
func TestAssistantHeldToolUpdateObservesFinalization(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			release := make(chan struct{})
			releaseTail := sync.OnceFunc(func() { close(release) })
			defer releaseTail()
			first := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"target.txt\\\"}\"}}]},\"finish_reason\":null}]}\n\n"
			tail := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
			scratch := "partialArgs"
			if api == ai.APIOpenAIResponses {
				first = "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc\",\"call_id\":\"call\",\"name\":\"read\",\"arguments\":\"\"}}\n\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"path\\\":\\\"target.txt\\\"}\"}\n\n"
				tail = "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc\",\"call_id\":\"call\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"target.txt\\\"}\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
				scratch = "partialJson"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, first)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, tail)
			}))
			defer server.Close()
			var provider ai.Provider
			if api == ai.APIOpenAICompletions {
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			} else {
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			}
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			stream, err := provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("probe")}}}), ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			observed := false
			a := NewAgent(AgentOptions{OnEvent: func(event AgentEvent) {
				update, ok := event.(MessageUpdateEvent)
				if !ok {
					return
				}
				delta, ok := update.AssistantMessageEvent.(ai.ToolCallDeltaEvent)
				if !ok {
					return
				}
				observed = true
				before, encodeErr := json.Marshal(update.Message)
				if encodeErr != nil {
					t.Error(encodeErr)
					return
				}
				if !bytes.Contains(before, []byte(`"`+scratch+`":"{\"path\":\"target.txt\"}"`)) {
					t.Errorf("pre-await missing scratch: %s", before)
				}
				releaseTail()
				final := stream.Result()
				after, encodeErr := json.Marshal(update.Message)
				if encodeErr != nil {
					t.Error(encodeErr)
					return
				}
				if bytes.Contains(after, []byte(scratch)) {
					t.Errorf("held update retained finalized scratch: %s", after)
				}
				if update.Message.Assistant.Observe().StopReason != ai.StopReasonPending || delta.Partial.Observe().StopReason != ai.StopReasonToolUse || final.StopReason != ai.StopReasonToolUse {
					t.Errorf("shallow/full/result stopReason = %s/%s/%s", update.Message.Assistant.Observe().StopReason, delta.Partial.Observe().StopReason, final.StopReason)
				}
				call := update.Message.Assistant.Observe().Content[0].(ai.ToolCall)
				if call.Arguments["path"] != "target.txt" {
					t.Errorf("held final arguments=%v", call.Arguments)
				}
			}})
			if _, _, err := a.consumeStream(context.WithoutCancel(ctx), stream, nil); err != nil {
				t.Fatal(err)
			}
			if !observed {
				t.Fatal("provider never reached tool delta sink")
			}
		})
	}
}

// upstream: packages/agent/src/agent-loop.ts:421-433. The embedded provider event is not the shallow Agent message.
func TestAssistantUpdateRetainsFullProviderEvent(t *testing.T) {
	partial := &ai.AssistantMessage{
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "one"}},
		StopReason: ai.StopReasonPending,
	}
	event := ai.TextDeltaEvent{ContentIndex: 0, Delta: "one", Partial: partial}
	var before, after, providerAfter AgentMessage
	var retained *AssistantMessage
	a := NewAgent(AgentOptions{OnEvent: func(ev AgentEvent) {
		update := ev.(MessageUpdateEvent)
		retained = update.Message.Assistant
		encoded, err := json.Marshal(update.Message)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &before); err != nil {
			t.Fatal(err)
		}
		// This is one synchronous producer segment between two observations. No exported field is changed concurrently with a reader.
		partial.Content[0] = ai.TextContent{Text: "one two"}
		partial.StopReason = ai.StopReasonStop
		encoded, err = json.Marshal(update.Message)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &after); err != nil {
			t.Fatal(err)
		}
		provider := update.AssistantMessageEvent.(ai.TextDeltaEvent).Partial
		if provider != partial {
			t.Fatal("embedded event lost full provider identity")
		}
		providerAfter = AgentMessage{Assistant: agentAssistantMessage(provider)}
	}})
	a.emitAssistantUpdate(nil, partial, event)
	if before.Assistant.Content[0].(ai.TextContent).Text != "one" || after.Assistant.Content[0].(ai.TextContent).Text != "one two" {
		t.Fatalf("retained nested content did not advance: before=%+v after=%+v", before.Assistant.Content, after.Assistant.Content)
	}
	if after.Assistant.StopReason != ai.StopReasonPending || providerAfter.Assistant.StopReason != ai.StopReasonStop {
		t.Fatalf("shallow/full stopReason=%s/%s; want pending/stop", after.Assistant.StopReason, providerAfter.Assistant.StopReason)
	}
	owned := retained.Observe()
	llm := retained.LLMMessage()
	clone := (AgentMessage{Assistant: retained}).Clone()
	partial.Content[0] = ai.TextContent{Text: "later"}
	for _, content := range [][]ai.AssistantContentBlock{owned.Content, llm.Content, clone.Assistant.Content} {
		if content[0].(ai.TextContent).Text != "one two" {
			t.Fatalf("owned observation changed: %+v", content)
		}
	}
}

func jsonEqual(a, b []byte) bool {
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}

func heldSinkFrames(api ai.API, shape string) (string, string) {
	if api == ai.APIOpenAICompletions {
		var chunks []string
		want := `[]`
		switch shape {
		case "text":
			chunks = []string{`{"choices":[{"delta":{"content":"one"},"finish_reason":null}]}`, `{"choices":[{"delta":{"content":" two"},"finish_reason":"stop"}]}`}
			want = `[{"type":"text","text":"one two"}]`
		case "multiple":
			chunks = []string{`{"choices":[{"delta":{"reasoning_content":"reason"},"finish_reason":null}]}`, `{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`}
			want = `[{"type":"thinking","thinking":"reason","thinkingSignature":"reasoning_content"},{"type":"text","text":"answer"}]`
		case "tool":
			chunks = []string{`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-r2","type":"function","function":{"name":"read","arguments":"{\"path\":\"target.txt\"}"}}]},"finish_reason":"tool_calls"}]}`}
			want = `[{"type":"toolCall","id":"call-r2","name":"read","arguments":{"path":"target.txt"}}]`
		default:
			chunks = []string{`{"choices":[{"delta":{},"finish_reason":"stop"}]}`}
		}
		var out strings.Builder
		for _, chunk := range chunks {
			fmt.Fprintf(&out, "data: %s\n\n", chunk)
		}
		out.WriteString("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n")
		return out.String(), want
	}
	events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "response-r2"}}}
	want := `[]`
	switch shape {
	case "tool":
		item := map[string]any{"type": "function_call", "id": "fc_r2", "call_id": "call-r2", "name": "read", "arguments": ""}
		events = append(events, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item}, map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "delta": `{"path":"target.txt"}`}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_r2", "call_id": "call-r2", "name": "read", "arguments": `{"path":"target.txt"}`}})
		want = `[{"type":"toolCall","id":"call-r2|fc_r2","name":"read","arguments":{"path":"target.txt"}}]`
	case "text", "multiple":
		if shape == "multiple" {
			events = append(events, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs_r2", "summary": []any{}}}, map[string]any{"type": "response.reasoning_summary_text.delta", "output_index": 0, "delta": "reason"}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs_r2", "summary": []any{map[string]any{"type": "summary_text", "text": "reason"}}}})
		}
		index := 0
		if shape == "multiple" {
			index = 1
		}
		item := map[string]any{"type": "message", "id": "msg_r2", "role": "assistant", "content": []any{}}
		events = append(events, map[string]any{"type": "response.output_item.added", "output_index": index, "item": item}, map[string]any{"type": "response.output_text.delta", "output_index": index, "delta": "one"}, map[string]any{"type": "response.output_text.delta", "output_index": index, "delta": " two"}, map[string]any{"type": "response.output_item.done", "output_index": index, "item": map[string]any{"type": "message", "id": "msg_r2", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "one two", "annotations": []any{}}}}})
		want = `[{"type":"text","text":"one two","textSignature":"{\"v\":1,\"id\":\"msg_r2\"}"}]`
		if shape == "multiple" {
			want = `[{"type":"thinking","thinking":"reason","thinkingSignature":"{\"id\":\"rs_r2\",\"summary\":[{\"text\":\"reason\",\"type\":\"summary_text\"}],\"type\":\"reasoning\"}"},{"type":"text","text":"one two","textSignature":"{\"v\":1,\"id\":\"msg_r2\"}"}]`
		}
	}
	events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{"id": "response-r2", "status": "completed", "usage": map[string]any{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}}})
	var out strings.Builder
	for _, event := range events {
		raw, _ := json.Marshal(event)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
	return out.String(), want
}

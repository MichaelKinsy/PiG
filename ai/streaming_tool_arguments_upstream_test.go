package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// upstream: packages/ai/test/streaming-tool-arguments.test.ts (#9265): every provider re-parsed the accumulated tool-call arguments on each delta and kept the raw text on the content block, where it reached partial messages, Durable commits and transcripts.

// streamingArgumentContent is the file content of a write call: 20,000 lines with a backslash and a quote in each.
var streamingArgumentContent = func() string {
	var content strings.Builder
	for line := range 20_000 {
		fmt.Fprintf(&content, "const line%d = \"value %d\"; // \\ done\n", line, line)
	}
	return content.String()
}()

// streamingWriteCall is write arguments with size bytes of file content and their JSON text.
func streamingWriteCall(t testing.TB, size int) (arguments JsonObject, text string) {
	t.Helper()
	content := streamingArgumentContent[:size]
	arguments = JsonObject{"path": "/src/file.ts", "content": content}
	// JSON.stringify keeps the members in the order they were written: path first.
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return arguments, `{"path":"/src/file.ts","content":` + string(encoded) + `}`
}

func streamingDeltas(text string, size int) []string {
	var deltas []string
	for index := 0; index < len(text); index += size {
		deltas = append(deltas, text[index:min(index+size, len(text))])
	}
	return deltas
}

func streamingSSE(event string, data any) string {
	encoded, _ := json.Marshal(data)
	if event != "" {
		return "event: " + event + "\ndata: " + string(encoded) + "\n\n"
	}
	return "data: " + string(encoded) + "\n\n"
}

type streamingArgumentsPath struct {
	name     string
	provider func(url string) Provider
	// body streams one write call whose argument text arrives as deltas; arguments are the final arguments and text their JSON.
	body func(deltas []string, arguments JsonObject, text string) string
}

var streamingArgumentsPaths = []streamingArgumentsPath{
	{
		name: "anthropic-messages",
		provider: func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "sk-ant-test", Model: "claude-test"})
		},
		body: func(deltas []string, _ JsonObject, _ string) string {
			return anthropicStreamingBody(deltas, true)
		},
	},
	{
		name: "openai-completions",
		provider: func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "sk-test", Model: "gpt-test", ProviderID: "openai"})
		},
		body: func(deltas []string, _ JsonObject, _ string) string {
			return chatStreamingBody(deltas, false)
		},
	},
	{
		name: "openai-responses",
		provider: func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "sk-test", Model: "gpt-test", ProviderID: "openai"})
		},
		body: responsesStreamingBody,
	},
	{
		name: "pi-messages",
		provider: func(url string) Provider {
			return NewPiMessagesProvider(PiMessagesConfig{BaseURL: url + "/v1", APIKey: "test", Model: "auto", ProviderID: "radius"})
		},
		body: piMessagesStreamingBody,
	},
}

func anthropicStreamingBody(deltas []string, stop bool) string {
	var body strings.Builder
	body.WriteString(streamingSSE("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_1", "model": "claude-test", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}}))
	body.WriteString(streamingSSE("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": "write", "input": map[string]any{}}}))
	for _, delta := range deltas {
		body.WriteString(streamingSSE("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": delta}}))
	}
	if stop {
		body.WriteString(streamingSSE("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}))
		body.WriteString(streamingSSE("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 10}}))
		body.WriteString(streamingSSE("message_stop", map[string]any{"type": "message_stop"}))
	}
	return body.String()
}

func chatStreamingBody(deltas []string, idEveryChunk bool) string {
	var body strings.Builder
	base := func(choices any) map[string]any {
		return map[string]any{"id": "chat_1", "object": "chat.completion.chunk", "created": 1, "model": "test", "choices": choices}
	}
	call := func(fields map[string]any) map[string]any {
		fields["index"] = 0
		return base([]any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{fields}}}})
	}
	body.WriteString(streamingSSE("", call(map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "write", "arguments": ""}})))
	for _, delta := range deltas {
		if idEveryChunk {
			body.WriteString(streamingSSE("", call(map[string]any{"id": "call_1", "function": map[string]any{"name": "write", "arguments": delta}})))
		} else {
			body.WriteString(streamingSSE("", call(map[string]any{"function": map[string]any{"arguments": delta}})))
		}
	}
	finish := base([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}})
	finish["usage"] = map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	body.WriteString(streamingSSE("", finish))
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

func responsesStreamingBody(deltas []string, _ JsonObject, text string) string {
	var body strings.Builder
	item := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "write", "arguments": "", "status": "in_progress"}
	body.WriteString(streamingSSE("", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_1"}}))
	body.WriteString(streamingSSE("", map[string]any{"type": "response.output_item.added", "output_index": 0, "sequence_number": 0, "item": item}))
	for _, delta := range deltas {
		body.WriteString(streamingSSE("", map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_1", "sequence_number": 0, "delta": delta}))
	}
	body.WriteString(streamingSSE("", map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "item_id": "fc_1", "sequence_number": 0, "arguments": text}))
	done := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "write", "arguments": text, "status": "completed"}
	body.WriteString(streamingSSE("", map[string]any{"type": "response.output_item.done", "output_index": 0, "sequence_number": 0, "item": done}))
	body.WriteString(streamingSSE("", map[string]any{"type": "response.completed", "sequence_number": 0, "response": map[string]any{"id": "resp_1", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}))
	return body.String()
}

func piMessagesStreamingBody(deltas []string, arguments JsonObject, _ string) string {
	var body strings.Builder
	body.WriteString(streamingSSE("", map[string]any{"type": "start"}))
	body.WriteString(streamingSSE("", map[string]any{"type": "toolcall_start", "contentIndex": 0, "id": "call_1", "toolName": "write"}))
	for _, delta := range deltas {
		body.WriteString(streamingSSE("", map[string]any{"type": "toolcall_delta", "contentIndex": 0, "delta": delta}))
	}
	body.WriteString(streamingSSE("", map[string]any{"type": "toolcall_end", "contentIndex": 0, "toolCall": map[string]any{"type": "toolCall", "id": "call_1", "name": "write", "arguments": arguments}}))
	body.WriteString(streamingSSE("", map[string]any{"type": "done", "reason": "toolUse", "usage": map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}))
	return body.String()
}

// streamingBlockKeys are the members a content block may carry; anything else is stream state leaking into partial messages.
var streamingBlockKeys = map[string][]string{
	"text":     {"type", "text", "textSignature"},
	"thinking": {"type", "thinking", "thinkingSignature", "redacted"},
	"toolCall": {"type", "id", "name", "arguments", "argumentsError", "thoughtSignature", "namespace"},
}

func expectNoStreamState(t *testing.T, message *AssistantMessage, label string) {
	t.Helper()
	encoded, err := json.Marshal(message.Observe())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, block := range decoded.Content {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		for key := range block {
			if !slices.Contains(streamingBlockKeys[kind], key) {
				t.Fatalf("%s: %s.%s is stream state in a partial message", label, kind, key)
			}
		}
	}
}

// streamPath streams one write call through path and returns every event with the result.
func streamPath(t testing.TB, path streamingArgumentsPath, deltas []string, arguments JsonObject, text string, onEvent func(AssistantMessageEvent)) *AssistantMessage {
	t.Helper()
	server := sequentialToolCallServer(path.body(deltas, arguments, text))
	defer server.Close()
	stream := sequentialToolCallStream(t, path.provider(server.URL))
	for event := range stream.Events(context.Background()) {
		if onEvent != nil {
			onEvent(event)
		}
	}
	return stream.Result()
}

func TestStreamingToolArgumentsShowGrowingPartialsKeepStreamStateOffEveryPartialAndFinishWithTheExactArguments(t *testing.T) {
	for _, path := range streamingArgumentsPaths {
		t.Run(path.name, func(t *testing.T) {
			arguments, text := streamingWriteCall(t, 5_000)
			shown, partials := -1, 0
			message := streamPath(t, path, streamingDeltas(text, 24), arguments, text, func(event AssistantMessageEvent) {
				partial := eventPartial(event)
				if partial == nil {
					return
				}
				expectNoStreamState(t, partial, string(event.EventType()))
				if event.EventType() != EventToolCallDelta {
					return
				}
				observed := partial.Observe()
				delta := event.(ToolCallDeltaEvent)
				call, ok := observed.Content[delta.ContentIndex].(ToolCall)
				if !ok {
					return
				}
				content, isString := call.Arguments["content"].(string)
				if !isString {
					return
				}
				partials++
				if !strings.HasPrefix(arguments["content"].(string), content) || len(content) < shown {
					t.Fatalf("shown content of %d bytes is not a growing prefix of the file", len(content))
				}
				shown = len(content)
			})
			if message.StopReason != StopReasonToolUse {
				t.Fatalf("stop reason %s: %s", message.StopReason, message.ErrorMessage)
			}
			if partials <= 100 {
				t.Fatalf("%d partials showed the content, want more than 100", partials)
			}
			call, ok := message.Content[0].(ToolCall)
			if len(message.Content) != 1 || !ok || call.Name != "write" || call.ID == "" || !equalArguments(call.Arguments, arguments) {
				t.Fatalf("content = %#v", message.Content)
			}
		})
	}
}

func equalArguments(a, b JsonObject) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right) || reflectJSONEqual(left, right)
}

func reflectJSONEqual(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && fmt.Sprint(left) == fmt.Sprint(right)
}

func TestStreamingToolArgumentsLeaveNoStreamStateOnTheErrorMessageOfAStreamCutOffMidCall(t *testing.T) {
	_, text := streamingWriteCall(t, 2_000)
	cut := streamingDeltas(text, 16)[:20]
	server := sequentialToolCallServer(anthropicStreamingBody(cut, false))
	defer server.Close()
	stream := sequentialToolCallStream(t, NewAnthropicProvider(AnthropicConfig{BaseURL: server.URL, APIKey: "sk-ant-test", Model: "claude-test"}))
	for range stream.Events(context.Background()) {
	}
	message := stream.Result()
	if message.StopReason != StopReasonError {
		t.Fatalf("stop reason %s, want error", message.StopReason)
	}
	expectNoStreamState(t, message, "error")
	if call, ok := message.Content[0].(ToolCall); !ok || call.Arguments["path"] != "/src/file.ts" {
		t.Fatalf("content = %#v", message.Content)
	}
}

// faux.ts streamWithDeltas feeds each chunk of a tool call's arguments to a ToolCallArgumentsStream, so consumers see the partial arguments grow.
func TestFauxToolCallStreamsGrowingPartialArguments(t *testing.T) {
	arguments, _ := streamingWriteCall(t, 5_000)
	provider := NewFauxProvider(FauxConfig{TokenSize: &FauxTokenSize{Min: new(8), Max: new(8)}})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
		Content:    []FauxContentBlock{FauxToolCall("write", map[string]any(arguments), &FauxToolCallOptions{ID: "call_1"})},
		StopReason: "toolUse",
	})})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("write")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	shown, partials := -1, 0
	for event := range stream.Events(t.Context()) {
		partial := eventPartial(event)
		if partial == nil {
			continue
		}
		expectNoStreamState(t, partial, string(event.EventType()))
		delta, ok := event.(ToolCallDeltaEvent)
		if !ok {
			continue
		}
		call, ok := partial.Observe().Content[delta.ContentIndex].(ToolCall)
		if content, isString := call.Arguments["content"].(string); ok && isString {
			partials++
			if !strings.HasPrefix(arguments["content"].(string), content) || len(content) < shown {
				t.Fatalf("shown content of %d bytes is not a growing prefix of the file", len(content))
			}
			shown = len(content)
		}
	}
	message := stream.Result()
	if message.StopReason != StopReasonToolUse || partials < 10 {
		t.Fatalf("stop reason %s, %d partials showed the content", message.StopReason, partials)
	}
	if call, ok := message.Content[0].(ToolCall); !ok || !equalArguments(call.Arguments, arguments) {
		t.Fatalf("content = %#v", message.Content)
	}
}

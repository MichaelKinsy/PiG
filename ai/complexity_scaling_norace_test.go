//go:build !race

package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gates for the per-delta and per-turn provider paths. Each gate measures the path at n and 10n and fails when allocations or allocated bytes grow faster than the declared bound.

func scalingSSEClient(body string) *http.Client {
	return &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

func scalingOpenAIModel(api API) *Model {
	return &Model{ID: "scaling-model", DisplayName: "Scaling", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}, ProviderMeta: ProviderMetadata{API: api, ProviderID: "scaling", BaseURL: "http://scaling.invalid/v1"}}
}

// scalingHistory is n user/assistant exchanges, the second half with a tool call and its result.
func scalingHistory(n int) Context {
	messages := make([]Message, 0, 3*n+1)
	for i := range n {
		messages = append(messages, UserMessage{Content: UserText(fmt.Sprintf("question %d about the repository layout", i)), Timestamp: int64(i)})
		if i%2 == 0 {
			messages = append(messages, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: fmt.Sprintf("answer %d: the package keeps one file per upstream module", i)}}, API: APIOpenAICompletions, Provider: "scaling", Model: "scaling-model", StopReason: StopReasonStop, Timestamp: int64(i)})
			continue
		}
		call := fmt.Sprintf("call_%d", i)
		messages = append(messages,
			AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: call, Name: "read", Arguments: JsonObject{"path": fmt.Sprintf("file%d.go", i)}}}, API: APIOpenAICompletions, Provider: "scaling", Model: "scaling-model", StopReason: StopReasonToolUse, Timestamp: int64(i)},
			ToolResultMessage{ToolCallID: call, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "package main\n\nfunc main() {}\n"}}, Timestamp: int64(i)},
		)
	}
	messages = append(messages, UserMessage{Content: UserText("next"), Timestamp: int64(n)})
	return Context{SystemPrompt: "You are a scaling probe.", Messages: messages}
}

func scalingDrain(stream *AssistantMessageEventStream, err error) {
	if err != nil {
		panic(err)
	}
	for range stream.Events(context.Background()) {
	}
	if result := stream.Result(); result.StopReason == StopReasonError {
		panic(result.ErrorMessage)
	}
}

// scalingDeltaPadding makes each streamed delta long enough that re-copying the accumulated text or arguments on each delta outweighs the fixed per-event cost at the gated sizes, so a quadratic regression exceeds the linear bound by a clear margin rather than at its edge.
const scalingDeltaPadding = "of a streamed answer, padded so that copying the text so far dominates the per-event cost"

func openAITextBody(deltas int) string {
	var body strings.Builder
	for i := range deltas {
		fmt.Fprintf(&body, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"word%d %s\"}}]}\n\n", i%10, scalingDeltaPadding)
	}
	body.WriteString("data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
	return body.String()
}

func openAIToolArgumentsBody(deltas int) string {
	var body strings.Builder
	body.WriteString("data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\",\\\"content\\\":\\\"\"}}]}}]}\n\n")
	for i := range deltas {
		fmt.Fprintf(&body, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"line %d %s\\\\n\"}}]}}]}\n\n", i%10, scalingDeltaPadding)
	}
	body.WriteString("data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"}\"}}]}}]}\n\n")
	body.WriteString("data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	return body.String()
}

func anthropicTextBody(deltas int) string {
	var body strings.Builder
	body.WriteString(anthropicFixtureEvent("message_start", `{"message":{"id":"msg","usage":{"input_tokens":12,"output_tokens":0}}}`))
	body.WriteString(anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":""}}`))
	for i := range deltas {
		body.WriteString(anthropicFixtureEvent("content_block_delta", fmt.Sprintf(`{"index":0,"delta":{"type":"text_delta","text":"word%d %s"}}`, i%10, scalingDeltaPadding)))
	}
	body.WriteString(anthropicFixtureEvent("content_block_stop", `{"index":0}`))
	body.WriteString(anthropicFixtureEnd("end_turn"))
	return body.String()
}

func anthropicToolArgumentsBody(deltas int) string {
	var body strings.Builder
	body.WriteString(anthropicFixtureEvent("message_start", `{"message":{"id":"msg","usage":{"input_tokens":12,"output_tokens":0}}}`))
	body.WriteString(anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write","input":{}}}`))
	body.WriteString(anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\",\"content\":\""}}`))
	for i := range deltas {
		body.WriteString(anthropicFixtureEvent("content_block_delta", fmt.Sprintf(`{"index":0,"delta":{"type":"input_json_delta","partial_json":"line %d %s\\n"}}`, i%10, scalingDeltaPadding)))
	}
	body.WriteString(anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"input_json_delta","partial_json":"\"}"}}`))
	body.WriteString(anthropicFixtureEvent("content_block_stop", `{"index":0}`))
	body.WriteString(anthropicFixtureEnd("tool_use"))
	return body.String()
}

func responsesTextBody(deltas int) string {
	var body strings.Builder
	body.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
	body.WriteString("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]}}\n\n")
	body.WriteString("data: {\"type\":\"response.content_part.added\",\"output_index\":0,\"item_id\":\"msg_1\",\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\n")
	var text strings.Builder
	for i := range deltas {
		delta := fmt.Sprintf("word%d %s", i%10, scalingDeltaPadding)
		text.WriteString(delta)
		fmt.Fprintf(&body, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"item_id\":\"msg_1\",\"content_index\":0,\"delta\":%q}\n\n", delta)
	}
	fmt.Fprintf(&body, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n\n", text.String())
	body.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}}\n\n")
	return body.String()
}

// A streamed message costs O(1) per delta: the text and the tool-call arguments accumulate in builders and the arguments parser resumes where the previous delta ended.
func TestScalingProviderStreamPerDelta(t *testing.T) {
	small := userTranscript("hi")
	for _, tc := range []struct {
		name string
		api  API
		body func(int) string
	}{
		{"openai-completions text deltas", APIOpenAICompletions, openAITextBody},
		{"openai-completions tool-call argument deltas", APIOpenAICompletions, openAIToolArgumentsBody},
		{"openai-responses text deltas", APIOpenAIResponses, responsesTextBody},
	} {
		testscaling.Check(t, testscaling.Case{Name: tc.name, Size: 300, Bound: testscaling.Linear, Prepare: func(n int) func() {
			client := scalingSSEClient(tc.body(n))
			model := scalingOpenAIModel(tc.api)
			return func() {
				scalingDrain(StreamSimple(context.Background(), model, small, StreamOptions{APIKey: "test", Fetch: client}))
			}
		}})
	}
	for _, tc := range []struct {
		name string
		body func(int) string
	}{
		{"anthropic-messages text deltas", anthropicTextBody},
		{"anthropic-messages tool-call argument deltas", anthropicToolArgumentsBody},
	} {
		testscaling.Check(t, testscaling.Case{Name: tc.name, Size: 300, Bound: testscaling.Linear, Prepare: func(n int) func() {
			body := tc.body(n)
			provider := &anthropicProvider{cfg: AnthropicConfig{Model: "claude-haiku-4-5", ProviderID: "anthropic"}}
			return func() {
				builder := newAssistantStreamBuilder(context.Background(), APIAnthropicMessages, "anthropic", "claude-haiku-4-5")
				provider.parseAnthropicSSE(context.Background(), strings.NewReader(body), builder, anthropicStreamNames{})
				scalingDrain(builder.stream, nil)
			}
		}})
	}
}

// A provider request converts and encodes the whole transcript once per turn, which is linear in the history.
func TestScalingProviderRequestPerTurn(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		testscaling.Check(t, testscaling.Case{Name: string(api) + " request at history n", Size: 40, Bound: testscaling.Linear, Prepare: func(n int) func() {
			transcript := NormalizeContext(scalingHistory(n))
			client := scalingSSEClient(map[API]string{APIOpenAICompletions: openAITextBody(1), APIOpenAIResponses: responsesTextBody(1)}[api])
			model := scalingOpenAIModel(api)
			return func() {
				scalingDrain(StreamSimple(context.Background(), model, transcript, StreamOptions{APIKey: "test", Fetch: client}))
			}
		}})
	}
	testscaling.Check(t, testscaling.Case{Name: "anthropic-messages request at history n", Size: 40, Bound: testscaling.Linear, Prepare: func(n int) func() {
		transcript := NormalizeContext(scalingHistory(n))
		body := anthropicTextBody(1)
		model := &Model{ID: "claude-haiku-4-5", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "anthropic", BaseURL: "http://scaling.invalid"}}
		client := scalingSSEClient(body)
		return func() {
			scalingDrain(StreamSimple(context.Background(), model, transcript, StreamOptions{APIKey: "test", Fetch: client}))
		}
	}})
}

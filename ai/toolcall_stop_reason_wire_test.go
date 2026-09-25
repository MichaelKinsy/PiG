package ai

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func assertWireStopReason(t *testing.T, stream *AssistantMessageEventStream, want StopReason) {
	t.Helper()
	result := stream.Result()
	if result.StopReason != want {
		t.Fatalf("message stop reason = %q, want %q", result.StopReason, want)
	}
	if len(result.Content) != 1 {
		t.Fatalf("message content = %#v, want one tool call", result.Content)
	}
	if _, ok := result.Content[0].(ToolCall); !ok {
		t.Fatalf("message content[0] = %T, want ToolCall", result.Content[0])
	}
	var terminal StopReason
	for event := range stream.Events(context.Background()) {
		if done, ok := event.(DoneEvent); ok {
			terminal = done.Reason
		}
	}
	if terminal != want {
		t.Fatalf("terminal stop reason = %q, want %q", terminal, want)
	}
}

// Upstream OpenAI Completions maps the provider finish reason directly even
// when the streamed content contains tool calls. The agent loop independently
// derives continuation from that content.
func TestOpenAICompletionsWireToolCallsPreserveStop(t *testing.T) {
	for _, providerID := range []string{"openai", "github-copilot"} {
		for _, finishReason := range []string{"stop", "end"} {
			t.Run(providerID+"/"+finishReason, func(t *testing.T) {
				wire := fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]},"finish_reason":%q}]}

`, finishReason) +
					"data: [DONE]\n\n"
				provider := &openAIProvider{}
				builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, providerID, "model")
				provider.parseSSE(context.Background(), strings.NewReader(wire), builder, nil)
				assertWireStopReason(t, builder.stream, StopReasonStop)
			})
		}
	}
}

// OpenAI Responses is shared by stock OpenAI, Copilot Responses, Azure, and
// Codex. Each route must preserve the function-call-over-completed rule.
func TestOpenAIResponsesWireToolCallsOverrideCompleted(t *testing.T) {
	tests := []struct {
		name       string
		api        API
		providerID string
	}{
		{name: "openai", api: APIOpenAIResponses, providerID: "openai"},
		{name: "github-copilot", api: APIOpenAIResponses, providerID: "github-copilot"},
		{name: "azure", api: APIAzureOpenAIResponses, providerID: "azure-openai-responses"},
		{name: "codex", api: APIOpenAICodexResponses, providerID: "openai-codex"},
	}
	for _, test := range tests {
		for _, status := range []string{"", "completed", "in_progress", "queued"} {
			name := status
			if name == "" {
				name = "missing-status"
			}
			t.Run(test.name+"/"+name, func(t *testing.T) {
				wire := fmt.Sprintf(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":"{}"}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":"{}"}}

data: {"type":"response.completed","response":{"id":"response-1","status":%q}}

`, status)
				provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: test.providerID, Model: "model"}}
				builder := newAssistantStreamBuilder(context.Background(), test.api, test.providerID, "model")
				provider.parseResponsesSSE(context.Background(), strings.NewReader(wire), builder, nil)
				assertWireStopReason(t, builder.stream, StopReasonToolUse)
			})
		}
	}
}

func TestAnthropicWireToolCallsPreserveSuccessfulStopReason(t *testing.T) {
	for _, providerID := range []string{"anthropic", "github-copilot"} {
		for _, stopReason := range []string{"end_turn", "stop_sequence", "pause_turn"} {
			t.Run(providerID+"/"+stopReason, func(t *testing.T) {
				wire := fmt.Sprintf(`event: message_start
data: {"type":"message_start","message":{"id":"message-1","model":"claude-test","usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"read","input":{}}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`, stopReason)
				provider := &anthropicProvider{cfg: AnthropicConfig{ProviderID: providerID, Model: "claude-test"}}
				builder := newAssistantStreamBuilder(context.Background(), APIAnthropicMessages, providerID, "claude-test")
				provider.parseAnthropicSSE(context.Background(), strings.NewReader(wire), builder, anthropicStreamNames{})
				assertWireStopReason(t, builder.stream, StopReasonStop)
			})
		}
	}
}

func TestGoogleWireFunctionCallOverridesStop(t *testing.T) {
	tests := []struct {
		name       string
		api        API
		providerID string
	}{
		{name: "generative-ai", api: APIGoogleGenerativeAI, providerID: "google"},
		{name: "vertex", api: APIGoogleVertex, providerID: "google-vertex"},
	}
	const wire = `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"read","args":{}}}]},"finishReason":"STOP"}]}
`
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &googleProvider{cfg: GoogleConfig{ProviderID: test.providerID, Model: "gemini-test"}}
			builder := newAssistantStreamBuilder(context.Background(), test.api, test.providerID, "gemini-test")
			provider.parseGeminiSSE(context.Background(), strings.NewReader(wire), builder)
			assertWireStopReason(t, builder.stream, StopReasonToolUse)
		})
	}
}

func TestMistralWireToolCallsPreserveStop(t *testing.T) {
	wire := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read","arguments":"{}"}}]},"finish_reason":"stop"}]}

data: [DONE]

`
	provider := &mistralProvider{cfg: MistralConfig{ProviderID: "mistral", Model: "mistral-test"}}
	builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "mistral-test")
	provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader(wire)), builder)
	assertWireStopReason(t, builder.stream, StopReasonStop)
}

func TestBedrockWireToolCallsPreserveSuccessfulStopReason(t *testing.T) {
	for _, stopReason := range []btypes.StopReason{btypes.StopReasonEndTurn, btypes.StopReasonStopSequence} {
		t.Run(string(stopReason), func(t *testing.T) {
			result, events := runBedrockEvents(t,
				&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
				&btypes.ConverseStreamOutputMemberContentBlockStart{Value: btypes.ContentBlockStartEvent{
					ContentBlockIndex: aws.Int32(0),
					Start:             &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{ToolUseId: aws.String("call-1"), Name: aws.String("read")}},
				}},
				&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{
					ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(`{}`)}},
				}},
				&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
				&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: stopReason}},
			)
			if result.StopReason != StopReasonStop {
				t.Fatalf("message stop reason = %q, want %q", result.StopReason, StopReasonStop)
			}
			if done, ok := events[len(events)-1].(DoneEvent); !ok || done.Reason != StopReasonStop {
				t.Fatalf("terminal event = %#v, want stop done", events[len(events)-1])
			}
		})
	}
}

func TestPiMessagesWireToolCallPreservesStop(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{
		map[string]any{"type": "toolcall_start", "contentIndex": 0, "id": "call-1", "toolName": "read"},
		map[string]any{"type": "toolcall_end", "contentIndex": 0, "toolCall": map[string]any{"type": "toolCall", "id": "call-1", "name": "read", "arguments": map[string]any{}}},
		map[string]any{"type": "done", "reason": "stop", "usage": piMessagesTestUsage},
	}})
	provider := newTestPiMessagesProvider(baseURL, nil)
	stream, err := provider.Stream(context.Background(), piMessagesTestContext(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertWireStopReason(t, stream, StopReasonStop)
}

func TestToolCallDonePreservesProviderTerminalReasons(t *testing.T) {
	for _, reason := range []StopReason{StopReasonStop, StopReasonToolUse, StopReasonLength, StopReasonDeferred} {
		t.Run(fmt.Sprintf("done_%s", reason), func(t *testing.T) {
			message := testAssistant(reason)
			message.Content = []AssistantContentBlock{ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{}}}
			stream := NewAssistantMessageEventStream()
			if err := stream.Push(StartEvent{Partial: testAssistant(StopReasonPending)}); err != nil {
				t.Fatal(err)
			}
			if err := stream.Push(DoneEvent{Reason: reason, Message: message}); err != nil {
				t.Fatal(err)
			}
			if stream.Result().StopReason != reason {
				t.Fatalf("stop reason = %q, want %q", stream.Result().StopReason, reason)
			}
		})
	}
	for _, reason := range []StopReason{StopReasonError, StopReasonAborted} {
		t.Run(fmt.Sprintf("error_%s", reason), func(t *testing.T) {
			message := testAssistant(reason)
			message.Content = []AssistantContentBlock{ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{}}}
			stream := NewAssistantMessageEventStream()
			if err := stream.Push(ErrorEvent{Reason: reason, Error: message}); err != nil {
				t.Fatal(err)
			}
			if stream.Result().StopReason != reason {
				t.Fatalf("stop reason = %q, want %q", stream.Result().StopReason, reason)
			}
		})
	}
}

//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

const upstreamBedrockRedactedBase64 = "cnNuXzVaVnJpZjRKMGJYSXFtV2RsZWRqN1FJRmVOaWtSUWJF"

func bedrockRedactedDelta(index int32, data []byte) btypes.ConverseStreamOutput {
	return &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(index), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: data}}}}
}

func upstreamBedrockRedactedBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(upstreamBedrockRedactedBase64)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func upstreamBedrockRedactedEvents(t *testing.T) []btypes.ConverseStreamOutput {
	t.Helper()
	return []btypes.ConverseStreamOutput{
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		bedrockRedactedDelta(0, upstreamBedrockRedactedBytes(t)),
		&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(1), Delta: &btypes.ContentBlockDeltaMemberText{Value: "done"}}},
		&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)}},
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}},
	}
}

func assertBedrockRedactedThinking(t *testing.T, result *AssistantMessage) ThinkingContent {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatalf("missing thinking: %+v", result)
	}
	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("first block = %T, want thinking", result.Content[0])
	}
	if !thinking.Redacted || thinking.ThinkingSignature != upstreamBedrockRedactedBase64 {
		t.Fatalf("thinking = %+v", thinking)
	}
	raw, err := json.Marshal(thinking)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"redactedChunks", "index"} {
		if _, present := object[key]; present {
			t.Fatalf("streaming scratch field %q persisted: %s", key, raw)
		}
	}
	return thinking
}

func TestBedrockUpstreamRedactedReasoning(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/bedrock-redacted-reasoning.test.ts:137
	t.Run("does not fail the stream when reasoning arrives as redactedContent", func(t *testing.T) {
		result, _ := runBedrockEvents(t, upstreamBedrockRedactedEvents(t)...)
		if result.StopReason == StopReasonError {
			t.Fatalf("result = %+v", result)
		}
		if len(result.Content) != 2 {
			t.Fatalf("content = %#v", result.Content)
		}
		if _, ok := result.Content[0].(ThinkingContent); !ok {
			t.Fatalf("first = %T", result.Content[0])
		}
		if !reflect.DeepEqual(result.Content[1], TextContent{Text: "done"}) {
			t.Fatalf("second = %#v", result.Content[1])
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-redacted-reasoning.test.ts:148
	t.Run("preserves the encrypted reasoning payload on the assistant message", func(t *testing.T) {
		result, _ := runBedrockEvents(t, upstreamBedrockRedactedEvents(t)...)
		assertBedrockRedactedThinking(t, result)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-redacted-reasoning.test.ts:164
	t.Run("encodes the payload when the stream never sends contentBlockStop", func(t *testing.T) {
		events := upstreamBedrockRedactedEvents(t)
		result, _ := runBedrockEvents(t, events[0], events[1], events[5])
		assertBedrockRedactedThinking(t, result)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-redacted-reasoning.test.ts:185
	t.Run("joins encrypted reasoning split across deltas", func(t *testing.T) {
		events := upstreamBedrockRedactedEvents(t)
		data := upstreamBedrockRedactedBytes(t)
		result, _ := runBedrockEvents(t, events[0], bedrockRedactedDelta(0, data[:7]), bedrockRedactedDelta(0, data[7:]), events[2], events[5])
		thinking := assertBedrockRedactedThinking(t, result)
		if thinking.Thinking != "[Reasoning redacted]" {
			t.Fatalf("thinking text = %q", thinking.Thinking)
		}
	})
	for _, tc := range []struct {
		name string
		tool bool
	}{
		// .upstream/v0.87.1/packages/ai/test/bedrock-redacted-reasoning.test.ts:203
		{"replays redacted reasoning as reasoningContent.redactedContent", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-redacted-reasoning.test.ts:232
		{"replays redacted reasoning before the toolUse block it belongs to", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assistant := AssistantMessage{API: APIBedrockConverseStream, Provider: "amazon-bedrock", Model: "global.openai.gpt-5.6-terra", StopReason: StopReasonStop, Content: []AssistantContentBlock{ThinkingContent{ThinkingSignature: upstreamBedrockRedactedBase64, Redacted: true}, TextContent{Text: "done"}}}
			messages := []Message{UserMessage{Content: UserText("hello")}, assistant, UserMessage{Content: UserText("continue")}}
			if tc.tool {
				assistant.Content[1] = ToolCall{ID: "tool-1", Name: "read", Arguments: JsonObject{"path": "/tmp/a.txt"}}
				assistant.StopReason = StopReasonToolUse
				messages = []Message{UserMessage{Content: UserText("read the file")}, assistant, ToolResultMessage{ToolCallID: "tool-1", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "file body"}}}}
			}
			payload, err := convertBedrockMessages(messages, assistant.Model, "GPT-5.6 Terra (Global)", bedrockCacheNone, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(payload) != 3 || payload[1].Role != btypes.ConversationRoleAssistant || len(payload[1].Content) != 2 {
				t.Fatalf("messages = %#v", payload)
			}
			reasoning, ok := payload[1].Content[0].(*btypes.ContentBlockMemberReasoningContent)
			if !ok {
				t.Fatalf("first = %T", payload[1].Content[0])
			}
			redacted, ok := reasoning.Value.(*btypes.ReasoningContentBlockMemberRedactedContent)
			if !ok || !bytes.Equal(redacted.Value, upstreamBedrockRedactedBytes(t)) {
				t.Fatalf("reasoning = %#v", reasoning)
			}
			if tc.tool {
				tool, ok := payload[1].Content[1].(*btypes.ContentBlockMemberToolUse)
				if !ok || aws.ToString(tool.Value.ToolUseId) != "tool-1" || aws.ToString(tool.Value.Name) != "read" {
					t.Fatalf("tool = %#v", payload[1].Content[1])
				}
				raw, err := tool.Value.Input.MarshalSmithyDocument()
				if err != nil {
					t.Fatal(err)
				}
				assertShapeJSON(t, raw, `{"path":"/tmp/a.txt"}`)
			} else if text, ok := payload[1].Content[1].(*btypes.ContentBlockMemberText); !ok || text.Value != "done" {
				t.Fatalf("text = %#v", payload[1].Content[1])
			}
		})
	}
}

func TestBedrockUpstreamRawStopReason(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		stop      StopReason
		err       string
	}{
		// .upstream/v0.87.1/packages/ai/test/bedrock-raw-stop-reason.test.ts:61
		{"preserves raw Bedrock stop reasons for successful stops", "end_turn", StopReasonStop, ""},
		// .upstream/v0.87.1/packages/ai/test/bedrock-raw-stop-reason.test.ts:71
		{"preserves raw Bedrock stop reasons for provider error stops", "guardrail_intervened", StopReasonError, "Provider stopped with: guardrail_intervened"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := runBedrockEvents(t, &btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}}, &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReason(tc.raw)}})
			if result.StopReason != tc.stop || result.RawStopReason != tc.raw || result.ErrorMessage != tc.err {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

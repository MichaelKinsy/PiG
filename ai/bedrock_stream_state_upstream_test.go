//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// upstream: packages/ai/test/bedrock-stream-state.test.ts (#9265): Bedrock kept `index`, `partialJson` and the encrypted reasoning bytes on the content blocks while streaming, and re-parsed the accumulated tool-call arguments on every delta.

// bedrockStreamStateEvents is encrypted reasoning, then a write tool call whose arguments arrive in 32-character deltas.
func bedrockStreamStateEvents(text string) []btypes.ConverseStreamOutput {
	events := []btypes.ConverseStreamOutput{
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		bedrockRedactedDelta(0, []byte{1, 2}),
		bedrockRedactedDelta(0, []byte{3}),
		&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		&btypes.ConverseStreamOutputMemberContentBlockStart{Value: btypes.ContentBlockStartEvent{
			ContentBlockIndex: aws.Int32(1), Start: &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{ToolUseId: aws.String("tool_1"), Name: aws.String("write")}},
		}},
	}
	for _, delta := range streamingDeltas(text, 32) {
		events = append(events, &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(delta)}},
		}})
	}
	return append(events,
		&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)}},
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonToolUse}},
	)
}

func TestBedrockStreamStateStaysOffPartialBlocksAndTheToolCallFinishesWithItsExactArguments(t *testing.T) {
	arguments, text := streamingWriteCall(t, 5_000)
	result, events := runBedrockEvents(t, bedrockStreamStateEvents(text)...)
	shown := -1
	for _, event := range events {
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
		if !ok {
			continue
		}
		content, isString := call.Arguments["content"].(string)
		if !isString {
			continue
		}
		if !strings.HasPrefix(arguments["content"].(string), content) || len(content) < shown {
			t.Fatalf("shown content of %d bytes is not a growing prefix of the file", len(content))
		}
		shown = len(content)
	}
	if result.StopReason != StopReasonToolUse {
		t.Fatalf("stop reason %s: %s", result.StopReason, result.ErrorMessage)
	}
	expectNoStreamState(t, result, "result")
	thinking, ok := result.Content[0].(ThinkingContent)
	call, isCall := result.Content[1].(ToolCall)
	if len(result.Content) != 2 || !ok || thinking.Thinking != "[Reasoning redacted]" || thinking.ThinkingSignature != "AQID" || !thinking.Redacted ||
		!isCall || call.ID != "tool_1" || call.Name != "write" || !equalArguments(call.Arguments, arguments) {
		t.Fatalf("content = %#v", result.Content)
	}
}

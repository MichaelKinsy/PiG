//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/aws"
)

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
			if result.StopReason != ai.StopReasonStop {
				t.Fatalf("message stop reason = %q, want %q", result.StopReason, ai.StopReasonStop)
			}
			if done, ok := events[len(events)-1].(ai.DoneEvent); !ok || done.Reason != ai.StopReasonStop {
				t.Fatalf("terminal event = %#v, want stop done", events[len(events)-1])
			}
		})
	}
}

func assertWireStopReason(t *testing.T, stream *ai.AssistantMessageEventStream, want ai.StopReason) {
	t.Helper()
	result := stream.Result()
	if result.StopReason != want {
		t.Fatalf("message stop reason = %q, want %q", result.StopReason, want)
	}
	if len(result.Content) != 1 {
		t.Fatalf("message content = %#v, want one tool call", result.Content)
	}
	if _, ok := result.Content[0].(ai.ToolCall); !ok {
		t.Fatalf("message content[0] = %T, want ToolCall", result.Content[0])
	}
	var terminal ai.StopReason
	for event := range stream.Events(context.Background()) {
		if done, ok := event.(ai.DoneEvent); ok {
			terminal = done.Reason
		}
	}
	if terminal != want {
		t.Fatalf("terminal stop reason = %q, want %q", terminal, want)
	}
}

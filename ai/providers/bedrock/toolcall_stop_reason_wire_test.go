//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/MichaelKinsy/PiG/ai"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
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

//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

func bedrockSignatureDeltas(count int) ([]btypes.ConverseStreamOutput, string) {
	input := []btypes.ConverseStreamOutput{&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}}}
	var want strings.Builder
	for i := range count {
		piece := fmt.Sprintf("sig%d.", i)
		want.WriteString(piece)
		input = append(input, &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberSignature{Value: piece}}}})
	}
	input = append(input, indexedBedrockStop(0), &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}})
	return input, want.String()
}

// A reasoning signature arrives in many deltas (bedrock-converse-stream.ts:663 appends each to thinkingSignature). Every piece lands, in order, in the final block.
func TestBedrockSignatureDeltasConcatenateInOrder(t *testing.T) {
	input, want := bedrockSignatureDeltas(3000)
	result, _ := runBedrockEvents(t, input...)
	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok || thinking.ThinkingSignature != want {
		t.Fatalf("signature = %.60q (%d bytes), want %.60q (%d bytes)", thinking.ThinkingSignature, len(thinking.ThinkingSignature), want, len(want))
	}
}

// The signature is appended through the block accumulator, not rebuilt from its start on every delta: 4x the deltas allocate far less than 16x the bytes.
func TestBedrockSignatureDeltasAllocateLinearly(t *testing.T) {
	measure := func(count int) uint64 {
		input, _ := bedrockSignatureDeltas(count)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		runBedrockEvents(t, input...)
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := measure(1000), measure(4000)
	if large > small*8 {
		t.Fatalf("bytes allocated grew from %d to %d for 4x the signature deltas", small, large)
	}
}

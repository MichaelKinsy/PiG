//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// TestBedrockUsageCostMatchesUpstream ports bedrock-cache-write-1h-cost.test.ts:
// 1h cacheDetails bill at twice the input rate while cacheWrite keeps the total.
func TestBedrockUsageCostMatchesUpstream(t *testing.T) {
	generated, ok := LookupModelExact("amazon-bedrock/us.anthropic.claude-opus-4-8")
	if !ok {
		t.Fatal("catalog model missing")
	}
	input := make(chan btypes.ConverseStreamOutput, 3)
	input <- &btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}}
	input <- &btypes.ConverseStreamOutputMemberMetadata{Value: btypes.ConverseStreamMetadataEvent{Usage: &btypes.TokenUsage{
		InputTokens: aws.Int32(100), OutputTokens: aws.Int32(5), TotalTokens: aws.Int32(1_000_105), CacheWriteInputTokens: aws.Int32(1_000_000),
		CacheDetails: []btypes.CacheDetail{
			{Ttl: btypes.CacheTTLOneHour, InputTokens: aws.Int32(150_000)},
			{Ttl: btypes.CacheTTLFiveMinutes, InputTokens: aws.Int32(600_000)},
			{Ttl: btypes.CacheTTLOneHour, InputTokens: aws.Int32(250_000)},
		},
	}}}
	input <- &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}}
	close(input)
	builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "us.anthropic.claude-opus-4-8")
	builder.modelCost = (&Model{Capabilities: generated.ToCapabilities()}).CostRates()
	go (&BedrockProvider{}).parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: input}, builder, "")
	assertUsageJSON(t, builder.stream.Result().Usage, `{"input":100,"output":5,"cacheRead":0,"cacheWrite":1000000,"cacheWrite1h":400000,"totalTokens":1000105,"cost":{"input":0.00055,"output":0.0001375,"cacheRead":0,"cacheWrite":8.525,"total":8.5256875}}`)
}

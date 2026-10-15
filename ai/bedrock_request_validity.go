//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// checkBedrockRequest validates the message list of a Converse request against the Bedrock contract.
func checkBedrockRequest(ctx context.Context, input *bedrockruntime.ConverseStreamInput) {
	if !requestvalidity.Enabled() || input == nil {
		return
	}
	turns := make([]requestvalidity.Turn, 0, len(input.Messages))
	for _, message := range input.Messages {
		turn := requestvalidity.Turn{Role: string(message.Role), Empty: len(message.Content) == 0}
		other := false
		for _, block := range message.Content {
			switch block := block.(type) {
			case *btypes.ContentBlockMemberToolUse:
				turn.Calls = append(turn.Calls, aws.ToString(block.Value.ToolUseId))
			case *btypes.ContentBlockMemberToolResult:
				if other {
					turn.ContentBeforeResult = true
				}
				turn.Results = append(turn.Results, aws.ToString(block.Value.ToolUseId))
			case *btypes.ContentBlockMemberText:
				other = true
				if block.Value == "" {
					turn.Empty = true
				}
			case *btypes.ContentBlockMemberCachePoint:
			default:
				other = true
			}
		}
		turns = append(turns, turn)
	}
	requestvalidity.Enforce(ctx, string(APIBedrockConverseStream), requestvalidity.Bedrock.Check(turns))
}

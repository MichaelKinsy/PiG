//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"

	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// bedrockStreamState is the loop state of bedrock-converse-stream.ts's `for await (const item of response.stream!)` body and the statements after it. handle is one iteration; finish is the code after the loop.
type bedrockStreamState struct {
	provider *BedrockProvider
	builder  *ai.AssistantStreamBuilder
	blocks   map[int32]*activeBlock
	usage    ai.Usage
	// stopReason is output.stopReason; hasStopReason is stopReason !== "pending".
	stopReason    ai.StopReason
	hasStopReason bool
	stopErrMsg    string
}

func newBedrockStreamState(provider *BedrockProvider, builder *ai.AssistantStreamBuilder) *bedrockStreamState {
	return &bedrockStreamState{provider: provider, builder: builder, blocks: map[int32]*activeBlock{}, stopReason: ai.StopReasonStop}
}

// finalizeBlocks is flushOpenBlocks: it flushes the redacted reasoning of the blocks a stream settled without stopping, without emitting events.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:flushOpenBlocks
func (state *bedrockStreamState) finalizeBlocks() {
	for _, block := range state.blocks {
		flushBedrockRedactedContent(block, state.builder)
	}
	state.builder.ClearToolCalls()
}

// handle is one item of the Pi loop (bedrock-converse-stream.ts:296-330) for an SDK union member. A returned error is a thrown exception.
func (state *bedrockStreamState) handle(ev btypes.ConverseStreamOutput) error {
	return state.handleItem(bedrockStreamItem{event: ev})
}

func (state *bedrockStreamState) handleItem(item bedrockStreamItem) error {
	builder := state.builder
	switch e := item.event.(type) {
	case *btypes.ConverseStreamOutputMemberMessageStart:
		if e.Value.Role != btypes.ConversationRoleAssistant {
			return errors.New("Unexpected assistant message start but got user message start instead")
		}
		builder.Start()

	case *btypes.ConverseStreamOutputMemberContentBlockStart:
		idx := aws.ToInt32(e.Value.ContentBlockIndex)
		if start := e.Value.Start; start != nil {
			if tu, ok := start.(*btypes.ContentBlockStartMemberToolUse); ok {
				state.blocks[idx] = &activeBlock{
					kind:     "toolUse",
					toolID:   aws.ToString(tu.Value.ToolUseId),
					toolName: aws.ToString(tu.Value.Name),
				}
				builder.ToolCallStart(ai.NewStreamToolCallDelta(int(idx), aws.ToString(tu.Value.ToolUseId), aws.ToString(tu.Value.Name), ""))
			}
		}

	case *btypes.ConverseStreamOutputMemberContentBlockDelta:
		state.provider.handleBedrockDeltaFields(e.Value, item.reasoning, state.blocks, builder)

	case *btypes.ConverseStreamOutputMemberContentBlockStop:
		idx := aws.ToInt32(e.Value.ContentBlockIndex)
		if block, ok := state.blocks[idx]; ok {
			flushBedrockRedactedContent(block, builder)
			switch block.kind {
			case "text":
				text := builder.Partial().Content[block.contentIndex].(ai.TextContent)
				builder.TextBlockEnd(block.contentIndex, text.Text, text.TextSignature)
			case "thinking":
				thinking := builder.Partial().Content[block.contentIndex].(ai.ThinkingContent)
				builder.ThinkingBlockEnd(block.contentIndex, thinking.Thinking, thinking.ThinkingSignature)
			case "toolUse":
				builder.EndToolCall(int(idx))
			}
		}
		delete(state.blocks, idx)

	case *btypes.ConverseStreamOutputMemberMessageStop:
		rawStopReason := string(e.Value.StopReason)
		builder.SetResponseMetadata("", "", rawStopReason, "", nil)
		stopErrMsg := ""
		state.stopReason, stopErrMsg = mapBedrockStopReason(rawStopReason)
		state.hasStopReason = true
		// output.stopReason and output.errorMessage change in place: later partials carry them before `done`. A later messageStop replaces the stop reason but keeps an errorMessage an earlier one set (bedrock-converse-stream.ts:313-318).
		builder.Partial().StopReason = state.stopReason
		if stopErrMsg != "" {
			state.stopErrMsg = stopErrMsg
			builder.Partial().ErrorMessage = stopErrMsg
		}

	case *btypes.ConverseStreamOutputMemberMetadata:
		if u := e.Value.Usage; u != nil {
			handleBedrockUsage(u, builder, &state.usage)
		}

	case *btypes.UnknownUnionMember:
		// `throw item.internalServerException` and its four siblings (bedrock-converse-stream.ts:318-327).
		if exception := bedrockStreamException(e.Tag, e.Value); exception != nil {
			return exception
		}
	}
	return nil
}

// bedrockStreamItemModeled reports whether Pi's ConverseStream union models the item. The SDK reader yields *types.UnknownUnionMember for every event type the Go union lacks; Pi's deserializer returns `$unknown` for those outside its union, which SmithyMessageDecoderStream drops before the loop sees them.
func bedrockStreamItemModeled(event btypes.ConverseStreamOutput) bool {
	unknown, ok := event.(*btypes.UnknownUnionMember)
	return !ok || bedrockStreamException(unknown.Tag, unknown.Value) != nil
}

// finish is the code after the loop: an ended stream without a stop reason, or with an error stop reason, fails; otherwise the message completes. streamErr is the stream's terminal error.
func (state *bedrockStreamState) finish(ctx context.Context, streamErr error, requestID string) {
	builder := state.builder
	if streamErr != nil {
		failBedrockResponse(ctx, builder, streamErr, requestID, true)
		return
	}
	if !state.hasStopReason {
		failBedrockResponse(ctx, builder, errors.New("Bedrock stream ended without a stop reason"), requestID, true)
		return
	}
	if state.stopReason == ai.StopReasonError {
		message := state.stopErrMsg
		if message == "" {
			message = "An unknown error occurred"
		}
		failBedrockResponse(ctx, builder, errors.New(message), requestID, true)
		return
	}
	builder.Done(state.stopReason, nil, state.stopErrMsg)
}

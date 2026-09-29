package ai

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// bedrockStreamState is the loop state of bedrock-converse-stream.ts's `for await (const item of response.stream!)` body and the statements after it. handle is one iteration; finish is the code after the loop.
type bedrockStreamState struct {
	provider *BedrockProvider
	builder  *assistantStreamBuilder
	blocks   map[int32]*activeBlock
	usage    Usage
	// stopReason is output.stopReason; hasStopReason is stopReason !== "pending".
	stopReason    StopReason
	hasStopReason bool
	stopErrMsg    string
}

func newBedrockStreamState(provider *BedrockProvider, builder *assistantStreamBuilder) *bedrockStreamState {
	return &bedrockStreamState{provider: provider, builder: builder, blocks: map[int32]*activeBlock{}, stopReason: StopReasonStop}
}

// finalizeBlocks is `for (const block of output.content) finalizeStreamingBlock(block)`: it drops streaming scratch and flushes redacted reasoning without emitting events.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:finalizeStreamingBlock
func (state *bedrockStreamState) finalizeBlocks() {
	for _, block := range state.blocks {
		flushBedrockRedactedContent(block, state.builder)
	}
	state.builder.clearStreamingScratch()
	clear(state.builder.toolCalls)
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
		builder.start()

	case *btypes.ConverseStreamOutputMemberContentBlockStart:
		idx := aws.ToInt32(e.Value.ContentBlockIndex)
		if start := e.Value.Start; start != nil {
			if tu, ok := start.(*btypes.ContentBlockStartMemberToolUse); ok {
				state.blocks[idx] = &activeBlock{
					kind:     "toolUse",
					toolID:   aws.ToString(tu.Value.ToolUseId),
					toolName: aws.ToString(tu.Value.Name),
				}
				builder.toolCallStart(streamToolCallDelta{
					index: int(idx), id: aws.ToString(tu.Value.ToolUseId), name: aws.ToString(tu.Value.Name),
					scratch: toolCallScratch{hasPartialJson: builder.managed, hasIndex: builder.managed, index: int(idx)},
				})
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
				text := builder.partial.Content[block.contentIndex].(TextContent)
				builder.textBlockEnd(block.contentIndex, text.Text, text.TextSignature)
			case "thinking":
				thinking := builder.partial.Content[block.contentIndex].(ThinkingContent)
				builder.thinkingBlockEnd(block.contentIndex, thinking.Thinking, thinking.ThinkingSignature)
			case "toolUse":
				builder.endToolCall(int(idx))
			}
		}
		delete(state.blocks, idx)

	case *btypes.ConverseStreamOutputMemberMessageStop:
		rawStopReason := string(e.Value.StopReason)
		builder.setResponseMetadata("", "", rawStopReason, "", nil)
		state.stopReason, state.stopErrMsg = mapBedrockStopReason(rawStopReason)
		state.hasStopReason = true
		// output.stopReason and output.errorMessage change in place: later partials carry them before `done`.
		builder.partial.StopReason = state.stopReason
		if state.stopErrMsg != "" {
			builder.partial.ErrorMessage = state.stopErrMsg
		}

	case *btypes.ConverseStreamOutputMemberMetadata:
		if u := e.Value.Usage; u != nil {
			handleBedrockUsage(u, builder, &state.usage)
		}
	}
	return nil
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
	if state.stopReason == StopReasonError {
		message := state.stopErrMsg
		if message == "" {
			message = "An unknown error occurred"
		}
		failBedrockResponse(ctx, builder, errors.New(message), requestID, true)
		return
	}
	builder.done(state.stopReason, nil, "")
}

// bedrockBlockScratch is the streaming `index` field. Pi's output blocks carry it until the block stops, visible to any consumer whose tick falls before then; a builder outside the executor pushes emission-time snapshots (D82), which do not model that window, so the field is only published when the producer runs as an executor turn.
func bedrockBlockScratch(builder *assistantStreamBuilder, index int32) contentIndexScratch {
	if !builder.managed {
		return ""
	}
	return scratchIndexFor(int(index))
}

package ai

import "strconv"

// toolCallScratch is value-owned so publishing a ToolCall cannot retain a mutable parser buffer.
// upstream: packages/ai/src/api/openai-completions.ts:StreamingToolCallBlock
// upstream: packages/ai/src/api/openai-responses-shared.ts:StreamingToolCall
type toolCallScratch struct {
	partialArgs    string
	partialJson    string
	hasPartialArgs bool
	hasPartialJson bool
	streamIndex    int
	hasStreamIndex bool
	index          int
	hasIndex       bool
	customInput    bool
	property       string
	jsonBuffer     grammarToolInputJSONBuffer
}

// contentIndexScratch is the provider event index that Anthropic's and Bedrock's streams keep on a text or thinking block while its content block is open (packages/ai/src/api/anthropic-messages.ts:Block, bedrock-converse-stream.ts:Block), as decimal text; empty means absent. A string keeps TextContent formattable with %q, which tests in other packages apply to text blocks.
type contentIndexScratch string

func scratchIndexFor(index int) contentIndexScratch {
	return contentIndexScratch(strconv.Itoa(index))
}

func (scratch contentIndexScratch) wire() *int {
	index, err := strconv.Atoi(string(scratch))
	if scratch == "" || err != nil {
		return nil
	}
	return &index
}

type toolCallScratchJSON struct {
	PartialArgs *string `json:"partialArgs,omitempty"`
	PartialJson *string `json:"partialJson,omitempty"`
	CustomInput *struct {
		Property   string `json:"property"`
		JSONBuffer struct {
			Input   string `json:"input"`
			Started bool   `json:"started"`
			Closed  bool   `json:"closed"`
		} `json:"jsonBuffer"`
	} `json:"customInput,omitempty"`
	StreamIndex *int `json:"streamIndex,omitempty"`
	Index       *int `json:"index,omitempty"`
}

func (scratch toolCallScratch) wire() toolCallScratchJSON {
	var wire toolCallScratchJSON
	if scratch.hasPartialArgs {
		wire.PartialArgs = &scratch.partialArgs
	}
	if scratch.hasPartialJson {
		wire.PartialJson = &scratch.partialJson
	}
	if scratch.hasIndex {
		wire.Index = &scratch.index
	}
	if scratch.hasStreamIndex {
		wire.StreamIndex = &scratch.streamIndex
	}
	if scratch.customInput {
		wire.CustomInput = new(struct {
			Property   string `json:"property"`
			JSONBuffer struct {
				Input   string `json:"input"`
				Started bool   `json:"started"`
				Closed  bool   `json:"closed"`
			} `json:"jsonBuffer"`
		})
		wire.CustomInput.Property = scratch.property
		wire.CustomInput.JSONBuffer.Input = scratch.jsonBuffer.Input
		wire.CustomInput.JSONBuffer.Started = scratch.jsonBuffer.Started
		wire.CustomInput.JSONBuffer.Closed = scratch.jsonBuffer.Closed
	}
	return wire
}

func (builder *assistantStreamBuilder) setToolCallScratch(index int, scratch toolCallScratch) {
	state := builder.toolCalls[index]
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	block.scratch = scratch
	builder.partial.Content[state.contentIndex] = block
}

func (builder *assistantStreamBuilder) setCustomToolCallInput(index int, property, input string, buffer grammarToolInputJSONBuffer) {
	state := builder.toolCalls[index]
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	block.scratch.hasPartialArgs = false
	block.scratch.partialArgs = ""
	block.scratch.customInput = true
	block.scratch.property = property
	block.scratch.jsonBuffer = buffer
	block.Arguments = JsonObject{property: input}
	builder.partial.Content[state.contentIndex] = block
}

// Errors preserve the last parsed arguments but do not synthesize successful block ends.
// upstream: packages/ai/src/api/openai-completions.ts:stream
// upstream: packages/ai/src/api/openai-responses.ts:stream
func (builder *assistantStreamBuilder) failUnfinished(reason StopReason, err error) {
	for i, content := range builder.partial.Content {
		switch block := content.(type) {
		case ToolCall:
			block.scratch = toolCallScratch{}
			builder.partial.Content[i] = block
		case TextContent:
			block.scratch = ""
			builder.partial.Content[i] = block
		case ThinkingContent:
			block.scratch = ""
			builder.partial.Content[i] = block
		}
	}
	clear(builder.toolCalls)
	builder.partial.StopReason = reason
	if err != nil {
		builder.partial.ErrorMessage = builder.failureMessage(err)
	}
	builder.push(ErrorEvent{Reason: reason, Error: builder.partial})
}

// clearStreamingScratch is finalizeStreamingBlock over every block: it drops the streaming `index` and `partialJson` fields without emitting events.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:finalizeStreamingBlock
func (builder *assistantStreamBuilder) clearStreamingScratch() {
	for i, content := range builder.partial.Content {
		switch block := content.(type) {
		case TextContent:
			block.scratch = ""
			builder.partial.Content[i] = block
		case ThinkingContent:
			block.scratch = ""
			builder.partial.Content[i] = block
		case ToolCall:
			block.scratch = toolCallScratch{}
			builder.partial.Content[i] = block
		}
	}
}

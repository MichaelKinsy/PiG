package ai

import (
	"context"
	"sort"
	"strings"
	"time"
)

type assistantStreamBuilder struct {
	ctx          context.Context
	stream       *AssistantMessageEventStream
	partial      *AssistantMessage
	cell         *assistantMessageCell
	turn         *continuationTurn
	managed      bool
	abort        context.CancelFunc
	started      bool
	replacements assistantMessageReplacements
	partialCopy  func(*AssistantMessage) *AssistantMessage
	afterPush    func()
	// dirty records a mutation of partial that no push has published yet.
	dirty bool

	activeText     int
	activeThinking int
	toolCalls      map[int]*streamToolCall

	// modelCost is the requested model's price (StreamOptions.ModelCost).
	modelCost          ModelCost
	requestServiceTier string
}

type streamToolCall struct {
	contentIndex int
	arguments    strings.Builder
}

type streamToolCallDelta struct {
	index            int
	id               string
	name             string
	argumentsDelta   string
	initialArguments JsonObject
	initialJSON      string
	scratch          toolCallScratch
	thoughtSignature string
	namespace        string
}

func newAssistantStreamBuilder(ctx context.Context, api API, provider, model string) *assistantStreamBuilder {
	if ctx == nil {
		panic("assistant stream builder: nil context")
	}
	return &assistantStreamBuilder{
		ctx:    ctx,
		stream: NewAssistantMessageEventStream(),
		partial: &AssistantMessage{
			Content: []AssistantContentBlock{}, API: api, Provider: provider, Model: model,
			Usage: Usage{}, StopReason: StopReasonPending, Timestamp: time.Now().UnixMilli(),
		},
		activeText: -1, activeThinking: -1, toolCalls: map[int]*streamToolCall{},
	}
}

// calculateCost prices usage with the requested model, as upstream providers
// call calculateCost(model, output.usage).
func (builder *assistantStreamBuilder) calculateCost(usage *Usage) {
	calculateUsageCost(builder.modelCost, usage)
}

func (builder *assistantStreamBuilder) start() {
	if builder.started {
		return
	}
	builder.started = true
	builder.push(StartEvent{Partial: builder.partial})
}

func (builder *assistantStreamBuilder) textBlockStart() int {
	return builder.textBlockStartWithContent("")
}

func (builder *assistantStreamBuilder) textBlockStartWithContent(content string) int {
	return builder.textBlockStartWithBlock(TextContent{Text: content})
}

func (builder *assistantStreamBuilder) textBlockStartWithBlock(block TextContent) int {
	builder.start()
	contentIndex := len(builder.partial.Content)
	builder.partial.Content = append(builder.partial.Content, block)
	builder.push(TextStartEvent{ContentIndex: contentIndex, Partial: builder.partial})
	return contentIndex
}

func (builder *assistantStreamBuilder) textBlockDelta(contentIndex int, delta string) {
	block := builder.partial.Content[contentIndex].(TextContent)
	block.Text += delta
	builder.partial.Content[contentIndex] = block
	builder.push(TextDeltaEvent{ContentIndex: contentIndex, Delta: delta, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) textBlockEnd(contentIndex int, content, signature string) {
	block := builder.partial.Content[contentIndex].(TextContent)
	block.Text = content
	block.TextSignature = signature
	block.scratch = ""
	builder.partial.Content[contentIndex] = block
	builder.push(TextEndEvent{ContentIndex: contentIndex, Content: content, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) thinkingBlockStart() int {
	return builder.thinkingBlockStartWithContent(ThinkingContent{})
}

func (builder *assistantStreamBuilder) thinkingBlockStartWithContent(content ThinkingContent) int {
	builder.start()
	contentIndex := len(builder.partial.Content)
	builder.partial.Content = append(builder.partial.Content, content)
	builder.push(ThinkingStartEvent{ContentIndex: contentIndex, Partial: builder.partial})
	return contentIndex
}

func (builder *assistantStreamBuilder) thinkingBlockDelta(contentIndex int, delta string) {
	block := builder.partial.Content[contentIndex].(ThinkingContent)
	block.Thinking += delta
	builder.partial.Content[contentIndex] = block
	builder.push(ThinkingDeltaEvent{ContentIndex: contentIndex, Delta: delta, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) thinkingBlockSignature(contentIndex int, signature string) {
	block := builder.partial.Content[contentIndex].(ThinkingContent)
	block.ThinkingSignature = signature
	block.thinkingSignatureEmpty = signature == ""
	builder.partial.Content[contentIndex] = block
}

func (builder *assistantStreamBuilder) thinkingBlockEnd(contentIndex int, content, signature string) {
	block := builder.partial.Content[contentIndex].(ThinkingContent)
	block.Thinking = content
	block.ThinkingSignature = signature
	if signature != "" {
		block.thinkingSignatureEmpty = false
	}
	block.scratch = ""
	builder.partial.Content[contentIndex] = block
	builder.push(ThinkingEndEvent{ContentIndex: contentIndex, Content: content, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) textStart(content string) {
	builder.start()
	builder.endThinking()
	if builder.activeText >= 0 {
		return
	}
	builder.activeText = len(builder.partial.Content)
	builder.partial.Content = append(builder.partial.Content, TextContent{Text: content})
	builder.push(TextStartEvent{ContentIndex: builder.activeText, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) textDelta(delta string) {
	builder.textStart("")
	block := builder.partial.Content[builder.activeText].(TextContent)
	block.Text += delta
	builder.partial.Content[builder.activeText] = block
	builder.push(TextDeltaEvent{ContentIndex: builder.activeText, Delta: delta, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) textSignature(signature string) {
	if builder.activeText < 0 {
		return
	}
	block := builder.partial.Content[builder.activeText].(TextContent)
	block.TextSignature = signature
	builder.partial.Content[builder.activeText] = block
}

func (builder *assistantStreamBuilder) thinkingStart(content ThinkingContent) {
	builder.start()
	builder.endText()
	if builder.activeThinking >= 0 {
		return
	}
	builder.activeThinking = len(builder.partial.Content)
	builder.partial.Content = append(builder.partial.Content, content)
	builder.push(ThinkingStartEvent{ContentIndex: builder.activeThinking, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) thinkingDelta(delta string, redacted bool) {
	builder.thinkingStart(ThinkingContent{Redacted: redacted})
	block := builder.partial.Content[builder.activeThinking].(ThinkingContent)
	block.Thinking += delta
	block.Redacted = block.Redacted || redacted
	builder.partial.Content[builder.activeThinking] = block
	builder.push(ThinkingDeltaEvent{ContentIndex: builder.activeThinking, Delta: delta, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) thinkingSignature(signature string) {
	if builder.activeThinking < 0 {
		return
	}
	block := builder.partial.Content[builder.activeThinking].(ThinkingContent)
	block.ThinkingSignature = signature
	block.thinkingSignatureEmpty = signature == ""
	builder.partial.Content[builder.activeThinking] = block
}

func (builder *assistantStreamBuilder) toolCallStart(delta streamToolCallDelta) {
	builder.start()
	builder.endText()
	builder.endThinking()
	if builder.toolCalls[delta.index] != nil {
		return
	}
	state := &streamToolCall{contentIndex: len(builder.partial.Content)}
	state.arguments.WriteString(delta.initialJSON)
	builder.toolCalls[delta.index] = state
	arguments := delta.initialArguments
	if arguments == nil {
		arguments = JsonObject{}
	}
	builder.partial.Content = append(builder.partial.Content, ToolCall{
		ID: delta.id, Name: delta.name, Arguments: arguments, Namespace: delta.namespace, scratch: delta.scratch,
	})
	builder.push(ToolCallStartEvent{ContentIndex: state.contentIndex, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) toolCallDelta(delta streamToolCallDelta) {
	builder.toolCallStart(delta)
	state := builder.toolCalls[delta.index]
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	if delta.id != "" {
		block.ID = delta.id
	}
	if delta.name != "" {
		block.Name = delta.name
	}
	if delta.thoughtSignature != "" {
		block.ThoughtSignature = delta.thoughtSignature
	}
	if delta.namespace != "" {
		block.Namespace = delta.namespace
	}
	state.arguments.WriteString(delta.argumentsDelta)
	if !block.scratch.customInput {
		block.Arguments = parseStreamingJsonObject(state.arguments.String())
	}
	if block.scratch.hasPartialArgs {
		block.scratch.partialArgs = state.arguments.String()
	}
	if block.scratch.hasPartialJson {
		block.scratch.partialJson = state.arguments.String()
	}
	builder.partial.Content[state.contentIndex] = block
	builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: delta.argumentsDelta, Partial: builder.partial})
}

// Finalization updates the existing output slot without replacing its streamed identity.
func (builder *assistantStreamBuilder) setToolCallFinal(index int, namespace, arguments string) {
	state := builder.toolCalls[index]
	if state == nil {
		return
	}
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	if namespace != "" {
		block.Namespace = namespace
	}
	state.arguments.Reset()
	state.arguments.WriteString(arguments)
	block.Arguments = parseStreamingJsonObject(arguments)
	if block.scratch.hasPartialJson {
		block.scratch.partialJson = arguments
	}
	builder.partial.Content[state.contentIndex] = block
}

func (builder *assistantStreamBuilder) setResponseMetadata(responseID, responseModel, rawStopReason, providerThinkingLevel string, endTurn *bool) {
	if responseID != "" && builder.partial.ResponseID == "" {
		builder.partial.ResponseID = responseID
	}
	if responseModel != "" && builder.partial.ResponseModel == "" {
		builder.partial.ResponseModel = responseModel
	}
	if rawStopReason != "" {
		builder.partial.RawStopReason = rawStopReason
	}
	if providerThinkingLevel != "" {
		builder.partial.ProviderThinkingLevel = providerThinkingLevel
	}
	if endTurn != nil {
		builder.partial.EndTurn = new(*endTurn)
	}
}

func (builder *assistantStreamBuilder) setUsage(usage *Usage) {
	if usage != nil {
		builder.partial.Usage = *usage
		builder.replacements.Usage = true
	}
}

func (builder *assistantStreamBuilder) done(reason StopReason, usage *Usage, errorMessage string) {
	builder.start()
	builder.finishBlocks()
	builder.setUsage(usage)
	builder.partial.StopReason = reason
	builder.partial.ErrorMessage = errorMessage
	builder.push(DoneEvent{Reason: reason, Message: builder.partial})
}

func (builder *assistantStreamBuilder) fail(reason StopReason, err error) {
	builder.finishBlocks()
	builder.partial.StopReason = reason
	if err != nil {
		builder.partial.ErrorMessage = err.Error()
	}
	builder.push(ErrorEvent{Reason: reason, Error: builder.partial})
}

func (builder *assistantStreamBuilder) finishBlocks() {
	builder.finishBlocksWithThinking()
}

func (builder *assistantStreamBuilder) finishBlocksWithThinking(thinkingContentIndices ...int) {
	type activeBlock struct {
		contentIndex int
		kind         string
		toolIndex    int
	}
	blocks := make([]activeBlock, 0, len(builder.toolCalls)+len(thinkingContentIndices)+2)
	if builder.activeText >= 0 {
		blocks = append(blocks, activeBlock{contentIndex: builder.activeText, kind: "text"})
	}
	if builder.activeThinking >= 0 {
		blocks = append(blocks, activeBlock{contentIndex: builder.activeThinking, kind: "thinking"})
	}
	for _, contentIndex := range thinkingContentIndices {
		blocks = append(blocks, activeBlock{contentIndex: contentIndex, kind: "thinkingBlock"})
	}
	for toolIndex, tool := range builder.toolCalls {
		blocks = append(blocks, activeBlock{contentIndex: tool.contentIndex, kind: "tool", toolIndex: toolIndex})
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].contentIndex < blocks[j].contentIndex })
	for _, block := range blocks {
		switch block.kind {
		case "text":
			builder.endText()
		case "thinking":
			builder.endThinking()
		case "thinkingBlock":
			thinking := builder.partial.Content[block.contentIndex].(ThinkingContent)
			builder.thinkingBlockEnd(block.contentIndex, thinking.Thinking, thinking.ThinkingSignature)
		case "tool":
			builder.endToolCall(block.toolIndex)
		}
	}
}

func (builder *assistantStreamBuilder) endToolCall(index int) {
	state := builder.toolCalls[index]
	if state == nil {
		return
	}
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	if block.scratch.customInput {
		input, _ := block.Arguments[block.scratch.property].(string)
		delta, emit, err := appendGrammarToolInputJSONDelta(&block.scratch.jsonBuffer, block.scratch.property, input, true)
		if err != nil {
			builder.failUnfinished(StopReasonError, err)
			return
		}
		builder.partial.Content[state.contentIndex] = block
		if emit {
			builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: delta, Partial: builder.partial})
		}
	} else {
		block.Arguments = parseStreamingJsonObject(state.arguments.String())
	}
	block.scratch = toolCallScratch{}
	builder.partial.Content[state.contentIndex] = block
	builder.push(ToolCallEndEvent{ContentIndex: state.contentIndex, ToolCall: block, Partial: builder.partial})
	delete(builder.toolCalls, index)
}

func (builder *assistantStreamBuilder) endText() {
	if builder.activeText < 0 {
		return
	}
	block := builder.partial.Content[builder.activeText].(TextContent)
	builder.push(TextEndEvent{ContentIndex: builder.activeText, Content: block.Text, Partial: builder.partial})
	builder.activeText = -1
}

func (builder *assistantStreamBuilder) endThinking() {
	if builder.activeThinking < 0 {
		return
	}
	block := builder.partial.Content[builder.activeThinking].(ThinkingContent)
	builder.push(ThinkingEndEvent{ContentIndex: builder.activeThinking, Content: block.Thinking, Partial: builder.partial})
	builder.activeThinking = -1
}

// touch records that the producer mutated partial outside a push, so that the next release of the executor publishes it. Upstream consumers read the live output object at their own tick.
func (builder *assistantStreamBuilder) touch() { builder.dirty = true }

// publishPending publishes mutations made since the last push before the producer releases execution.
func (builder *assistantStreamBuilder) publishPending() {
	if builder.dirty {
		builder.publish()
	}
}

func (builder *assistantStreamBuilder) publish() *AssistantMessage {
	builder.dirty = false
	if builder.cell == nil {
		builder.cell = newAssistantMessageCell(builder.partial)
	} else {
		builder.cell.publish(builder.partial, builder.replacements)
	}
	builder.replacements = assistantMessageReplacements{}
	if !builder.managed || builder.turn == nil {
		// A producer outside the JavaScript-order executor pushes its construction message; Push stores an emission-time snapshot of it.
		return builder.partial
	}
	return builder.cell.view()
}

func (builder *assistantStreamBuilder) push(event AssistantMessageEvent) {
	view := builder.publish()
	if builder.partialCopy != nil {
		view = builder.partialCopy(view)
	}
	event = mapAssistantEventPartial(event, func(message *AssistantMessage) *AssistantMessage {
		if message == builder.partial {
			return view
		}
		return message
	})
	if err := builder.stream.Push(event); err != nil {
		builder.failInvariant(err)
	}
	if builder.afterPush != nil {
		builder.afterPush()
	}
}

func (builder *assistantStreamBuilder) failInvariant(err error) {
	if builder.partial.StopReason == StopReasonError {
		return
	}
	builder.partial.StopReason = StopReasonError
	builder.partial.ErrorMessage = err.Error()
	_ = builder.stream.Push(ErrorEvent{Reason: StopReasonError, Error: builder.partial})
}

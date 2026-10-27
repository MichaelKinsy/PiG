package ai

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"sync/atomic"
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
	// streamedArguments is, by content index, the immutable snapshot a tool call's ToolCallArgumentsStream last returned when the
	// stream vouches for it (immutable): publishing keeps that map as the cell's copy instead of copying it.
	streamedArguments map[int]JsonObject
	// streamedBlocks appends deltas to open blocks without copying them again.
	streamedBlocks

	// modelCost is the requested model's price (StreamOptions.ModelCost).
	modelCost          ModelCost
	requestServiceTier string
	// errorMessage rewrites the message of a failed stream, or is nil.
	errorMessage func(string) string
	// providerEvent is the bound OnProviderStreamEvent observer, or nil.
	providerEvent func(data any) error
	// providerEventOption and providerEventModel are the request's OnProviderStreamEvent option and the model it receives, for an adapter that
	// passes the option on (processResponsesStream's options.onProviderStreamEvent and model).
	providerEventOption func(ctx context.Context, data any, model *Model) error
	providerEventModel  *Model
	// providerEventWanted is the request's ProviderStreamEventWanted, or nil.
	providerEventWanted func() bool
}

// streamToolCall is the stream state of an open tool call, kept beside the content block and never on it, so it never reaches
// partials: the JSON arguments of a function call, or the input of a custom (grammar) tool call.
type streamToolCall struct {
	contentIndex int
	// args reads a function call's argument deltas; nil for a custom call.
	args *ToolCallArgumentsStream
	// custom is a custom call's input; nil for a function call.
	custom *customToolInput
}

// customToolInput is the stream state of a custom (grammar) tool call's input.
type customToolInput struct {
	property string
	buffer   grammarToolInputJSONBuffer
	// unsent is the JSON text of an initial input, sent with the first delta.
	unsent string
}

// newCustomToolInput is the state of a custom tool call that starts with initialInput.
func newCustomToolInput(property, initialInput string) *customToolInput {
	input := &customToolInput{property: property}
	input.unsent, _, _ = appendGrammarToolInputJSONDelta(&input.buffer, property, initialInput)
	return input
}

type streamToolCallDelta struct {
	index            int
	id               string
	name             string
	argumentsDelta   string
	initialArguments JsonObject
	initialJSON      string
	// custom starts the call as a custom tool call.
	custom *customToolInput
	// skipEmpty does not read an empty argumentsDelta (openai-completions and the Responses stream read a delta only when it has text);
	// every other stream reads each delta, empty ones included.
	skipEmpty bool
	// initialUnparsed keeps initialJSON as the call's text without reading it (an added item's arguments that is not a string).
	initialUnparsed bool
	// recordOnly keeps the text for the final parse without reading it: the caller built the arguments itself (Google).
	recordOnly       bool
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
	block.Text = builder.appendContent(contentIndex, block.Text, delta)
	builder.partial.Content[contentIndex] = block
	builder.push(TextDeltaEvent{ContentIndex: contentIndex, Delta: delta, Partial: builder.partial})
}

func (builder *assistantStreamBuilder) textBlockEnd(contentIndex int, content, signature string) {
	block := builder.partial.Content[contentIndex].(TextContent)
	block.Text = content
	block.TextSignature = signature
	builder.partial.Content[contentIndex] = block
	builder.release(contentIndex)
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
	block.Thinking = builder.appendContent(contentIndex, block.Thinking, delta)
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
	builder.partial.Content[contentIndex] = block
	builder.release(contentIndex)
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
	block.Text = builder.appendContent(builder.activeText, block.Text, delta)
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

// activeTextSignature is the signature of the open text block, empty when there is none.
func (builder *assistantStreamBuilder) activeTextSignature() string {
	if builder.activeText < 0 {
		return ""
	}
	return builder.partial.Content[builder.activeText].(TextContent).TextSignature
}

// activeThinkingSignature is the signature of the open thinking block, empty when there is none.
func (builder *assistantStreamBuilder) activeThinkingSignature() string {
	if builder.activeThinking < 0 {
		return ""
	}
	return builder.partial.Content[builder.activeThinking].(ThinkingContent).ThinkingSignature
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
	block.Thinking = builder.appendContent(builder.activeThinking, block.Thinking, delta)
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
	state := &streamToolCall{contentIndex: len(builder.partial.Content), custom: delta.custom}
	arguments := delta.initialArguments
	var order schemaObjectOrder
	if delta.custom != nil {
		arguments = JsonObject{delta.custom.property: delta.custom.buffer.Input()}
	} else {
		state.args = NewToolCallArgumentsStream()
		switch {
		case delta.initialJSON == "":
		case delta.initialUnparsed:
			state.args.Record(delta.initialJSON)
		default:
			arguments, order = state.args.Append(delta.initialJSON)
		}
	}
	if arguments == nil {
		arguments = JsonObject{}
	}
	builder.toolCalls[delta.index] = state
	call := ToolCall{ID: delta.id, Name: delta.name, Arguments: arguments, Namespace: delta.namespace}
	call.argumentOrder = order
	builder.partial.Content = append(builder.partial.Content, call)
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
	switch {
	case state.args == nil:
	case delta.recordOnly:
		state.args.Record(delta.argumentsDelta)
	case delta.argumentsDelta != "" || !delta.skipEmpty:
		arguments, order := state.args.Append(delta.argumentsDelta)
		builder.showStreamedArguments(state.contentIndex, &block, state.args, arguments, order)
	}
	builder.partial.Content[state.contentIndex] = block
	builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: delta.argumentsDelta, Partial: builder.partial})
}

// convertToCustomToolCall makes the open call at index a custom (grammar) tool call with the empty input of custom, when it is
// not one yet (openai-completions learns a call is custom from a later delta).
func (builder *assistantStreamBuilder) convertToCustomToolCall(index int, custom *customToolInput) {
	state := builder.toolCalls[index]
	if state == nil || state.custom != nil {
		return
	}
	state.custom, state.args = custom, nil
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	block.Arguments = JsonObject{custom.property: ""}
	block.argumentOrder, block.nonObjectArguments = nil, nil
	builder.partial.Content[state.contentIndex] = block
}

// replaceToolCallArguments takes the provider's complete argument text of a function call (ToolCallArgumentsStream.Replace): the
// call shows the partial arguments of text, and delta is the piece of it the stream had not received, when text continues it.
func (builder *assistantStreamBuilder) replaceToolCallArguments(index int, text string) (delta string, hasDelta bool) {
	state := builder.toolCalls[index]
	if state == nil || state.args == nil {
		return "", false
	}
	delta, hasDelta, arguments, order := state.args.Replace(text)
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	builder.showStreamedArguments(state.contentIndex, &block, state.args, arguments, order)
	builder.partial.Content[state.contentIndex] = block
	return delta, hasDelta
}

// showStreamedArguments makes the call at contentIndex show the partial arguments stream returned (arguments and their member order),
// and records them as the snapshot the cell may keep as it is (streamedArguments) when the stream says it is immutable, forgetting any
// earlier one otherwise.
func (builder *assistantStreamBuilder) showStreamedArguments(contentIndex int, call *ToolCall, stream *ToolCallArgumentsStream, arguments JsonObject, order schemaObjectOrder) {
	call.setParsedArguments(arguments, order, nil)
	if !stream.immutable() {
		delete(builder.streamedArguments, contentIndex)
		return
	}
	if builder.streamedArguments == nil {
		builder.streamedArguments = map[int]JsonObject{}
	}
	builder.streamedArguments[contentIndex] = arguments
}

// pushToolCallDelta pushes a toolcall_delta event for the call at index, whose arguments the caller already updated.
func (builder *assistantStreamBuilder) pushToolCallDelta(index int, delta string) {
	if state := builder.toolCalls[index]; state != nil {
		builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: delta, Partial: builder.partial})
	}
}

// takeFunctionCallItem gives the function call at index the complete argument text of its finished item, when it has one, and the
// item's namespace; endToolCall then reads the final arguments from the text.
func (builder *assistantStreamBuilder) takeFunctionCallItem(index int, namespace, text string) {
	state := builder.toolCalls[index]
	if state == nil || state.args == nil {
		return
	}
	if text != "" {
		state.args.Replace(text)
	}
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	if namespace != "" {
		block.Namespace = namespace
	}
	builder.partial.Content[state.contentIndex] = block
}

// appendCustomToolCallInput appends delta to the input of the custom tool call at index and returns the matching piece of its JSON
// arguments text; the call shows the input so far.
func (builder *assistantStreamBuilder) appendCustomToolCallInput(index int, delta string) (string, bool, error) {
	state := builder.toolCalls[index]
	if state == nil || state.custom == nil {
		return "", false, nil
	}
	jsonDelta, ok, err := appendGrammarToolInputJSONDelta(&state.custom.buffer, state.custom.property, delta)
	if err != nil {
		return "", false, err
	}
	present := ok || state.custom.unsent != ""
	return builder.sendCustomToolCallInput(state, jsonDelta), present, nil
}

// closeCustomToolCallInput closes the input of the custom tool call at index with the provider's complete input, if it sent one.
func (builder *assistantStreamBuilder) closeCustomToolCallInput(index int, finalInput *string) (string, bool, error) {
	state := builder.toolCalls[index]
	if state == nil || state.custom == nil {
		return "", false, nil
	}
	jsonDelta, ok, err := closeGrammarToolInputJSON(&state.custom.buffer, state.custom.property, finalInput)
	if err != nil {
		return "", false, err
	}
	present := ok || state.custom.unsent != ""
	return builder.sendCustomToolCallInput(state, jsonDelta), present, nil
}

func (builder *assistantStreamBuilder) sendCustomToolCallInput(state *streamToolCall, jsonDelta string) string {
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	block.Arguments = JsonObject{state.custom.property: state.custom.buffer.Input()}
	block.argumentOrder, block.nonObjectArguments = nil, nil
	builder.partial.Content[state.contentIndex] = block
	if state.custom.unsent == "" {
		return jsonDelta
	}
	sent := state.custom.unsent + jsonDelta
	state.custom.unsent = ""
	return sent
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
	builder.recordTransportFailure(err)
	builder.finishBlocks()
	builder.partial.StopReason = reason
	if err != nil {
		builder.partial.ErrorMessage = builder.failureMessage(err)
	}
	builder.push(ErrorEvent{Reason: reason, Error: builder.partial})
}

// failFaux is fail for a faux provider's failure: faux.ts createErrorMessage builds the message with errorMessage before
// timestamp.
func (builder *assistantStreamBuilder) failFaux(reason StopReason, err error) {
	builder.partial.ErrorBeforeTimestamp = true
	builder.fail(reason, err)
}

// recordTransportFailure appends the provider_transport_failure diagnostic for a transport failure; the message, stop reason and events are untouched.
func (builder *assistantStreamBuilder) recordTransportFailure(err error) {
	if err == nil {
		return
	}
	if diagnostic, ok := transportFailureDiagnostic(err, builder.started); ok {
		AppendAssistantMessageDiagnostic(builder.partial, diagnostic)
	}
}

// failureMessage is the ErrorMessage recorded for err.
func (builder *assistantStreamBuilder) failureMessage(err error) string {
	if builder.errorMessage != nil {
		return builder.errorMessage(err.Error())
	}
	return err.Error()
}

func (builder *assistantStreamBuilder) finishBlocks() {
	builder.finishBlocksWithThinking()
}

func (builder *assistantStreamBuilder) finishBlocksWithThinking(thinkingContentIndices ...int) {
	blocks := make([]endingBlock, 0, len(builder.toolCalls)+len(thinkingContentIndices)+2)
	if builder.activeText >= 0 {
		blocks = append(blocks, endingBlock{contentIndex: builder.activeText, kind: "text"})
	}
	if builder.activeThinking >= 0 {
		blocks = append(blocks, endingBlock{contentIndex: builder.activeThinking, kind: "thinking"})
	}
	for _, contentIndex := range thinkingContentIndices {
		blocks = append(blocks, endingBlock{contentIndex: contentIndex, kind: "thinkingBlock"})
	}
	for toolIndex, tool := range builder.toolCalls {
		blocks = append(blocks, endingBlock{contentIndex: tool.contentIndex, kind: "tool", toolIndex: toolIndex})
	}
	sortEndingBlocks(blocks)
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

func (builder *assistantStreamBuilder) endToolCall(index int) { builder.endToolCallWith(index, nil) }

// finalArgumentsAPI reports whether the API's tool-call end reads the complete arguments with parseToolCallArguments
// (Pi 87f9a364: anthropic-messages, bedrock-converse-stream, mistral-conversations, openai-completions and the
// openai-responses family). Google and faux build the whole call at once with their own fallback and keep
// parseStreamingJson's reading, so a non-object value such as [] stays as sent.
func finalArgumentsAPI(api API) bool {
	switch api {
	case APIAnthropicMessages, APIBedrockConverseStream, APIMistralConversations, APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses:
		return true
	}
	return false
}

// endToolCallWith ends a tool call; finalize, when non-nil, edits the ToolCall of the end event only, as faux.ts pushes the response block while its partial message keeps the streamed block.
func (builder *assistantStreamBuilder) endToolCallWith(index int, finalize func(*ToolCall)) {
	state := builder.toolCalls[index]
	if state == nil {
		return
	}
	block := builder.partial.Content[state.contentIndex].(ToolCall)
	switch {
	case state.custom != nil:
		delta, emit, err := closeGrammarToolInputJSONState(state)
		if err != nil {
			builder.failUnfinished(StopReasonError, err)
			return
		}
		builder.partial.Content[state.contentIndex] = block
		if emit {
			builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: delta, Partial: builder.partial})
		}
	case finalArgumentsAPI(builder.partial.API):
		block.SetFinalArguments(state.args.Text())
	default:
		block.SetStreamingArguments(state.args.Text())
	}
	if block.ID == "" {
		// pig divergence (D120): a call streamed without an id gets a unique one, so its tool result can name it.
		block.ID = generatedToolCallID(block.Name)
	}
	builder.partial.Content[state.contentIndex] = block
	ended := block
	if finalize != nil {
		finalize(&ended)
	}
	builder.push(ToolCallEndEvent{ContentIndex: state.contentIndex, ToolCall: ended, Partial: builder.partial})
	delete(builder.toolCalls, index)
}

func (builder *assistantStreamBuilder) endText() {
	if builder.activeText < 0 {
		return
	}
	block := builder.partial.Content[builder.activeText].(TextContent)
	builder.push(TextEndEvent{ContentIndex: builder.activeText, Content: block.Text, Partial: builder.partial})
	builder.release(builder.activeText)
	builder.activeText = -1
}

func (builder *assistantStreamBuilder) endThinking() {
	if builder.activeThinking < 0 {
		return
	}
	block := builder.partial.Content[builder.activeThinking].(ThinkingContent)
	builder.push(ThinkingEndEvent{ContentIndex: builder.activeThinking, Content: block.Thinking, Partial: builder.partial})
	builder.release(builder.activeThinking)
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

// publishesViews reports whether pushes publish live views of the partial (publish). A producer outside the executor pushes
// emission-time snapshots.
func (builder *assistantStreamBuilder) publishesViews() bool {
	return builder.managed && builder.turn != nil
}

func (builder *assistantStreamBuilder) publish() *AssistantMessage {
	builder.dirty = false
	if builder.cell == nil {
		builder.cell = newAssistantMessageCell(builder.partial)
	} else {
		builder.cell.publishSharing(builder.partial, builder.replacements, builder.streamedArguments)
	}
	builder.replacements = assistantMessageReplacements{}
	if !builder.publishesViews() {
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
	// The stream timed the final message in place (event-stream.ts:127-128 mutates the same output object every earlier event shares), so the live view the earlier events retain shows durationMs too.
	switch event.(type) {
	case DoneEvent, ErrorEvent:
		builder.publish()
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

// streamedToolCallCounter numbers the ids generatedToolCallID gives tool calls a provider streamed without one.
var streamedToolCallCounter atomic.Int64

// generatedToolCallID is the id of a tool call streamed without one: name_<epoch ms>_<counter>, the form Pi's Google adapters generate for a missing id (google-generative-ai.ts:195-200), so it is unique across turns.
func generatedToolCallID(name string) string {
	//portlint:allow clock the id reads the wall clock as Pi's Date.now() does (google-generative-ai.ts:200); the ai package's clock seams are OAuth provider fields that do not reach the stream builder
	return name + "_" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "_" + strconv.FormatInt(streamedToolCallCounter.Add(1), 10)
}

// endingBlock is a block still open when its stream ends.
type endingBlock struct {
	contentIndex int
	kind         string
	toolIndex    int
}

// sortEndingBlocks orders blocks by content index, the order their end events follow. Content indices are distinct, so the unstable sort is deterministic; the typed comparison avoids sort.Slice's reflective swapper, which allocates on every stream end.
func sortEndingBlocks(blocks []endingBlock) {
	slices.SortFunc(blocks, func(a, b endingBlock) int { return cmp.Compare(a.contentIndex, b.contentIndex) })
}

// closeGrammarToolInputJSONState closes the input of a custom tool call that ends without a complete input of its own.
func closeGrammarToolInputJSONState(state *streamToolCall) (string, bool, error) {
	jsonDelta, ok, err := closeGrammarToolInputJSON(&state.custom.buffer, state.custom.property, nil)
	if err != nil {
		return "", false, err
	}
	return jsonDelta, ok, nil
}

// Errors preserve the last parsed arguments but do not synthesize successful block ends.
// upstream: packages/ai/src/api/openai-completions.ts:stream
// upstream: packages/ai/src/api/openai-responses.ts:stream
func (builder *assistantStreamBuilder) failUnfinished(reason StopReason, err error) {
	clear(builder.toolCalls)
	builder.recordTransportFailure(err)
	builder.partial.StopReason = reason
	if err != nil {
		builder.partial.ErrorMessage = builder.failureMessage(err)
	}
	builder.push(ErrorEvent{Reason: reason, Error: builder.partial})
}

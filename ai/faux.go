package ai

// Ports packages/ai/src/providers/faux.ts
// Every resolved, non-aborted response starts before its content or terminal event, including empty error responses.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

const (
	fauxDefaultAPI          = "faux"
	fauxDefaultProvider     = "faux"
	fauxDefaultModelID      = "faux-1"
	fauxDefaultMinTokenSize = 3
	fauxDefaultMaxTokenSize = 5
)

// FauxContentBlockType is the "type" of a FauxContentBlock: the closed union of TextContent | ThinkingContent | ToolCall types (faux.ts FauxContentBlock).
type FauxContentBlockType string

// The FauxContentBlock types.
const (
	FauxContentText     FauxContentBlockType = "text"
	FauxContentThinking FauxContentBlockType = "thinking"
	FauxContentToolCall FauxContentBlockType = "toolCall"
)

type FauxContentBlock struct {
	Type FauxContentBlockType `json:"type"`
	Text string               `json:"text,omitempty"`
	// TextSignature is the optional TextContent member of a text block (types.ts TextContent.textSignature). Like the optional ToolCall members, only the final message keeps it.
	TextSignature string `json:"textSignature,omitempty"`
	Thinking      string `json:"thinking,omitempty"`
	// ThinkingSignature and Redacted are the optional ThinkingContent members of a thinking block (types.ts ThinkingContent). Only the final message keeps them.
	ThinkingSignature string         `json:"thinkingSignature,omitempty"`
	Redacted          bool           `json:"redacted,omitempty"`
	ID                string         `json:"id,omitempty"`
	Name              string         `json:"name,omitempty"`
	Arguments         map[string]any `json:"arguments,omitempty"`
	// ThoughtSignature and Namespace are the optional ToolCall members of a toolCall block (types.ts ToolCall). The stream carries them on the toolcall_end event and the final message, as faux.ts does; the partial message of the events keeps only id, name and arguments.
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
	Namespace        string `json:"namespace,omitempty"`
}

func FauxText(text string) FauxContentBlock {
	return FauxContentBlock{Type: FauxContentText, Text: text}
}
func FauxThinking(thinking string) FauxContentBlock {
	return FauxContentBlock{Type: FauxContentThinking, Thinking: thinking}
}

// FauxToolCallOptions are the options of faux.ts fauxToolCall.
type FauxToolCallOptions struct {
	// ID names the tool call; empty draws a random "tool:..." id.
	ID string
}

// FauxToolCall is faux.ts fauxToolCall(name, arguments, options = {}).
func FauxToolCall(name string, args map[string]any, options *FauxToolCallOptions) FauxContentBlock {
	id := ""
	if options != nil {
		id = options.ID
	}
	if id == "" {
		id = fauxRandomID("tool")
	}
	return FauxContentBlock{Type: FauxContentToolCall, ID: id, Name: name, Arguments: args}
}

// fauxRandomID is faux.ts randomId: `${prefix}:${Date.now()}:${Math.random().toString(36).slice(2)}`.
func fauxRandomID(prefix string) string {
	return fmt.Sprintf("%s:%d:%s", prefix, time.Now().UnixMilli(), strconv.FormatUint(rand.Uint64N(1<<52), 36))
}

// FauxAssistantMessageOptions are the options of faux.ts fauxAssistantMessage.
type FauxAssistantMessageOptions struct {
	StopReason   string
	Deferred     *DeferredHandle
	ErrorMessage string
	ResponseID   string
	Timestamp    *int64
}

// FauxAssistantContent is the `string | FauxContentBlock | FauxContentBlock[]` content of faux.ts fauxAssistantMessage: a [FauxContentString] becomes one text block, a
// [FauxContentBlock] one block and [FauxContentBlocks] the blocks as given. A nil content is no blocks.
type FauxAssistantContent interface {
	fauxBlocks() []FauxContentBlock
}

// FauxContentString is the string form of fauxAssistantMessage's content: one text block.
type FauxContentString string

// FauxContentBlocks is the array form of fauxAssistantMessage's content.
type FauxContentBlocks []FauxContentBlock

func (c FauxContentString) fauxBlocks() []FauxContentBlock {
	return []FauxContentBlock{FauxText(string(c))}
}
func (b FauxContentBlock) fauxBlocks() []FauxContentBlock  { return []FauxContentBlock{b} }
func (b FauxContentBlocks) fauxBlocks() []FauxContentBlock { return b }

// FauxAssistantMessage is faux.ts fauxAssistantMessage: a scripted response with the given content blocks, a "stop"
// stop reason unless options say otherwise, and the creation time as its timestamp unless options supply one.
func FauxAssistantMessage(content FauxAssistantContent, options FauxAssistantMessageOptions) AssistantMessage {
	stopReason := options.StopReason
	// upstream: packages/ai/src/providers/faux.ts:fauxAssistantMessage
	if stopReason == "" {
		stopReason = "stop"
	}
	timestamp := options.Timestamp
	if timestamp == nil {
		timestamp = new(time.Now().UnixMilli())
	}
	var blocks []FauxContentBlock
	if content != nil {
		blocks = content.fauxBlocks()
	}
	return FauxResponse{API: fauxDefaultAPI, Provider: fauxDefaultProvider, Model: fauxDefaultModelID, Content: blocks, StopReason: stopReason, Deferred: options.Deferred, ErrorMessage: options.ErrorMessage, ResponseID: options.ResponseID, Timestamp: timestamp}.AssistantMessage()
}

// AssistantMessage is the AssistantMessage a scripted FauxResponse stands for, the value faux.ts factories and fauxAssistantMessage return. A response without a timestamp takes the creation time, as
// fauxAssistantMessage does.
func (r FauxResponse) AssistantMessage() AssistantMessage {
	timestamp := time.Now().UnixMilli()
	if r.Timestamp != nil {
		timestamp = *r.Timestamp
	}
	message := AssistantMessage{
		API: r.API, Provider: r.Provider, Model: r.Model, Usage: r.Usage, Deferred: cloneDeferredHandle(r.Deferred), Timestamp: timestamp,
		StopReason: StopReason(r.StopReason), ErrorMessage: r.ErrorMessage, ResponseID: r.ResponseID, ResponseModel: r.ResponseModel,
		ProviderThinkingLevel: r.ProviderThinkingLevel, ThinkingLevel: r.ThinkingLevel, Diagnostics: cloneDiagnostics(r.Diagnostics),
		RawStopReason: r.RawStopReason, DurationMs: cloneInt64Pointer(r.DurationMs),
		Content: make([]AssistantContentBlock, 0, len(r.Content)),
	}
	if r.EndTurn != nil {
		message.EndTurn = new(*r.EndTurn)
	}
	for _, block := range r.Content {
		switch block.Type {
		case FauxContentText:
			message.Content = append(message.Content, TextContent{Text: block.Text, TextSignature: block.TextSignature})
		case FauxContentThinking:
			message.Content = append(message.Content, ThinkingContent{Thinking: block.Thinking, ThinkingSignature: block.ThinkingSignature, Redacted: block.Redacted})
		case FauxContentToolCall:
			message.Content = append(message.Content, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Arguments, ThoughtSignature: block.ThoughtSignature, Namespace: block.Namespace})
		}
	}
	return message
}

// FauxResponse is the scripted AssistantMessage of faux.ts: a FauxResponseStep that carries one response. API, Provider, Model and Usage are the
// message's own members; the provider replaces them on every message it streams (faux.ts cloneMessage and withUsageEstimate), so a scripted value
// never reaches a caller.
type FauxResponse struct {
	API      API
	Provider string
	Model    string
	Usage    Usage
	Deferred *DeferredHandle
	usage    *Usage
	// Timestamp supplies the response's Unix-millisecond timestamp; nil keeps the provider clock.
	Timestamp    *int64
	Content      []FauxContentBlock
	StopReason   string
	ErrorMessage string
	ResponseID   string
	// The scripted message keeps these members on every partial and on the final message (faux.ts:282-291 overwrites only api, provider, model, timestamp and usage).
	ResponseModel         string
	ProviderThinkingLevel string
	ThinkingLevel         ModelThinkingLevel
	Diagnostics           []AssistantMessageDiagnostic
	RawStopReason         string
	EndTurn               *bool
	// DurationMs is the scripted message's durationMs; the event stream measures it only when the message has none (event-stream.ts:127).
	DurationMs *int64
}

// FauxProviderState is the live state of a faux provider (faux.ts FauxProviderState). A factory receives it while
// other streams run, so every member is read through a method that is safe across concurrent factories.
type FauxProviderState struct {
	callCount          atomic.Int64
	deferredFetchCount atomic.Int64
	mu                 sync.Mutex
	cancelledDeferred  []DeferredHandle
}

// CallCount is the number of streams started on the provider (faux.ts FauxProviderState.callCount).
func (s *FauxProviderState) CallCount() int { return int(s.callCount.Load()) }

// DeferredFetchCount is the number of deferred fetches started on the provider (faux.ts FauxProviderState.deferredFetchCount).
func (s *FauxProviderState) DeferredFetchCount() int { return int(s.deferredFetchCount.Load()) }

// CancelledDeferred returns copies of the handles passed to the provider's cancelDeferred, in call order
// (faux.ts FauxProviderState.cancelledDeferred).
func (s *FauxProviderState) CancelledDeferred() []DeferredHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]DeferredHandle, len(s.cancelledDeferred))
	for i := range s.cancelledDeferred {
		result[i] = *cloneDeferredHandle(&s.cancelledDeferred[i])
	}
	return result
}

func (s *FauxProviderState) recordCancelledDeferred(handle DeferredHandle) {
	s.mu.Lock()
	s.cancelledDeferred = append(s.cancelledDeferred, *cloneDeferredHandle(&handle))
	s.mu.Unlock()
}

// FauxResponseFactory is awaited by its stream; a returned error terminates that stream before generation.
type FauxResponseFactory func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (AssistantMessage, error)

// FauxResponseStep is faux.ts FauxResponseStep, `AssistantMessage | FauxResponseFactory`: a scripted AssistantMessage (an ai.AssistantMessage or the
// FauxResponse that fauxAssistantMessage builds) or a FauxResponseFactory.
type FauxResponseStep interface {
	fauxResponseStep()
}

func (FauxResponse) fauxResponseStep()        {}
func (AssistantMessage) fauxResponseStep()    {}
func (FauxResponseFactory) fauxResponseStep() {}

// FauxStaticStep is the step for a scripted response.
func FauxStaticStep(response FauxResponse) FauxResponseStep { return response }

// FauxFactoryStep is the step for a function literal, which has no FauxResponseFactory methods until it is converted.
func FauxFactoryStep(factory FauxResponseFactory) FauxResponseStep { return factory }

// fauxResponseFromMessage is the scripted response an AssistantMessage step stands for: its content blocks and every member the stream keeps.
func fauxResponseFromMessage(message AssistantMessage) FauxResponse {
	response := FauxResponse{
		API: message.API, Provider: message.Provider, Model: message.Model, Usage: message.Usage,
		Deferred: message.Deferred, Timestamp: new(message.Timestamp), StopReason: string(message.StopReason), ErrorMessage: message.ErrorMessage, ResponseID: message.ResponseID,
		ResponseModel: message.ResponseModel, ProviderThinkingLevel: message.ProviderThinkingLevel, ThinkingLevel: message.ThinkingLevel,
		Diagnostics: message.Diagnostics, RawStopReason: message.RawStopReason, EndTurn: message.EndTurn, DurationMs: cloneInt64Pointer(message.DurationMs),
		Content: make([]FauxContentBlock, 0, len(message.Content)),
	}
	for _, block := range message.Content {
		switch block := block.(type) {
		case TextContent:
			response.Content = append(response.Content, FauxContentBlock{Type: FauxContentText, Text: block.Text, TextSignature: block.TextSignature})
		case ThinkingContent:
			response.Content = append(response.Content, FauxContentBlock{Type: FauxContentThinking, Thinking: block.Thinking, ThinkingSignature: block.ThinkingSignature, Redacted: block.Redacted})
		case ToolCall:
			response.Content = append(response.Content, FauxContentBlock{Type: FauxContentToolCall, ID: block.ID, Name: block.Name, Arguments: block.Arguments, ThoughtSignature: block.ThoughtSignature, Namespace: block.Namespace})
		}
	}
	return response
}

type FauxModelDefinition struct {
	ID        string
	Name      string
	Reasoning bool
	Input     []string
	// InputLimits mirrors upstream FauxModelDefinition.inputLimits.
	InputLimits *ModelInputLimits
	// Cost is the per-million-token price; nil is a free model. Its Tiers are ignored (upstream's cost has none).
	Cost          *ModelCost
	ContextWindow int
	MaxTokens     int
}

type FauxConfig struct {
	Deferred        *FauxDeferredConfig
	API             API
	ProviderID      string
	Model           string
	Models          []FauxModelDefinition
	TokensPerSecond int
	// TokenSize is faux.ts RegisterFauxProviderOptions.tokenSize: the streamed chunk size range in tokens. A nil bound takes the default (3, 5).
	TokenSize *FauxTokenSize
}

// FauxTokenSize is the faux.ts tokenSize option {min?, max?}. A nil Min or Max is absent (the TypeScript `??` default), so a set 0 is not the default.
type FauxTokenSize struct {
	Min *int
	Max *int
}

// RegisterFauxProviderOptions is faux.ts RegisterFauxProviderOptions: the options NewFauxProvider takes.
type RegisterFauxProviderOptions = FauxConfig

// FauxProviderHandle is faux.ts FauxProviderHandle: the registered faux provider with its models and shared response queue.
type FauxProviderHandle struct {
	cfg          FauxConfig
	minTokenSize int
	maxTokenSize int
	mu           sync.Mutex
	responses    []FauxResponseStep
	state        FauxProviderState
	models       []*Model
	promptCache  map[string]fauxPrompt
	afterPush    func()
	unregistered bool
	// sourceID is the registry source id RegisterFauxProvider registered the handle's API under; empty for a handle NewFauxProvider returned.
	sourceID          string
	definition        *ModelsProvider
	deferredResponses map[string]*fauxDeferredResponse
}

// FauxProviderRegistration is faux.ts FauxProviderRegistration: the handle plus unregister, which [FauxProviderHandle.Unregister] is.
type FauxProviderRegistration = FauxProviderHandle

type fauxModelProvider struct {
	owner *FauxProviderHandle
	model *Model
}

func (p *fauxModelProvider) ID() string   { return p.owner.ID() }
func (p *fauxModelProvider) Close() error { return nil }
func (p *fauxModelProvider) Stream(ctx context.Context, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return p.owner.streamModel(ctx, p.model, request, options)
}

// RegisterFauxProvider is compat.ts registerFauxProvider: it creates the faux provider (createFauxCore) and registers its API in the global API registry under a
// generated `faux-provider-<id>` source id, so [Stream] and [StreamSimple] reach it for a model of its API. Unregister removes the registry entry.
func RegisterFauxProvider(options ...RegisterFauxProviderOptions) *FauxProviderRegistration {
	var cfg RegisterFauxProviderOptions
	if len(options) > 0 {
		cfg = options[0]
	}
	handle := CreateFauxCore(cfg).handle
	suffix := strconv.FormatUint(rand.Uint64N(1<<52), 36)
	handle.sourceID = "faux-provider-" + suffix[:min(len(suffix), 8)]
	RegisterAPIProvider(APIProvider{API: handle.cfg.API, Stream: handle.streamModel, StreamSimple: handle.streamModel}, handle.sourceID)
	return handle
}

// NewFauxProvider creates a deterministic response source with a shared queue and per-session cache simulation.
func NewFauxProvider(cfg FauxConfig) *FauxProviderHandle {
	if cfg.API == "" {
		cfg.API = API(fauxRandomID(fauxDefaultAPI))
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = fauxDefaultProvider
	}
	if cfg.Model == "" {
		cfg.Model = fauxDefaultModelID
	}
	// faux.ts createFauxCore: minTokenSize = max(1, min(tokenSize?.min ?? DEFAULT_MIN, tokenSize?.max ?? DEFAULT_MAX)); maxTokenSize = max(minTokenSize, tokenSize?.max ?? DEFAULT_MAX).
	tokenSize := cfg.TokenSize
	if tokenSize == nil {
		tokenSize = &FauxTokenSize{}
	}
	tokenSizeMax := fauxDefaultMaxTokenSize
	if tokenSize.Max != nil {
		tokenSizeMax = *tokenSize.Max
	}
	tokenSizeMin := fauxDefaultMinTokenSize
	if tokenSize.Min != nil {
		tokenSizeMin = *tokenSize.Min
	}
	minTokenSize := max(1, min(tokenSizeMin, tokenSizeMax))
	maxTokenSize := max(minTokenSize, tokenSizeMax)
	p := &FauxProviderHandle{cfg: cfg, minTokenSize: minTokenSize, maxTokenSize: maxTokenSize, promptCache: map[string]fauxPrompt{}, deferredResponses: map[string]*fauxDeferredResponse{}}
	definitions := cfg.Models
	if len(definitions) == 0 {
		definitions = []FauxModelDefinition{{ID: cfg.Model, Name: "Faux Model"}}
	}
	for _, definition := range definitions {
		name := definition.Name
		if name == "" {
			name = definition.ID
		}
		input := definition.Input
		if input == nil {
			input = []string{"text", "image"}
		}
		window, tokens := definition.ContextWindow, definition.MaxTokens
		if window == 0 {
			window = 128000
		}
		if tokens == 0 {
			tokens = 16384
		}
		model := &Model{ID: definition.ID, DisplayName: name, Input: slices.Clone(input), Capabilities: ModelCapabilities{ContextWindow: window, MaxOutputTokens: tokens, SupportsImages: slices.Contains(input, "image"), SupportsToolUse: true}, ProviderMeta: ProviderMetadata{ProviderID: cfg.ProviderID, API: cfg.API, Reasoning: definition.Reasoning, BaseURL: "http://localhost:0"}, InputLimits: definition.InputLimits}
		if cost := definition.Cost; cost != nil {
			model.Capabilities.InputCostPer1M, model.Capabilities.OutputCostPer1M = cost.Input, cost.Output
			model.Capabilities.CacheReadCostPer1M, model.Capabilities.CacheWriteCostPer1M = cost.CacheRead, cost.CacheWrite
		}
		if definition.Reasoning {
			model.Capabilities.MaxThinking = ThinkingLevelHigh
		}
		model.Provider = &fauxModelProvider{owner: p, model: model}
		p.models = append(p.models, model)
	}
	p.definition = p.makeDefinition()
	return p
}
func (p *FauxProviderHandle) ID() string { return p.cfg.ProviderID }

// API is the API name every model of the handle carries (faux.ts FauxProviderHandle.api): the requested one, else "faux:<time>:<random>".
func (p *FauxProviderHandle) API() API { return p.cfg.API }

// State is the live call counters of the provider (faux.ts FauxProviderHandle.state).
func (p *FauxProviderHandle) State() *FauxProviderState { return &p.state }

func (p *FauxProviderHandle) Close() error     { return nil }
func (p *FauxProviderHandle) Models() []*Model { return slices.Clone(p.models) }

// GetModel returns a canonical model with its provider identity, or nil for an unknown ID.
func (p *FauxProviderHandle) GetModel(id ...string) *Model {
	if len(id) == 0 || id[0] == "" {
		return p.models[0]
	}
	for _, model := range p.models {
		if model.ID == id[0] {
			return model
		}
	}
	return nil
}
func (p *FauxProviderHandle) Unregister() {
	p.mu.Lock()
	p.unregistered = true
	sourceID := p.sourceID
	p.mu.Unlock()
	if sourceID != "" {
		UnregisterAPIProviders(sourceID)
	}
}
func (p *FauxProviderHandle) SetResponses(responses []FauxResponseStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = slices.Clone(responses)
}
func (p *FauxProviderHandle) AppendResponses(responses []FauxResponseStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, responses...)
}
func (p *FauxProviderHandle) PendingResponseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.responses)
}
func (p *FauxProviderHandle) CallCount() int { return p.state.CallCount() }
func (p *FauxProviderHandle) Stream(ctx context.Context, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return p.streamModel(ctx, p.models[0], request, options)
}

func (p *FauxProviderHandle) streamModel(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, request); err != nil {
		return nil, fmt.Errorf("faux: invalid transcript: %w", err)
	}
	p.mu.Lock()
	if p.unregistered {
		p.mu.Unlock()
		return nil, fmt.Errorf("No API provider registered for api: %s", p.cfg.API)
	}
	var step *FauxResponseStep
	if len(p.responses) > 0 {
		value := p.responses[0]
		p.responses = p.responses[1:]
		step = &value
	}
	p.state.callCount.Add(1)
	p.mu.Unlock()
	return p.streamStep(ctx, model, request, options, step)
}

// streamStep ports faux.ts:stream. Pi creates the stream, then runs the response body from `queueMicrotask(async () => ...)`:
// the turn is reserved here, in the same reaction position, and every await below is one turn suspension in the same order.
func (p *FauxProviderHandle) streamStep(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions, step *FauxResponseStep) (*AssistantMessageEventStream, error) {
	builder := newObservedProviderBuilder(ctx, p.cfg.API, p.cfg.ProviderID, model.ID)
	// streamWithDeltas spreads each partial envelope; its content arrays are replaced when a new block begins.
	builder.partialCopy = (*AssistantMessage).ShallowCopy
	builder.afterPush = p.afterPush
	turn := builder.stream.executor.newTurn()
	go turn.run(func(turn *continuationTurn) {
		defer builder.produceUnder(turn)()
		// faux.ts:stream `await streamOptions?.onResponse?.(...)`. The handler's own asynchronous work is a separate scoped wait.
		var hookErr error
		if options.OnResponse != nil {
			hookErr = options.OnResponse(ctx, ProviderResponse{Status: 200, Headers: map[string]string{}}, model)
		}
		suspendContinuation(turn)
		if hookErr != nil {
			builder.fail(StopReasonError, hookErr)
			return
		}
		if step == nil {
			builder.setUsage(p.estimateUsage(request, options, nil))
			builder.fail(StopReasonError, errors.New("No more faux responses queued"))
			return
		}
		if options.Deferred != nil && (options.Deferred.Enabled || options.Deferred.Object) {
			handle := p.submitDeferred(model, request, options, *step)
			builder.partial.Deferred = &handle
			p.streamWithDeltas(turn, ctx, builder, FauxResponse{StopReason: string(StopReasonDeferred), Deferred: &handle, usage: &Usage{}})
			return
		}
		response, err := p.resolveResponse(turn, *step, request, options, model)
		if err != nil {
			builder.fail(StopReasonError, err)
			return
		}
		p.streamWithDeltas(turn, ctx, builder, response)
	})
	return builder.stream, nil
}

// produceUnder makes turn the stream's producer, so pushes publish live cells whose fields the consumer's tick materializes: faux.ts pushes `{...partial}` copies that share the block objects it keeps mutating.
func (builder *assistantStreamBuilder) produceUnder(turn *continuationTurn) (release func()) {
	builder.managed = true
	builder.turn = turn
	stream := builder.stream
	stream.mu.Lock()
	stream.producer = turn
	stream.mu.Unlock()
	return func() {
		stream.mu.Lock()
		stream.producer = nil
		stream.mu.Unlock()
		builder.turn = nil
	}
}

// resolveResponse ports faux.ts:resolveResponse and its caller's `await resolveResponse(...)`. A static step returns an already resolved promise (one reaction); a factory adds the `await step(...)` inside the async function (a second).
func (p *FauxProviderHandle) resolveResponse(turn *continuationTurn, step FauxResponseStep, request TranscriptContext, options StreamOptions, model *Model) (FauxResponse, error) {
	var response FauxResponse
	var err error
	switch step := step.(type) {
	case FauxResponse:
		response = step
	case AssistantMessage:
		response = fauxResponseFromMessage(step)
	case FauxResponseFactory:
		var message AssistantMessage
		message, err = step(request, options, &p.state, model)
		response = fauxResponseFromMessage(message)
		suspendContinuation(turn)
	default:
		err = errors.New("faux response step has neither static nor factory")
	}
	suspendContinuation(turn)
	if err != nil {
		return FauxResponse{}, err
	}
	if response.usage == nil {
		response.usage = p.estimateUsage(request, options, response.Content)
	}
	return response, nil
}

// streamWithDeltas ports faux.ts:streamWithDeltas. Its synchronous prefix pushes start and the first block's start; each chunk waits in scheduleChunk. A thrown error settles the returned promise, so the caller's catch runs one reaction later.
func (p *FauxProviderHandle) streamWithDeltas(turn *continuationTurn, ctx context.Context, builder *assistantStreamBuilder, response FauxResponse) {
	builder.setUsage(response.usage)
	builder.partial.Deferred = cloneDeferredHandle(response.Deferred)
	builder.partial.ErrorMessage = response.ErrorMessage
	builder.setResponseMetadata(response.ResponseID, response.ResponseModel, response.RawStopReason, response.ProviderThinkingLevel, response.EndTurn)
	builder.partial.ThinkingLevel = response.ThinkingLevel
	builder.partial.Diagnostics = cloneDiagnostics(response.Diagnostics)
	builder.partial.DurationMs = cloneInt64Pointer(response.DurationMs)
	if response.Timestamp != nil {
		builder.partial.Timestamp = *response.Timestamp
	}
	if ctx.Err() != nil {
		abortFauxStream(builder)
		return
	}
	builder.start()
	// finalToolCalls holds the toolCall blocks whose optional members only the final message keeps: faux.ts pushes `message`, which holds the response blocks, while the partial message holds {type, id, name, arguments}.
	finalToolCalls := map[int]FauxContentBlock{}
	// finalBlocks holds the text and thinking blocks whose optional members (textSignature, thinkingSignature, redacted) only the final message keeps, the same way.
	finalBlocks := map[int]FauxContentBlock{}
	for index, block := range response.Content {
		if ctx.Err() != nil {
			abortFauxStream(builder)
			return
		}
		builder.replacements.Content = true
		switch block.Type {
		case "text":
			builder.endThinking()
			builder.endText()
			builder.activeText = builder.textBlockStart()
			textIndex := builder.activeText
			for _, chunk := range splitByTokenSize(block.Text, p.minTokenSize, p.maxTokenSize) {
				p.scheduleChunk(turn, chunk)
				if ctx.Err() != nil {
					abortFauxStream(builder)
					return
				}
				builder.textDelta(chunk)
			}
			builder.endText()
			if block.TextSignature != "" {
				finalBlocks[textIndex] = block
			}
		case "thinking":
			builder.endText()
			builder.endThinking()
			builder.activeThinking = builder.thinkingBlockStart()
			thinkingIndex := builder.activeThinking
			for _, chunk := range splitByTokenSize(block.Thinking, p.minTokenSize, p.maxTokenSize) {
				p.scheduleChunk(turn, chunk)
				if ctx.Err() != nil {
					abortFauxStream(builder)
					return
				}
				builder.thinkingDelta(chunk, false)
			}
			builder.endThinking()
			if block.ThinkingSignature != "" || block.Redacted {
				finalBlocks[thinkingIndex] = block
			}
		case "toolCall":
			builder.toolCallStart(streamToolCallDelta{index: index, id: block.ID, name: block.Name})
			arguments := SafeJsonStringify(block.Arguments)
			for _, chunk := range splitByTokenSize(arguments, p.minTokenSize, p.maxTokenSize) {
				p.scheduleChunk(turn, chunk)
				if ctx.Err() != nil {
					abortFauxStream(builder)
					return
				}
				state := builder.toolCalls[index]
				state.arguments.WriteString(chunk)
				builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: chunk, Partial: builder.partial})
			}
			contentIndex := builder.toolCalls[index].contentIndex
			builder.endToolCallWith(index, func(call *ToolCall) {
				call.ThoughtSignature, call.Namespace = block.ThoughtSignature, block.Namespace
			})
			if block.ThoughtSignature != "" || block.Namespace != "" {
				finalToolCalls[contentIndex] = block
			}
		}
	}
	if len(finalToolCalls) > 0 || len(finalBlocks) > 0 {
		builder.replacements.Content = true
	}
	for contentIndex, block := range finalBlocks {
		switch content := builder.partial.Content[contentIndex].(type) {
		case TextContent:
			content.TextSignature = block.TextSignature
			builder.partial.Content[contentIndex] = content
		case ThinkingContent:
			content.ThinkingSignature, content.Redacted = block.ThinkingSignature, block.Redacted
			builder.partial.Content[contentIndex] = content
		}
	}
	for contentIndex, block := range finalToolCalls {
		call := builder.partial.Content[contentIndex].(ToolCall)
		call.ThoughtSignature, call.Namespace = block.ThoughtSignature, block.Namespace
		builder.partial.Content[contentIndex] = call
	}
	reason := StopReason(response.StopReason)
	// upstream: packages/ai/src/providers/faux.ts:fauxAssistantMessage
	if reason == "" {
		reason = StopReasonStop
	}
	if reason == StopReasonPending {
		// The `throw` rejects streamWithDeltas' promise; the caller's `await` resumes into its catch one reaction later.
		suspendContinuation(turn)
		builder.partial.Content = []AssistantContentBlock{}
		builder.partial.Usage = Usage{}
		builder.replacements.Content = true
		builder.replacements.Usage = true
		// upstream: faux.ts createErrorMessage stamps the failure with Date.now(), not the scripted response's timestamp.
		//portlint:allow clock createErrorMessage reads Date.now() directly, not the injected clock
		builder.partial.Timestamp = time.Now().UnixMilli()
		builder.fail(StopReasonError, errors.New("Faux response ended without a stop reason"))
		return
	}
	if reason == StopReasonError || reason == StopReasonAborted {
		builder.fail(reason, errors.New(response.ErrorMessage))
		return
	}
	builder.done(reason, nil, response.ErrorMessage)
}

// Aborting a faux response preserves partial content without emitting block-end events.
func abortFauxStream(builder *assistantStreamBuilder) {
	builder.partial.StopReason = StopReasonAborted
	builder.partial.ErrorMessage = "Request was aborted"
	builder.partial.Timestamp = time.Now().UnixMilli()
	builder.push(ErrorEvent{Reason: StopReasonAborted, Error: builder.partial})
}

// scheduleChunk ports faux.ts:scheduleChunk and the `await` on it. Without a rate the promise resolves from `queueMicrotask`, so the await costs the queued resolve and then the resumption. With a rate it is a timer, an external completion.
func (p *FauxProviderHandle) scheduleChunk(turn *continuationTurn, chunk string) {
	executor := turn.executor
	scheduled := newContinuationPromise[struct{}](executor)
	if p.cfg.TokensPerSecond <= 0 {
		executor.post(func() { scheduled.resolve(struct{}{}) })
	} else {
		tokens := (utf16Length(chunk) + 3) / 4
		// Node clamps a timer delay below one millisecond to one (lib/internal/timers.js Timeout constructor).
		delay := max(time.Duration(float64(tokens)/float64(p.cfg.TokensPerSecond)*float64(time.Second)), time.Millisecond)
		time.AfterFunc(delay, func() { executor.postExternal(func() { scheduled.resolve(struct{}{}) }) })
	}
	awaitContinuation(turn, scheduled)
}

func splitByTokenSize(text string, minSize, maxSize int) []string {
	if text == "" {
		return []string{""}
	}
	var chunks []string
	for index := 0; index < len(text); {
		size := max(1, (minSize+rand.IntN(maxSize-minSize+1))*4)
		end := min(index+size, len(text))
		chunks = append(chunks, text[index:end])
		index = end
	}
	return chunks
}

func fauxAssistantText(content []FauxContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "thinking":
			parts = append(parts, block.Thinking)
		case "toolCall":
			parts = append(parts, block.Name+":"+SafeJsonStringify(block.Arguments))
		}
	}
	return strings.Join(parts, "\n")
}
func (p *FauxProviderHandle) estimateUsage(request TranscriptContext, options StreamOptions, content []FauxContentBlock) *Usage {
	var builder strings.Builder
	if options.SessionID != "" {
		// A session's next prompt is about as long as its last; sizing the builder once avoids its doubling garbage.
		p.mu.Lock()
		//portlint:allow numbers a capacity hint for the prompt builder, not a ported computation; the estimate itself does not depend on it
		builder.Grow(len(p.promptCache[options.SessionID].text) * 17 / 16)
		p.mu.Unlock()
	}
	if request.err == nil {
		for index, message := range request.messages {
			if index > 0 {
				builder.WriteString("\n\n")
			}
			switch message.(type) {
			case SystemMessage:
				builder.WriteString("system:")
			case UserMessage:
				builder.WriteString("user:")
			case AssistantMessage:
				builder.WriteString("assistant:")
			case ToolResultMessage:
				builder.WriteString("toolResult:")
			default:
				builder.WriteString(":")
			}
			writeFauxMessageText(&builder, message)
		}
	}
	prompt := builder.String()
	promptUnits, promptASCII := utf16Count(prompt)
	//portlint:allow numbers (n+3)/4 is Math.ceil(n / 4) for n >= 0 (faux.ts estimateTokens)
	tokens := (promptUnits + 3) / 4
	output := (utf16Length(fauxAssistantText(content)) + 3) / 4
	usage := &Usage{Input: tokens, Output: output}
	if options.SessionID != "" && options.CacheRetention != CacheRetentionNone {
		p.mu.Lock()
		previous := p.promptCache[options.SessionID]
		p.promptCache[options.SessionID] = fauxPrompt{text: prompt, ascii: promptASCII}
		p.mu.Unlock()
		if previous.text != "" {
			var common int
			if previous.ascii || promptASCII {
				// Every byte before the first difference is ASCII when either text is, and ASCII has one UTF-16 unit per byte.
				common = commonPrefixBytes(previous.text, prompt)
			} else {
				a, b := utf16.Encode([]rune(previous.text)), utf16.Encode([]rune(prompt))
				for common < min(len(a), len(b)) && a[common] == b[common] {
					common++
				}
			}
			usage.CacheRead = (common + 3) / 4
			//portlint:allow numbers (n+3)/4 is Math.ceil(n / 4) for n >= 0 (faux.ts estimateTokens of promptText.slice(cachedChars))
			usage.CacheWrite = (promptUnits - common + 3) / 4
			usage.Input = max(0, tokens-usage.CacheRead)
		} else {
			usage.CacheWrite = tokens
		}
	}
	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	return usage
}

// fauxPrompt is a serialized prompt kept for the next request's prefix comparison.
type fauxPrompt struct {
	text  string
	ascii bool
}

// utf16Count is utf16Length plus whether the text is pure ASCII.
func utf16Count(value string) (units int, ascii bool) {
	for index := 0; index+8 <= len(value); index += 8 {
		word := uint64(value[index]) | uint64(value[index+1])<<8 | uint64(value[index+2])<<16 | uint64(value[index+3])<<24 |
			uint64(value[index+4])<<32 | uint64(value[index+5])<<40 | uint64(value[index+6])<<48 | uint64(value[index+7])<<56
		if word&0x8080808080808080 != 0 {
			return utf16Length(value), false
		}
	}
	for index := len(value) &^ 7; index < len(value); index++ {
		if value[index] >= 0x80 {
			return utf16Length(value), false
		}
	}
	return len(value), true
}

// commonPrefixBytes is the length of the longest common prefix of a and b.
func commonPrefixBytes(a, b string) int {
	limit := min(len(a), len(b))
	index := 0
	for index+4096 <= limit && a[index:index+4096] == b[index:index+4096] {
		index += 4096
	}
	for index < limit && a[index] == b[index] {
		index++
	}
	return index
}

// writeFauxMessageText writes faux.ts messageToText(message) into builder without building its parts.
func writeFauxMessageText(builder *strings.Builder, message Message) {
	first := true
	part := func(text string) {
		if !first {
			builder.WriteByte('\n')
		}
		first = false
		builder.WriteString(text)
	}
	image := func(block ImageContent) {
		part(fmt.Sprintf("[image:%s:%d]", block.MimeType, utf16Length(block.Data)))
	}
	switch message := message.(type) {
	case SystemMessage:
		if text := GetCurrentSystemPrompt([]Message{message}); text != "" {
			part(text)
		}
		for _, tool := range message.ToolsRemoved {
			part("tool-:" + SafeJsonStringify(tool))
		}
		for _, tool := range message.ToolsAdded {
			part("tool+:" + SafeJsonStringify(tool))
		}
	case UserMessage:
		if text, ok := message.Content.(UserText); ok {
			builder.WriteString(string(text))
			return
		}
		blocks, _ := message.Content.(UserContentBlocks)
		for _, block := range blocks {
			switch block := block.(type) {
			case TextContent:
				part(block.Text)
			case ImageContent:
				image(block)
			}
		}
	case AssistantMessage:
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				part(block.Text)
			case ThinkingContent:
				part(block.Thinking)
			case ToolCall:
				part(block.Name + ":" + SafeJsonStringify(block.Arguments))
			}
		}
	case ToolResultMessage:
		part(message.ToolName)
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				part(block.Text)
			case ImageContent:
				image(block)
			}
		}
	}
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	return new(*value)
}

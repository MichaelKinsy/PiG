package ai

// Ports packages/ai/src/providers/faux.ts
// Every resolved, non-aborted response starts before its content or terminal event, including empty error responses.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

const (
	fauxDefaultMinTokenSize = 3
	fauxDefaultMaxTokenSize = 5
)

type FauxContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

func FauxText(text string) FauxContentBlock { return FauxContentBlock{Type: "text", Text: text} }
func FauxThinking(thinking string) FauxContentBlock {
	return FauxContentBlock{Type: "thinking", Thinking: thinking}
}
func FauxToolCall(name string, args map[string]any, id string) FauxContentBlock {
	if id == "" {
		id = fmt.Sprintf("tool:%d:%d", time.Now().UnixMilli(), rand.IntN(1000000))
	}
	return FauxContentBlock{Type: "toolCall", ID: id, Name: name, Arguments: args}
}

type FauxResponse struct {
	Deferred *DeferredHandle
	usage    *Usage
	// Timestamp supplies the response's Unix-millisecond timestamp; nil keeps the provider clock.
	Timestamp    *int64
	Content      []FauxContentBlock
	StopReason   string
	ErrorMessage string
	ResponseID   string
}

// FauxProviderState exposes the live call counter safely across concurrent factories.
type FauxProviderState struct {
	CallCount          atomic.Int64
	DeferredFetchCount atomic.Int64
}

// FauxResponseFactory is awaited by its stream; a returned error terminates that stream before generation.
type FauxResponseFactory func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (FauxResponse, error)
type FauxResponseStep struct {
	Static  *FauxResponse
	Factory FauxResponseFactory
}

func FauxStaticStep(response FauxResponse) FauxResponseStep {
	return FauxResponseStep{Static: &response}
}
func FauxFactoryStep(factory FauxResponseFactory) FauxResponseStep {
	return FauxResponseStep{Factory: factory}
}

type FauxModelDefinition struct {
	ID            string
	Name          string
	Reasoning     bool
	Input         []string
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
	MinTokenSize    int
	MaxTokenSize    int
}

type fauxProvider struct {
	cfg               FauxConfig
	mu                sync.Mutex
	responses         []FauxResponseStep
	state             FauxProviderState
	models            []*Model
	promptCache       map[string]string
	afterPush         func()
	unregistered      bool
	definition        *ModelsProvider
	deferredResponses map[string]*fauxDeferredResponse
	cancelledDeferred []DeferredHandle
}

type fauxModelProvider struct {
	owner *fauxProvider
	model *Model
}

func (p *fauxModelProvider) ID() string   { return p.owner.ID() }
func (p *fauxModelProvider) Close() error { return nil }
func (p *fauxModelProvider) Stream(ctx context.Context, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return p.owner.streamModel(ctx, p.model, request, options)
}

// NewFauxProvider creates a deterministic response source with a shared queue and per-session cache simulation.
func NewFauxProvider(cfg FauxConfig) *fauxProvider {
	if cfg.API == "" {
		cfg.API = "faux"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "faux"
	}
	if cfg.Model == "" {
		cfg.Model = "faux-1"
	}
	if cfg.MinTokenSize <= 0 {
		cfg.MinTokenSize = fauxDefaultMinTokenSize
	}
	if cfg.MaxTokenSize <= 0 {
		cfg.MaxTokenSize = fauxDefaultMaxTokenSize
	}
	cfg.MinTokenSize = max(1, min(cfg.MinTokenSize, cfg.MaxTokenSize))
	cfg.MaxTokenSize = max(cfg.MinTokenSize, cfg.MaxTokenSize)
	p := &fauxProvider{cfg: cfg, promptCache: map[string]string{}, deferredResponses: map[string]*fauxDeferredResponse{}}
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
		model := &Model{ID: definition.ID, DisplayName: name, Input: slices.Clone(input), Capabilities: ModelCapabilities{ContextWindow: window, MaxOutputTokens: tokens, SupportsImages: slices.Contains(input, "image"), SupportsToolUse: true}, ProviderMeta: ProviderMetadata{ProviderID: cfg.ProviderID, API: cfg.API, Reasoning: definition.Reasoning, BaseURL: "http://localhost:0"}}
		if definition.Reasoning {
			model.Capabilities.MaxThinking = ThinkingHigh
		}
		model.Provider = &fauxModelProvider{owner: p, model: model}
		p.models = append(p.models, model)
	}
	p.definition = p.makeDefinition()
	return p
}
func (p *fauxProvider) ID() string       { return p.cfg.ProviderID }
func (p *fauxProvider) Close() error     { return nil }
func (p *fauxProvider) Models() []*Model { return slices.Clone(p.models) }

// GetModel returns a canonical model with its provider identity, or nil for an unknown ID.
func (p *fauxProvider) GetModel(id ...string) *Model {
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
func (p *fauxProvider) Unregister() { p.mu.Lock(); defer p.mu.Unlock(); p.unregistered = true }
func (p *fauxProvider) SetResponses(responses []FauxResponseStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = slices.Clone(responses)
}
func (p *fauxProvider) AppendResponses(responses []FauxResponseStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, responses...)
}
func (p *fauxProvider) PendingResponseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.responses)
}
func (p *fauxProvider) CallCount() int { return int(p.state.CallCount.Load()) }
func (p *fauxProvider) Stream(ctx context.Context, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return p.streamModel(ctx, p.models[0], request, options)
}

func (p *fauxProvider) streamModel(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
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
	p.state.CallCount.Add(1)
	p.mu.Unlock()
	return p.streamStep(ctx, model, request, options, step)
}

// streamStep ports faux.ts:stream. Pi creates the stream, then runs the response body from `queueMicrotask(async () => ...)`:
// the turn is reserved here, in the same reaction position, and every await below is one turn suspension in the same order.
func (p *fauxProvider) streamStep(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions, step *FauxResponseStep) (*AssistantMessageEventStream, error) {
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
func (p *fauxProvider) resolveResponse(turn *continuationTurn, step FauxResponseStep, request TranscriptContext, options StreamOptions, model *Model) (FauxResponse, error) {
	var response FauxResponse
	var err error
	switch {
	case step.Static != nil:
		response = *step.Static
	case step.Factory != nil:
		response, err = step.Factory(request, options, &p.state, model)
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
func (p *fauxProvider) streamWithDeltas(turn *continuationTurn, ctx context.Context, builder *assistantStreamBuilder, response FauxResponse) {
	builder.setUsage(response.usage)
	builder.partial.Deferred = cloneDeferredHandle(response.Deferred)
	builder.partial.ErrorMessage = response.ErrorMessage
	if response.ResponseID != "" {
		builder.setResponseMetadata(response.ResponseID, "", "", "", nil)
	}
	if response.Timestamp != nil {
		builder.partial.Timestamp = *response.Timestamp
	}
	if ctx.Err() != nil {
		abortFauxStream(builder)
		return
	}
	builder.start()
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
			for _, chunk := range splitByTokenSize(block.Text, p.cfg.MinTokenSize, p.cfg.MaxTokenSize) {
				p.scheduleChunk(turn, chunk)
				if ctx.Err() != nil {
					abortFauxStream(builder)
					return
				}
				builder.textDelta(chunk)
			}
			builder.endText()
		case "thinking":
			builder.endText()
			builder.endThinking()
			builder.activeThinking = builder.thinkingBlockStart()
			for _, chunk := range splitByTokenSize(block.Thinking, p.cfg.MinTokenSize, p.cfg.MaxTokenSize) {
				p.scheduleChunk(turn, chunk)
				if ctx.Err() != nil {
					abortFauxStream(builder)
					return
				}
				builder.thinkingDelta(chunk, false)
			}
			builder.endThinking()
		case "toolCall":
			builder.toolCallStart(streamToolCallDelta{index: index, id: block.ID, name: block.Name})
			arguments := SafeJsonStringify(block.Arguments)
			for _, chunk := range splitByTokenSize(arguments, p.cfg.MinTokenSize, p.cfg.MaxTokenSize) {
				p.scheduleChunk(turn, chunk)
				if ctx.Err() != nil {
					abortFauxStream(builder)
					return
				}
				state := builder.toolCalls[index]
				state.arguments.WriteString(chunk)
				builder.push(ToolCallDeltaEvent{ContentIndex: state.contentIndex, Delta: chunk, Partial: builder.partial})
			}
			builder.endToolCall(index)
		}
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
func (p *fauxProvider) scheduleChunk(turn *continuationTurn, chunk string) {
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
func fauxMessageText(message Message) string {
	switch message := message.(type) {
	case SystemMessage:
		parts := []string{}
		if text := GetCurrentSystemPrompt([]Message{message}); text != "" {
			parts = append(parts, text)
		}
		for _, tool := range message.ToolsRemoved {
			parts = append(parts, "tool-:"+SafeJsonStringify(tool))
		}
		for _, tool := range message.ToolsAdded {
			parts = append(parts, "tool+:"+SafeJsonStringify(tool))
		}
		return strings.Join(parts, "\n")
	case UserMessage:
		if text, ok := message.Content.(UserText); ok {
			return string(text)
		}
		blocks, _ := message.Content.(UserContentBlocks)
		parts := []string{}
		for _, block := range blocks {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ImageContent:
				parts = append(parts, fmt.Sprintf("[image:%s:%d]", block.MimeType, utf16Length(block.Data)))
			}
		}
		return strings.Join(parts, "\n")
	case AssistantMessage:
		parts := []string{}
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ThinkingContent:
				parts = append(parts, block.Thinking)
			case ToolCall:
				parts = append(parts, block.Name+":"+SafeJsonStringify(block.Arguments))
			}
		}
		return strings.Join(parts, "\n")
	case ToolResultMessage:
		parts := []string{message.ToolName}
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ImageContent:
				parts = append(parts, fmt.Sprintf("[image:%s:%d]", block.MimeType, utf16Length(block.Data)))
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
func (p *fauxProvider) estimateUsage(request TranscriptContext, options StreamOptions, content []FauxContentBlock) *Usage {
	parts := []string{}
	for _, message := range request.Messages() {
		role := ""
		switch message.(type) {
		case SystemMessage:
			role = "system"
		case UserMessage:
			role = "user"
		case AssistantMessage:
			role = "assistant"
		case ToolResultMessage:
			role = "toolResult"
		}
		parts = append(parts, role+":"+fauxMessageText(message))
	}
	prompt := strings.Join(parts, "\n\n")
	tokens := (utf16Length(prompt) + 3) / 4
	output := (utf16Length(fauxAssistantText(content)) + 3) / 4
	usage := &Usage{Input: tokens, Output: output}
	if options.SessionID != "" && options.CacheRetention != CacheRetentionNone {
		p.mu.Lock()
		previous := p.promptCache[options.SessionID]
		p.promptCache[options.SessionID] = prompt
		p.mu.Unlock()
		if previous != "" {
			a, b := utf16.Encode([]rune(previous)), utf16.Encode([]rune(prompt))
			common := 0
			for common < min(len(a), len(b)) && a[common] == b[common] {
				common++
			}
			usage.CacheRead = (common + 3) / 4
			usage.CacheWrite = (len(b) - common + 3) / 4
			usage.Input = max(0, tokens-usage.CacheRead)
		} else {
			usage.CacheWrite = tokens
		}
	}
	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	return usage
}

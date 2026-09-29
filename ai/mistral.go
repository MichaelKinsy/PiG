package ai

// Ports packages/ai/src/api/mistral-conversations.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

const (
	mistralToolCallIDLength  = 9
	maxMistralErrorBodyChars = 4000
	mistralJSWhitespace      = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
)

// MistralConfig configures the Mistral API provider.
type MistralConfig struct {
	// ModelMetadata retains the selected model's input capabilities, identity and thinking map.
	ModelMetadata *Model
	APIKey        string
	Model         string
	ProviderID    string
	BaseURL       string
	ExtraHeaders  map[string]string
	// SessionID for x-affinity header (KV-cache reuse).
	SessionID string
	// Reasoning indicates whether the model supports extended reasoning.
	Reasoning bool
}

type mistralProvider struct {
	cfg    MistralConfig
	client *http.Client
}

// NewMistralProvider creates a Provider backed by the Mistral API. BaseURL defaults to https://api.mistral.ai; requests resolve v1/chat/completions beneath its normalized base pathname, preserving any existing v1 segment.
func NewMistralProvider(cfg MistralConfig) Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.mistral.ai"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "mistral"
	}
	return &mistralProvider{cfg: cfg, client: streamingHTTPClientNoRetry()}
}

func (p *mistralProvider) ID() string   { return p.cfg.ProviderID }
func (p *mistralProvider) Close() error { return nil }

func mistralChatCompletionsURL(baseURL string) (string, error) {
	return nodeurl.ResolveDirectoryPath(baseURL, "v1/chat/completions")
}

// ─── Request wire types ──────────────────────────────────────────────────────

type mistralRequest struct {
	Model           string           `json:"model"`
	Messages        []mistralMessage `json:"messages"`
	Stream          bool             `json:"stream"`
	Tools           []mistralTool    `json:"tools,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"maxTokens,omitempty"`
	ToolChoice      any              `json:"toolChoice,omitempty"`
	PromptMode      string           `json:"promptMode,omitempty"`
	ReasoningEffort string           `json:"reasoningEffort,omitempty"`
	PromptCacheKey  string           `json:"promptCacheKey,omitempty"`
}

type mistralMessage struct {
	Role       string `json:"role"`
	Prefix     *bool  `json:"prefix,omitempty"`
	Content    any    `json:"content,omitempty"`   // string | []mistralContentChunk
	ToolCalls  any    `json:"toolCalls,omitempty"` // []mistralToolCallMsg for assistant
	ToolCallID string `json:"toolCallId,omitempty"`
	Name       string `json:"name,omitempty"`
}

type mistralContentChunk struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	ImageURL string  `json:"imageUrl,omitempty"`
	Thinking any     `json:"thinking,omitempty"` // []map[string]string for thinking content
}

type mistralToolCallMsg struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function mistralToolCallFnMsg `json:"function"`
	Index    int                  `json:"index"`
}

type mistralToolCallFnMsg struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type mistralTool struct {
	Type     string        `json:"type"`
	Function mistralToolFn `json:"function"`
}

type mistralToolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

// ─── Response wire types ─────────────────────────────────────────────────────

type mistralSSEChunk struct {
	ID      string            `json:"id"`
	Choices []json.RawMessage `json:"choices"`
	Usage   *mistralSSEUsage  `json:"usage,omitempty"`
}

type mistralSSEChoice struct {
	Delta        json.RawMessage `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type mistralSSEDelta struct {
	Content   json.RawMessage      `json:"content"` // string | []mistralContentItem | null
	ToolCalls []mistralSSEToolCall `json:"tool_calls"`
}

type mistralContentItem struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	Thinking []struct {
		Text *string `json:"text"`
	} `json:"thinking,omitempty"`
}

type mistralSSEToolCall struct {
	ID       string          `json:"id"`
	Index    *int            `json:"index"`
	Function json.RawMessage `json:"function"`
}

type mistralSSEToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mistralSSEUsage struct {
	PromptTokens             int                  `json:"prompt_tokens"`
	CompletionTokens         int                  `json:"completion_tokens"`
	TotalTokens              int                  `json:"total_tokens"`
	PromptTokensDetails      *mistralCacheDetails `json:"promptTokensDetails"`
	PromptTokensDetailsSnake *mistralCacheDetails `json:"prompt_tokens_details"`
	PromptTokenDetails       *mistralCacheDetails `json:"promptTokenDetails"`
	PromptTokenDetailsSnake  *mistralCacheDetails `json:"prompt_token_details"`
	NumCachedTokens          *int                 `json:"numCachedTokens"`
	NumCachedTokensSnake     *int                 `json:"num_cached_tokens"`
}

type mistralCacheDetails struct {
	CachedTokens      *int `json:"cachedTokens"`
	CachedTokensSnake *int `json:"cached_tokens"`
}

func (usage mistralSSEUsage) cachedPromptTokens() int {
	var candidates []*int
	for _, details := range []*mistralCacheDetails{usage.PromptTokensDetails, usage.PromptTokensDetailsSnake, usage.PromptTokenDetails, usage.PromptTokenDetailsSnake} {
		if details != nil {
			candidates = append(candidates, details.CachedTokens, details.CachedTokensSnake)
		}
	}
	candidates = append(candidates, usage.NumCachedTokens, usage.NumCachedTokensSnake)
	for _, candidate := range candidates {
		if candidate != nil {
			return min(usage.PromptTokens, max(0, *candidate))
		}
	}
	return 0
}

// ─── ID normalization ────────────────────────────────────────────────────────

func deriveMistralToolCallID(id string, attempt int) string {
	var normalized strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		}
	}
	n := normalized.String()
	if attempt == 0 && len(n) == mistralToolCallIDLength {
		return n
	}
	seedBase := n
	if seedBase == "" {
		seedBase = id
	}
	seed := seedBase
	if attempt > 0 {
		seed = fmt.Sprintf("%s:%d", seedBase, attempt)
	}
	h := shortHash32(seed)
	// Filter to alnum
	var out strings.Builder
	for _, r := range h {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			if out.Len() >= mistralToolCallIDLength {
				break
			}
		}
	}
	return out.String()
}

type mistralIDNormalizer struct {
	forward map[string]string
	reverse map[string]string
}

func newMistralIDNormalizer() *mistralIDNormalizer {
	return &mistralIDNormalizer{
		forward: make(map[string]string),
		reverse: make(map[string]string),
	}
}

func (n *mistralIDNormalizer) normalize(id string) string {
	if existing, ok := n.forward[id]; ok {
		return existing
	}
	for attempt := range 1000 {
		candidate := deriveMistralToolCallID(id, attempt)
		owner, taken := n.reverse[candidate]
		if !taken || owner == id {
			n.forward[id] = candidate
			n.reverse[candidate] = id
			return candidate
		}
		_ = attempt
	}
	// Fallback (should never happen)
	return id
}

// ─── Stream ──────────────────────────────────────────────────────────────────

func (p *mistralProvider) resolveModel() *Model {
	if p.cfg.ModelMetadata != nil {
		return new(*p.cfg.ModelMetadata)
	}
	if generated, ok := LookupModelExact(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		return generated.ToModel()
	}
	return &Model{
		ID: p.cfg.Model, Input: []string{"text"},
		ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: p.cfg.ProviderID, BaseURL: p.cfg.BaseURL, Headers: p.cfg.ExtraHeaders, Reasoning: p.cfg.Reasoning},
		Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh},
	}
}

func (p *mistralProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("mistral: invalid transcript: %w", err)
	}
	apiKey := p.cfg.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for Mistral provider")
	}

	model := p.resolveModel()
	supportsMidConversation := model.ProviderMeta.Compat != nil && model.ProviderMeta.Compat.SupportsMidConvoSystemMessages != nil && *model.ProviderMeta.Compat.SupportsMidConvoSystemMessages
	resolved := ResolveTranscript(transcript, supportsMidConversation)
	normalizer := newMistralIDNormalizer()
	messages := TransformMessages(resolved.Messages(), model, func(id string, _ *Model, _ AssistantMessage) string { return normalizer.normalize(id) })
	msgs := p.convertMessages(WithoutInitialSystemMessage(messages), slices.Contains(model.Input, "image"))

	if systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))]); systemPrompt != "" {
		system := mistralMessage{Role: "system", Content: sanitizeSurrogates(systemPrompt)}
		msgs = append([]mistralMessage{system}, msgs...)
	}

	req := mistralRequest{
		Model:    p.cfg.Model,
		Stream:   true,
		Messages: msgs,
	}
	tools := GetCurrentTools(messages)
	if len(tools) > 0 {
		convertedTools, err := p.convertTools(tools)
		if err != nil {
			return nil, fmt.Errorf("mistral: convert tools: %w", err)
		}
		req.Tools = convertedTools
	}
	if opts.IsReasoning {
		reasoning := ClampThinkingLevel(model, opts.Thinking)
		if reasoning != "" && reasoning != ThinkingOff {
			if usesMistralPromptModeReasoning(p.cfg.Model, model.ProviderMeta.Reasoning) {
				req.PromptMode = "reasoning"
			}
			if usesMistralReasoningEffort(p.cfg.Model) {
				if mapped, ok := model.ThinkingLevelMap[reasoning]; ok && mapped != nil {
					req.ReasoningEffort = *mapped
				} else {
					req.ReasoningEffort = "high"
				}
			}
		}
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		req.Temperature = new(opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		m := opts.MaxTokens
		req.MaxTokens = &m
	}
	if opts.PromptMode != "" {
		req.PromptMode = opts.PromptMode
	}
	if opts.ReasoningEffort != "" {
		req.ReasoningEffort = opts.ReasoningEffort
	}
	req.ToolChoice = opts.ToolChoice
	// Upstream shouldUsePromptCaching requires an explicit request session ID and retention other than none.
	if shouldUseMistralPromptCaching(opts.SessionID, opts.CacheRetention) {
		req.PromptCacheKey = opts.SessionID
	}

	payload := any(req)
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(req, model)
		if err != nil {
			return nil, fmt.Errorf("mistral: onPayload: %w", err)
		}
		if next != nil {
			payload = next
		}
	}

	wire, err := toMistralWirePayload(payload)
	if err != nil {
		return nil, fmt.Errorf("mistral: convert wire payload: %w", err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("mistral: marshal request: %w", err)
	}

	// upstream: packages/ai/src/api/mistral-conversations.ts:requestMistralStream
	timeoutMs := 60_000
	if opts.TimeoutMs != nil {
		timeoutMs = *opts.TimeoutMs
	}
	requestCtx, cancel := context.WithTimeoutCause(ctx, time.Duration(timeoutMs)*time.Millisecond, errors.New("The operation was aborted due to timeout"))
	streamOwnsRequest := false
	finishRequest := cancel
	defer func() {
		if !streamOwnsRequest {
			finishRequest()
		}
	}()
	endpoint, err := mistralChatCompletionsURL(p.cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	requestURL, err := nodeurl.RequestURL(endpoint)
	if err != nil {
		return nil, err
	}
	if opts.Fetch == nil && requestURL.User != nil {
		return nil, fmt.Errorf("Request cannot be constructed from a URL that includes credentials: %s", endpoint)
	}
	httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mistral: new request: %w", err)
	}
	httpReq.URL, httpReq.Host = requestURL, requestURL.Host
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", PiUserAgent())
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	for k, v := range p.cfg.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}
	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = p.cfg.SessionID
	}
	_, hasExplicitAffinity := httpReq.Header["X-Affinity"]
	if shouldUseMistralPromptCaching(sessionID, opts.CacheRetention) && !hasExplicitAffinity {
		httpReq.Header.Set("x-affinity", sessionID)
	}
	applyProviderHeaders(httpReq, opts.Headers)

	resp, err := providerHTTPClient(p.client, opts.Fetch).Do(httpReq)
	if err != nil {
		if cause := context.Cause(requestCtx); cause != nil {
			err = cause
		}
		return nil, fmt.Errorf("mistral: request failed: %w", err)
	}

	if observed, ok := resp.Body.(*observedResponseBody); ok {
		// The request's cleanup and the body reader's Close both close the body; the transport sees one close.
		observed.ReadCloser = &mistralCloseOnce{ReadCloser: observed.ReadCloser}
	}
	// Custom fetch bodies need explicit cancellation even when they ignore the request context.
	closeBody := sync.OnceFunc(func() { _ = resp.Body.Close() })
	abortDone := make(chan struct{})
	stopAbort := context.AfterFunc(requestCtx, func() {
		defer close(abortDone)
		closeBody()
	})
	finishRequest = func() {
		if !stopAbort() {
			<-abortDone
		}
		closeBody()
		cancel()
	}
	responseBody := &mistralResponseBody{reader: resp.Body, closeBody: closeBody, request: requestCtx}
	if err := observeProviderResponse(requestCtx, opts, resp, model); err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errBody, err := io.ReadAll(responseBody)
		if err != nil {
			return nil, err
		}
		errText := truncateMistralError(strings.Trim(string(errBody), mistralJSWhitespace))
		if errText == "" {
			_, errText, _ = strings.Cut(resp.Status, " ")
			if errText == "" {
				errText = fmt.Sprintf("Request failed with status %d", resp.StatusCode)
			}
		}
		return nil, fmt.Errorf("Mistral API error (%d): %s", resp.StatusCode, errText)
	}

	builder := newObservedProviderBuilder(ctx, APIMistralConversations, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	_, builder.managed = resp.Body.(*observedResponseBody)
	streamOwnsRequest = true
	go func() {
		defer finishRequest()
		_ = builder.responseTurn(func() error {
			p.streamResponse(requestCtx, resp.Body, responseBody, builder)
			return nil
		})
	}()
	return builder.stream, nil
}

// streamResponse is the rest of upstream stream()'s async IIFE after `await fetch` resolved (mistral-conversations.ts:143-181). An observed body runs as one executor turn, so the consumer of the event stream interleaves with it as it does with Pi's provider; any other body is read on this goroutine.
func (p *mistralProvider) streamResponse(requestCtx context.Context, body io.ReadCloser, wrapped *mistralResponseBody, builder *assistantStreamBuilder) {
	tick := func() {
		if builder.turn != nil {
			suspendContinuation(builder.turn)
		}
	}
	tick()          // mistral-conversations.ts:311 `await options?.onResponse?.(...)`: an await of undefined or of a settled promise costs one microtask.
	tick()          // :321 `return readMistralEvents(...)` settles requestMistralStream's promise, and the IIFE's `await` at :151 resumes one microtask later.
	builder.start() // :152
	if observed, ok := body.(*observedResponseBody); ok {
		reader := newNativeBodyReader(requestCtx, observed, builder.turn, nil) // readMistralEvents reads with getReader().read(), not values().next().
		p.consumeStream(requestCtx, reader, builder)
		return
	}
	p.consumeStream(requestCtx, wrapped, builder)
}

func shouldUseMistralPromptCaching(sessionID string, retention CacheRetention) bool {
	return retention != CacheRetentionNone && sessionID != ""
}

// toMistralWirePayload converts only documented SDK property positions, leaving authored tool schemas and arguments unchanged.
func toMistralWirePayload(payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	remap := func(object map[string]any, source, target string) {
		if value, exists := object[source]; exists {
			object[target] = value
			delete(object, source)
		}
	}
	for _, keys := range [][2]string{{"topP", "top_p"}, {"maxTokens", "max_tokens"}, {"randomSeed", "random_seed"}, {"responseFormat", "response_format"}, {"toolChoice", "tool_choice"}, {"presencePenalty", "presence_penalty"}, {"frequencyPenalty", "frequency_penalty"}, {"parallelToolCalls", "parallel_tool_calls"}, {"reasoningEffort", "reasoning_effort"}, {"promptMode", "prompt_mode"}, {"promptCacheKey", "prompt_cache_key"}, {"safePrompt", "safe_prompt"}} {
		remap(wire, keys[0], keys[1])
	}
	messages, ok := wire["messages"].([]any)
	if !ok {
		return nil, fmt.Errorf("messages must be an array")
	}
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message must be an object")
		}
		remap(message, "toolCalls", "tool_calls")
		remap(message, "toolCallId", "tool_call_id")
		if content, ok := message["content"].([]any); ok {
			for _, value := range content {
				chunk, ok := value.(map[string]any)
				if !ok {
					continue
				}
				for _, keys := range [][2]string{{"imageUrl", "image_url"}, {"documentUrl", "document_url"}, {"documentName", "document_name"}, {"fileId", "file_id"}, {"referenceIds", "reference_ids"}, {"inputAudio", "input_audio"}} {
					remap(chunk, keys[0], keys[1])
				}
			}
		}
	}
	if format, ok := wire["response_format"].(map[string]any); ok {
		remap(format, "jsonSchema", "json_schema")
		if schema, ok := format["json_schema"].(map[string]any); ok {
			remap(schema, "schemaDefinition", "schema")
		}
	}
	return wire, nil
}

func truncateMistralError(text string) string {
	units := jsstring.ToUTF16(text)
	if len(units) <= maxMistralErrorBodyChars {
		return text
	}
	return fmt.Sprintf("%s... [truncated %d chars]", jsstring.FromUTF16(units[:maxMistralErrorBodyChars]), len(units)-maxMistralErrorBodyChars)
}

// mistralEventData applies Pi's trimStart per data line and final trim after shared SSE framing.
func mistralEventData(data string) string {
	if strings.ContainsRune(data, '\n') {
		lines := strings.Split(data, "\n")
		for i := range lines {
			lines[i] = strings.TrimLeft(lines[i], mistralJSWhitespace)
		}
		data = strings.Join(lines, "\n")
	}
	return strings.Trim(data, mistralJSWhitespace)
}

// consumeStream is consumeChatStream and the tail of upstream stream()'s async IIFE (mistral-conversations.ts:143-181, 557-745): it reads the body through readMistralEvents, builds the assistant message in place, and pushes the terminal event. ctx is the request's signal, options.signal combined with the timeout; builder.ctx is options.signal alone.
func (p *mistralProvider) consumeStream(ctx context.Context, body io.ReadCloser, builder *assistantStreamBuilder) {
	defer func() { _ = body.Close() }()
	var reader mistralChunkReader = &mistralIOReader{body: body}
	if native, ok := body.(*nativeBodyIterator); ok {
		reader = &mistralNativeReader{nativeBodyIterator: native}
	}
	p.consumeChunks(ctx, reader, builder)
}

// consumeChunks runs consumeStream over a chunk reader; a reader that models its reads' microtasks is used with the builder's turn.
func (p *mistralProvider) consumeChunks(ctx context.Context, reader mistralChunkReader, builder *assistantStreamBuilder) {
	builder.start()
	loop := &mistralEventLoop{reader: reader, turn: builder.turn, aborted: func() error {
		if ctx.Err() == nil {
			return nil
		}
		return mistralAbortReason(context.Cause(ctx))
	}, publish: func() { builder.publish() }}
	consumer := &mistralChunkConsumer{builder: builder, toolBlocks: map[mistralToolKey]int{}}
	err := loop.run(consumer.consume)
	if err == nil {
		consumer.finish()
	}
	// consumeChatStream is an async function: its settlement, by return or by throw, resumes the IIFE's `await` (:153) one microtask later.
	loop.tick()
	if err == nil {
		err = consumer.terminalError()
	}
	if err != nil {
		p.fail(builder, err)
		return
	}
	builder.push(DoneEvent{Reason: builder.partial.StopReason, Message: builder.partial}) // :165
}

// terminalError is mistral-conversations.ts:155-163: the checks between consumeChatStream and the done push.
func (consumer *mistralChunkConsumer) terminalError() error {
	partial := consumer.builder.partial
	if consumer.builder.ctx.Err() != nil {
		return errors.New("Request was aborted") // :155-157
	}
	if partial.StopReason == StopReasonPending {
		return errors.New("Mistral stream ended without a finish reason") // :159-161
	}
	if partial.StopReason == StopReasonAborted || partial.StopReason == StopReasonError {
		if partial.ErrorMessage != "" {
			return errors.New(partial.ErrorMessage)
		}
		return errors.New("An unknown error occurred") // :162-164
	}
	return nil
}

// fail is the catch block of upstream stream() (mistral-conversations.ts:169-179). It reports the error without ending open blocks: the message keeps whatever content had streamed.
func (p *mistralProvider) fail(builder *assistantStreamBuilder, err error) {
	partial := builder.partial
	for index, block := range partial.Content {
		if call, ok := block.(ToolCall); ok {
			call.scratch = toolCallScratch{} // partialArgs is only a streaming scratch buffer; never persist it.
			partial.Content[index] = call
		}
	}
	reason := StopReasonError
	if builder.ctx.Err() != nil {
		reason = StopReasonAborted // :173 options?.signal?.aborted
	}
	partial.StopReason = reason
	partial.ErrorMessage = err.Error() // :174 formatMistralError: an Error's message.
	builder.push(ErrorEvent{Reason: reason, Error: partial})
}

// mistralAbortReason is `signal.reason` of the request's signal: a plain abort() reports DOMException AbortError's message, and AbortSignal.timeout() reports TimeoutError's.
func mistralAbortReason(cause error) error {
	switch {
	case cause == nil || errors.Is(cause, context.Canceled):
		return errors.New("This operation was aborted")
	case errors.Is(cause, context.DeadlineExceeded):
		return errors.New("The operation was aborted due to timeout")
	}
	return cause
}

type mistralToolKey struct {
	index int
	id    string
}

// mistralChunkConsumer is the state of consumeChatStream's for-await body (mistral-conversations.ts:557-745).
type mistralChunkConsumer struct {
	builder    *assistantStreamBuilder
	toolBlocks map[mistralToolKey]int
}

// consume handles one parsed event. A returned error is an exception thrown in the loop body.
func (consumer *mistralChunkConsumer) consume(raw json.RawMessage) error {
	builder := consumer.builder
	var chunk mistralSSEChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return err
	}
	if chunk.ID != "" { // :593 output.responseId ||= chunk.id
		builder.setResponseMetadata(chunk.ID, "", "", "", nil)
	}

	if chunk.Usage != nil { // :595-607: the usage object is mutated in place.
		promptTokens := chunk.Usage.PromptTokens
		cached := chunk.Usage.cachedPromptTokens()
		usage := &builder.partial.Usage
		usage.Input = max(0, promptTokens-cached)
		usage.Output = chunk.Usage.CompletionTokens
		usage.CacheRead = cached
		usage.CacheWrite = 0
		usage.TotalTokens = chunk.Usage.TotalTokens
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
		}
		builder.calculateCost(usage)
	}

	if len(chunk.Choices) == 0 || !jsonValueTruthy(chunk.Choices[0]) { // :609-610 const choice = chunk.choices[0]; if (!choice) continue
		return nil
	}
	var choice mistralSSEChoice
	if err := json.Unmarshal(chunk.Choices[0], &choice); err != nil {
		return err
	}

	if choice.FinishReason != nil && *choice.FinishReason != "" { // :612-619
		builder.partial.RawStopReason = *choice.FinishReason
		reason, message := mapMistralStopReason(*choice.FinishReason)
		builder.partial.StopReason = reason
		if message != "" {
			builder.partial.ErrorMessage = message
		}
	}

	var delta mistralSSEDelta
	switch trimmed := bytes.TrimSpace(choice.Delta); {
	case len(trimmed) == 0:
		return errors.New("Cannot read properties of undefined (reading 'content')") // :621 delta.content
	case string(trimmed) == "null":
		return errors.New("Cannot read properties of null (reading 'content')")
	default:
		if err := json.Unmarshal(trimmed, &delta); err != nil {
			return err
		}
	}
	if err := consumer.content(delta.Content); err != nil { // :621-683
		return err
	}
	return consumer.toolCalls(delta.ToolCalls) // :685-731
}

// content is mistral-conversations.ts:621-683. finishCurrentBlock's text_end and thinking_end are the builder's endText and endThinking, which the text and thinking starts run first.
func (consumer *mistralChunkConsumer) content(raw json.RawMessage) error {
	builder := consumer.builder
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var items []json.RawMessage
	if trimmed[0] == '"' {
		items = []json.RawMessage{trimmed}
	} else if err := json.Unmarshal(trimmed, &items); err != nil {
		return errors.New("contentItems is not iterable")
	}
	for _, rawItem := range items {
		var text string
		if json.Unmarshal(rawItem, &text) == nil && bytes.HasPrefix(bytes.TrimSpace(rawItem), []byte(`"`)) {
			builder.textDelta(sanitizeSurrogates(text)) // :623-644 A string item: an empty one still opens a text block.
			continue
		}
		if string(bytes.TrimSpace(rawItem)) == "null" {
			return errors.New("Cannot read properties of null (reading 'type')")
		}
		var item mistralContentItem
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return err
		}
		switch item.Type {
		case "thinking": // :646-666
			var thinking strings.Builder
			for _, part := range item.Thinking {
				if part.Text != nil {
					thinking.WriteString(*part.Text)
				}
			}
			if delta := sanitizeSurrogates(thinking.String()); delta != "" {
				builder.thinkingDelta(delta, false)
			}
		case "text": // :668-683
			delta := ""
			if item.Text != nil {
				delta = *item.Text
			}
			builder.textDelta(sanitizeSurrogates(delta))
		}
	}
	return nil
}

// toolCalls is mistral-conversations.ts:685-731. The tool block carries the partialArgs scratch buffer until toolcall_end deletes it.
func (consumer *mistralChunkConsumer) toolCalls(calls []mistralSSEToolCall) error {
	builder := consumer.builder
	for _, call := range calls {
		builder.endText() // :687-690 finishCurrentBlock(currentBlock)
		builder.endThinking()
		callID := call.ID
		index := 0
		if call.Index != nil {
			index = *call.Index
		}
		if callID == "" || callID == "null" {
			callID = deriveMistralToolCallID(fmt.Sprintf("toolcall:%d", index), 0)
		}
		key := mistralToolKey{id: callID}
		if call.Index != nil {
			key = mistralToolKey{index: index}
		}
		blockIndex, exists := consumer.toolBlocks[key]
		if !exists {
			blockIndex = len(consumer.toolBlocks)
		}
		if function := bytes.TrimSpace(call.Function); len(function) == 0 || string(function) == "null" {
			property := "name" // :707 toolCall.function.name when the block is created, :720 toolCall.function.arguments otherwise
			if exists {
				property = "arguments"
			}
			holder := "undefined"
			if len(function) != 0 {
				holder = "null"
			}
			return fmt.Errorf("Cannot read properties of %s (reading '%s')", holder, property)
		}
		var function mistralSSEToolFunction
		if err := json.Unmarshal(call.Function, &function); err != nil {
			return err
		}
		start := streamToolCallDelta{index: blockIndex, scratch: toolCallScratch{hasPartialArgs: true}}
		if !exists {
			consumer.toolBlocks[key] = blockIndex
			start.id, start.name = callID, function.Name
		}
		start.argumentsDelta = mistralArgumentsDelta(function.Arguments) // :720-723
		builder.toolCallDelta(start)
	}
	return nil
}

// mistralArgumentsDelta is `typeof arguments === "string" ? arguments : JSON.stringify(arguments || {})`.
func mistralArgumentsDelta(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) == nil {
			return text
		}
	}
	if !jsonValueTruthy(trimmed) {
		return "{}"
	}
	return compactJSON(trimmed)
}

// finish is the end of consumeChatStream (mistral-conversations.ts:734-745): the open text or thinking block ends, then every tool call finalizes in creation order.
func (consumer *mistralChunkConsumer) finish() {
	builder := consumer.builder
	builder.endText()
	builder.endThinking()
	for index := range len(consumer.toolBlocks) {
		builder.endToolCall(index)
	}
}

func mapMistralStopReason(reason string) (StopReason, string) {
	switch reason {
	case "stop":
		return StopReasonStop, ""
	case "length", "model_length":
		return StopReasonLength, ""
	case "tool_calls":
		return StopReasonToolUse, ""
	case "error":
		return StopReasonError, "Provider stopped with: error"
	default:
		return StopReasonError, "Provider stopped with: " + reason
	}
}

func usesMistralReasoningEffort(modelID string) bool {
	// upstream: packages/ai/src/api/mistral-conversations.ts:usesReasoningEffort
	return modelID == "mistral-small-2603" || modelID == "mistral-small-latest" || strings.HasPrefix(modelID, "mistral-medium-") || modelID == "zai-glm-5-2"
}

func usesMistralPromptModeReasoning(modelID string, reasoning bool) bool {
	return reasoning && !usesMistralReasoningEffort(modelID)
}

// ─── Message conversion ─────────────────────────────────────────────────────

func (p *mistralProvider) convertMessages(messages []Message, supportsImages bool) []mistralMessage {
	result := make([]mistralMessage, 0, len(messages))
	for _, message := range messages {
		switch message := message.(type) {
		case SystemMessage:
			if text := RenderSystemMessageUpdate(message); text != "" {
				result = append(result, mistralMessage{Role: "system", Content: sanitizeSurrogates(text)})
			}
		case UserMessage:
			if converted, ok := p.convertUserMessage(message); ok {
				result = append(result, converted)
			}
		case AssistantMessage:
			converted := p.convertAssistantMessage(message)
			if converted.Content != nil || converted.ToolCalls != nil {
				result = append(result, converted)
			}
		case ToolResultMessage:
			result = append(result, p.convertToolResultMessage(message, supportsImages))
		}
	}
	return result
}

func (p *mistralProvider) convertUserMessage(message UserMessage) (mistralMessage, bool) {
	switch content := message.Content.(type) {
	case UserText:
		return mistralMessage{Role: "user", Content: sanitizeSurrogates(string(content))}, true
	case UserContentBlocks:
		var chunks []mistralContentChunk
		for _, block := range content {
			switch block := block.(type) {
			case TextContent:
				chunks = append(chunks, mistralContentChunk{Type: "text", Text: new(sanitizeSurrogates(block.Text))})
			case ImageContent:
				chunks = append(chunks, mistralContentChunk{Type: "image_url", ImageURL: fmt.Sprintf("data:%s;base64,%s", block.MimeType, block.Data)})
			}
		}
		if len(chunks) > 0 {
			return mistralMessage{Role: "user", Content: chunks}, true
		}
	}
	return mistralMessage{}, false
}

func (p *mistralProvider) convertAssistantMessage(message AssistantMessage) mistralMessage {
	var content []mistralContentChunk
	var toolCalls []mistralToolCallMsg
	for _, block := range message.Content {
		switch block := block.(type) {
		case TextContent:
			if trimJSWhitespace(block.Text) != "" {
				content = append(content, mistralContentChunk{Type: "text", Text: new(sanitizeSurrogates(block.Text))})
			}
		case ThinkingContent:
			if trimJSWhitespace(block.Thinking) != "" {
				content = append(content, mistralContentChunk{Type: "thinking", Thinking: []map[string]string{{"type": "text", "text": sanitizeSurrogates(block.Thinking)}}})
			}
		case ToolCall:
			arguments, _ := json.Marshal(block.Arguments)
			if block.Arguments == nil {
				arguments = []byte("{}")
			}
			toolCalls = append(toolCalls, mistralToolCallMsg{
				ID: block.ID, Type: "function",
				Function: mistralToolCallFnMsg{Name: block.Name, Arguments: string(arguments)},
			})
		}
	}
	converted := mistralMessage{Role: "assistant", Prefix: new(false)}
	if len(content) > 0 {
		converted.Content = content
	}
	if len(toolCalls) > 0 {
		converted.ToolCalls = toolCalls
	}
	return converted
}

func (p *mistralProvider) convertToolResultMessage(message ToolResultMessage, supportsImages bool) mistralMessage {
	var textParts []string
	hasImages := false
	for _, block := range message.Content {
		switch block := block.(type) {
		case TextContent:
			textParts = append(textParts, sanitizeSurrogates(block.Text))
		case ImageContent:
			hasImages = true
		}
	}
	text := buildMistralToolResultText(strings.Join(textParts, "\n"), hasImages, supportsImages, message.IsError)
	content := []mistralContentChunk{{Type: "text", Text: new(text)}}
	if supportsImages {
		for _, block := range message.Content {
			if image, ok := block.(ImageContent); ok {
				content = append(content, mistralContentChunk{Type: "image_url", ImageURL: "data:" + image.MimeType + ";base64," + image.Data})
			}
		}
	}
	return mistralMessage{
		Role: "tool", Content: content,
		ToolCallID: message.ToolCallID, Name: message.ToolName,
	}
}

func buildMistralToolResultText(text string, hasImages, supportsImages, isError bool) string {
	text = trimJSWhitespace(text)
	prefix := ""
	if isError {
		prefix = "[tool error] "
	}
	if text != "" {
		if hasImages && !supportsImages {
			text += "\n[tool image omitted: model does not support images]"
		}
		return prefix + text
	}
	if hasImages {
		if supportsImages {
			return prefix + "(see attached image)"
		}
		return prefix + "(image omitted: model does not support images)"
	}
	return prefix + "(no tool output)"
}

func (p *mistralProvider) convertTools(tools []ToolSchema) ([]mistralTool, error) {
	result := make([]mistralTool, len(tools))
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, true)
		if err != nil {
			return nil, err
		}
		parameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		result[i] = mistralTool{
			Type: "function",
			Function: mistralToolFn{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  parameters,
				Strict:      strict != nil && *strict,
			},
		}
	}
	return result, nil
}

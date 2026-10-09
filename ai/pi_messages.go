package ai

// Mirrors upstream .upstream/current/packages/ai/src/api/pi-messages.ts.
//
// pi-messages streams Pi's own message protocol to a backend: one POST of
// {model, context, options} to <baseUrl>/messages, answered by an SSE stream
// of serialized assistant-message events plus a terminal done/error event.
// The Radius gateway speaks it; any backend implementing it can be used, e.g.
// through a models.json provider with "api": "pi-messages".

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PiMessagesConfig configures a pi-messages provider. The per-request options (debug, toolChoice, onResponse) are StreamOptions fields.
type PiMessagesConfig struct {
	BaseURL      string
	APIKey       string
	GetAPIKey    func(context.Context) (string, error)
	Model        string
	ProviderID   string
	ExtraHeaders map[string]string
	// ModelMetadata is the selected model, which an OnProviderStreamEvent observer receives. Nil hands the observer the configured identity only.
	ModelMetadata *Model
}

type piMessagesProvider struct {
	cfg    PiMessagesConfig
	client *http.Client
}

// NewPiMessagesProvider creates a pi-messages provider. Like upstream fetch,
// requests are not retried by the provider retry transport.
func NewPiMessagesProvider(cfg PiMessagesConfig) Provider {
	return &piMessagesProvider{cfg: cfg, client: streamingHTTPClientNoRetry()}
}

func (p *piMessagesProvider) ID() string   { return p.cfg.ProviderID }
func (p *piMessagesProvider) Close() error { return nil }

// PiMessagesResponseError is a non-2xx backend response with diagnostic details.
type PiMessagesResponseError struct {
	Message           string
	Code              string
	DiagnosticDetails map[string]any
}

func (e *PiMessagesResponseError) Error() string { return e.Message }

// NewPiMessagesResponseError ports pi-messages.ts PiMessagesResponseError constructor(message, code, diagnosticDetails); an empty code is upstream's undefined.
func NewPiMessagesResponseError(message, code string, diagnosticDetails map[string]any) *PiMessagesResponseError {
	e := &PiMessagesResponseError{Message: message, Code: code, DiagnosticDetails: diagnosticDetails}
	return e
}

// Name is the upstream `name` property.
func (*PiMessagesResponseError) Name() string { return "PiMessagesResponseError" }

// DiagnosticCode is the upstream `code` property; it is absent when the backend sent none.
func (e *PiMessagesResponseError) DiagnosticCode() any {
	if e.Code == "" {
		return nil
	}
	return e.Code
}

// Stream returns immediately; request and transport failures terminate the
// stream with an error event, as upstream's stream() does.
func (p *piMessagesProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("pi-messages: invalid transcript: %w", err)
	}
	stream := NewAssistantMessageEventStream()
	ctx = stream.ObservationContext(ctx)
	convert := newPiMessagesEventConverter(p.cfg.ProviderID, p.cfg.Model)
	if opts.OnProviderStreamEvent != nil {
		model := providerEventModel(p.cfg.ModelMetadata, APIPiMessages, p.cfg.ProviderID, p.cfg.Model)
		convert.observe = func(data any) error { return opts.OnProviderStreamEvent(ctx, data, model) }
	}
	go p.run(ctx, transcript, opts, stream, convert)
	return stream, nil
}

// run is the async IIFE of upstream stream() (pi-messages.ts:377-448). Its catch pushes the error event.
func (p *piMessagesProvider) run(ctx context.Context, transcript TranscriptContext, opts StreamOptions, stream *AssistantMessageEventStream, convert *piMessagesEventConverter) {
	response, err := p.send(ctx, transcript, opts) // pi-messages.ts:383-413 payload, `await fetch`, `await onResponse`, status check.
	if err != nil {
		_ = stream.Push(p.errorEvent(ctx, err))
		return
	}
	defer func() { _ = response.Body.Close() }()
	body, ok := response.Body.(*observedResponseBody)
	if !ok {
		// A body from a client that is not PiG's transport cannot report whether bytes are buffered: every read awaits input (D82).
		body = &observedResponseBody{ReadCloser: response.Body, owner: &bodyReadOwner{}, alwaysPending: true}
	}
	// The IIFE resumes from `await fetch` as an executor turn. Everything up to the next external completion runs in it.
	_ = stream.responseContinuation(func(turn *continuationTurn) error {
		// readPiMessagesEvents reads with getReader().read(), not values().next().
		reader := newNativeBodyReader(ctx, body, turn, nil)
		defer func() { _ = reader.Close() }()
		p.consumeBody(ctx, stream, convert, reader, turn)
		return nil
	})
}

// consumeBody continues the IIFE after the response headers: `await onResponse`, then the for-await over the body.
func (p *piMessagesProvider) consumeBody(ctx context.Context, stream *AssistantMessageEventStream, convert *piMessagesEventConverter, reader interface{ ReadChunk() ([]byte, error) }, turn *continuationTurn) {
	_, _ = jsAwait(turn, jsValue(struct{}{})) // pi-messages.ts:415 `await options?.onResponse?.(...)`: an await of undefined still costs one microtask.
	p.consume(ctx, stream, convert, &piMessagesEventLoop{reader: reader, turn: turn})
}

// consume is the `for await` over readPiMessagesEvents with the try/catch around it (pi-messages.ts:432-448).
func (p *piMessagesProvider) consume(ctx context.Context, stream *AssistantMessageEventStream, convert *piMessagesEventConverter, loop *piMessagesEventLoop) {
	err := loop.run(func(raw json.RawMessage) (bool, error) {
		if err := convert.observeEvent(loop.turn, raw); err != nil { // pi-messages.ts:415
			return false, err
		}
		events, err := convert.convert(raw)
		replaced := convert.replaced
		convert.replaced = assistantMessageReplacements{}
		for _, event := range events {
			if pushErr := stream.push(event, replaced); pushErr != nil {
				return false, pushErr
			}
			if isTerminalEvent(event) {
				return true, nil
			}
		}
		return false, err
	})
	switch {
	case err == nil:
	case errors.Is(err, errPiMessagesEventsEnded):
		_ = stream.Push(p.errorEvent(ctx, fmt.Errorf("%s %w", p.cfg.ProviderID, err))) // pi-messages.ts:443
	default:
		_ = stream.Push(p.errorEvent(ctx, err))
	}
}

func isTerminalEvent(event AssistantMessageEvent) bool {
	switch event.(type) {
	case DoneEvent, ErrorEvent:
		return true
	}
	return false
}

func (p *piMessagesProvider) apiKey(ctx context.Context) (string, error) {
	if p.cfg.GetAPIKey == nil {
		return p.cfg.APIKey, nil
	}
	return p.cfg.GetAPIKey(ctx)
}

func (p *piMessagesProvider) send(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*http.Response, error) {
	apiKey, err := p.apiKey(ctx)
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("No API key provided for provider %q", p.cfg.ProviderID)
	}
	endpoint, err := url.Parse(strings.TrimRight(p.cfg.BaseURL, "/") + "/messages")
	if err != nil {
		return nil, err
	}
	if opts.Debug {
		query := endpoint.Query()
		query.Set("debug", "1")
		endpoint.RawQuery = query.Encode()
	}
	body, err := p.payload(transcript, opts)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("authorization", "Bearer "+apiKey)
	request.Header.Set("accept", "text/event-stream")
	request.Header.Set("content-type", "application/json")
	applyProviderHeaders(request, mergeProviderHeaders(ProviderHeadersFromStrings(p.cfg.ExtraHeaders), opts.Headers))
	response, err := providerHTTPClient(p.client, opts.Fetch).Do(request)
	if err != nil {
		return nil, err
	}
	if err := observeProviderResponse(ctx, opts, response, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: APIPiMessages}}); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	if err := p.checkResponse(endpoint, response); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	return response, nil
}

func mergeProviderHeaders(base, override ProviderHeaders) ProviderHeaders {
	merged := maps.Clone(base)
	if merged == nil {
		merged = ProviderHeaders{}
	}
	maps.Copy(merged, override)
	return merged
}

func (p *piMessagesProvider) checkResponse(endpoint *url.URL, response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return nil
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	return newPiMessagesResponseError(p.cfg.ProviderID, p.cfg.Model, endpoint.String(), response, string(body))
}

type piMessagesPayloadOptions struct {
	Temperature    *float64       `json:"temperature,omitempty"`
	MaxTokens      *int           `json:"maxTokens,omitempty"`
	Reasoning      string         `json:"reasoning,omitempty"`
	CacheRetention CacheRetention `json:"cacheRetention,omitempty"`
	SessionID      string         `json:"sessionId,omitempty"`
	ToolChoice     any            `json:"toolChoice,omitempty"`
}

func resolvePiMessagesCacheRetention(cacheRetention CacheRetention, env ProviderEnv) CacheRetention {
	if cacheRetention != "" {
		return cacheRetention
	}
	// Backend defaults apply when unset; only the legacy env opt-in is mapped.
	if getProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return CacheRetentionLong
	}
	return ""
}

func (p *piMessagesProvider) payload(transcript TranscriptContext, opts StreamOptions) ([]byte, error) {
	options := piMessagesPayloadOptions{
		CacheRetention: resolvePiMessagesCacheRetention(opts.CacheRetention, opts.Env),
		SessionID:      opts.SessionID,
		ToolChoice:     opts.ToolChoice,
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		options.Temperature = new(opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		options.MaxTokens = &opts.MaxTokens
	}
	if opts.Thinking != "" {
		options.Reasoning = string(ModelThinkingLevel(opts.Thinking))
	}
	var payload any = map[string]any{
		"model":   p.cfg.Model,
		"context": map[string]any{"messages": transcript.Messages()},
		"options": options,
	}
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(payload, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: APIPiMessages, BaseURL: p.cfg.BaseURL}})
		if err != nil {
			return nil, err
		}
		if next != nil {
			payload = next
		}
	}
	return json.Marshal(payload)
}

type piMessagesErrorBody struct {
	Error map[string]any `json:"error"`
}

func parsePiMessagesErrorBody(body string) (map[string]any, bool) {
	var parsed map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &parsed) != nil || jsonKind(parsed["error"]) != '{' {
		return nil, false
	}
	var decoded piMessagesErrorBody
	if json.Unmarshal([]byte(body), &decoded) != nil {
		return nil, false
	}
	return decoded.Error, true
}

func newPiMessagesResponseError(providerID, modelID, endpoint string, response *http.Response, body string) *PiMessagesResponseError {
	errorBody, structured := parsePiMessagesErrorBody(body)
	message, hasMessage := errorBody["message"].(string)
	code, _ := errorBody["code"].(string)
	statusText := strings.TrimPrefix(response.Status, strconv.Itoa(response.StatusCode)+" ")
	suffix := body
	if hasMessage {
		suffix = message
	}
	text := fmt.Sprintf("%d %s: %s", response.StatusCode, statusText, suffix)
	if code != "" {
		text += " (" + code + ")"
	}
	details := map[string]any{
		"version": 1, "provider": providerID, "model": modelID, "url": endpoint,
		"status": response.StatusCode, "statusText": statusText, "timestampMs": time.Now().UnixMilli(),
	}
	if structured {
		details["error"] = errorBody
	} else {
		details["body"] = truncateDiagnosticString(body)
	}
	return NewPiMessagesResponseError(text, code, details)
}

func truncateDiagnosticString(value string) string {
	if utf16Length(value) <= 8192 {
		return value
	}
	return truncateUTF16(value, 8192) + "…"
}

// errorEvent is createErrorEvent (pi-messages.ts:325-346). aborted is options.signal.aborted at the time of the catch, and an abort
// that surfaced as a cancellation reports the signal's reason the way fetch rejects with it.
func (p *piMessagesProvider) errorEvent(ctx context.Context, err error) ErrorEvent {
	aborted := ctx.Err() != nil
	reason := StopReasonError
	if aborted {
		reason = StopReasonAborted
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = abortSignalReason(context.Cause(ctx))
		}
	}
	message := &AssistantMessage{
		Content: []AssistantContentBlock{}, API: APIPiMessages, Provider: p.cfg.ProviderID, Model: p.cfg.Model,
		StopReason: reason, ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli(),
	}
	var responseError *PiMessagesResponseError
	if !aborted && errors.As(err, &responseError) {
		AppendAssistantMessageDiagnostic(message, CreateAssistantMessageDiagnostic("pi_messages_response_failure", responseError, responseError.DiagnosticDetails))
	}
	return ErrorEvent{Reason: reason, Error: message}
}

// abortSignalReason is the error fetch rejects with when its signal aborts: the signal's reason. A plain abort() has DOMException
// AbortError's message, and AbortSignal.timeout() has TimeoutError's.
func abortSignalReason(cause error) error {
	switch {
	case cause == nil || errors.Is(cause, context.Canceled):
		return errors.New("This operation was aborted")
	case errors.Is(cause, context.DeadlineExceeded):
		return errors.New("The operation was aborted due to timeout")
	}
	return cause
}

// observeEvent is `await options?.onProviderStreamEvent?.(piEvent, model)`: the callback sees the parsed wire event, and the await of an absent callback still costs one microtask.
func (convert *piMessagesEventConverter) observeEvent(turn *continuationTurn, raw json.RawMessage) error {
	if convert.observe != nil {
		var data any
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		if err := convert.observe(data); err != nil {
			return err
		}
	}
	_, _ = jsAwait(turn, jsValue(struct{}{}))
	return nil
}

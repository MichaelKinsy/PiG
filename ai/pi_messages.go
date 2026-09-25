package ai

// Mirrors upstream .upstream/current/packages/ai/src/api/pi-messages.ts.
//
// pi-messages streams Pi's own message protocol to a backend: one POST of
// {model, context, options} to <baseUrl>/messages, answered by an SSE stream
// of serialized assistant-message events plus a terminal done/error event.
// The Radius gateway speaks it; any backend implementing it can be used, e.g.
// through a models.json provider with "api": "pi-messages".

import (
	"bufio"
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

// PiMessagesResponse is the HTTP status and headers reported to OnResponse.
type PiMessagesResponse struct {
	Status  int
	Headers map[string]string
}

// PiMessagesConfig configures a pi-messages provider. Debug, ToolChoice, and
// OnResponse carry upstream PiMessagesOptions fields that StreamOptions lacks.
type PiMessagesConfig struct {
	BaseURL      string
	APIKey       string
	GetAPIKey    func(context.Context) (string, error)
	Model        string
	ProviderID   string
	ExtraHeaders map[string]string
	// Debug asks the backend for debug metadata (?debug=1).
	Debug bool
	// ToolChoice is "auto", "none", "required", or {type:"function",...}.
	ToolChoice any
	OnResponse func(PiMessagesResponse) error
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

// Stream returns immediately; request and transport failures terminate the
// stream with an error event, as upstream's stream() does.
func (p *piMessagesProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("pi-messages: invalid transcript: %w", err)
	}
	stream := NewAssistantMessageEventStream()
	go func() {
		convert := newPiMessagesEventConverter(p.cfg.ProviderID, p.cfg.Model)
		if err := p.run(ctx, transcript, opts, stream, convert); err != nil {
			_ = stream.Push(p.errorEvent(err, ctx.Err() != nil))
		}
	}()
	return stream, nil
}

func (p *piMessagesProvider) run(ctx context.Context, transcript TranscriptContext, opts StreamOptions, stream *AssistantMessageEventStream, convert *piMessagesEventConverter) error {
	response, err := p.send(ctx, transcript, opts)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	var terminal bool
	err = readPiMessagesEvents(response.Body, func(raw json.RawMessage) (bool, error) {
		events, err := convert.convert(raw)
		for _, event := range events {
			if pushErr := stream.Push(event); pushErr != nil {
				return false, pushErr
			}
			if isTerminalEvent(event) {
				terminal = true
				return false, nil
			}
		}
		return true, err
	})
	if err != nil || terminal {
		return err
	}
	return fmt.Errorf("%s stream ended without a terminal event", p.cfg.ProviderID)
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
	if p.cfg.Debug {
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
	response, err := p.client.Do(request)
	if err != nil {
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
	if p.cfg.OnResponse != nil {
		headers := make(map[string]string, len(response.Header))
		for name := range response.Header {
			headers[strings.ToLower(name)] = response.Header.Get(name)
		}
		if err := p.cfg.OnResponse(PiMessagesResponse{Status: response.StatusCode, Headers: headers}); err != nil {
			return err
		}
	}
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
		ToolChoice:     p.cfg.ToolChoice,
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		options.Temperature = new(opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		options.MaxTokens = &opts.MaxTokens
	}
	if opts.Thinking != "" && opts.Thinking != ThinkingOff {
		options.Reasoning = string(opts.Thinking)
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
	return &PiMessagesResponseError{Message: text, Code: code, DiagnosticDetails: details}
}

func truncateDiagnosticString(value string) string {
	if utf16Length(value) <= 8192 {
		return value
	}
	return truncateUTF16(value, 8192) + "…"
}

func (p *piMessagesProvider) errorEvent(err error, aborted bool) ErrorEvent {
	reason := StopReasonError
	if aborted {
		reason = StopReasonAborted
	}
	message := &AssistantMessage{
		Content: []AssistantContentBlock{}, API: APIPiMessages, Provider: p.cfg.ProviderID, Model: p.cfg.Model,
		StopReason: reason, ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli(),
	}
	var responseError *PiMessagesResponseError
	if !aborted && errors.As(err, &responseError) {
		info := &DiagnosticErrorInfo{Name: "PiMessagesResponseError", Message: responseError.Message}
		if responseError.Code != "" {
			info.Code = responseError.Code
		}
		message.Diagnostics = append(message.Diagnostics, AssistantMessageDiagnostic{
			Type: "pi_messages_response_failure", Timestamp: time.Now().UnixMilli(), Error: info, Details: responseError.DiagnosticDetails,
		})
	}
	return ErrorEvent{Reason: reason, Error: message}
}

// readPiMessagesEvents splits the SSE body on blank lines and hands each
// event's first data payload to handle until handle reports false.
func readPiMessagesEvents(body io.Reader, handle func(json.RawMessage) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	scanner.Split(splitSSEEvents)
	for scanner.Scan() {
		data, ok := piMessagesEventData(scanner.Text())
		if !ok {
			continue
		}
		if !json.Valid([]byte(data)) {
			return fmt.Errorf("invalid pi-messages event JSON: %s", data)
		}
		more, err := handle(json.RawMessage(data))
		if err != nil || !more {
			return err
		}
	}
	return scanner.Err()
}

// splitSSEEvents yields blank-line-separated events after CRLF normalization.
func splitSSEEvents(data []byte, atEOF bool) (int, []byte, error) {
	for index := range data {
		if data[index] != '\n' {
			continue
		}
		next := index + 1
		if next < len(data) && data[next] == '\r' {
			next++
		}
		if next < len(data) && data[next] == '\n' {
			return next + 1, bytes.ReplaceAll(data[:index], []byte("\r\n"), []byte("\n")), nil
		}
	}
	if atEOF && len(bytes.TrimSpace(data)) > 0 {
		return len(data), bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), nil
	}
	if atEOF {
		return len(data), nil, nil
	}
	return 0, nil, nil
}

// upstream: ai/src/api/pi-messages.ts:parsePiMessagesEvent
func piMessagesEventData(raw string) (string, bool) {
	for line := range strings.SplitSeq(raw, "\n") {
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			data = strings.TrimSpace(data)
			return data, data != "" && data != "[DONE]"
		}
	}
	return "", false
}

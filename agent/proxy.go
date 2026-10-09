package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// ProxyAssistantMessageEventType is the closed `type` union of a proxy event (proxy.ts ProxyAssistantMessageEvent).
type ProxyAssistantMessageEventType string

// The proxy event types.
const (
	ProxyEventStart         ProxyAssistantMessageEventType = "start"
	ProxyEventTextStart     ProxyAssistantMessageEventType = "text_start"
	ProxyEventTextDelta     ProxyAssistantMessageEventType = "text_delta"
	ProxyEventTextEnd       ProxyAssistantMessageEventType = "text_end"
	ProxyEventThinkingStart ProxyAssistantMessageEventType = "thinking_start"
	ProxyEventThinkingDelta ProxyAssistantMessageEventType = "thinking_delta"
	ProxyEventThinkingEnd   ProxyAssistantMessageEventType = "thinking_end"
	ProxyEventToolcallStart ProxyAssistantMessageEventType = "toolcall_start"
	ProxyEventToolcallDelta ProxyAssistantMessageEventType = "toolcall_delta"
	ProxyEventToolcallEnd   ProxyAssistantMessageEventType = "toolcall_end"
	ProxyEventDone          ProxyAssistantMessageEventType = "done"
	ProxyEventError         ProxyAssistantMessageEventType = "error"
)

// ProxyAssistantMessageEvent is one compact event returned by a proxy server.
// Partial assistant messages are omitted and reconstructed by StreamProxy.
type ProxyAssistantMessageEvent struct {
	Type                  ProxyAssistantMessageEventType `json:"type"`
	ContentIndex          int                            `json:"contentIndex"`
	Delta                 string                         `json:"delta"`
	ContentSignature      *string                        `json:"contentSignature,omitempty"`
	ID                    string                         `json:"id"`
	ToolName              string                         `json:"toolName"`
	ToolCall              *ai.ToolCall                   `json:"toolCall,omitempty"`
	Reason                ai.StopReason                  `json:"reason"`
	Usage                 ai.Usage                       `json:"usage"`
	ErrorMessage          *string                        `json:"errorMessage,omitempty"`
	ProviderThinkingLevel *string                        `json:"providerThinkingLevel,omitempty"`

	// toolCallFields records which members the wire toolCall carried, so toolcall_end
	// merges them over the streamed call as Object.assign does.
	toolCallFields map[string]json.RawMessage
	// notArrayIndex marks a contentIndex that is not a JavaScript array index (see parseProxyContentIndex).
	notArrayIndex bool
}

// UnmarshalJSON decodes the event and records the members of a toolcall_end toolCall.
func (event *ProxyAssistantMessageEvent) UnmarshalJSON(data []byte) error {
	type plain ProxyAssistantMessageEvent
	var decoded struct {
		plain
		ContentIndex json.RawMessage `json:"contentIndex"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var members struct {
		ToolCall map[string]json.RawMessage `json:"toolCall"`
	}
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	*event = ProxyAssistantMessageEvent(decoded.plain)
	event.toolCallFields = members.ToolCall
	event.ContentIndex, event.notArrayIndex = parseProxyContentIndex(decoded.ContentIndex)
	return nil
}

// maxProxyArrayIndex is the largest JavaScript array index, 2^32-2.
const maxProxyArrayIndex = math.MaxUint32 - 1

// parseProxyContentIndex reads a wire contentIndex. Pi uses the member as a property key of the content array
// (`partial.content[proxyEvent.contentIndex]`), so an index is any value whose property key is an array index: an
// integral number from 0 (-0 included) to 2^32-2, or a string spelling such a number canonically ("0", "17", not "01").
// Any other value (absent, null, negative, fractional, another string) names a non-index property; it reports true.
func parseProxyContentIndex(raw json.RawMessage) (int, bool) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return -1, true
	}
	switch key := value.(type) {
	case float64:
		if key >= 0 && key <= maxProxyArrayIndex && key == math.Trunc(key) {
			return int(key), false
		}
	case string:
		if index, err := strconv.ParseUint(key, 10, 32); err == nil && index <= maxProxyArrayIndex && strconv.FormatUint(index, 10) == key {
			return int(index), false
		}
	}
	return -1, true
}

// MarshalJSON emits only the fields carried by the selected proxy event variant.
func (event ProxyAssistantMessageEvent) MarshalJSON() ([]byte, error) {
	type eventType struct {
		Type ProxyAssistantMessageEventType `json:"type"`
	}
	type indexed struct {
		Type         ProxyAssistantMessageEventType `json:"type"`
		ContentIndex int                            `json:"contentIndex"`
	}
	switch event.Type {
	case "start":
		return json.Marshal(eventType{Type: event.Type})
	case "text_start", "thinking_start":
		return json.Marshal(indexed{Type: event.Type, ContentIndex: event.ContentIndex})
	case "text_delta", "thinking_delta", "toolcall_delta":
		return json.Marshal(struct {
			Type         ProxyAssistantMessageEventType `json:"type"`
			ContentIndex int                            `json:"contentIndex"`
			Delta        string                         `json:"delta"`
		}{Type: event.Type, ContentIndex: event.ContentIndex, Delta: event.Delta})
	case "text_end", "thinking_end":
		return json.Marshal(struct {
			Type             ProxyAssistantMessageEventType `json:"type"`
			ContentIndex     int                            `json:"contentIndex"`
			ContentSignature *string                        `json:"contentSignature,omitempty"`
		}{Type: event.Type, ContentIndex: event.ContentIndex, ContentSignature: event.ContentSignature})
	case "toolcall_start":
		return json.Marshal(struct {
			Type         ProxyAssistantMessageEventType `json:"type"`
			ContentIndex int                            `json:"contentIndex"`
			ID           string                         `json:"id"`
			ToolName     string                         `json:"toolName"`
		}{Type: event.Type, ContentIndex: event.ContentIndex, ID: event.ID, ToolName: event.ToolName})
	case "toolcall_end":
		return json.Marshal(struct {
			Type         ProxyAssistantMessageEventType `json:"type"`
			ContentIndex int                            `json:"contentIndex"`
			ToolCall     *ai.ToolCall                   `json:"toolCall"`
		}{Type: event.Type, ContentIndex: event.ContentIndex, ToolCall: event.ToolCall})
	case "done":
		return json.Marshal(struct {
			Type                  ProxyAssistantMessageEventType `json:"type"`
			Reason                ai.StopReason                  `json:"reason"`
			Usage                 ai.Usage                       `json:"usage"`
			ProviderThinkingLevel *string                        `json:"providerThinkingLevel,omitempty"`
		}{Type: event.Type, Reason: event.Reason, Usage: event.Usage, ProviderThinkingLevel: event.ProviderThinkingLevel})
	case "error":
		return json.Marshal(struct {
			Type                  ProxyAssistantMessageEventType `json:"type"`
			Reason                ai.StopReason                  `json:"reason"`
			ErrorMessage          *string                        `json:"errorMessage,omitempty"`
			Usage                 ai.Usage                       `json:"usage"`
			ProviderThinkingLevel *string                        `json:"providerThinkingLevel,omitempty"`
		}{
			Type: event.Type, Reason: event.Reason, ErrorMessage: event.ErrorMessage,
			Usage: event.Usage, ProviderThinkingLevel: event.ProviderThinkingLevel,
		})
	default:
		return json.Marshal(eventType{Type: event.Type})
	}
}

// ProxyStreamOptions configures StreamProxy. Context cancellation is the Go
// equivalent of upstream's optional AbortSignal.
// Non-nil maps retain empty objects. ThinkingBudgets preserves each budget's presence, including explicit zeros.
type ProxyStreamOptions struct {
	Temperature     *float64
	SamplingParams  map[string]any
	MaxTokens       *int
	Reasoning       ai.ModelThinkingLevel
	CacheRetention  ai.CacheRetention
	SessionID       string
	Headers         ai.ProviderHeaders
	Metadata        map[string]any
	Transport       ai.Transport
	ThinkingBudgets *ProxyThinkingBudgets
	MaxRetryDelayMs *int
	AuthToken       string
	ProxyURL        string
}

type proxySerializableStreamOptions struct {
	Temperature     *float64              `json:"temperature,omitempty"`
	SamplingParams  map[string]any        `json:"samplingParams,omitzero"`
	MaxTokens       *int                  `json:"maxTokens,omitempty"`
	Reasoning       ai.ModelThinkingLevel `json:"reasoning,omitempty"`
	CacheRetention  ai.CacheRetention     `json:"cacheRetention,omitempty"`
	SessionID       string                `json:"sessionId,omitempty"`
	Headers         ai.ProviderHeaders    `json:"headers,omitzero"`
	Metadata        map[string]any        `json:"metadata,omitzero"`
	Transport       ai.Transport          `json:"transport,omitempty"`
	ThinkingBudgets *ProxyThinkingBudgets `json:"thinkingBudgets,omitempty"`
	MaxRetryDelayMs *int                  `json:"maxRetryDelayMs,omitempty"`
}

// ProxyThinkingBudgets carries optional per-level token budgets to the proxy server.
// Nil fields stay omitted; non-nil fields send their value, including zero.
// A non-nil empty struct sends an empty object without overriding server defaults.
type ProxyThinkingBudgets struct {
	Minimal *int `json:"minimal,omitempty"`
	Low     *int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High    *int `json:"high,omitempty"`
}

type proxyModel struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	API              ai.API               `json:"api"`
	Provider         string               `json:"provider"`
	BaseURL          string               `json:"baseUrl"`
	Reasoning        bool                 `json:"reasoning"`
	ThinkingLevelMap ai.ThinkingLevelMap  `json:"thinkingLevelMap,omitempty"`
	Input            []string             `json:"input"`
	InputLimits      *ai.ModelInputLimits `json:"inputLimits,omitempty"`
	Cost             ai.ModelCost         `json:"cost"`
	PromptCache      ai.ModelPromptCache  `json:"promptCache,omitempty"`
	ContextWindow    int                  `json:"contextWindow"`
	MaxTokens        int                  `json:"maxTokens"`
	SamplingParams   map[string]any       `json:"samplingParams,omitempty"`
	// SamplingParamsByThinkingLevel is Model.samplingParamsByThinkingLevel; the proxy request carries the whole model.
	SamplingParamsByThinkingLevel ai.SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	Headers                       map[string]string                `json:"headers,omitempty"`
	Compat                        *ai.ModelCompat                  `json:"compat,omitempty"`
}

type proxyRequestPayload struct {
	Model   proxyModel                     `json:"model"`
	Context proxyRequestContext            `json:"context"`
	Options proxySerializableStreamOptions `json:"options"`
}

type proxyRequestContext struct {
	Messages []ai.Message `json:"messages"`
}

// StreamProxy streams through a server that manages provider authentication.
// Each toolcall_start replaces the indexed tool and its accumulated JSON.
// It returns immediately; request, protocol, and cancellation failures are
// delivered as terminal error events.
func StreamProxy(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ProxyStreamOptions) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	partial := newProxyPartial(model)
	converter := &proxyEventConverter{partial: partial, toolJSON: map[int]string{}}
	body, err := buildProxyRequest(model, transcript, options)
	go func(runErr error) {
		if runErr == nil {
			runErr = runProxyRequest(ctx, options, body, converter, stream)
		}
		if runErr != nil {
			pushProxyFailure(stream, partial, runErr, ctx != nil && ctx.Err() != nil)
		}
	}(err)
	return stream
}

func newProxyPartial(model *ai.Model) *ai.AssistantMessage {
	partial := &ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonPending, Timestamp: time.Now().UnixMilli(),
	}
	if model == nil {
		return partial
	}
	partial.API = model.ProviderMeta.API
	partial.Provider = proxyProviderID(model)
	partial.Model = model.ID
	return partial
}

func proxyProviderID(model *ai.Model) string {
	if model.ProviderMeta.ProviderID != "" {
		return model.ProviderMeta.ProviderID
	}
	if model.Provider != nil {
		return model.Provider.ID()
	}
	return ""
}

func buildProxyRequest(model *ai.Model, transcript ai.TranscriptContext, options ProxyStreamOptions) ([]byte, error) {
	if model == nil {
		return nil, errors.New("proxy model is nil")
	}
	messages := transcript.Messages()
	if messages == nil {
		messages = []ai.Message{}
	}
	payload := proxyRequestPayload{
		Model: proxyModel{
			ID: model.ID, Name: model.DisplayName, API: model.ProviderMeta.API, Provider: proxyProviderID(model),
			BaseURL: model.ProviderMeta.BaseURL, Reasoning: model.ProviderMeta.Reasoning,
			ThinkingLevelMap: model.ThinkingLevelMap, Input: model.Input, InputLimits: model.InputLimits,
			Cost: model.CostRates(), PromptCache: model.PromptCache,
			ContextWindow: model.Capabilities.ContextWindow, MaxTokens: model.Capabilities.MaxOutputTokens,
			SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel, Headers: model.ProviderMeta.Headers, Compat: model.ProviderMeta.Compat,
		},
		Context: proxyRequestContext{Messages: messages},
		Options: proxySerializableStreamOptions{
			Temperature: options.Temperature, SamplingParams: options.SamplingParams, MaxTokens: options.MaxTokens,
			Reasoning: options.Reasoning, CacheRetention: options.CacheRetention, SessionID: options.SessionID,
			Headers: options.Headers, Metadata: options.Metadata, Transport: options.Transport,
			ThinkingBudgets: options.ThinkingBudgets, MaxRetryDelayMs: options.MaxRetryDelayMs,
		},
	}
	return json.Marshal(payload)
}

func runProxyRequest(ctx context.Context, options ProxyStreamOptions, body []byte, converter *proxyEventConverter, stream *ai.AssistantMessageEventStream) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, options.ProxyURL+"/api/stream", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+options.AuthToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			// upstream: packages/agent/src/proxy.ts:streamProxy fetch rejects with the signal's reason, a DOMException AbortError by default
			return proxyAbortReason(ctx)
		}
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return proxyHTTPError(response)
	}
	return readProxyEvents(ctx, response.Body, converter, stream)
}

// proxyAbortReason is the message fetch rejects with when the signal aborts before the response arrives: a cancel cause, the
// TimeoutError of AbortSignal.timeout for an expired deadline, or the default AbortError.
func proxyAbortReason(ctx context.Context) error {
	cause := context.Cause(ctx)
	switch {
	case cause == nil || errors.Is(cause, context.Canceled):
		return errors.New("This operation was aborted")
	case errors.Is(cause, context.DeadlineExceeded):
		return errors.New("The operation was aborted due to timeout")
	default:
		return cause
	}
}

func proxyHTTPError(response *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) == nil && payload.Error != "" {
		return errors.New("Proxy error: " + payload.Error)
	}
	return errors.New("Proxy error: " + response.Status)
}

type proxyEventConverter struct {
	partial  *ai.AssistantMessage
	toolJSON map[int]string
}

func readProxyEvents(ctx context.Context, body io.Reader, converter *proxyEventConverter, stream *ai.AssistantMessageEventStream) error {
	reader := bufio.NewReader(body)
	for {
		line, readErr := reader.ReadString('\n')
		if ctx.Err() != nil {
			return errors.New("Request aborted by user")
		}
		if line != "" {
			terminal, err := processProxyLine(line, converter, stream)
			if err != nil {
				return err
			}
			if terminal {
				// The terminal event hands partial to the consumer, so nothing may change it afterwards. Pi keeps reading and
				// applies later events, parse failures and aborts to the delivered message; in Go that would be a data race.
				// The rest of the body is drained without effect so the connection lives as long as Pi's.
				_, _ = io.Copy(io.Discard, reader)
				return nil
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}
	if ctx.Err() != nil {
		return errors.New("Request aborted by user")
	}
	converter.partial.StopReason = ai.StopReasonError
	converter.partial.ErrorMessage = "Connection closed by proxy server before the response completed"
	return stream.Push(ai.ErrorEvent{Reason: ai.StopReasonError, Error: converter.partial})
}

func processProxyLine(line string, converter *proxyEventConverter, stream *ai.AssistantMessageEventStream) (bool, error) {
	// upstream: packages/agent/src/proxy.ts:processLine
	if !strings.HasPrefix(line, "data: ") {
		return false, nil
	}
	// upstream: packages/agent/src/proxy.ts:processLine
	data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
	if data == "" {
		return false, nil
	}
	var proxyEvent ProxyAssistantMessageEvent
	if err := json.Unmarshal([]byte(data), &proxyEvent); err != nil {
		return false, err
	}
	event, err := converter.process(proxyEvent)
	if err != nil || event == nil {
		return false, err
	}
	terminal := event.EventType() == ai.EventDone || event.EventType() == ai.EventError
	if err := stream.Push(event); err != nil {
		return false, err
	}
	return terminal, nil
}

func (converter *proxyEventConverter) process(proxyEvent ProxyAssistantMessageEvent) (ai.AssistantMessageEvent, error) {
	partial := converter.partial
	if proxyEvent.notArrayIndex && proxyEventStoresContent(proxyEvent.Type) {
		// Pi stores the block as a non-index property of the content array, which JSON never shows; an ai.AssistantMessage has no such slot, so the event fails.
		return nil, fmt.Errorf("proxy %s event has no usable contentIndex", proxyEvent.Type)
	}
	switch proxyEvent.Type {
	case "start":
		return ai.StartEvent{Partial: partial}, nil
	case "text_start":
		if err := setProxyContent(partial, proxyEvent.ContentIndex, ai.TextContent{}); err != nil {
			return nil, err
		}
		return ai.TextStartEvent{ContentIndex: proxyEvent.ContentIndex, Partial: partial}, nil
	case "text_delta":
		content, err := proxyContent(partial, proxyEvent.ContentIndex)
		text, ok := content.(ai.TextContent)
		if err != nil || !ok {
			return nil, errors.New("Received text_delta for non-text content")
		}
		text.Text += proxyEvent.Delta
		partial.Content[proxyEvent.ContentIndex] = text
		return ai.TextDeltaEvent{ContentIndex: proxyEvent.ContentIndex, Delta: proxyEvent.Delta, Partial: partial}, nil
	case "text_end":
		content, err := proxyContent(partial, proxyEvent.ContentIndex)
		text, ok := content.(ai.TextContent)
		if err != nil || !ok {
			return nil, errors.New("Received text_end for non-text content")
		}
		text.TextSignature = optionalProxyString(proxyEvent.ContentSignature)
		partial.Content[proxyEvent.ContentIndex] = text
		return ai.TextEndEvent{ContentIndex: proxyEvent.ContentIndex, Content: text.Text, Partial: partial}, nil
	case "thinking_start":
		if err := setProxyContent(partial, proxyEvent.ContentIndex, ai.ThinkingContent{}); err != nil {
			return nil, err
		}
		return ai.ThinkingStartEvent{ContentIndex: proxyEvent.ContentIndex, Partial: partial}, nil
	case "thinking_delta":
		content, err := proxyContent(partial, proxyEvent.ContentIndex)
		thinking, ok := content.(ai.ThinkingContent)
		if err != nil || !ok {
			return nil, errors.New("Received thinking_delta for non-thinking content")
		}
		thinking.Thinking += proxyEvent.Delta
		partial.Content[proxyEvent.ContentIndex] = thinking
		return ai.ThinkingDeltaEvent{ContentIndex: proxyEvent.ContentIndex, Delta: proxyEvent.Delta, Partial: partial}, nil
	case "thinking_end":
		content, err := proxyContent(partial, proxyEvent.ContentIndex)
		thinking, ok := content.(ai.ThinkingContent)
		if err != nil || !ok {
			return nil, errors.New("Received thinking_end for non-thinking content")
		}
		thinking.ThinkingSignature = optionalProxyString(proxyEvent.ContentSignature)
		partial.Content[proxyEvent.ContentIndex] = thinking
		return ai.ThinkingEndEvent{ContentIndex: proxyEvent.ContentIndex, Content: thinking.Thinking, Partial: partial}, nil
	case "toolcall_start":
		toolCall := ai.ToolCall{ID: proxyEvent.ID, Name: proxyEvent.ToolName, Arguments: ai.JsonObject{}}
		toolCall.SetPartialJson("")
		if err := setProxyContent(partial, proxyEvent.ContentIndex, toolCall); err != nil {
			return nil, err
		}
		delete(converter.toolJSON, proxyEvent.ContentIndex)
		return ai.ToolCallStartEvent{ContentIndex: proxyEvent.ContentIndex, Partial: partial}, nil
	case "toolcall_delta":
		content, err := proxyContent(partial, proxyEvent.ContentIndex)
		toolCall, ok := content.(ai.ToolCall)
		if err != nil || !ok {
			return nil, errors.New("Received toolcall_delta for non-toolCall content")
		}
		converter.toolJSON[proxyEvent.ContentIndex] += proxyEvent.Delta
		toolCall.SetPartialJson(converter.toolJSON[proxyEvent.ContentIndex])
		toolCall.SetStreamingArguments(converter.toolJSON[proxyEvent.ContentIndex])
		partial.Content[proxyEvent.ContentIndex] = toolCall
		return ai.ToolCallDeltaEvent{ContentIndex: proxyEvent.ContentIndex, Delta: proxyEvent.Delta, Partial: partial}, nil
	case "toolcall_end":
		content, err := proxyContent(partial, proxyEvent.ContentIndex)
		toolCall, ok := content.(ai.ToolCall)
		if err != nil || !ok {
			return nil, nil
		}
		if proxyEvent.ToolCall != nil {
			toolCall = mergeProxyToolCall(toolCall, *proxyEvent.ToolCall, proxyEvent.toolCallFields)
		}
		toolCall.DeletePartialJson()
		partial.Content[proxyEvent.ContentIndex] = toolCall
		delete(converter.toolJSON, proxyEvent.ContentIndex)
		return ai.ToolCallEndEvent{ContentIndex: proxyEvent.ContentIndex, ToolCall: toolCall, Partial: partial}, nil
	case "done":
		finishProxyPartial(partial, proxyEvent)
		return ai.DoneEvent{Reason: proxyEvent.Reason, Message: partial}, nil
	case "error":
		finishProxyPartial(partial, proxyEvent)
		partial.ErrorMessage = optionalProxyString(proxyEvent.ErrorMessage)
		return ai.ErrorEvent{Reason: proxyEvent.Reason, Error: partial}, nil
	default:
		// upstream: packages/agent/src/proxy.ts:processProxyEvent console.warn
		fmt.Fprintf(os.Stderr, "Unhandled proxy event type: %s\n", proxyEvent.Type)
		return nil, nil
	}
}

// mergeProxyToolCall mirrors Object.assign(content, proxyEvent.toolCall): members the wire call omits keep the streamed call's value.
func mergeProxyToolCall(streamed, wire ai.ToolCall, fields map[string]json.RawMessage) ai.ToolCall {
	if _, ok := fields["id"]; !ok {
		wire.ID = streamed.ID
	}
	if _, ok := fields["name"]; !ok {
		wire.Name = streamed.Name
	}
	if _, ok := fields["arguments"]; !ok {
		wire.Arguments = streamed.Arguments
		if raw, err := streamed.ArgumentsJSON(); err == nil && streamed.Arguments != nil {
			_ = wire.SetArgumentsJSON(raw)
		}
	}
	return wire
}

// proxyEventStoresContent reports the events that write `partial.content[index]`. The others only read it, and a read at a
// non-index key finds nothing in Pig, as in Pi: a store at such a key has already ended the stream.
func proxyEventStoresContent(eventType ProxyAssistantMessageEventType) bool {
	switch eventType {
	case ProxyEventTextStart, ProxyEventThinkingStart, ProxyEventToolcallStart:
		return true
	}
	return false
}

// setProxyContent stores a block at an index as `partial.content[index] = block` does, except that an index past the end is an error: Pi leaves holes in the array, and a nil block cannot live in an ai.AssistantMessage (its copy, marshalling and estimation take only the three block kinds).
func setProxyContent(partial *ai.AssistantMessage, index int, content ai.AssistantContentBlock) error {
	if index > len(partial.Content) {
		return fmt.Errorf("proxy content index %d is past the end of the content array", index)
	}
	if index == len(partial.Content) {
		partial.Content = append(partial.Content, content)
	} else {
		partial.Content[index] = content
	}
	return nil
}

// proxyContent reads `partial.content[index]`: a missing block (a non-index key is -1) is undefined, which no event
// handler accepts.
func proxyContent(partial *ai.AssistantMessage, index int) (ai.AssistantContentBlock, error) {
	if index < 0 || index >= len(partial.Content) {
		return nil, nil
	}
	return partial.Content[index], nil
}

func optionalProxyString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func finishProxyPartial(partial *ai.AssistantMessage, event ProxyAssistantMessageEvent) {
	partial.StopReason = event.Reason
	partial.Usage = event.Usage
	if event.ProviderThinkingLevel != nil {
		partial.ProviderThinkingLevel = *event.ProviderThinkingLevel
	}
}

func pushProxyFailure(stream *ai.AssistantMessageEventStream, partial *ai.AssistantMessage, err error, aborted bool) {
	reason := ai.StopReasonError
	if aborted {
		reason = ai.StopReasonAborted
	}
	partial.StopReason = reason
	partial.ErrorMessage = err.Error()
	_ = stream.Push(ai.ErrorEvent{Reason: reason, Error: partial})
}

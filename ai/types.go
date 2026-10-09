package ai

// Mirrors @mariozechner/pi-ai from pi v0.69.0.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// maxSSETokenSize is the maximum buffer size for SSE line scanning across all
// providers. Matches the official OpenAI Go SDK (bufio.MaxScanTokenSize << 9 =
// 32 MB). Upstream Pi inherits Node.js stream semantics (no hard cap); 32 MB
// is generous enough that no real SSE event will approach it while still
// providing OOM protection against malformed streams.
//
// Reference: https://github.com/openai/openai-go/pull/373
var maxSSETokenSize = bufio.MaxScanTokenSize << 9 // 32 MB

// ─── Provider API kind ────────────────────────────────────────────────────────

// API identifies which provider-API protocol a model uses to talk to its
// backend. It mirrors upstream `@mariozechner/pi-ai`'s `Api` union type
// (.upstream/current/packages/ai/src/types.ts:17):
//
//	export type Api = KnownApi | (string & {});
//
// The string base type allows forward-compatibility for new APIs added
// upstream without breaking the pig build, while [KnownAPIs] enumerates
// the values defined as of the pinned UpstreamVersion.
//
// Use cases: model catalog (GeneratedModel.API), extension provider
// registration (extension.ProviderConfig.API + ProviderModelConfig.API),
// stream dispatch (matches one of the Stream<API> handlers).
//
// upstream: types.ts:17 (Api), types.ts:7-15 (KnownApi)
type API string

// KnownAPIs enumerates the API values defined by upstream as of
// UpstreamVersion. Mirrors upstream `KnownApi` union
// (.upstream/current/packages/ai/src/types.ts:7-15).
//
// Each constant value is the wire-format string consumed by provider
// dispatch. Provider extensions or generated catalog entries may carry
// API values not listed here (the underlying type is plain string) -
// this is forward-compat for new upstream additions.
//
// upstream: types.ts:7-15
const (
	APIOpenAICompletions     API = "openai-completions"
	APIMistralConversations  API = "mistral-conversations"
	APIOpenAIResponses       API = "openai-responses"
	APIAzureOpenAIResponses  API = "azure-openai-responses"
	APIOpenAICodexResponses  API = "openai-codex-responses"
	APIAnthropicMessages     API = "anthropic-messages"
	APIBedrockConverseStream API = "bedrock-converse-stream"
	APIGoogleGenerativeAI    API = "google-generative-ai"
	APIGoogleVertex          API = "google-vertex"
	APIPiMessages            API = "pi-messages"
)

// ─── Content Types ────────────────────────────────────────────────────────────

// TextContent is a text fragment in a message.
type TextContent struct {
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`

	scratch contentIndexScratch
}

func (TextContent) contentType() string { return "text" }

func (content TextContent) MarshalJSON() ([]byte, error) {
	return marshalContent(content.contentType(), struct {
		Text          string `json:"text"`
		TextSignature string `json:"textSignature,omitempty"`
		Index         *int   `json:"index,omitempty"`
	}{content.Text, content.TextSignature, content.scratch.wire()})
}

// ImageContent is a base64-encoded image in a message.
type ImageContent struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (ImageContent) contentType() string { return "image" }

func (content ImageContent) MarshalJSON() ([]byte, error) {
	type payload ImageContent
	return marshalContent(content.contentType(), payload(content))
}

// JsonValue is any JSON value: null, a boolean, a number, a string, an array or a JsonObject (types.ts:458).
type JsonValue = any

// JsonObject is a provider-facing JSON object (types.ts:459).
type JsonObject map[string]JsonValue

// ToolCall is a tool invocation block in an assistant message. Streaming OpenAI calls also serialize provider scratch fields until finalization or failure. Arguments is read and edited as a Go map; the member order the model sent is kept beside it and is written by ArgumentsJSON and MarshalJSON.
type ToolCall struct {
	scratch          toolCallScratch
	argumentOrder    schemaObjectOrder
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Arguments        JsonObject `json:"arguments"`
	ThoughtSignature string     `json:"thoughtSignature,omitempty"`
	Namespace        string     `json:"namespace,omitempty"`
}

func (ToolCall) contentType() string { return "toolCall" }

func (content ToolCall) MarshalJSON() ([]byte, error) {
	if err := validateJsonValue(content.Arguments); err != nil {
		return nil, fmt.Errorf("tool call arguments: %w", err)
	}
	var arguments any = content.Arguments
	if content.argumentOrder != nil && content.Arguments != nil {
		ordered, err := content.ArgumentsJSON()
		if err != nil {
			return nil, fmt.Errorf("tool call arguments: %w", err)
		}
		arguments = json.RawMessage(ordered)
	}
	return marshalContent(content.contentType(), struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		Arguments        any    `json:"arguments"`
		ThoughtSignature string `json:"thoughtSignature,omitempty"`
		Namespace        string `json:"namespace,omitempty"`
		toolCallScratchJSON
	}{content.ID, content.Name, arguments, content.ThoughtSignature, content.Namespace, content.scratch.wire()})
}

// UnmarshalJSON decodes the call and keeps the member order of its arguments.
func (content *ToolCall) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	type payload ToolCall
	var decoded payload
	if err := sdkjson.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var members struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	if len(members.Arguments) > 0 {
		order, err := readSchemaObjectOrder(members.Arguments)
		if err != nil {
			return err
		}
		decoded.argumentOrder = order
	}
	*content = ToolCall(decoded)
	return nil
}

// NewDecodedToolCall returns the tool call that decoding {"id":...,"name":...,"arguments":rawArguments,...} yields: the
// arguments object and the member order of its objects, which rawArguments, the JSON text of the arguments value, fixes.
// A caller that has already parsed the object uses it to skip the general decode.
// pig additive (D104): builds a decoded tool call for the single-pass entry decoder.
func NewDecodedToolCall(id, name, thoughtSignature, namespace string, arguments JsonObject, rawArguments []byte) (ToolCall, error) {
	call := ToolCall{ID: id, Name: name, Arguments: arguments, ThoughtSignature: thoughtSignature, Namespace: namespace}
	if len(rawArguments) > 0 {
		order, err := readSchemaObjectOrder(rawArguments)
		if err != nil {
			return ToolCall{}, err
		}
		call.argumentOrder = order
	}
	return call, nil
}

func marshalContent(contentType string, content any) ([]byte, error) {
	// sdkjson keeps a lone UTF-16 unit as an escape instead of replacing it, as JSON.stringify does.
	body, err := sdkjson.Marshal(content)
	if err != nil {
		return nil, err
	}
	typeField, err := json.Marshal(contentType)
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, len(body))
	encoded = append(encoded, `{"type":`...)
	encoded = append(encoded, typeField...)
	if len(body) > 2 {
		encoded = append(encoded, ',')
		encoded = append(encoded, body[1:]...)
	} else {
		encoded = append(encoded, '}')
	}
	return encoded, nil
}

// ThinkingContent is a thinking/reasoning block. JSON decoding and provider streams preserve the distinction between omitted and explicitly empty signatures.
type ThinkingContent struct {
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`

	thinkingSignatureEmpty bool
	scratch                contentIndexScratch
}

func (ThinkingContent) contentType() string { return "thinking" }

func (content ThinkingContent) MarshalJSON() ([]byte, error) {
	var signature *string
	if content.ThinkingSignature != "" || content.thinkingSignatureEmpty {
		signature = &content.ThinkingSignature
	}
	return marshalContent(content.contentType(), struct {
		Thinking          string  `json:"thinking"`
		ThinkingSignature *string `json:"thinkingSignature,omitempty"`
		Redacted          bool    `json:"redacted,omitempty"`
		Index             *int    `json:"index,omitempty"`
	}{content.Thinking, signature, content.Redacted, content.scratch.wire()})
}

func (content *ThinkingContent) UnmarshalJSON(data []byte) error {
	type payload ThinkingContent
	var decoded struct {
		payload
		ThinkingSignature *string `json:"thinkingSignature"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*content = ThinkingContent(decoded.payload)
	if decoded.ThinkingSignature != nil {
		content.ThinkingSignature = *decoded.ThinkingSignature
		content.thinkingSignatureEmpty = content.ThinkingSignature == ""
	}
	return nil
}

// ContentBlock is the closed union of provider-facing content types.
type ContentBlock interface {
	contentBlock()
	contentType() string
}

func (TextContent) contentBlock()     {}
func (ImageContent) contentBlock()    {}
func (ToolCall) contentBlock()        {}
func (ThinkingContent) contentBlock() {}

// ─── Messages ─────────────────────────────────────────────────────────────────

// ToolReference names a tool removed from the transcript's active tool set.
type ToolReference struct {
	Name string `json:"name"`
}

// PromptSection preserves the insertion order of named system-prompt sections.
// A nil Value is an explicit JSON null that removes the named section.
type PromptSection struct {
	Name  string
	Value *string
}

// OrderedSections is the ordered representation of upstream's section object.
type OrderedSections []PromptSection

func (s OrderedSections) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	writeString := func(value string) error {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return err
		}
		buf.Write(bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}))
		return nil
	}
	buf.WriteByte('{')
	for i, section := range s {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeString(section.Name); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if section.Value == nil {
			buf.WriteString("null")
			continue
		}
		if err := writeString(*section.Value); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (s *OrderedSections) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("system message sections must be an object")
	}
	sections := OrderedSections{}
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := nameToken.(string)
		if !ok {
			return fmt.Errorf("system message section name must be a string")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		var value *string
		if string(raw) != "null" {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return fmt.Errorf("system message section %q: %w", name, err)
			}
			value = &text
		}
		if index := slices.IndexFunc(sections, func(section PromptSection) bool { return section.Name == name }); index >= 0 {
			sections[index].Value = value
		} else {
			sections = append(sections, PromptSection{Name: name, Value: value})
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*s = sections
	return nil
}

// StopReason is the terminal state of an assistant response.
type StopReason string

const (
	StopReasonPending  StopReason = "pending"
	StopReasonStop     StopReason = "stop"
	StopReasonLength   StopReason = "length"
	StopReasonToolUse  StopReason = "toolUse"
	StopReasonError    StopReason = "error"
	StopReasonAborted  StopReason = "aborted"
	StopReasonDeferred StopReason = "deferred"
)

// ─── Tool Schema ──────────────────────────────────────────────────────────────

// ToolSchema describes an LLM-callable tool in JSON Schema format. JSON decoding and encoding retain schema declaration order for provider strict-schema derivation; directly authored Go maps use deterministic key order.
type ToolSchema struct {
	parameterOrder   schemaObjectOrder
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Parameters       map[string]any `json:"parameters"`                 // JSON Schema object
	PromptGuidelines []string       `json:"promptGuidelines,omitempty"` // Guidelines injected into system prompt
	// ConstrainedSampling is the optional provider-side constrained sampling
	// request for this tool (nil = unset, equivalent to upstream's false).
	// Mirrors upstream Tool.constrainedSampling (types.ts).
	ConstrainedSampling *ConstrainedSamplingConfig `json:"constrainedSampling,omitempty"`
	// ConstrainedSamplingDisabled records upstream's explicit `constrainedSampling: false`. It behaves like an
	// unset request for providers, but transcript tool declarations keep it (transcript.ts:123-129).
	ConstrainedSamplingDisabled bool `json:"-"`
}

// GrammarFormat is an OpenAI grammar variant key. Mirrors upstream GrammarFormat.
type GrammarFormat = string

const (
	GrammarFormatOpenAILark  GrammarFormat = "openai_lark"
	GrammarFormatOpenAIRegex GrammarFormat = "openai_regex"
)

// ConstrainedSamplingConfig is the optional provider-side constrained sampling
// config for a tool. Type is "json_schema" (JSON-schema strict sampling) or
// "grammar" (Lark/regex grammar variants). Mirrors upstream
// ConstrainedSamplingConfig; upstream's `false` maps to a nil pointer.
type ConstrainedSamplingConfig struct {
	Type     ConstrainedSamplingType   `json:"type"`
	Strict   ConstrainedSamplingStrict `json:"strict,omitempty"`   // json_schema only
	Variants GrammarVariants           `json:"variants,omitempty"` // grammar only: format → definition
}

// ConstrainedSamplingType is the discriminator of ConstrainedSamplingConfig: `"json_schema" | "grammar"`.
// upstream: packages/ai/src/types.ts:707-715
type ConstrainedSamplingType string

const (
	ConstrainedSamplingJSONSchema ConstrainedSamplingType = "json_schema"
	ConstrainedSamplingGrammar    ConstrainedSamplingType = "grammar"
)

// ConstrainedSamplingStrict is how strictly a json_schema tool must be sampled: `"prefer" | "require"`.
// upstream: packages/ai/src/types.ts:709-711
type ConstrainedSamplingStrict string

const (
	ConstrainedSamplingStrictPrefer  ConstrainedSamplingStrict = "prefer"
	ConstrainedSamplingStrictRequire ConstrainedSamplingStrict = "require"
)

// ─── Streaming Protocol ───────────────────────────────────────────────────────

// AssistantEventType enumerates all streaming event types.
type AssistantEventType string

const (
	EventStart         AssistantEventType = "start"
	EventTextStart     AssistantEventType = "text_start"
	EventTextDelta     AssistantEventType = "text_delta"
	EventTextEnd       AssistantEventType = "text_end"
	EventThinkingStart AssistantEventType = "thinking_start"
	EventThinkingDelta AssistantEventType = "thinking_delta"
	EventThinkingEnd   AssistantEventType = "thinking_end"
	EventToolCallStart AssistantEventType = "toolcall_start"
	EventToolCallDelta AssistantEventType = "toolcall_delta"
	EventToolCallEnd   AssistantEventType = "toolcall_end"
	EventDone          AssistantEventType = "done"
	EventError         AssistantEventType = "error"
)

// Usage reports token usage from the provider.
type Usage struct {
	Input        int       `json:"input"`
	Output       int       `json:"output"`
	CacheRead    int       `json:"cacheRead"`
	CacheWrite   int       `json:"cacheWrite"`
	CacheWrite1h *int      `json:"cacheWrite1h,omitempty"`
	Reasoning    *int      `json:"reasoning,omitempty"`
	TotalTokens  int       `json:"totalTokens"`
	Cost         UsageCost `json:"cost"`
}

// UsageCost is the provider-reported cost breakdown carried by upstream Usage.
type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// DeferredHandle identifies a provider-side deferred response and the data
// required to retrieve it later.
type DeferredHandle struct {
	Provider    string    `json:"provider"`
	ModelID     string    `json:"modelId"`
	API         API       `json:"api"`
	ID          string    `json:"id"`
	ExpiresAt   *int64    `json:"expiresAt,omitempty"`
	PollAfterMS *int64    `json:"pollAfterMs,omitempty"`
	Data        JsonValue `json:"data,omitempty"`
}

// DiagnosticErrorInfo is a redacted, serializable error summary attached to
// assistant diagnostics.
type DiagnosticErrorInfo struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
	Code    any    `json:"code,omitempty"`
}

// AssistantMessageDiagnostic is a provider/runtime diagnostic attached to an
// assistant message when a recoverable failure or transport fallback occurs.
type AssistantMessageDiagnostic struct {
	Type      string               `json:"type"`
	Timestamp int64                `json:"timestamp"`
	Error     *DiagnosticErrorInfo `json:"error,omitempty"`
	Details   map[string]any       `json:"details,omitempty"`
}

// ─── Provider Compat + Interface ─────────────────────────────────────────────

// ModelCompat is the shared compat bag used by generated model metadata,
// models.json overrides, and provider configs. It merges the upstream
// OpenAI-compatible compat fields with the newer Anthropic/OpenAI Responses
// flags used by specific provider families.
//
// Package-local provider code continues to refer to the API-specific aliases
// below (OpenAICompat, OpenAIResponsesCompat, AnthropicMessagesCompat), but
// they all share the same underlying shape so generated metadata and registry
// merge logic can stay simple.
// ModelCompat is the shared compat bag used by generated model metadata,
// the provider configs, and the registry. It is identical to OpenAICompat.
type ModelCompat = OpenAICompat

// ThinkingLevel controls extended-thinking depth (where supported).
type OpenAIResponsesCompat = OpenAICompat
type AnthropicMessagesCompat = OpenAICompat

// SessionAffinityFormat selects which session-affinity headers a provider
// sends from options.SessionID. Mirrors upstream SessionAffinityFormat
// (types.ts). Empty string means auto-detect at the provider.
type SessionAffinityFormat string

const (
	// SessionAffinityOpenAI sends session_id + x-client-request-id
	// (+ x-session-affinity on Completions).
	SessionAffinityOpenAI SessionAffinityFormat = "openai"
	// SessionAffinityOpenAINoSession omits session_id.
	SessionAffinityOpenAINoSession SessionAffinityFormat = "openai-nosession"
	// SessionAffinityOpenRouter sends x-session-id.
	SessionAffinityOpenRouter SessionAffinityFormat = "openrouter"
)

// ThinkingLevel is the reasoning effort a request can ask for (types.ts ThinkingLevel). It has no "off": an omitted level means no reasoning request.
type ThinkingLevel string

// ModelThinkingLevel is "off" or a ThinkingLevel (types.ts ModelThinkingLevel); it is the level a model supports, maps and clamps to.
type ModelThinkingLevel string

// ReasoningOption is the request option for a model level: "off" is no request (the `reasoning` option omitted), every other level is itself.
func (level ModelThinkingLevel) ReasoningOption() ThinkingLevel {
	if level == ThinkingOff {
		return ""
	}
	return ThinkingLevel(level)
}

// The request levels of ThinkingLevel.
const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	// ThinkingLevelXHigh is supported only by models whose thinkingLevelMap maps "xhigh".
	ThinkingLevelXHigh ThinkingLevel = "xhigh"
	// ThinkingLevelMax is supported only by models whose thinkingLevelMap maps "max" (for example Claude Opus 4.6 adaptive thinking). Like xhigh it clamps down for models that do not map it.
	ThinkingLevelMax ThinkingLevel = "max"
)

// The levels of ModelThinkingLevel: "off" and the request levels.
const (
	ThinkingOff     ModelThinkingLevel = "off"
	ThinkingMinimal ModelThinkingLevel = "minimal"
	ThinkingLow     ModelThinkingLevel = "low"
	ThinkingMedium  ModelThinkingLevel = "medium"
	ThinkingHigh    ModelThinkingLevel = "high"
	ThinkingXHigh   ModelThinkingLevel = "xhigh"
	ThinkingMax     ModelThinkingLevel = "max"
)

// thinkingLevelOrder maps each ThinkingLevel to its ordinal position
// in the canonical order (off < minimal < low < medium < high < xhigh).
// String comparison is NOT equivalent because "off" > "high" lexicographically.
var thinkingLevelOrder = map[ModelThinkingLevel]int{
	ThinkingOff:     0,
	ThinkingMinimal: 1,
	ThinkingLow:     2,
	ThinkingMedium:  3,
	ThinkingHigh:    4,
	ThinkingXHigh:   5,
	ThinkingMax:     6,
}

// CompareThinkingLevels returns -1, 0, or +1 comparing a and b by
// semantic order (off < minimal < low < medium < high < xhigh).
// Unknown levels sort before "off".
func CompareThinkingLevels(a, b ModelThinkingLevel) int {
	oa, ob := thinkingLevelOrder[a], thinkingLevelOrder[b]
	if oa < ob {
		return -1
	}
	if oa > ob {
		return 1
	}
	return 0
}

type ThinkingLevelMap = map[ModelThinkingLevel]*string

// SamplingParams holds free-form request body sampling keys such as temperature, top_p, top_k, min_p or repetition_penalty.
type SamplingParams = map[string]any

// SamplingParamsByThinkingLevel holds sampling overrides keyed by Pi thinking level, not by provider effort value.
type SamplingParamsByThinkingLevel = map[ModelThinkingLevel]SamplingParams

// CacheRetention selects the requested prompt-cache lifetime. Empty means the option is unset so the provider can apply its documented default.
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

type Transport string

const (
	TransportSSE             Transport = "sse"
	TransportWebSocket       Transport = "websocket"
	TransportWebSocketCached Transport = "websocket-cached"
	TransportAuto            Transport = "auto"
)

// ProviderHeaders mirrors Pi's request-header map. A nil value removes an
// existing header after configured and authentication headers are merged.
type ProviderHeaders map[string]*string

// ProviderHeader returns a non-null ProviderHeaders value.
//
//go:fix inline
func ProviderHeader(value string) *string { return new(value) }

// ProviderHeadersFromStrings converts non-null configured headers to request
// header values.
func ProviderHeadersFromStrings(values map[string]string) ProviderHeaders {
	if len(values) == 0 {
		return nil
	}
	headers := make(ProviderHeaders, len(values))
	for name, value := range values {
		headers[name] = new(value)
	}
	return headers
}

// ProviderResponse is the status and headers observed before consuming a provider response.
type ProviderResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// FetchFunction is a per-request HTTP transport. The request context owns cancellation; the caller owns and closes the returned streaming response body.
// Ports packages/ai/src/types.ts (ProviderRequestOptions.fetch).
type FetchFunction func(*http.Request) (*http.Response, error)

func (fetch FetchFunction) RoundTrip(request *http.Request) (*http.Response, error) {
	return fetch(request)
}

// BedrockThinkingDisplay is how Bedrock Claude returns thinking content; it has the values of AnthropicThinkingDisplay.
// Mirrors upstream BedrockThinkingDisplay (api/bedrock-converse-stream.ts:77).
type BedrockThinkingDisplay = AnthropicThinkingDisplay

// ToolChoice is the provider-neutral tool choice: let the model decide, or call no tool.
// Mirrors upstream ToolChoice (types.ts:84).
type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
)

// toolChoiceName returns the name of a string or ToolChoice tool choice.
func toolChoiceName(choice any) (string, bool) {
	switch value := choice.(type) {
	case string:
		return value, true
	case ToolChoice:
		return string(value), true
	}
	return "", false
}

// StreamOptions are the options for a streaming LLM call.
type StreamOptions struct {
	// TelemetryContext parents provider request spans; nil means no recording backend.
	TelemetryContext telemetry.TelemetryContext
	// Deferred preserves the deferred-generation boolean or window object.
	Deferred *DeferredOption
	// Fetch replaces HTTP execution with a caller-owned client, preserving its redirect policy and request context.
	Fetch     *http.Client `json:"-"`
	MaxTokens int
	// Region selects the Bedrock request region. An inference-profile ARN's embedded region takes precedence.
	Region string `json:"region,omitempty"`
	// Profile selects the AWS shared credentials profile for a Bedrock request.
	Profile string `json:"profile,omitempty"`
	// Project is the Google Cloud project of a Vertex AI request; it takes precedence over the provider configuration and GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT (GoogleVertexOptions.project).
	Project string `json:"project,omitempty"`
	// Location is the Google Cloud location of a Vertex AI request; it takes precedence over the provider configuration and GOOGLE_CLOUD_LOCATION (GoogleVertexOptions.location).
	Location    string `json:"location,omitempty"`
	Temperature float64
	// TemperatureSet distinguishes an explicit zero temperature from omission.
	// Non-zero Temperature values remain present for existing callers.
	TemperatureSet  bool
	SamplingParams  map[string]any
	ThinkingBudgets *ThinkingBudgets
	Thinking        ThinkingLevel `json:"reasoning,omitempty"`
	// GoogleThinking carries Google's raw thinking object; Thinking is the provider-neutral reasoning option.
	GoogleThinking       *GoogleThinkingOptions `json:"thinking,omitempty"`
	ThinkingEnabled      *bool                  `json:"thinkingEnabled,omitempty"`
	ThinkingBudgetTokens *int                   `json:"thinkingBudgetTokens,omitempty"`
	Effort               AnthropicEffort        `json:"effort,omitempty"`
	// ThinkingDisplay controls how Anthropic returns thinking content. Empty means "summarized" when thinking is enabled (anthropic-messages.ts:271, :1238, :1246).
	ThinkingDisplay     AnthropicThinkingDisplay `json:"thinkingDisplay,omitempty"`
	InterleavedThinking *bool                    `json:"interleavedThinking,omitempty"`
	RequestMetadata     map[string]string        `json:"requestMetadata,omitempty"`
	// BearerToken is the Bedrock bearer token; it takes precedence over APIKey and AWS_BEARER_TOKEN_BEDROCK (BedrockOptions.bearerToken).
	BearerToken string `json:"bearerToken,omitempty"`
	// ServiceTier is the OpenAI Responses and Codex service_tier request value (OpenAIResponsesOptions.serviceTier).
	ServiceTier string `json:"serviceTier,omitempty"`
	// TextVerbosity is the Codex text verbosity ("low", "medium" or "high"); empty is "low" (OpenAICodexResponsesOptions.textVerbosity).
	TextVerbosity string `json:"textVerbosity,omitempty"`
	// ReasoningEffort is the raw OpenAI-compatible or Mistral API effort. Unlike the provider-neutral Thinking level, it is mapped but not clamped so the provider can reject unsupported values.
	ReasoningEffort string
	// ReasoningSummary is the raw OpenAI Responses reasoning summary mode ("auto", "detailed" or "concise"). Without an effort it requests medium effort.
	ReasoningSummary string `json:"reasoningSummary,omitempty"`
	// PromptMode is the raw Mistral prompt mode, independent of provider-neutral Thinking.
	PromptMode string `json:"promptMode,omitempty"`
	// IsReasoning indicates whether the model supports extended reasoning.
	// Used by providers to decide developer vs system role and reasoning_effort gating.
	// Mirrors upstream model.reasoning field.
	IsReasoning bool
	// ModelCost is the requested model's price. Providers price Usage.Cost
	// with it where upstream providers call calculateCost(model, usage).
	ModelCost ModelCost
	// Env contains request-specific provider environment values. Runtime-resolved
	// values are merged first; request values win.
	Env ProviderEnv
	// TransformHeaders runs once after configured, auth, and request headers are
	// merged. ModelRuntime consumes it and does not forward it to providers.
	TransformHeaders func(context.Context, ProviderHeaders) (ProviderHeaders, error)
	// Headers are request-specific HTTP headers. A nil value deletes a header
	// supplied by configured or resolved authentication state.
	Headers ProviderHeaders
	// Signal carries the request lifetime to extension-owned provider callbacks. Native Provider.Stream implementations receive the same lifetime as their context argument.
	Signal context.Context `json:"-"`
	// APIKey is the request credential; ModelRuntime and direct StreamSimple construct the selected API provider with it. Bedrock also accepts it directly as a bearer token.
	APIKey string
	// CacheRetention requests a provider-supported prompt-cache lifetime. Empty leaves the option unset.
	CacheRetention CacheRetention
	// SessionID is passed to the provider for prompt caching (OpenAI prompt_cache_key). When non-empty and the provider supports it, repeated calls with the same session ID can reuse cached prompt processing. Mirrors upstream openai-completions.ts:449.
	SessionID string
	// ToolChoice selects provider-neutral automatic/no-tool behavior (a ToolChoice or its string) or a
	// provider-specific choice object. Anthropic and Bedrock also accept "any" and
	// {"type":"tool","name":...}.
	ToolChoice any
	// Debug asks a pi-messages backend for debug metadata such as routing response headers (pi-messages.ts PiMessagesOptions.debug).
	Debug bool `json:"debug,omitempty"`
	// Metadata contains optional provider request metadata. Providers extract
	// recognized fields and ignore the rest.
	Metadata map[string]any
	// AzureAPIVersion, AzureResourceName, AzureBaseURL and AzureDeploymentName are the per-request Azure endpoint options (AzureEndpointOptions, api/azure-openai-config.ts:7). Each outranks the matching AZURE_OPENAI_* variable.
	AzureAPIVersion     string `json:"azureApiVersion,omitempty"`
	AzureResourceName   string `json:"azureResourceName,omitempty"`
	AzureBaseURL        string `json:"azureBaseUrl,omitempty"`
	AzureDeploymentName string `json:"azureDeploymentName,omitempty"`
	// Transport requests a provider-specific streaming transport.
	// Empty lets the provider choose its default.
	Transport Transport
	// TimeoutMs bounds the full Mistral request through SSE consumption, and response headers or stream idleness for other supported providers. Nil selects the provider default; zero remains explicit and follows the selected provider's timeout semantics.
	TimeoutMs *int `json:"timeoutMs,omitempty"`
	// WebSocketConnectTimeoutMs bounds only the opening handshake. Nil selects the provider default; zero explicitly disables it.
	WebSocketConnectTimeoutMs *int `json:"websocketConnectTimeoutMs,omitempty"`
	MaxRetries                *int `json:"maxRetries,omitempty"`
	MaxRetryDelayMs           *int `json:"maxRetryDelayMs,omitempty"`
	// OnPayload is an optional payload-inspection hook invoked after the provider builds its native payload and before it serializes and sends the request. Returning nil
	// keeps the payload unchanged; returning a replacement asks providers that
	// support replacement to send that value instead. Mirrors upstream
	// StreamOptions.onPayload.
	OnPayload func(payload any, model *Model) (any, error)
	// OnResponse is awaited before response consumption. The request context owns cancellation.
	OnResponse func(context.Context, ProviderResponse, *Model) error
	// OnProviderStreamEvent observes each parsed provider stream event before normalization. Event data is adapter-owned and read-only; an adapter without explicit support never calls it. Mirrors upstream StreamOptions.onProviderStreamEvent (packages/ai/src/types.ts:198).
	OnProviderStreamEvent func(ctx context.Context, data any, model *Model) error
}

// Provider is the interface implemented by each LLM backend.
type Provider interface {
	// ID returns the provider identifier (e.g. "openai", "github-copilot").
	ID() string
	// Stream initiates a request from an already normalized transcript.
	Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error)
	// Close releases any persistent connections or background resources.
	io.Closer
}

// ─── Model ────────────────────────────────────────────────────────────────────

// ModelCapabilities describes what a model supports.
type ModelCapabilities struct {
	MaxThinking     ThinkingLevel
	SupportsImages  bool
	SupportsToolUse bool
	ContextWindow   int
	MaxOutputTokens int
	InputCostPer1M  float64 // USD per 1M input tokens
	OutputCostPer1M float64 // USD per 1M output tokens
	// CacheReadCostPer1M / CacheWriteCostPer1M are the separate cache rates
	// upstream models expose; the write rate is for 5m (short) writes. 1h
	// writes bill at 2x base input (see CalculateCost).
	CacheReadCostPer1M  float64 // USD per 1M cache-read tokens
	CacheWriteCostPer1M float64 // USD per 1M cache-write tokens
	// CostTiers overrides the base rates for high-volume requests: the highest
	// matching InputTokensAbove threshold applies to the whole request.
	// Mirrors upstream ModelCost.tiers.
	CostTiers []CostTier
}

// ModelPromptCache gives the best-effort prompt-cache lifetime in seconds for each retention tier.
type ModelPromptCache map[string]int

// ModelImageResizeOptions is the cache-safe resize profile applied before a new
// image enters conversation history. Upstream values are integers of at least
// one, so zero means the field is omitted.
type ModelImageResizeOptions struct {
	MaxWidth  int `json:"maxWidth,omitempty"`
	MaxHeight int `json:"maxHeight,omitempty"`
	// MaxBytes limits the base64-encoded image payload.
	MaxBytes    int `json:"maxBytes,omitempty"`
	JPEGQuality int `json:"jpegQuality,omitempty"`
}

// ModelImageInputLimits describes provider image limits. Zero means omitted.
type ModelImageInputLimits struct {
	Resize *ModelImageResizeOptions `json:"resize,omitempty"`
	// MaxPerMessage is the maximum number of images in one provider message.
	MaxPerMessage int `json:"maxPerMessage,omitempty"`
	// MaxPerRequest is the maximum number of images across one provider request.
	MaxPerRequest int `json:"maxPerRequest,omitempty"`
}

// ModelInputLimits carries provider input limits and cache-safe preprocessing
// metadata. Zero numeric fields and nil objects are omitted fields.
type ModelInputLimits struct {
	// MaxRequestBytes is the maximum serialized provider request size.
	MaxRequestBytes int                    `json:"maxRequestBytes,omitempty"`
	Images          *ModelImageInputLimits `json:"images,omitempty"`
}

// Clone returns a deep copy so catalog, registry, and extension projections
// never alias mutable limit metadata.
func (l *ModelInputLimits) Clone() *ModelInputLimits {
	if l == nil {
		return nil
	}
	out := &ModelInputLimits{MaxRequestBytes: l.MaxRequestBytes}
	if l.Images != nil {
		images := *l.Images
		if l.Images.Resize != nil {
			resize := *l.Images.Resize
			images.Resize = &resize
		}
		out.Images = &images
	}
	return out
}

// ModelCost is the provider price per million tokens for one model.
type ModelCost struct {
	Input      float64    `json:"input"`
	Output     float64    `json:"output"`
	CacheRead  float64    `json:"cacheRead"`
	CacheWrite float64    `json:"cacheWrite"`
	Tiers      []CostTier `json:"tiers,omitempty"`
}

// AnthropicAllowedFallbackModel describes a server-side fallback accepted by Anthropic.
type AnthropicAllowedFallbackModel struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Cost     ModelCost `json:"cost"`
}

// ModelCatalogProvider preserves provider-routing metadata carried by generated catalogs.
type ModelCatalogProvider struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Credential string `json:"credential"`
	Source     string `json:"source"`
}

// CostTier overrides the base per-1M rates for requests whose total input
// usage (input+cacheRead+cacheWrite) exceeds InputTokensAbove. Mirrors
// upstream ModelCostTier.
type CostTier struct {
	InputTokensAbove    int     `json:"inputTokensAbove"`
	InputCostPer1M      float64 `json:"input"`
	OutputCostPer1M     float64 `json:"output"`
	CacheReadCostPer1M  float64 `json:"cacheRead"`
	CacheWriteCostPer1M float64 `json:"cacheWrite"`
}

// ProviderMetadata carries generated/registry metadata that provider adapters
// use for compat decisions beyond the live Provider implementation.
type ProviderMetadata struct {
	ProviderID string
	API        API
	BaseURL    string
	Headers    map[string]string
	Compat     *ModelCompat
	Reasoning  bool
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func cloneThinkingLevelMap(in ThinkingLevelMap) ThinkingLevelMap {
	if len(in) == 0 {
		return nil
	}
	out := make(ThinkingLevelMap, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		value := *v
		out[k] = &value
	}
	return out
}

func ptrString(v string) *string { return new(v) }

func cloneCompat(in *ModelCompat) *ModelCompat {
	if in == nil {
		return nil
	}
	if out, ok := cloneScalarCompat(in); ok {
		return out
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out ModelCompat
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return &out
}

// Model pairs an identifier with a provider and capabilities.
type Model struct {
	// Type is optional: chat is the default model type, so models without it are chat models.
	Type             ModelType
	ID               string
	DisplayName      string
	Provider         Provider
	Capabilities     ModelCapabilities
	ProviderMeta     ProviderMetadata
	Input            []string
	ThinkingLevelMap ThinkingLevelMap
	SamplingParams   map[string]any
	// SamplingParamsByThinkingLevel overrides SamplingParams for the effective thinking level on OpenAI-compatible APIs.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel
	PromptCache                   ModelPromptCache
	// InputLimits mirrors upstream Model.inputLimits.
	InputLimits *ModelInputLimits
	// catalog is the persisted record this model was decoded from; it keeps the fields the model has no member for.
	catalog *catalogShape
}

// AnthropicEffort is the Anthropic output_config effort level (anthropic-messages.ts:186).
type AnthropicEffort string

const (
	AnthropicEffortLow    AnthropicEffort = "low"
	AnthropicEffortMedium AnthropicEffort = "medium"
	AnthropicEffortHigh   AnthropicEffort = "high"
	AnthropicEffortXHigh  AnthropicEffort = "xhigh"
	AnthropicEffortMax    AnthropicEffort = "max"
)

// ThinkingTokenBudgetField is the top-level request field that caps reasoning tokens on OpenAI-compatible servers.
//
// upstream: packages/ai/src/types.ts:101
type ThinkingTokenBudgetField string

// The request fields an OpenAI-compatible server reads a reasoning-token cap from.
const (
	ThinkingTokenBudgetFieldThinkingTokenBudget  ThinkingTokenBudgetField = "thinking_token_budget"
	ThinkingTokenBudgetFieldThinkingBudget       ThinkingTokenBudgetField = "thinking_budget"
	ThinkingTokenBudgetFieldThinkingBudgetTokens ThinkingTokenBudgetField = "thinking_budget_tokens"
)

// GrammarVariants maps a grammar format to its definition.
//
// upstream: packages/ai/src/types.ts:698
type GrammarVariants = map[GrammarFormat]string

// AnthropicThinkingDisplay selects whether thinking blocks carry summarized text or an empty field (anthropic-messages.ts:188).
type AnthropicThinkingDisplay string

const (
	AnthropicThinkingDisplaySummarized AnthropicThinkingDisplay = "summarized"
	AnthropicThinkingDisplayOmitted    AnthropicThinkingDisplay = "omitted"
)

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
	"slices"
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
}

func (TextContent) contentType() string { return "text" }

func (content TextContent) MarshalJSON() ([]byte, error) {
	type payload TextContent
	return marshalContent(content.contentType(), payload(content))
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

// JSONObject is a provider-facing JSON object.
type JsonObject map[string]any

// ToolCall is a tool invocation block in an assistant message.
type ToolCall struct {
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
	type payload ToolCall
	return marshalContent(content.contentType(), payload(content))
}

func marshalContent(contentType string, content any) ([]byte, error) {
	body, err := json.Marshal(content)
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

// ThinkingContent is a thinking/reasoning block.
type ThinkingContent struct {
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`
}

func (ThinkingContent) contentType() string { return "thinking" }

func (content ThinkingContent) MarshalJSON() ([]byte, error) {
	type payload ThinkingContent
	return marshalContent(content.contentType(), payload(content))
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

// ToolSchema describes an LLM-callable tool in JSON Schema format.
type ToolSchema struct {
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Parameters       map[string]any `json:"parameters"`                 // JSON Schema object
	PromptGuidelines []string       `json:"promptGuidelines,omitempty"` // Guidelines injected into system prompt
	// ConstrainedSampling is the optional provider-side constrained sampling
	// request for this tool (nil = unset, equivalent to upstream's false).
	// Mirrors upstream Tool.constrainedSampling (types.ts).
	ConstrainedSampling *ConstrainedSamplingConfig `json:"constrainedSampling,omitempty"`
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
	Type     string                   `json:"type"`               // "json_schema" | "grammar"
	Strict   string                   `json:"strict,omitempty"`   // json_schema: "prefer" | "require"
	Variants map[GrammarFormat]string `json:"variants,omitempty"` // grammar: format → definition
}

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
	Provider    string `json:"provider"`
	ModelID     string `json:"modelId"`
	API         API    `json:"api"`
	ID          string `json:"id"`
	ExpiresAt   *int64 `json:"expiresAt,omitempty"`
	PollAfterMS *int64 `json:"pollAfterMs,omitempty"`
	Data        any    `json:"data,omitempty"`
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

type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingNone    ThinkingLevel = ThinkingOff // backward-compatible alias
	ThinkingMinimal ThinkingLevel = "minimal"   // lightest reasoning: mirrors upstream "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	// ThinkingXHigh is supported only by models that explicitly declare
	// MaxThinking = ThinkingXHigh in their Capabilities.
	// Mirrors upstream THINKING_LEVELS_WITH_XHIGH (agent-session.ts:232).
	ThinkingXHigh ThinkingLevel = "xhigh"
	// ThinkingMax is supported only by models that explicitly map "max" in
	// their thinkingLevelMap (e.g. Claude Opus 4.6 adaptive thinking). Like
	// xhigh it clamps to "high" for budget-based and non-supporting providers.
	// Mirrors upstream ThinkingLevel "max" (ai/types.ts:79).
	ThinkingMax ThinkingLevel = "max"
)

// thinkingLevelOrder maps each ThinkingLevel to its ordinal position
// in the canonical order (off < minimal < low < medium < high < xhigh).
// String comparison is NOT equivalent because "off" > "high" lexicographically.
var thinkingLevelOrder = map[ThinkingLevel]int{
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
func CompareThinkingLevels(a, b ThinkingLevel) int {
	oa, ob := thinkingLevelOrder[a], thinkingLevelOrder[b]
	if oa < ob {
		return -1
	}
	if oa > ob {
		return 1
	}
	return 0
}

type ModelThinkingLevel = ThinkingLevel
type ThinkingLevelMap = map[ModelThinkingLevel]*string

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

// StreamOptions are the options for a streaming LLM call.
type StreamOptions struct {
	MaxTokens   int
	Temperature float64
	// TemperatureSet distinguishes an explicit zero temperature from omission.
	// Non-zero Temperature values remain present for existing callers.
	TemperatureSet  bool
	SamplingParams  map[string]any
	ThinkingBudgets *ThinkingBudgets
	Thinking        ThinkingLevel
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
	// APIKey is a request-specific credential, upstream's options.apiKey. When
	// set, ModelRuntime builds the provider with it instead of the configured
	// credential and does not require configured auth. Providers never read it.
	APIKey string
	// CacheRetention requests a provider-supported prompt-cache lifetime. Empty leaves the option unset.
	CacheRetention CacheRetention
	// SessionID is passed to the provider for prompt caching (OpenAI prompt_cache_key). When non-empty and the provider supports it, repeated calls with the same session ID can reuse cached prompt processing. Mirrors upstream openai-completions.ts:449.
	SessionID string
	// ToolChoice selects provider-neutral automatic/no-tool behavior or a
	// provider-specific choice object. Anthropic also accepts "any" and
	// {"type":"tool","name":...}.
	ToolChoice any
	// Metadata contains optional provider request metadata. Providers extract
	// recognized fields and ignore the rest.
	Metadata map[string]any
	// Transport requests a provider-specific streaming transport.
	// Empty lets the provider choose its default.
	Transport Transport
	// TimeoutMs bounds response headers and WebSocket stream idleness for
	// providers that support request timeouts. Zero leaves the provider default.
	TimeoutMs int
	// WebSocketConnectTimeoutMs bounds only the WebSocket opening handshake.
	// Zero selects the provider default.
	WebSocketConnectTimeoutMs int
	// OnPayload is an optional parity/debug hook invoked after the provider
	// builds its wire payload and before it sends the request. Returning nil
	// keeps the payload unchanged; returning a replacement asks providers that
	// support replacement to send that value instead. Mirrors upstream
	// StreamOptions.onPayload.
	OnPayload func(payload any, model *Model) (any, error)
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
	ID               string
	DisplayName      string
	Provider         Provider
	Capabilities     ModelCapabilities
	ProviderMeta     ProviderMetadata
	Input            []string
	ThinkingLevelMap ThinkingLevelMap
	SamplingParams   map[string]any
	PromptCache      ModelPromptCache
	// InputLimits mirrors upstream Model.inputLimits.
	InputLimits *ModelInputLimits
}

package ai

// Mirrors @mariozechner/pi-ai from pi v0.69.0.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

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
	return marshalContent(content.contentType(), content.wire())
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (sdkjson.StringifyAppender).
func (content TextContent) AppendStringify(dst []byte) ([]byte, error) {
	return appendContent(dst, content.contentType(), content.wire())
}

// textContentWire is the object TextContent writes after its type member.
type textContentWire struct {
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
	Index         *int   `json:"index,omitempty"`
}

func (content TextContent) wire() textContentWire {
	return textContentWire{content.Text, content.TextSignature, content.scratch.wire()}
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

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (sdkjson.StringifyAppender).
func (content ImageContent) AppendStringify(dst []byte) ([]byte, error) {
	type payload ImageContent
	return appendContent(dst, content.contentType(), payload(content))
}

// JsonValue is any JSON value: null, a boolean, a number, a string, an array or a JsonObject (types.ts:458).
type JsonValue = any

// JsonObject is a provider-facing JSON object (types.ts:459).
type JsonObject map[string]JsonValue

// ToolCall is a tool invocation block in an assistant message. Streaming OpenAI calls also serialize provider scratch fields until finalization or failure. Arguments is read and edited as a Go map; the member order the model sent is kept beside it and is written by ArgumentsJSON and MarshalJSON.
type ToolCall struct {
	scratch       toolCallScratch
	argumentOrder schemaObjectOrder
	// nonObjectArguments is the JSON text of arguments that are valid JSON but not an object ([], null, 5, "s"). Pi's parseStreamingJson returns such a value as it is, so the validator reports it and the session stores it; Arguments is nil while it is set.
	// deferredArguments is the argument text of a streamed call whose parse waits for a reader (deferArguments); empty once Arguments holds its parse. Only a stream builder's own partial and the cells it publishes to hold it.
	nonObjectArguments json.RawMessage
	deferredArguments  string
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Arguments          JsonObject `json:"arguments"`
	ThoughtSignature   string     `json:"thoughtSignature,omitempty"`
	Namespace          string     `json:"namespace,omitempty"`
	// ArgumentsError is set when the model's complete arguments text was not a valid JSON object. Arguments is then empty; the text describes the problem and includes what the model sent, and tool argument validation fails with it (Pi 87f9a364).
	ArgumentsError string `json:"argumentsError,omitempty"`
}

func (ToolCall) contentType() string { return "toolCall" }

func (content ToolCall) MarshalJSON() ([]byte, error) {
	wire, err := content.wire(false)
	if err != nil {
		return nil, err
	}
	return marshalContent(content.contentType(), wire)
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (sdkjson.StringifyAppender): arguments in their recorded
// member order are appended in place rather than encoded apart.
func (content ToolCall) AppendStringify(dst []byte) ([]byte, error) {
	wire, err := content.wire(true)
	if err != nil {
		return dst, err
	}
	return appendContent(dst, content.contentType(), wire)
}

// toolCallWire is the object ToolCall writes after its type member.
type toolCallWire struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Arguments        any    `json:"arguments"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
	Namespace        string `json:"namespace,omitempty"`
	ArgumentsError   string `json:"argumentsError,omitempty"`
	toolCallScratchJSON
}

// wire returns the members after type. Arguments with a recorded member order are their encoded text, or with stringify an
// orderedArguments that the JSON.stringify mode appends in place. Arguments whose ArgumentsJSON bytes are kept are those bytes.
func (content ToolCall) wire(stringify bool) (toolCallWire, error) {
	if content.Arguments != nil && (stringify || content.argumentOrder != nil) {
		if encoded := argumentEncodings.find(content.Arguments, content.argumentOrder); encoded != nil {
			var arguments any = json.RawMessage(encoded)
			if stringify {
				arguments = encodedArguments(encoded)
			}
			return toolCallWire{content.ID, content.Name, arguments, content.ThoughtSignature, content.Namespace, content.ArgumentsError, content.scratch.wire()}, nil
		}
	}
	if err := validateJsonValue(content.Arguments); err != nil {
		return toolCallWire{}, fmt.Errorf("tool call arguments: %w", err)
	}
	var arguments any = content.Arguments
	if content.Arguments == nil && content.nonObjectArguments != nil {
		arguments = content.nonObjectArguments
	} else if content.argumentOrder != nil && content.Arguments != nil {
		if stringify {
			arguments = orderedArguments{content.Arguments, content.argumentOrder}
		} else {
			ordered, err := content.ArgumentsJSON()
			if err != nil {
				return toolCallWire{}, fmt.Errorf("tool call arguments: %w", err)
			}
			arguments = json.RawMessage(ordered)
		}
	}
	return toolCallWire{content.ID, content.Name, arguments, content.ThoughtSignature, content.Namespace, content.ArgumentsError, content.scratch.wire()}, nil
}

// orderedArguments is a validated arguments object written with its members in their recorded order (ToolCall.ArgumentsJSON).
type orderedArguments struct {
	values JsonObject
	order  schemaObjectOrder
}

func (arguments orderedArguments) MarshalJSON() ([]byte, error) {
	return marshalSchemaWithOrder(map[string]any(arguments.values), arguments.order, "")
}

// AppendStringify appends MarshalJSON's output, which is already JSON.stringify text, to dst.
func (arguments orderedArguments) AppendStringify(dst []byte) ([]byte, error) {
	out, err := appendSchemaWithOrder(dst, map[string]any(arguments.values), arguments.order, "", nil)
	if err != nil {
		return dst, err
	}
	return out, nil
}

// UnmarshalJSON decodes the call and keeps the member order of its arguments.
func (content *ToolCall) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	type payload ToolCall
	var members struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	// Arguments that are valid JSON but not an object decode as the empty object and are kept as text beside it.
	nonObject := compactNonObjectArguments(members.Arguments)
	decodable := data
	if nonObject != nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		fields["arguments"] = json.RawMessage("{}")
		var err error
		//portlint:allow mapkeyorder re-encoded only for the typed decode below; arguments become {} and are kept as text, and the other members decode into struct fields, so no order survives
		if decodable, err = json.Marshal(fields); err != nil {
			return err
		}
	}
	var decoded payload
	if err := sdkjson.Unmarshal(decodable, &decoded); err != nil {
		return err
	}
	if nonObject != nil {
		decoded.Arguments, decoded.nonObjectArguments = nil, nonObject
	} else if len(members.Arguments) > 0 {
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
	encoded := make([]byte, 0, len(`{"type":,`)+len(typeField)+len(body))
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

// appendContent appends the JSON.stringify text of marshalContent(contentType, content) to dst: content is encoded in place after the type
// member, its opening brace overwritten.
func appendContent(dst []byte, contentType string, content any) ([]byte, error) {
	start := len(dst)
	dst = append(dst, `{"type":`...)
	dst = jsparity.AppendString(dst, contentType)
	body := len(dst)
	dst, err := sdkjson.AppendStringify(dst, content)
	if err != nil {
		return dst[:start], err
	}
	if len(dst)-body > 2 {
		dst[body] = ','
	} else {
		dst = append(dst[:body], '}')
	}
	return dst, nil
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
	return marshalContent(content.contentType(), content.wire())
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (sdkjson.StringifyAppender).
func (content ThinkingContent) AppendStringify(dst []byte) ([]byte, error) {
	return appendContent(dst, content.contentType(), content.wire())
}

// thinkingContentWire is the object ThinkingContent writes after its type member.
type thinkingContentWire struct {
	Thinking          string  `json:"thinking"`
	ThinkingSignature *string `json:"thinkingSignature,omitempty"`
	Redacted          bool    `json:"redacted,omitempty"`
	Index             *int    `json:"index,omitempty"`
}

func (content ThinkingContent) wire() thinkingContentWire {
	var signature *string
	if content.ThinkingSignature != "" || content.thinkingSignatureEmpty {
		signature = &content.ThinkingSignature
	}
	return thinkingContentWire{content.Thinking, signature, content.Redacted, content.scratch.wire()}
}

func (content *ThinkingContent) UnmarshalJSON(data []byte) error {
	type payload ThinkingContent
	var decoded struct {
		payload
		ThinkingSignature *string `json:"thinkingSignature"`
	}
	if err := sdkjson.Unmarshal(data, &decoded); err != nil {
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
	// A JavaScript object enumerates its integer-like keys first, so JSON.stringify writes them first.
	s = slices.Clone(s)
	jsstring.SortOwnKeys(s, func(section PromptSection) string { return section.Name })
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
	// JSON.parse yields an object whose integer-like keys enumerate first.
	jsstring.SortOwnKeys(sections, func(section PromptSection) string { return section.Name })
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
	// splitsAfterCost records that a provider created CacheWrite1h or Reasoning after the other members (Usage.MarshalJSON).
	splitsAfterCost bool
}

// UsageCost is the cost breakdown carried by upstream Usage. The component costs are catalog estimates; Total is the best available total.
type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
	// TotalFromProvider is true when Total is the billed cost the provider reported and false when it is a catalog estimate. Nil is the
	// absent member of a usage recorded before the field existed, which also means false (types.ts Usage.cost.totalFromProvider).
	TotalFromProvider *bool `json:"totalFromProvider,omitempty"`
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
	// detailsOrder is JSON text whose object members, at every depth, are in the order Pi's object literal or the parsed wire value gave Details. Nil sorts them.
	detailsOrder []byte
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
	// Each value is its own element of one array, so the values cost one allocation rather than one per header.
	slab := make([]string, 0, len(values))
	for name, value := range values {
		slab = append(slab, value)
		headers[name] = &slab[len(slab)-1]
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
	// Telemetry is the explicit parent span for telemetry produced by this logical request; nil is Pi's omitted option, which uses the no-op span.
	Telemetry telemetry.TelemetrySpan
	// Deferred preserves the deferred-generation boolean or window object.
	Deferred *DeferredOption
	// Fetch replaces HTTP execution with a caller-owned client, preserving its redirect policy and request context.
	Fetch *http.Client `json:"-"`
	// AnthropicClient replaces the Anthropic Messages provider's client construction entirely: the provider sends its request through it and skips federation, OAuth and API-key header assembly, as Pi's `AnthropicOptions.client` does (anthropic-messages.ts:286-290, 607-609). Other providers ignore it.
	AnthropicClient AnthropicClient `json:"-"`
	MaxTokens       int
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
	// RequestMetadataOrder is the insertion order of RequestMetadata's keys, which Bedrock's requestMetadata object keeps on the wire.
	// Keys missing from it follow in sorted order. Callers that decode the metadata from JSON fill it.
	RequestMetadataOrder []string `json:"-"`
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
	// rawThinking makes the Bedrock provider send the thinking budget and output cap it is given: the API module's stream (bedrock-converse-stream.ts stream) does no budget lowering, only streamSimple does.
	rawThinking bool
	// IsReasoning indicates whether the model supports extended reasoning.
	// Used by providers to decide developer vs system role and reasoning_effort gating.
	// Mirrors upstream model.reasoning field.
	IsReasoning bool
	// ModelCost is the requested model's price. Providers price Usage.Cost
	// with it where upstream providers call calculateCost(model, usage).
	ModelCost ModelCost
	// RepairToolFlow makes a real provider repair the tool results of its request before transformMessages: an out-of-order result moves to its call and an orphaned or repeated result is dropped. The agent loop sets it for a model request; a direct provider call keeps Pi's request.
	// pig divergence (D48): upstream sends such results and the provider rejects the request.
	RepairToolFlow bool `json:"-"`
	// Env contains request-specific provider environment values. Runtime-resolved
	// values are merged first; request values win.
	Env ProviderEnv
	// TransformHeaders runs once after configured, auth, and request headers are
	// merged. ModelRuntime consumes it and does not forward it to providers.
	TransformHeaders func(context.Context, ProviderHeaders) (ProviderHeaders, error)
	// Headers are request-specific HTTP headers. A nil value deletes a header
	// supplied by configured or resolved authentication state.
	Headers ProviderHeaders
	// Identity is how the request identifies the calling app: `User-Agent` and provider attribution headers. Absent fields use the defaults: a detailed `User-Agent` and no attribution (ProviderRequestOptions.identity).
	Identity *ClientIdentity `json:"identity,omitempty"`
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
	// ProviderStreamEventWanted reports, before an adapter parses a wire event for OnProviderStreamEvent, whether the observer would act on it now. When it reports false, the adapter skips parsing and calling OnProviderStreamEvent for that event; the caller promises that such a call would have returned nil and had no other effect. A wire event that would fail to parse is still parsed, so its error ends the request as before. Nil means the observer always acts.
	ProviderStreamEventWanted func() bool
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
	// Mirrors upstream ModelCost.tiers, presence included: nil is no tiers member, empty is tiers: [] (see ModelCost).
	CostTiers []CostTier
}

// ModelPromptCache gives the best-effort prompt-cache lifetime in seconds for each retention tier. Pi's ModelPromptCacheSchema types a lifetime as Type.Number({ exclusiveMinimum: 0 }), so it need not be whole.
type ModelPromptCache map[string]float64

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
	// The copy and its nested objects share one allocation; each is still its own object.
	switch {
	case l.Images == nil:
		return &ModelInputLimits{MaxRequestBytes: l.MaxRequestBytes}
	case l.Images.Resize == nil:
		block := &struct {
			limits ModelInputLimits
			images ModelImageInputLimits
		}{ModelInputLimits{MaxRequestBytes: l.MaxRequestBytes}, *l.Images}
		block.limits.Images = &block.images
		return &block.limits
	default:
		block := &struct {
			limits ModelInputLimits
			images ModelImageInputLimits
			resize ModelImageResizeOptions
		}{ModelInputLimits{MaxRequestBytes: l.MaxRequestBytes}, *l.Images, *l.Images.Resize}
		block.images.Resize = &block.resize
		block.limits.Images = &block.images
		return &block.limits
	}
}

// ModelCost is the provider price per million tokens for one model. Tiers is optional (types.ts ModelCost.tiers): a nil Tiers
// is an absent member and an empty one an authored [], which JSON keeps apart, so copies of a cost keep a nil Tiers nil and an
// empty one empty (slices.Clone, not append to nil).
type ModelCost struct {
	Input      float64    `json:"input"`
	Output     float64    `json:"output"`
	CacheRead  float64    `json:"cacheRead"`
	CacheWrite float64    `json:"cacheWrite"`
	Tiers      []CostTier `json:"tiers,omitzero"`
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
	InputTokensAbove    float64 `json:"inputTokensAbove"`
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
	// Each copied value is its own element of one array, so the copy costs one allocation for its values rather than one per level.
	values := make([]string, 0, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		values = append(values, *v)
		out[k] = &values[len(values)-1]
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

// ModelInputModality is an input a model accepts (model-schema.ts ModelInputModality): "text" or "image". It is an alias of string, so the lists of
// catalog and models.json inputs stay []string.
type ModelInputModality = string

// The modalities of ModelInputModality.
const (
	ModelInputText  ModelInputModality = "text"
	ModelInputImage ModelInputModality = "image"
)

// Model pairs an identifier with a provider and capabilities.
type Model struct {
	// Type is optional: chat is the default model type, so models without it are chat models.
	Type             ModelType
	ID               string
	DisplayName      string
	Provider         Provider
	Capabilities     ModelCapabilities
	ProviderMeta     ProviderMetadata
	Input            []ModelInputModality
	ThinkingLevelMap ThinkingLevelMap
	SamplingParams   map[string]any
	// SamplingParamsByThinkingLevel overrides SamplingParams for the effective thinking level on OpenAI-compatible APIs.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel
	PromptCache                   ModelPromptCache
	// InputLimits mirrors upstream Model.inputLimits.
	InputLimits *ModelInputLimits
	// catalog is the persisted record this model was decoded from; it keeps the fields the model has no member for.
	catalog *catalogShape
	// catalogRecord is the built-in catalog entry this model was made from (GeneratedModel.Record). Its shape is built
	// when the model is encoded, so listing the catalog does not parse every entry.
	catalogRecord string
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

package ai

// Ports packages/ai/src/api/openai-completions.ts.
// Covers OpenAI, OpenRouter, Groq, Cerebras, Fireworks, xAI, Ollama, vLLM, llama.cpp,
// and any other OpenAI-compatible endpoint.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
	"github.com/MichaelKinsy/PiG/internal/jsonopts"
	"github.com/MichaelKinsy/PiG/internal/jsonwire"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// OpenAIConfig configures an OpenAI-compatible provider.
type OpenAIConfig struct {
	// BaseURL is the API base (default: https://api.openai.com/v1).
	BaseURL string
	// APIKey is the bearer token. May be empty for local endpoints.
	APIKey string
	// Model is the model name sent in the request.
	Model string
	// ModelMetadata supplies the selected model's capabilities and thinking map without falling back to a same-named catalog entry.
	ModelMetadata *Model
	// ThinkingLevelMap supplies an explicit map when selected ModelMetadata is absent.
	ThinkingLevelMap ThinkingLevelMap
	// ProviderID is the provider label (e.g. "openai", "openrouter", "ollama").
	ProviderID string
	// ExtraHeaders are added to every request.
	ExtraHeaders   map[string]string
	SamplingParams map[string]any
	// SamplingParamsByThinkingLevel overrides SamplingParams for the effective thinking level.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel
	// SamplingParamsOrder and SamplingParamsByThinkingLevelOrder are Model.SamplingParamsOrder and Model.SamplingParamsByThinkingLevelOrder.
	SamplingParamsOrder                []string
	SamplingParamsByThinkingLevelOrder map[ModelThinkingLevel][]string
	// Env holds provider-scoped environment overrides that take precedence over
	// the process environment when resolving provider configuration such as
	// PI_CACHE_RETENTION and Cloudflare base-URL placeholders. Mirrors the
	// upstream StreamOptions.env threaded into the provider.
	Env ProviderEnv

	// Compat controls behavior for non-standard OpenAI-compatible endpoints.
	// Mirrors upstream OpenAICompletionsCompat.
	Compat *OpenAICompat

	// GetAPIKey is an optional callback for dynamically obtaining the bearer
	// token (e.g. refreshing an OAuth access token). When set, takes precedence
	// over APIKey. Called once per Stream() invocation.
	GetAPIKey func(ctx context.Context) (string, error)
	// GetBaseURL is an optional callback for dynamically resolving the base URL
	// (e.g. extracting proxy-ep from a refreshed Copilot token). When set,
	// takes precedence over BaseURL. Called once per Stream() invocation.
	GetBaseURL func(ctx context.Context) (string, error)
	// DynamicHeaders, when set, is called per-request with the StreamOptions
	// and returns headers to add to that request (e.g. Copilot's X-Initiator).
	DynamicHeaders func(transcript TranscriptContext, opts StreamOptions) map[string]string
	// pig additive (D36): opt in to TLS without certificate verification.
	Insecure bool

	// azure resolves the Azure endpoint and deployment per request (NewAzureCompletionsProvider).
	azure *azureCompletionsEndpoint
	// requestModel is the model sent on the wire (an Azure deployment name) without changing the logical model.
	requestModel string
}

// OpenAICompat controls behavior for non-standard OpenAI-compatible endpoints
// (Ollama, vLLM, LiteLLM, MLX, etc.).
// Mirrors upstream OpenAICompletionsCompat (types.ts).
type OpenAICompat struct {
	SupportsDeveloperRole                       *bool  `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort                     *bool  `json:"supportsReasoningEffort,omitempty"`
	SupportsStore                               *bool  `json:"supportsStore,omitempty"`
	SupportsUsageInStreaming                    *bool  `json:"supportsUsageInStreaming,omitempty"`
	SupportsFinishReason                        *bool  `json:"supportsFinishReason,omitempty"`
	MaxTokensField                              string `json:"maxTokensField,omitempty"`
	RequiresToolResultName                      *bool  `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult            *bool  `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresNonNullAssistantContent             *bool  `json:"requiresNonNullAssistantContent,omitempty"`
	RequiresToolsWithToolHistory                *bool  `json:"requiresToolsWithToolHistory,omitempty"`
	RequiresThinkingAsText                      *bool  `json:"requiresThinkingAsText,omitempty"`
	RequiresReasoningContentOnAssistantMessages *bool  `json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	SupportsOpenAIGrammarTools                  *bool  `json:"supportsOpenAIGrammarTools,omitempty"`
	SupportsExplicitPromptCacheMode             *bool  `json:"supportsExplicitPromptCacheMode,omitempty"`
	SupportsStrictTools                         *bool  `json:"supportsStrictTools,omitempty"`
	// ThinkingFormat controls how reasoning/thinking content is sent to the provider.
	// Values: "openai" (default), "deepseek", "zai", "qwen", "openrouter".
	// - "openai": standard reasoning_effort parameter
	// - "deepseek": thinking: {type:"enabled"|"disabled"} plus reasoning_effort
	// - "zai": zai-specific thinking format
	// - "qwen": QwenThinkingType with content_type: "thinking"
	// - "openrouter": openrouter reasoning with include_reasoning: true
	ThinkingFormat string `json:"thinkingFormat,omitempty"`
	// CacheControlFormat selects cache markers on the first instruction's last text part, last tool definition, and last user/assistant/tool text part.
	// Values: "" (none), "anthropic".
	CacheControlFormat         string `json:"cacheControlFormat,omitempty"`
	SendSessionAffinityHeaders *bool  `json:"sendSessionAffinityHeaders,omitempty"`
	SupportsStrictMode         *bool  `json:"supportsStrictMode,omitempty"`
	// OpenRouterRouting contains OpenRouter-specific routing config.
	OpenRouterRouting  OpenRouterRouting                 `json:"openRouterRouting,omitempty"`
	ChatTemplateKwargs map[string]ChatTemplateKwargValue `json:"chatTemplateKwargs,omitempty"`
	VLLMPriority       *float64                          `json:"vllmPriority,omitempty"`
	// VercelGatewayRouting contains Vercel AI Gateway routing preferences.
	VercelGatewayRouting            *VercelGatewayRouting `json:"vercelGatewayRouting,omitempty"`
	SupportsLongCacheRetention      *bool                 `json:"supportsLongCacheRetention,omitempty"`
	SendSessionIdHeader             *bool                 `json:"sendSessionIdHeader,omitempty"`
	SupportsEagerToolInputStreaming *bool                 `json:"supportsEagerToolInputStreaming,omitempty"`
	SupportsCacheControlOnTools     *bool                 `json:"supportsCacheControlOnTools,omitempty"`
	// ForceAdaptiveThinking forces adaptive thinking (type: "adaptive"
	// + output_config.effort) regardless of model ID. Built-in models
	// set this in generated metadata. Custom Anthropic-compatible
	// providers can set it for any model requiring adaptive format.
	ForceAdaptiveThinking *bool `json:"forceAdaptiveThinking,omitempty"`
	// AllowEmptySignature preserves thinking blocks with empty signatures
	// instead of converting them to text. For Anthropic-compatible
	// providers that return empty thinking signatures.
	AllowEmptySignature *bool `json:"allowEmptySignature,omitempty"`
	// SupportsTemperature indicates whether the model accepts temperature.
	// Claude Opus 4.7+ rejects non-default temperature values.
	SupportsTemperature *bool `json:"supportsTemperature,omitempty"`
	ZaiToolStream       *bool `json:"zaiToolStream,omitempty"`
	// DeferredToolsMode selects provider-specific deferred-tool
	// serialization on OpenAI Completions. Only "kimi" is defined
	// (Moonshot Kimi). Empty = no deferred-tool handling.
	DeferredToolsMode string `json:"deferredToolsMode,omitempty"`
	// SessionAffinityFormat selects which session-affinity headers are sent
	// from options.SessionID. "openai" sends session_id + x-client-request-id
	// + x-session-affinity (Completions) / session-id + x-client-request-id
	// (Responses); "openai-nosession" omits the session header; "openrouter" sends
	// x-session-id. Empty = auto-detect (openrouter base URL => "openrouter",
	// else "openai"). Replaces the pre-0.81 sendSessionIdHeader flag.
	SessionAffinityFormat SessionAffinityFormat `json:"sessionAffinityFormat,omitempty"`
	// PromptCacheKeyMode controls the prompt_cache_key request field. Empty = auto (see PromptCacheKeyAuto).
	PromptCacheKeyMode PromptCacheKeyMode `json:"promptCacheKeyMode,omitempty"`
	// SupportsToolSearch indicates the model supports client-executed tool
	// search for deferred tools (OpenAI Responses). Default: false.
	SupportsToolSearch      *bool `json:"supportsToolSearch,omitempty"`
	SupportsMaxOutputTokens *bool `json:"supportsMaxOutputTokens,omitempty"`
	// SupportsToolReferences indicates the provider supports deferred tools
	// loaded by tool_reference blocks in tool results (Anthropic Messages).
	// Default: true for first-party Anthropic except Haiku / pre-Claude-4.5.
	// ChatTemplateArgs are provider-specific vLLM/Baseten chat-template values.
	ChatTemplateArgs            map[string]any `json:"chatTemplateArgs,omitempty"`
	SupportsThinkingTokenBudget *bool          `json:"supportsThinkingTokenBudget,omitempty"`
	// ThinkingTokenBudgetField selects the budget wire field and takes precedence over SupportsThinkingTokenBudget.
	ThinkingTokenBudgetField       ThinkingTokenBudgetField `json:"thinkingTokenBudgetField,omitempty"`
	SupportsAdditionalTools        *bool                    `json:"supportsAdditionalTools,omitempty"`
	SupportsMidConvoEffort         *bool                    `json:"supportsMidConvoEffort,omitempty"`
	SupportsMidConvoSystemMessages *bool                    `json:"supportsMidConvoSystemMessages,omitempty"`
	SupportsMidConvoToolAdditions  *bool                    `json:"supportsMidConvoToolAdditions,omitempty"`
	SupportsMidConvoToolChanges    *bool                    `json:"supportsMidConvoToolChanges,omitempty"`
	// SupportsMessageThreads reports whether the endpoint supports Anthropic message threads. Unset, it is true for a baseUrl on api.anthropic.com and false otherwise.
	SupportsMessageThreads *bool                           `json:"supportsMessageThreads,omitempty"`
	AllowedFallbackModels  []AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
	// Extra holds the members PiG does not model, as written. Pi's compat schemas allow additional properties and a compat object keeps
	// them (model-config.ts getProvider, provider-composer.ts mergeCompat), so a custom API can read its own compat settings.
	Extra map[string]json.RawMessage `json:"-"`
	// objectOrder is the member order of OpenRouterRouting, ChatTemplateKwargs and ChatTemplateArgs, nested objects included, where the JSON the
	// compat was decoded from or a merge of two compats lists them in another order than the sorted one encoding/json writes. A compat
	// built in Go has none.
	objectOrder schemaObjectOrder
}

func ptrBool(v bool) *bool { return new(v) }

type openAIProvider struct {
	cfg    OpenAIConfig
	client *http.Client

	// compatProviderID and compatBaseURL preserve the model's own provider ID
	// and base URL as given before NewOpenAIProvider applies HTTP defaults, so
	// detectOpenAICompletionsCompat mirrors Pi's detectCompat(model) (which
	// reads model.provider and model.baseUrl directly). When both ProviderID
	// and BaseURL are omitted, NewOpenAIProvider defaults both to OpenAI direct
	// (matching prompt_cache_key on the defaulted BaseURL). When constructed
	// via a struct literal, compatIdentitySet is false and compat() reads
	// p.cfg.ProviderID and p.cfg.BaseURL.
	compatIdentitySet bool
	compatProviderID  string
	compatBaseURL     string

	compatOnce     sync.Once
	resolvedCompat *OpenAICompat
}

// compat is the model's resolved compat settings (openai-completions.ts getCompat): detected from the provider, base URL and model id, then
// overlaid with the model's own. It is never nil.
func (p *openAIProvider) compat() *OpenAICompat {
	p.compatOnce.Do(func() {
		providerID, baseURL := p.cfg.ProviderID, p.cfg.BaseURL
		if p.compatIdentitySet {
			providerID, baseURL = p.compatProviderID, p.compatBaseURL
		}
		p.resolvedCompat = getOpenAICompletionsCompat(providerID, baseURL, p.cfg.Model, p.cfg.Compat)
	})
	return p.resolvedCompat
}

// NewOpenAIProvider creates a Provider backed by the OpenAI Completions API.
func NewOpenAIProvider(cfg OpenAIConfig) Provider {
	compatProviderID := cfg.ProviderID
	compatBaseURL := cfg.BaseURL
	cfg = withAzureEndpoint(cfg)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "openai"
	}
	if compatProviderID == "" && compatBaseURL == "" {
		compatProviderID = cfg.ProviderID
		compatBaseURL = cfg.BaseURL
	}
	return &openAIProvider{
		cfg:               cfg,
		client:            newStreamingHTTPClient(cfg.Insecure),
		compatIdentitySet: true,
		compatProviderID:  compatProviderID,
		compatBaseURL:     compatBaseURL,
	}
}

func (p *openAIProvider) ID() string { return p.cfg.ProviderID }

// thinkingModel resolves the selected model's thinking metadata: the selected ModelMetadata, else the catalog entry for the configured model, with an explicit ThinkingLevelMap taking precedence.
func (p *openAIProvider) thinkingModel() *Model {
	if p.cfg.ModelMetadata != nil {
		return p.cfg.ModelMetadata
	}
	model := &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}}
	if generated, ok := LookupModelExact(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	} else if p.cfg.ProviderID == "openrouter" {
		if generated, ok := LookupModel(p.cfg.Model); ok {
			model = generated.ToModel()
		} else if generated, ok := LookupModel(strings.TrimPrefix(p.cfg.Model, "openrouter/")); ok {
			model = generated.ToModel()
		}
	} else if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	} else if generated, ok := LookupModel(p.cfg.Model); ok {
		model = generated.ToModel()
	}
	if p.cfg.ThinkingLevelMap != nil {
		model.ThinkingLevelMap = cloneThinkingLevelMap(p.cfg.ThinkingLevelMap)
		model.Capabilities.MaxThinking = thinkingMaxLevel(true, model.ThinkingLevelMap)
	}
	return model
}

// samplingModel is the model view resolveSamplingParams reads: reasoning support and the thinking map clamp the level, and the configured defaults and per-level overrides are merged.
func (p *openAIProvider) samplingModel(reasoning bool) *Model {
	model := &Model{SamplingParams: p.cfg.SamplingParams, SamplingParamsByThinkingLevel: p.cfg.SamplingParamsByThinkingLevel, SamplingParamsOrder: p.cfg.SamplingParamsOrder, SamplingParamsByThinkingLevelOrder: p.cfg.SamplingParamsByThinkingLevelOrder}
	if reasoning {
		model.ProviderMeta.Reasoning = true
		model.ThinkingLevelMap = p.thinkingModel().ThinkingLevelMap
	}
	return model
}

// completionsSamplingLevel is the thinking level whose sampling overrides apply: the request effort, else the simple reasoning level, else off (openai-completions.ts buildParams).
func completionsSamplingLevel(opts StreamOptions) ModelThinkingLevel {
	switch {
	case opts.ReasoningEffort != "":
		return ModelThinkingLevel(opts.ReasoningEffort)
	case opts.Thinking != "":
		return ModelThinkingLevel(opts.Thinking)
	}
	return ThinkingOff
}

// systemPromptRole returns "developer" for reasoning models on supported endpoints,
// "system" otherwise. Mirrors upstream openai-completions.ts:683-684:
//
//	const useDeveloperRole = model.reasoning && compat.supportsDeveloperRole;
func (p *openAIProvider) systemPromptRole(isReasoning bool) string {
	if isReasoning && *p.compat().SupportsDeveloperRole {
		return "developer"
	}
	return "system"
}

// compatBool returns the compat flag value, or the default if the resolved compat does not set it.
func (p *openAIProvider) compatBool(getter func(*OpenAICompat) *bool, defaultVal bool) bool {
	if v := getter(p.compat()); v != nil {
		return *v
	}
	return defaultVal
}

// maxTokensField returns "max_completion_tokens" (OpenAI standard) or "max_tokens" (compat).
func (p *openAIProvider) maxTokensField() string {
	return p.compat().MaxTokensField
}

// cacheControlFormat returns the cache control format ("anthropic" or "").
func (p *openAIProvider) cacheControlFormat() string {
	return p.compat().CacheControlFormat
}

// toolAnchors says which of a conversation's system messages carry their own tool changes in place: those from index from on. The zero
// value anchors none. It is a value, not a function, so a request build allocates no closure for it.
type toolAnchors struct {
	enabled bool
	from    int
}

func (anchors toolAnchors) anchored(systemIndex int) bool {
	return anchors.enabled && systemIndex >= anchors.from
}

// convertMessagesWithCompat wraps convertMessages with compat-aware transformations:
// - requiresAssistantAfterToolResult: inserts assistant bridge messages
// - requiresToolResultName: adds name field to tool result messages
// - requiresThinkingAsText: converts thinking blocks to <thinking> text
// Mirrors upstream openai-completions.ts convertMessages (lines 680-870).
func (p *openAIProvider) convertMessagesWithCompat(msgs []Message, grammarProps map[string]string, instructionRole string, anchors toolAnchors, isReasoning bool, leadingSlots int) ([]oaiMessage, error) {
	requiresAssistant := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresAssistantAfterToolResult }, false)
	requiresNonNullContent := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresNonNullAssistantContent }, false)
	requiresName := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresToolResultName }, false)
	requiresThinkingAsText := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresThinkingAsText }, false)
	requiresReasoningContent := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresReasoningContentOnAssistantMessages }, false)
	out, err := convertCompletionsMessages(msgs, completionsConvertOptions{
		requiresToolResultName:           requiresName,
		reasoningContentOnAssistant:      requiresReasoningContent && isReasoning,
		supportsImages:                   p.modelSupportsImages(),
		grammarProps:                     grammarProps,
		thinkingAsText:                   requiresThinkingAsText,
		requiresAssistantAfterToolResult: requiresAssistant,
		requiresNonNullAssistantContent:  requiresNonNullContent,
		instructionRole:                  instructionRole,
		leadingSlots:                     leadingSlots,
		systemTools: func(systemIndex int, message SystemMessage) ([]oaiTool, error) {
			if !anchors.anchored(systemIndex) || len(message.ToolsAdded) == 0 {
				return nil, nil
			}
			return p.convertTools(message.ToolsAdded)
		},
	})
	if err != nil {
		return nil, err
	}
	if p.cfg.ProviderID == "opencode-go" {
		for index := range out {
			if out[index].Role == "assistant" && out[index].Reasoning != "" && out[index].ReasoningContent == nil {
				out[index].ReasoningContent = new(out[index].Reasoning)
				out[index].Reasoning = ""
			}
		}
	}

	return out, nil
}

func (p *openAIProvider) Close() error { return nil }

// ─── Request / response wire types ───────────────────────────────────────────

type oaiMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"` // string | []oaiContentPart
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"name,omitempty"` // for tool results when requiresToolResultName=true
	// The field a thinking block replays under is assigned before tool_calls (openai-completions.ts:1342, :1356).
	Reasoning        string               `json:"reasoning,omitempty"`
	ReasoningContent *string              `json:"reasoning_content,omitempty"`
	ReasoningText    string               `json:"reasoning_text,omitempty"`
	ToolCalls        []oaiRequestToolCall `json:"tool_calls,omitempty"`
	ReasoningDetails []json.RawMessage    `json:"reasoning_details,omitempty"`
	// Tools makes a tool-bearing system message that loads tools in place
	// (Kimi mid-conversation tool additions).
	Tools []oaiTool `json:"tools,omitempty"`
	// reasoningContentLast marks the empty reasoning_content of compat.requiresReasoningContentOnAssistantMessages, which Pi assigns
	// after tool_calls and reasoning_details (openai-completions.ts:1383-1387).
	reasoningContentLast bool
}

// MarshalJSON sends a tool-bearing system message as {role, tools} without
// content. Mirrors upstream KimiToolSystemMessageParam.
func (oaiMessage) carriesRequestStrings() {}

// encodesRequestStrings: no field of the completions request, nor of a message in it, writes a string in another form than its own.
func (oaiRequest) encodesRequestStrings() {}

func (message oaiMessage) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalKeepingSurrogates(message.wire())
}

func (message oaiMessage) wire() any {
	type wire oaiMessage
	switch {
	case message.Tools != nil:
		return struct {
			Role  string    `json:"role"`
			Tools []oaiTool `json:"tools"`
		}{message.Role, message.Tools}
	case message.reasoningContentLast:
		return struct {
			Role             string               `json:"role"`
			Content          any                  `json:"content"`
			Reasoning        string               `json:"reasoning,omitempty"`
			ReasoningText    string               `json:"reasoning_text,omitempty"`
			ToolCalls        []oaiRequestToolCall `json:"tool_calls,omitempty"`        //portlint:allow emptydrop Pi sets tool_calls only when the message has calls (openai-completions.ts:1355)
			ReasoningDetails []json.RawMessage    `json:"reasoning_details,omitempty"` //portlint:allow emptydrop Pi sets reasoning_details only when it kept some (openai-completions.ts:1378)
			ReasoningContent *string              `json:"reasoning_content,omitempty"`
		}{message.Role, message.Content, message.Reasoning, message.ReasoningText, message.ToolCalls, message.ReasoningDetails, message.ReasoningContent}
	}
	return wire(message)
}

// oaiCacheControl mirrors Anthropic's cache_control annotation.
type oaiCacheControl struct {
	Type string `json:"type"` // "ephemeral"
	TTL  string `json:"ttl,omitempty"`
}

// oaiContentPart writes cache_control last: addCacheControlToMessage creates it after the text part's members (openai-completions.ts:1165-1186).
type oaiContentPart struct {
	Type string `json:"type"`
	// Text is a pointer so a text part whose sanitized text is empty keeps its "text":"" member, as Pi writes it; an image part has none.
	Text     *string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	CacheControl *oaiCacheControl `json:"cache_control,omitempty"`
}

// oaiRequestToolCall is a tool call in a request assistant message. Unlike the
// streaming delta tool call (oaiToolCall), it carries no index field: upstream
// ChatCompletionMessageToolCall (openai-completions.ts:1188-1210) emits only
// id, type, and function.
type oaiRequestToolCall struct {
	ID       string                      `json:"id"`
	Type     string                      `json:"type"` // "function" | "custom"
	Function *oaiRequestToolCallFunction `json:"function,omitempty"`
	Custom   *oaiRequestToolCallCustom   `json:"custom,omitempty"`
}

type oaiRequestToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// oaiRequestToolCallCustom replays a prior grammar tool call. Mirrors
// openai-completions.ts:1189-1198.
type oaiRequestToolCallCustom struct {
	Name  string `json:"name"`
	Input string `json:"input"`
}

type oaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"` // "function" | "custom"
	Function *oaiToolCallFunction `json:"function"`
	// Custom carries a streamed grammar tool call. Its Input is a raw grammar
	// string that is reconstructed into JSON arguments. Mirrors
	// openai-completions.ts StreamingToolCallDelta.custom.
	Custom *struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"custom,omitempty"`
	Index *int `json:"index"`
}

// oaiTool writes cache_control last: applyAnthropicCacheControl assigns it to the tool convertTools built (openai-completions.ts:1125-1135).
type oaiTool struct {
	Type         string           `json:"type"` // "function" | "custom"
	Function     *oaiToolFunction `json:"function,omitempty"`
	Custom       *oaiCustomTool   `json:"custom,omitempty"`
	CacheControl *oaiCacheControl `json:"cache_control,omitempty"`
}

type oaiToolFunction struct {
	parameterOrder schemaObjectOrder
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Parameters     map[string]any `json:"parameters"`
	Strict         *bool          `json:"strict,omitempty"`
}

// MarshalJSON writes the parameters with the tool schema's own member order (convertTools passes tool.parameters through).
func (f oaiToolFunction) MarshalJSON() ([]byte, error) {
	wire, err := f.wire()
	if err != nil {
		return nil, err
	}
	// The name and the description are written as JSON.stringify writes them: a lone UTF-16 surrogate as its escape (openai-completions.ts convertTools).
	return jsstring.MarshalKeepingSurrogates(wire)
}

// oaiToolFunctionWire is the value oaiToolFunction's MarshalJSON writes.
type oaiToolFunctionWire struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

func (f oaiToolFunction) wire() (oaiToolFunctionWire, error) {
	parameters, err := marshalToolParameters(f.Parameters, f.parameterOrder)
	if err != nil {
		return oaiToolFunctionWire{}, err
	}
	return oaiToolFunctionWire{f.Name, f.Description, parameters, f.Strict}, nil
}

// oaiCustomTool is a grammar-constrained-sampling tool. Unlike responses, the
// completions API nests the grammar under custom.format.grammar. Mirrors
// openai-completions.ts:1345-1358.
type oaiCustomTool struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Format      oaiCustomToolFormat `json:"format"`
}

type oaiCustomToolFormat struct {
	Type    string               `json:"type"` // "grammar"
	Grammar oaiCustomToolGrammar `json:"grammar"`
}

type oaiCustomToolGrammar struct {
	Syntax     string `json:"syntax"` // "lark" | "regex"
	Definition string `json:"definition"`
}

// oaiRequest is the openai-completions request body. JSON.stringify writes params in the order buildParams creates its keys, so the fields
// follow that order (openai-completions.ts:822-1001): the literal (model, messages, stream, prompt_cache_key, prompt_cache_retention), then
// stream_options, store, the max-tokens field, temperature, tools, tool_stream, tool_choice, priority, the thinking-format members, the
// thinking budget field, provider and providerOptions. Each thinking format sets a subset of its members in this order.
type oaiRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	Stream   bool         `json:"stream"`
	// PromptCacheKey enables server-side prompt caching (OpenAI). Value is
	// the session ID; repeated calls with the same key skip re-processing
	// the unchanged prefix. Mirrors upstream openai-completions.ts:826-829.
	PromptCacheKey *string `json:"prompt_cache_key,omitempty"`
	// PromptCacheRetention controls how long cached prompts are kept.
	// Value "24h" for long retention. Mirrors upstream :830.
	PromptCacheRetention *string `json:"prompt_cache_retention,omitempty"`
	StreamOptions        *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	// Store enables server-side storage of the conversation. Gated on compat.supportsStore.
	Store *bool `json:"store,omitempty"`
	// MaxTokens and MaxCompletionTokens are mutually exclusive: one is set
	// based on compat.maxTokensField. Default: "max_completion_tokens" for
	// api.openai.com, "max_tokens" for everything else.
	MaxTokens           int      `json:"max_tokens,omitempty"`
	MaxCompletionTokens int      `json:"max_completion_tokens,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	// Tools is a pointer so the request can express the three states upstream
	// separates (openai-completions.ts:855-863): a populated array, an explicit
	// empty array required by Anthropic-via-proxy backends once the conversation
	// carries tool history, and the omitted field. A plain slice with omitempty
	// cannot represent the empty-but-present case.
	Tools      *[]oaiTool `json:"tools,omitempty"`
	ToolStream *bool      `json:"tool_stream,omitempty"`
	ToolChoice any        `json:"tool_choice,omitempty"`
	Priority   *float64   `json:"priority,omitempty"`
	Thinking   any        `json:"thinking,omitempty"`
	// EnableThinking / ChatTemplateKwargs carry the qwen and qwen-chat-template
	// thinking formats. Mirrors openai-completions.ts:893-910.
	EnableThinking     *bool `json:"enable_thinking,omitempty"`
	ChatTemplateKwargs any   `json:"chat_template_kwargs,omitempty"`
	ChatTemplateArgs   any   `json:"chat_template_args,omitempty"`
	Reasoning          any   `json:"reasoning,omitempty"`
	// ReasoningEffort maps to upstream reasoning_effort for models that support
	// extended reasoning (o1, o3, claude via Copilot, etc.).
	// Values: "low", "medium", "high" (or absent for no extended reasoning). A thinkingLevelMap entry that is the empty string is sent as "".
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	// The thinking budget goes to the one field compat.thinkingTokenBudgetField names (openai-completions.ts:982-984).
	ThinkingTokenBudget  int `json:"thinking_token_budget,omitempty"`
	ThinkingBudget       int `json:"thinking_budget,omitempty"`
	ThinkingBudgetTokens int `json:"thinking_budget_tokens,omitempty"`
	// Provider/ProviderOptions carry OpenRouter / Vercel AI Gateway routing
	// preferences. Mirrors openai-completions.ts:988-1000.
	Provider        any `json:"provider,omitempty"`
	ProviderOptions any `json:"providerOptions,omitempty"`
}

// oaiThinkingToggle is the `thinking` member of the zai and deepseek formats, with the members in the order of Pi's literals
// (openai-completions.ts:876, :917-919).
type oaiThinkingToggle struct {
	Type          string `json:"type"`
	ClearThinking *bool  `json:"clear_thinking,omitempty"`
}

// SSE chunk types
type oaiDelta struct {
	Role             string            `json:"role,omitempty"`
	Content          string            `json:"content,omitempty"`
	ToolCalls        []oaiToolCall     `json:"tool_calls,omitempty"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningText    string            `json:"reasoning_text,omitempty"`
}

type oaiChunkChoice struct {
	Delta              oaiDelta `json:"delta"`
	FinishReason       string   `json:"finish_reason"`
	NativeFinishReason string   `json:"native_finish_reason"`
	// finishReasonNotString marks a FinishReason that is the String() form of a finish_reason that was not a string.
	finishReasonNotString bool
	Index                 int `json:"index"`
	// Usage is the Moonshot-style usage carried on the choice instead of the chunk.
	Usage *oaiUsage `json:"usage"`
}

// oaiUsage is the raw chunk usage. Cache-read counts are pointers because
// upstream picks the first one that is present (??), including a present 0.
type oaiUsage struct {
	PromptTokens         int  `json:"prompt_tokens"`
	CompletionTokens     int  `json:"completion_tokens"`
	CachedTokens         *int `json:"cached_tokens,omitempty"`
	PromptCacheHitTokens *int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptTokensDetails  *struct {
		CachedTokens     *int `json:"cached_tokens,omitempty"`
		CacheWriteTokens int  `json:"cache_write_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	} `json:"completion_tokens_details,omitempty"`
	// ReasoningTokens is the count at the top level of usage, where GitHub Copilot reports it (Pi 86918f9eeb, #12019).
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	// OpenRouter adds billing fields to streamed usage at runtime, although the OpenAI SDK does not type them (openai-completions.ts parseChunkUsage).
	openRouterUsageAccounting
}

// parseChunkUsage mirrors upstream openai-completions.ts parseChunkUsage.
// cached_tokens counts cache reads; a separately reported cache_write_tokens
// is not subtracted from it, and both leave the uncached input.
func parseChunkUsage(raw *oaiUsage, cost ModelCost, openRouter bool) *Usage {
	cacheReadTokens := 0
	cacheWriteTokens := 0
	if raw.PromptTokensDetails != nil {
		cacheWriteTokens = raw.PromptTokensDetails.CacheWriteTokens
	}
	switch {
	case raw.PromptTokensDetails != nil && raw.PromptTokensDetails.CachedTokens != nil:
		cacheReadTokens = *raw.PromptTokensDetails.CachedTokens
	case raw.PromptCacheHitTokens != nil:
		cacheReadTokens = *raw.PromptCacheHitTokens
	case raw.CachedTokens != nil:
		cacheReadTokens = *raw.CachedTokens
	}
	input := max(0, raw.PromptTokens-cacheReadTokens-cacheWriteTokens)
	// `completion_tokens_details?.reasoning_tokens || rawUsage.reasoning_tokens || 0`: the top-level count when the nested one is missing or 0.
	reasoning := 0
	if raw.CompletionTokensDetails != nil {
		reasoning = raw.CompletionTokensDetails.ReasoningTokens
	}
	if reasoning == 0 {
		reasoning = raw.ReasoningTokens
	}
	usage := &Usage{
		Input:       input,
		Output:      raw.CompletionTokens,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		Reasoning:   &reasoning,
		TotalTokens: input + raw.CompletionTokens + cacheReadTokens + cacheWriteTokens,
	}
	calculateUsageCost(cost, usage)
	if openRouter {
		applyOpenRouterReportedCost(usage, raw.openRouterUsageAccounting)
	}
	return usage
}

type oaiChunk struct {
	ID      string           `json:"id"`
	Model   json.RawMessage  `json:"model"`
	Choices []oaiChunkChoice `json:"choices"`
	Usage   *oaiUsage        `json:"usage"`
	Error   json.RawMessage  `json:"error,omitempty"`
}

// UnmarshalJSON rejects a JSON null choice element so the slow path routes it to normalizeOpenAIChunkJS
// (matching openai 7.59.0 normalizeChatCompletionChunk throwing TypeError on choice.finish_reason).
func (c *oaiChunkChoice) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeFor[oaiChunkChoice]()}
	}
	type rawChoice oaiChunkChoice
	return jsstring.UnmarshalKeepingSurrogates(data, (*rawChoice)(c))
}

// ─── Message conversion ───────────────────────────────────────────────────────

// qwenReasoningEffort mirrors the qwen thinkingFormat branch of
// openai-completions.ts: enable_thinking always, plus reasoning_effort (mapped
// through thinkingLevelMap) when reasoning is on and the model supports it.
func qwenReasoningEffort(reasoningOn, supportsReasoningEffort bool, mappedEffort func() string) *string {
	if !reasoningOn || !supportsReasoningEffort {
		return nil
	}
	return new(mappedEffort())
}

// thinkingToReasoningEffort maps a ModelThinkingLevel to an OpenAI-compatible
// reasoning_effort string using the model's thinkingLevelMap when present.
// Returns "" when the requested level maps to disabled reasoning so callers
// can omit the field entirely.
func thinkingToReasoningEffort(model *Model, level ModelThinkingLevel) string {
	if model == nil {
		if level == ThinkingOff || level == "" {
			return ""
		}
		return string(level)
	}
	clamped := ClampThinkingLevel(model, level)
	if clamped == ThinkingOff || clamped == "" {
		return ""
	}
	if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; ok && mapped != nil {
		return *mapped
	}
	return string(clamped)
}

// modelSupportsImages reports whether the configured model accepts image
// input. Mirrors upstream's `model.input.includes("image")` gate, resolving the
// catalog entry with the same precedence as reasoning-level resolution.
func (p *openAIProvider) modelSupportsImages() bool {
	if p.cfg.ModelMetadata != nil {
		return slices.Contains(p.cfg.ModelMetadata.Input, "image")
	}
	if generated, ok := LookupModelExact(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		return generated.ToCapabilities().SupportsImages
	}
	var generated *GeneratedModel
	var ok bool
	if p.cfg.ProviderID == "openrouter" {
		if generated, ok = LookupModel(p.cfg.Model); !ok {
			generated, ok = LookupModel(strings.TrimPrefix(p.cfg.Model, "openrouter/"))
		}
	} else if generated, ok = LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); !ok {
		generated, ok = LookupModel(p.cfg.Model)
	}
	if !ok || generated == nil {
		return false
	}
	return generated.ToCapabilities().SupportsImages
}

// convertMessages lowers the neutral message list into OpenAI Completions wire
// messages. supportsImages gates whether tool-result images are forwarded as a
// trailing user turn (OpenAI tool messages cannot carry image parts).
func convertMessages(messages []Message, supportsImages bool, grammarProps map[string]string) ([]oaiMessage, error) {
	return convertMessagesInternal(messages, supportsImages, grammarProps, false)
}

func convertMessagesInternal(messages []Message, supportsImages bool, grammarProps map[string]string, thinkingAsText bool) ([]oaiMessage, error) {
	return convertCompletionsMessages(messages, completionsConvertOptions{supportsImages: supportsImages, grammarProps: grammarProps, thinkingAsText: thinkingAsText, instructionRole: "system"})
}

// completionsConvertOptions are the inputs of upstream openai-completions.ts
// convertMessages that vary by model.
type completionsConvertOptions struct {
	supportsImages                   bool
	grammarProps                     map[string]string
	thinkingAsText                   bool
	requiresAssistantAfterToolResult bool
	// requiresNonNullAssistantContent sends "" instead of null content on replayed assistant messages that carry no text.
	requiresNonNullAssistantContent bool
	// requiresToolResultName adds the tool result's tool name to its message.
	requiresToolResultName bool
	// reasoningContentOnAssistant sets an empty reasoning_content on assistant messages that carry none (compat.requiresReasoningContentOnAssistantMessages with a reasoning model).
	reasoningContentOnAssistant bool
	instructionRole             string
	// leadingSlots is the number of zero messages the conversion leaves at the start of what it returns, for the caller to fill with the
	// messages that lead the request, so those are not prepended by copying every converted message.
	leadingSlots int
	// systemTools converts the tools a later system message loads in place, or returns none when the transcript does not anchor
	// additions. systemIndex counts the conversation's system messages from 0.
	systemTools func(systemIndex int, message SystemMessage) ([]oaiTool, error)
}

// convertCompletionsMessages converts the conversation after the leading
// system message. A later system message becomes a tool-bearing system
// message for its anchored additions, then its update text in the
// instruction role.
func convertCompletionsMessages(messages []Message, options completionsConvertOptions) ([]oaiMessage, error) {
	supportsImages, grammarProps, thinkingAsText := options.supportsImages, options.grammarProps, options.thinkingAsText
	out := make([]oaiMessage, options.leadingSlots, options.leadingSlots+len(messages))
	// lastRole is the role of the previous message that reached the end of the loop body; a message skipped as empty leaves it (openai-completions.ts convertMessages).
	lastRole, skipped := "", false
	systemIndex := 0
	for index := 0; index < len(messages); index++ {
		if _, user := messages[index].(UserMessage); user && options.requiresAssistantAfterToolResult && lastRole == "toolResult" {
			out = append(out, oaiMessage{Role: "assistant", Content: "I have processed the tool results."})
		}
		skipped = false
		switch message := messages[index].(type) {
		case SystemMessage:
			if options.systemTools != nil {
				tools, err := options.systemTools(systemIndex, message)
				if err != nil {
					return nil, err
				}
				if len(tools) > 0 {
					out = append(out, oaiMessage{Role: "system", Tools: tools})
				}
			}
			systemIndex++
			if text := RenderSystemMessageUpdate(message); text != "" {
				out = append(out, oaiMessage{Role: options.instructionRole, Content: sanitizeSurrogates(text)})
			}
		case UserMessage:
			switch content := message.Content.(type) {
			case UserText:
				out = append(out, oaiMessage{Role: "user", Content: sanitizeSurrogates(string(content))})
			case UserContentBlocks:
				var parts []oaiContentPart
				for _, block := range content {
					switch block := block.(type) {
					case TextContent:
						// Pi drops empty text parts so image-only messages stay valid for
						// compatible providers; the message is dropped only when empty.
						if block.Text != "" {
							parts = append(parts, oaiContentPart{Type: "text", Text: new(sanitizeSurrogates(block.Text))})
						}
					case ImageContent:
						parts = append(parts, oaiContentPart{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:" + block.MimeType + ";base64," + block.Data}})
					}
				}
				if len(parts) > 0 {
					out = append(out, oaiMessage{Role: "user", Content: parts})
				} else {
					skipped = true
				}
			}
		case AssistantMessage:
			converted := oaiMessage{Role: "assistant"}
			if options.requiresAssistantAfterToolResult || options.requiresNonNullAssistantContent {
				converted.Content = ""
			}
			// texts holds the sanitized text of each text block that is not blank; the array keeps the usual few off the heap.
			var textsInline [4]string
			texts := textsInline[:0]
			var thinking []ThinkingContent
			var legacyReasoningDetails []json.RawMessage
			for _, block := range message.Content {
				switch block := block.(type) {
				case TextContent:
					if trimJSWhitespace(block.Text) != "" {
						texts = append(texts, sanitizeSurrogates(block.Text))
					}
				case ThinkingContent:
					thinking = append(thinking, block)
				case ToolCall:
					if property, ok := grammarProps[block.Name]; ok {
						input, err := getGrammarToolInput(block.Name, block.Arguments, property)
						if err != nil {
							return nil, err
						}
						converted.ToolCalls = append(converted.ToolCalls, oaiRequestToolCall{ID: block.ID, Type: "custom", Custom: &oaiRequestToolCallCustom{Name: block.Name, Input: sanitizeSurrogates(input)}})
					} else {
						arguments, _ := block.ArgumentsJSON()
						converted.ToolCalls = append(converted.ToolCalls, oaiRequestToolCall{ID: block.ID, Type: "function", Function: &oaiRequestToolCallFunction{Name: block.Name, Arguments: keptArgumentsString(arguments)}})
					}
					if detail := parseLegacyOpenAIReasoningDetail(block.ThoughtSignature); detail != nil {
						legacyReasoningDetails = append(legacyReasoningDetails, detail)
					}
				}
			}
			var thinkingText []string
			var nonEmptyThinking []ThinkingContent
			for _, block := range thinking {
				if trimJSWhitespace(block.Thinking) != "" {
					thinkingText = append(thinkingText, block.Thinking)
					nonEmptyThinking = append(nonEmptyThinking, block)
				}
				if converted.ReasoningDetails == nil {
					converted.ReasoningDetails = parseOpenAIReasoningDetails(block.ThinkingSignature)
				}
			}
			if converted.ReasoningDetails == nil && len(legacyReasoningDetails) > 0 {
				converted.ReasoningDetails = legacyReasoningDetails
			}
			if thinkingAsText && len(thinkingText) > 0 {
				sanitized := make([]string, len(thinkingText))
				for i, text := range thinkingText {
					sanitized[i] = sanitizeSurrogates(text)
				}
				parts := make([]oaiContentPart, 0, 1+len(texts))
				parts = append(parts, oaiContentPart{Type: "text", Text: new(strings.Join(sanitized, "\n\n"))})
				for _, text := range texts {
					parts = append(parts, oaiContentPart{Type: "text", Text: new(text)})
				}
				converted.Content = parts
			} else {
				// The content is the text parts joined (openai-completions.ts: textParts.map((part) => part.text).join("")). One part, the usual
				// message, is that part's string itself; several are joined into a string of their exact size.
				if joined := joinTexts(texts); joined != "" {
					converted.Content = joined
				}
				if len(converted.ReasoningDetails) == 0 && len(thinkingText) > 0 {
					signature := nonEmptyThinking[0].ThinkingSignature
					reasoning := strings.Join(thinkingText, "\n")
					switch signature {
					case "reasoning":
						converted.Reasoning = reasoning
					case "reasoning_content":
						converted.ReasoningContent = new(reasoning)
					case "reasoning_text":
						converted.ReasoningText = reasoning
					}
				}
			}
			if options.reasoningContentOnAssistant && converted.ReasoningContent == nil {
				converted.ReasoningContent = new("")
				converted.reasoningContentLast = true
			}
			// An assistant message needs content or tool calls; reasoning fields alone do not keep it.
			hasContent := false
			switch content := converted.Content.(type) {
			case string:
				hasContent = content != ""
			case []oaiContentPart:
				hasContent = len(content) > 0
			}
			if hasContent || len(converted.ToolCalls) > 0 {
				out = append(out, converted)
			} else {
				skipped = true
			}
		case ToolResultMessage:
			var images []oaiContentPart
			for index < len(messages) {
				result, ok := messages[index].(ToolResultMessage)
				if !ok {
					break
				}
				var textParts []string
				hasImages := false
				for _, block := range result.Content {
					switch block := block.(type) {
					case TextContent:
						textParts = append(textParts, block.Text)
					case ImageContent:
						hasImages = true
						if supportsImages {
							images = append(images, oaiContentPart{Type: "image_url", ImageURL: &struct {
								URL string `json:"url"`
							}{URL: "data:" + block.MimeType + ";base64," + block.Data}})
						}
					}
				}
				textResult := strings.Join(textParts, "\n")
				// Whitespace-only text counts as no text (openai-completions.ts hasText: textResult.trim(), Pi 4dd2b149).
				if trimJSWhitespace(textResult) == "" {
					if hasImages {
						textResult = "(see attached image)"
					} else {
						textResult = "(no tool output)"
					}
				}
				toolMessage := oaiMessage{Role: "tool", Content: sanitizeSurrogates(textResult), ToolCallID: result.ToolCallID}
				if options.requiresToolResultName && result.ToolName != "" {
					toolMessage.ToolName = result.ToolName
				}
				out = append(out, toolMessage)
				index++
			}
			index--
			if len(images) > 0 {
				if options.requiresAssistantAfterToolResult {
					out = append(out, oaiMessage{Role: "assistant", Content: "I have processed the tool results."})
				}
				parts := append([]oaiContentPart{{Type: "text", Text: new("Attached image(s) from tool result:")}}, images...)
				out = append(out, oaiMessage{Role: "user", Content: parts})
				lastRole, skipped = "user", true
			} else {
				lastRole, skipped = "toolResult", true
			}
		}
		if !skipped {
			lastRole = completionsRole(messages[index])
		}
	}
	return out, nil
}

// joinTexts is strings.Join(texts, "") without a copy for one text.
func joinTexts(texts []string) string {
	if len(texts) == 1 {
		return texts[0]
	}
	size := 0
	for _, text := range texts {
		size += len(text)
	}
	if size == 0 {
		return ""
	}
	var joined strings.Builder
	joined.Grow(size)
	for _, text := range texts {
		joined.WriteString(text)
	}
	return joined.String()
}

type openAIReasoningDetail struct {
	fields []openAIReasoningDetailField
	typ    string
	// Once a later delta merges into a text or summary entry, the entry's content lives in mergedContent and the
	// field named mergedField holds only the position; marshalling writes the content once.
	mergedField   string
	mergedContent strings.Builder
}

type openAIReasoningDetailField struct {
	name  string
	value json.RawMessage
}

func decodeOpenAIReasoningDetail(raw json.RawMessage) (*openAIReasoningDetail, bool) {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, false
	}
	stringOrNull := func(name string) bool {
		value, ok := values[name]
		if !ok || bytes.Equal(value, []byte("null")) {
			return true
		}
		var decoded string
		return json.Unmarshal(value, &decoded) == nil
	}
	if !stringOrNull("id") {
		return nil, false
	}
	if value, ok := values["format"]; ok {
		if bytes.Equal(value, []byte("null")) {
			return nil, false
		}
		var decoded string
		if json.Unmarshal(value, &decoded) != nil {
			return nil, false
		}
	}
	if value, ok := values["index"]; ok {
		if bytes.Equal(value, []byte("null")) {
			return nil, false
		}
		var decoded float64
		if json.Unmarshal(value, &decoded) != nil {
			return nil, false
		}
	}
	var typ string
	if json.Unmarshal(values["type"], &typ) != nil {
		return nil, false
	}
	required := ""
	switch typ {
	case "reasoning.text":
		if !stringOrNull("signature") {
			return nil, false
		}
		required = "text"
	case "reasoning.summary":
		required = "summary"
	case "reasoning.encrypted":
		required = "data"
	default:
		return nil, false
	}
	var requiredValue string
	requiredRaw, hasRequired := values[required]
	// pig divergence (D122): OpenRouter streams the signature of a reasoning.text block in a delta without text (pi-mono #9534); Pi drops it.
	signatureOnly := typ == "reasoning.text" && !hasRequired && isJSONString(values["signature"])
	if !signatureOnly && (bytes.Equal(requiredRaw, []byte("null")) || json.Unmarshal(requiredRaw, &requiredValue) != nil) {
		return nil, false
	}

	decoder := jsontext.NewDecoder(bytes.NewReader(raw), jsonopts.Parse)
	if token, err := decoder.ReadToken(); err != nil || token.Kind() != '{' {
		return nil, false
	}
	detail := &openAIReasoningDetail{typ: typ}
	positions := map[string]int{}
	for decoder.PeekKind() != '}' {
		name, err := jsonopts.ReadName(decoder) // the JavaScript key: "\ud800" and "\ud801" stay two keys, as in JSON.parse
		if err != nil {
			return nil, false
		}
		valueBytes, err := decoder.ReadValue()
		if err != nil {
			return nil, false
		}
		value := json.RawMessage(bytes.Clone(valueBytes))
		if position, exists := positions[name]; exists {
			detail.fields[position].value = value
			continue
		}
		positions[name] = len(detail.fields)
		detail.fields = append(detail.fields, openAIReasoningDetailField{name: jsonopts.ValidName(name), value: value})
	}
	return detail, true
}

func isJSONString(raw json.RawMessage) bool {
	var value string
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, &value) == nil
}

func (detail *openAIReasoningDetail) field(name string) (json.RawMessage, bool) {
	for _, field := range detail.fields {
		if field.name == name {
			return field.value, true
		}
	}
	return nil, false
}

func (detail *openAIReasoningDetail) stringField(name string) string {
	raw, ok := detail.field(name)
	if !ok {
		return ""
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func (detail *openAIReasoningDetail) setField(name string, value json.RawMessage) {
	for index := range detail.fields {
		if detail.fields[index].name == name {
			detail.fields[index].value = slices.Clone(value)
			return
		}
	}
	detail.fields = append(detail.fields, openAIReasoningDetailField{name: name, value: slices.Clone(value)})
}

func marshalOpenAIJSONString(value string) json.RawMessage {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
}

func (detail *openAIReasoningDetail) nullishField(name string) bool {
	value, ok := detail.field(name)
	return !ok || bytes.Equal(value, []byte("null"))
}

func appendOpenAIReasoningDetail(details []*openAIReasoningDetail, raw json.RawMessage) ([]*openAIReasoningDetail, bool) {
	detail, ok := decodeOpenAIReasoningDetail(raw)
	if !ok {
		return details, false
	}
	if len(details) == 0 || details[len(details)-1].typ != detail.typ || (detail.typ != "reasoning.text" && detail.typ != "reasoning.summary") {
		return append(details, detail), true
	}
	last := details[len(details)-1]
	contentField := "text"
	if detail.typ == "reasoning.summary" {
		contentField = "summary"
	}
	if last.mergedField == "" {
		last.mergedField = contentField
		last.mergedContent.WriteString(last.stringField(contentField))
	}
	last.mergedContent.WriteString(detail.stringField(contentField))
	if detail.typ == "reasoning.text" && last.stringField("signature") == "" {
		if value, exists := detail.field("signature"); exists {
			last.setField("signature", value)
		}
	}
	for _, name := range []string{"id", "format", "index"} {
		if !last.nullishField(name) {
			continue
		}
		if value, exists := detail.field(name); exists {
			last.setField(name, value)
		}
	}
	return details, true
}

func marshalOpenAIReasoningDetails(details []*openAIReasoningDetail) string {
	var encoded bytes.Buffer
	encoded.WriteByte('[')
	for detailIndex, detail := range details {
		if detailIndex > 0 {
			encoded.WriteByte(',')
		}
		encoded.WriteByte('{')
		for fieldIndex, field := range detail.fields {
			if fieldIndex > 0 {
				encoded.WriteByte(',')
			}
			encoded.Write(marshalOpenAIJSONString(field.name))
			encoded.WriteByte(':')
			if detail.mergedField == field.name {
				encoded.Write(marshalOpenAIJSONString(detail.mergedContent.String()))
				continue
			}
			_ = json.Compact(&encoded, field.value)
		}
		encoded.WriteByte('}')
	}
	encoded.WriteByte(']')
	return encoded.String()
}

func parseOpenAIReasoningDetails(signature string) []json.RawMessage {
	if signature == "" {
		return nil
	}
	var rawDetails []json.RawMessage
	if json.Unmarshal([]byte(signature), &rawDetails) != nil || len(rawDetails) == 0 {
		return nil
	}
	details := make([]json.RawMessage, 0, len(rawDetails))
	for _, raw := range rawDetails {
		if _, ok := decodeOpenAIReasoningDetail(raw); !ok {
			return nil
		}
		details = append(details, raw)
	}
	return details
}

func parseLegacyOpenAIReasoningDetail(signature string) json.RawMessage {
	if signature == "" || !jsonwire.Valid(signature) {
		return nil
	}
	detail, ok := decodeOpenAIReasoningDetail(json.RawMessage(signature))
	if !ok || detail.typ != "reasoning.encrypted" || detail.stringField("id") == "" || detail.stringField("data") == "" {
		return nil
	}
	return json.RawMessage(signature)
}

// isOpenAIDirectURL returns true when the base URL targets OpenAI's own API
// (where prompt_cache_key is supported). Mirrors upstream's
// `model.baseUrl.includes("api.openai.com")` check.
func isOpenAIDirectURL(baseURL string) bool {
	return strings.Contains(baseURL, "api.openai.com")
}

// DetectCompat is the compat flags a provider name and base URL imply (openai-completions.ts detectCompat for a model without an id).
func DetectCompat(providerID, baseURL string) *OpenAICompat {
	detected := detectOpenAICompletionsCompat(providerID, baseURL, "")
	return &detected
}

var toolCallIDForbidden = lazyregexp.New(`[^a-zA-Z0-9_-]`)

// ShortHash ports upstream packages/ai/src/utils/hash.ts shortHash: a 32-bit
// non-cryptographic hash rendered as two base36 numbers. Iterating UTF-16 code
// units with uint32 arithmetic (matching JS Math.imul low-32-bit wrap and `>>>`)
// keeps the output byte-identical to upstream for the tool-call ids it hashes.
func ShortHash(s string) string {
	var h1 uint32 = 0xdeadbeef
	var h2 uint32 = 0x41c6ce57
	for _, unit := range utf16.Encode([]rune(s)) {
		ch := uint32(unit)
		h1 = (h1 ^ ch) * 2654435761
		h2 = (h2 ^ ch) * 1597334677
	}
	h1 = (h1^(h1>>16))*2246822507 ^ (h2^(h2>>13))*3266489909
	h2 = (h2^(h2>>16))*2246822507 ^ (h1^(h1>>13))*3266489909
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}

// normalizeCompletionsToolCallID mirrors upstream openai-completions.ts
// normalizeToolCallId. OpenAI Responses providers (github-copilot, openai-codex,
// opencode) emit ids as {call_id}|{item_id} where item_id can be 400+ chars with
// +,/,=: Chat Completions backends reject those as "call_id too long". Extract
// and sanitize the parts, keep them under the 40-char limit, and fall back to a
// hashed id when the combination is still too long. Tool-call ids are ASCII, so
// byte length matches upstream's UTF-16 length.
func normalizeCompletionsToolCallID(id, providerID string) string {
	if before, after, ok := strings.Cut(id, "|"); ok {
		callID := toolCallIDForbidden.ReplaceAllString(before, "_")
		itemID := toolCallIDForbidden.ReplaceAllString(after, "_")
		combined := callID
		if len(itemID) > 0 {
			combined = callID + "_" + itemID
		}
		if len(combined) <= 40 {
			return combined
		}
		hash := ShortHash(id)
		if len(hash) > 8 {
			hash = hash[:8]
		}
		prefixLen := min(max(40-len(hash)-1, 1), len(callID))
		return callID[:prefixLen] + "_" + hash
	}
	if providerID == "openai" && len(id) > 40 {
		return id[:40]
	}
	return id
}

// hasToolHistory reports whether the transcript, before the provider's message transformation drops error and aborted turns, already references
// tools via a tool result or an assistant tool call. Mirrors upstream openai-completions.ts
// hasToolHistory: some OpenAI-compatible proxies (Anthropic via LiteLLM) require
// the tools param to be present, even empty, once the history references tools.
func hasToolHistory(transcript TranscriptContext) bool {
	for i := range transcript.Len() {
		switch message := transcript.At(i).(type) {
		case ToolResultMessage:
			return true
		case AssistantMessage:
			for _, block := range message.Content {
				if _, ok := block.(ToolCall); ok {
					return true
				}
			}
		}
	}
	return false
}

// resolveRequestTools returns the value for the request `tools` field, mirroring
// upstream openai-completions.ts:729-737: the converted tools when any are
// present, an explicit empty array when the conversation has tool history so
// Anthropic-via-proxy backends accept the request, otherwise nil so the field is
// omitted (some compatible backends reject `tools: []` outright).
func (p *openAIProvider) resolveRequestTools(transcript TranscriptContext, tools []ToolSchema) (*[]oaiTool, error) {
	if len(tools) > 0 {
		converted, err := p.convertTools(tools)
		if err != nil {
			return nil, err
		}
		return &converted, nil
	}
	if p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresToolsWithToolHistory }, false) && hasToolHistory(transcript) {
		return &[]oaiTool{}, nil
	}
	return nil, nil
}

func (p *openAIProvider) convertTools(tools []ToolSchema) ([]oaiTool, error) {
	// supportsStrictMode defaults to true (OpenAI supports strict mode). When it
	// is true, every function tool carries strict = resolveJSONSchemaStrictSampling
	// (false for a plain tool without json-schema constrained sampling). When it
	// is false, strict is omitted entirely because some providers reject the
	// unknown field. A grammar-constrained-sampling tool serializes as a custom
	// tool instead. Mirrors openai-completions.ts:1340-1372.
	supportsGrammar := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsOpenAIGrammarTools }, false)
	// Upstream detectCompat defaults supportsStrictMode to false: without compat
	// enabling it, tools carry no strict field at all.
	supportsStrict := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsStrictMode }, false)
	out := make([]oaiTool, 0, len(tools))
	for _, t := range tools {
		grammar, err := resolveGrammarConstrainedSampling(t, supportsGrammar)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			out = append(out, oaiTool{
				Type: "custom",
				Custom: &oaiCustomTool{
					Name:        t.Name,
					Description: t.Description,
					Format: oaiCustomToolFormat{
						Type: "grammar",
						Grammar: oaiCustomToolGrammar{
							Syntax:     grammar.Format,
							Definition: grammar.Definition,
						},
					},
				},
			})
			continue
		}
		fn := &oaiToolFunction{
			Name:        t.Name,
			Description: t.Description,
		}
		// Upstream resolves strict sampling for every tool, so strict "require" fails on
		// a provider without strict mode; strict is only sent when supported.
		strict, err := resolveJSONSchemaStrictSampling(t, supportsStrict, nil)
		if err != nil {
			return nil, err
		}
		toolParameters, err := getJSONSchemaToolParameters(t, strict)
		if err != nil {
			return nil, err
		}
		fn.Parameters, fn.parameterOrder = toolParameters.Schema, toolParameters.order
		if supportsStrict {
			v := strict != nil && *strict
			fn.Strict = &v
		}
		out = append(out, oaiTool{Type: "function", Function: fn})
	}
	return out, nil
}

// ─── Stream ───────────────────────────────────────────────────────────────────

// completionsConversion is a transcript converted for a chat-completions request.
type completionsConversion struct {
	// messages is the resolved and transformed transcript, the leading system message included.
	messages []Message
	tools    TranscriptTools
	// grammarProps is the grammar tool input property map the conversion used.
	grammarProps map[string]string
	// chat is the request's messages: the instruction message first, then the conversation.
	chat []oaiMessage
}

// convertTranscript resolves, transforms and converts a transcript for the provider's model and compat. A nil
// grammarProps is derived from the transcript's declared tools, as Pi's createGrammarToolInputProperties default does.
func (p *openAIProvider) convertTranscript(transcript TranscriptContext, grammarProps map[string]string, isReasoning bool) (completionsConversion, error) {
	supportsMidConversation := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsMidConvoSystemMessages }, false)
	messages := resolvedMessages(transcript, supportsMidConversation)
	target := &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: p.cfg.ProviderID}, Input: []string{"text"}}
	if p.modelSupportsImages() {
		target.Input = append(target.Input, "image")
	}
	// upstream: packages/ai/src/api/openai-completions.ts:convertMessages passes normalizeToolCallId to transformMessages, which applies it to the calls of other models only.
	messages = TransformMessages(messages, target, func(id string, _ *Model, _ AssistantMessage) string {
		return normalizeCompletionsToolCallID(id, p.cfg.ProviderID)
	})
	conversation := withoutInitialSystemMessage(messages)
	// Kimi loads later tool additions in tool-bearing system messages; the
	// request tools then hold only the initial tools. Pi's buildParams resolves them, and the declared tools, from context.messages as given
	// (openai-completions.ts:813, :808): a transcript whose first message is an aborted assistant has no initial system message there, even
	// though transformMessages drops that assistant.
	given := transcript.borrowed()
	toolSupport := TranscriptToolSupportNone
	if supportsMidConversation && p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsMidConvoToolAdditions }, false) {
		toolSupport = TranscriptToolSupportAdditions
	}
	transcriptTools := ResolveTranscriptTools(given, toolSupport)
	if grammarProps == nil {
		// grammarProps maps a grammar tool's name to the single string property that
		// carries its constrained input. It is derived once from every declared tool and
		// threaded into message replay, tool conversion, and stream reconstruction so
		// custom (grammar) tool calls round-trip. Mirrors api/openai-completions.ts:343-346 (stream) and :806-811 (buildParams).
		supportsGrammar := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsOpenAIGrammarTools }, false)
		var err error
		grammarProps, err = createGrammarToolInputProperties(GetDeclaredTools(given), supportsGrammar)
		if err != nil {
			return completionsConversion{}, err
		}
	}
	// The conversation drops the leading system message, but system messages count from the transcript's start.
	leadingSystemMessages := len(messages) - len(conversation)
	// The leading system message becomes up to two messages ahead of the conversation's: its anchored tools, then the system prompt. The
	// conversion leaves room for them, so they are written in place instead of every converted message being copied behind them.
	var leadTools []ToolSchema
	systemPrompt := ""
	leadingSlots := 0
	if leadingSystemMessages == 1 {
		// Only a transcript whose tool changes the request's tool list does not already hold anchors the leading message's tools.
		if message, ok := messages[0].(SystemMessage); ok && transcriptTools.FoldedSystemMessages == 0 && len(message.ToolsAdded) > 0 {
			leadTools = message.ToolsAdded
			leadingSlots++
		}
		if systemPrompt = GetCurrentSystemPrompt(messages[:1]); systemPrompt != "" {
			leadingSlots++
		}
	}
	msgs, err := p.convertMessagesWithCompat(conversation, grammarProps, p.systemPromptRole(isReasoning), toolAnchors{enabled: true, from: transcriptTools.FoldedSystemMessages - leadingSystemMessages}, isReasoning, leadingSlots)
	if err != nil {
		return completionsConversion{}, err
	}
	slot := 0
	if leadTools != nil {
		tools, err := p.convertTools(leadTools)
		if err != nil {
			return completionsConversion{}, err
		}
		msgs[slot] = oaiMessage{Role: "system", Tools: tools}
		slot++
	}
	if systemPrompt != "" {
		msgs[slot] = oaiMessage{Role: p.systemPromptRole(isReasoning), Content: sanitizeSurrogates(systemPrompt)}
	}
	return completionsConversion{messages: messages, tools: transcriptTools, grammarProps: grammarProps, chat: msgs}, nil
}

// OpenAIChatMessage is one message of a chat-completions request.
type OpenAIChatMessage = oaiMessage

// ConvertCompletionsMessagesOptions are the options of [ConvertCompletionsMessages].
//
// Ports packages/ai/src/api/openai-completions.ts (ConvertCompletionsMessagesOptions).
type ConvertCompletionsMessagesOptions struct {
	// GrammarToolInputProperties maps a grammar tool's name to its constrained input property. Nil derives it from the transcript.
	GrammarToolInputProperties map[string]string
}

// ConvertCompletionsMessages converts a transcript to the messages of an openai-completions request for model and
// compat: the transcript is resolved and transformed for the model, and the instruction message leads. A nil compat
// resolves every flag to its default.
//
// Ports packages/ai/src/api/openai-completions.ts (convertMessages).
func ConvertCompletionsMessages(model *Model, transcript TranscriptContext, compat *OpenAICompat, options ConvertCompletionsMessagesOptions) ([]OpenAIChatMessage, error) {
	p := &openAIProvider{cfg: OpenAIConfig{
		BaseURL:       model.ProviderMeta.BaseURL,
		Model:         model.ID,
		ModelMetadata: model,
		ProviderID:    model.ProviderMeta.ProviderID,
		Compat:        compat,
	}}
	conversion, err := p.convertTranscript(transcript, options.GrammarToolInputProperties, model.ProviderMeta.Reasoning)
	if err != nil {
		return nil, err
	}
	return conversion.chat, nil
}

// Stream returns before HTTP setup settles. Successful setup admits start before waiting for body data.
func (p *openAIProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if p.cfg.azure != nil {
		resolved, err := p.cfg.azure.resolve(p, opts)
		if err != nil {
			// azure.ts azureStreams resolves the endpoint in lazyStream's setup, outside the API module.
			return nil, &lazySetupError{err: err}
		}
		p = resolved
	}
	ctx = withProviderRequestOptions(ctx, opts)
	// pig divergence (D48): the agent loop asks for the tool-flow repair.
	if opts.RepairToolFlow {
		transcript = transcript.withRepairedToolFlow()
	}
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("openai: invalid transcript: %w", err)
	}
	conversion, err := p.convertTranscript(transcript, nil, opts.IsReasoning)
	if err != nil {
		return nil, err
	}
	msgs, tools, grammarProps := conversion.chat, conversion.tools.RequestTools, conversion.grammarProps
	if msgs == nil {
		// convertMessages returns an array: a transcript that converts to no message is "messages":[] (openai-completions.ts:804-806, :816-818).
		msgs = []oaiMessage{}
	}

	req := oaiRequest{
		Model:    cmp.Or(p.cfg.requestModel, p.cfg.Model),
		Messages: msgs,
		Stream:   true,
	}
	requestTools, err := p.resolveRequestTools(transcript, tools)
	if err != nil {
		return nil, err
	}
	req.Tools = requestTools
	// openai-completions.ts:869 `if (options?.toolChoice)`: an empty choice is falsy and is not sent.
	if name, ok := toolChoiceName(opts.ToolChoice); !ok || name != "" {
		req.ToolChoice = opts.ToolChoice
	}
	if len(tools) > 0 && p.compat().ZaiToolStream != nil && *p.compat().ZaiToolStream {
		req.ToolStream = new(true)
	}
	// Diagnostic: log tool count for debugging.
	if os.Getenv("PIG_DEBUG_TOOLS") == "1" {
		sent := 0
		if req.Tools != nil {
			sent = len(*req.Tools)
		}
		fmt.Fprintf(os.Stderr, "openai.Stream: sending %d tools (tools=%d)\n", sent, len(tools))
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		req.Temperature = new(opts.Temperature)
	}
	// max_tokens vs max_completion_tokens: upstream defaults to max_completion_tokens
	// for standard OpenAI endpoints, max_tokens for non-standard (Ollama, etc.).
	// Gated on compat.maxTokensField.
	if opts.MaxTokens > 0 {
		if p.maxTokensField() == "max_tokens" {
			req.MaxTokens = opts.MaxTokens
		} else {
			req.MaxCompletionTokens = opts.MaxTokens
		}
	}
	if p.compat().VLLMPriority != nil {
		priority := *p.compat().VLLMPriority
		req.Priority = &priority
	}
	// Upstream sends store: false whenever compat.supportsStore holds, which by
	// default is every provider except the non-standard ones. PiG used to send
	// store: true, which asks OpenAI to retain the conversation.
	if *p.compat().SupportsStore {
		f := false
		req.Store = &f
	}
	// Wire thinking/reasoning level when the model supports extended reasoning.
	// Explicit compat overrides the provider/URL-based detectCompat default.
	thinkingBudgetField := ""
	thinkingBudget := 0
	if opts.IsReasoning {
		model := p.thinkingModel()
		if model.Capabilities.MaxThinking == "" {
			model = new(*model)
			model.Capabilities.MaxThinking = ThinkingLevelHigh
		}
		// Native API effort is mapped without clamping. Omitted and disabled reasoning stay off.
		clamped := ModelThinkingLevel(opts.Thinking)
		if opts.ReasoningEffort != "" {
			clamped = ModelThinkingLevel(opts.ReasoningEffort)
		} else if clamped != ThinkingOff && clamped != "" {
			clamped = ClampThinkingLevel(model, ModelThinkingLevel(opts.Thinking))
		}
		thinkingFormat := p.compat().ThinkingFormat
		supportsRE := *p.compat().SupportsReasoningEffort
		reasoningOn := clamped != ThinkingOff && clamped != ""
		if reasoningOn {
			ceiling := req.MaxTokens
			if ceiling == 0 {
				ceiling = req.MaxCompletionTokens
			}
			if ceiling == 0 {
				ceiling = model.Capabilities.MaxOutputTokens
			}
			thinkingBudget = resolveClampedThinkingBudget(clamped, opts.ThinkingBudgets, ceiling)
		}
		// mappedEffort resolves the wire effort string via thinkingLevelMap.
		mappedEffort := func() string {
			e := string(clamped)
			if m, ok := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; ok && m != nil {
				e = *m
			}
			return e
		}
		// offEffort mirrors `model.thinkingLevelMap?.off` with `?? "none"`, plus
		// the `!== null` guard: returns (value, emit) where emit is false only
		// when off is explicitly null (present-and-nil in the map).
		offEffort := func() (string, bool) {
			v, ok := model.ThinkingLevelMap[ModelThinkingLevel(ThinkingOff)]
			if ok && v == nil {
				return "", false // explicit null → skip
			}
			if ok && v != nil {
				return *v, true
			}
			return "none", true // absent/undefined → "none"
		}
		// templateEffort is resolveChatTemplateKwargValue's effort (openai-completions.ts:1073-1074): thinkingLevelMap[level] for the requested level, or
		// thinkingLevelMap.off when reasoning is off; an absent entry is the requested level itself (nothing when off), and a null entry is nothing.
		templateEffort := func() (string, bool) {
			level := ModelThinkingLevel(clamped)
			if !reasoningOn {
				level = ThinkingOff
			}
			mapped, present := model.ThinkingLevelMap[level]
			switch {
			case !present && reasoningOn:
				return string(clamped), true
			case mapped == nil:
				return "", false
			}
			return *mapped, true
		}
		// Mirrors openai-completions.ts:555-615 thinkingFormat dispatch.
		switch thinkingFormat {
		case "zai":
			if reasoningOn {
				// Z.ai clears prior-turn reasoning_content by default; Pi opts out.
				req.Thinking = oaiThinkingToggle{Type: "enabled", ClearThinking: new(false)}
			} else {
				req.Thinking = oaiThinkingToggle{Type: "disabled"}
			}
			if reasoningOn && supportsRE {
				if mapped, exists := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; !exists || mapped != nil {
					req.ReasoningEffort = new(mappedEffort())
				}
			}
		case "qwen":
			req.EnableThinking = &reasoningOn
			req.ReasoningEffort = qwenReasoningEffort(reasoningOn, supportsRE, mappedEffort)
		case "qwen-chat-template":
			req.ChatTemplateKwargs = oaiQwenChatTemplateKwargs{EnableThinking: reasoningOn, PreserveThinking: true}
		case "chat-template":
			if resolved := resolveChatTemplateValues(p.compat().ChatTemplateKwargs, reasoningOn, templateEffort, thinkingBudget); resolved != nil {
				req.ChatTemplateKwargs = p.compat().requestObject(resolved, "chatTemplateKwargs")
			}
		case "baseten":
			if resolved := resolveChatTemplateValues(p.compat().ChatTemplateArgs, reasoningOn, templateEffort, thinkingBudget); resolved != nil {
				req.ChatTemplateArgs = p.compat().requestObject(resolved, "chatTemplateArgs")
			}
			// openai-completions.ts:919-924: the requested level's mapped value, or the off value when reasoning is off; a null entry is unsupported and an absent off entry sends nothing.
			if value, emit := templateEffort(); supportsRE && emit {
				req.ReasoningEffort = new(value)
			}
		case "deepseek":
			if reasoningOn {
				req.Thinking = oaiThinkingToggle{Type: "enabled"}
			} else if _, emit := offEffort(); emit {
				// 0.79.5: suppress thinking:{disabled} when thinkingLevelMap.off is
				// explicitly null; still send it when off is absent or a value.
				req.Thinking = oaiThinkingToggle{Type: "disabled"}
			}
			if reasoningOn && supportsRE {
				req.ReasoningEffort = new(mappedEffort())
			}
		case "openrouter":
			if reasoningOn {
				req.Reasoning = map[string]any{"effort": mappedEffort()}
			} else if v, emit := offEffort(); emit {
				req.Reasoning = map[string]any{"effort": v}
			}
		case "ant-ling":
			// Only emits when the mapped effort is a non-null string.
			if reasoningOn {
				if m, ok := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; ok && m != nil {
					req.Reasoning = map[string]any{"effort": *m}
				}
			} else if v, ok := model.ThinkingLevelMap[ModelThinkingLevel(ThinkingOff)]; ok && v != nil && supportsRE {
				// The ant-ling branch (openai-completions.ts:949) needs a requested effort; without one the chain falls through to the off-value reasoning_effort at :970-975.
				req.ReasoningEffort = new(*v)
			}
		case "together":
			req.Reasoning = map[string]any{"enabled": reasoningOn}
			if reasoningOn && supportsRE {
				req.ReasoningEffort = new(mappedEffort())
			}
		case "string-thinking":
			if reasoningOn {
				req.Thinking = mappedEffort()
			} else if v, emit := offEffort(); emit {
				req.Thinking = v
			}
		default: // "openai"
			if reasoningOn && supportsRE {
				req.ReasoningEffort = new(mappedEffort())
			} else if !reasoningOn && supportsRE {
				if v, ok := model.ThinkingLevelMap[ModelThinkingLevel(ThinkingOff)]; ok && v != nil {
					req.ReasoningEffort = new(*v)
				}
			}
		}

		thinkingBudgetField = string(p.compat().ThinkingTokenBudgetField)
		if thinkingBudgetField == "" && p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsThinkingTokenBudget }, false) {
			thinkingBudgetField = string(ThinkingTokenBudgetFieldThinkingTokenBudget)
		}
		if thinkingBudget > 0 {
			switch ThinkingTokenBudgetField(thinkingBudgetField) {
			case ThinkingTokenBudgetFieldThinkingTokenBudget:
				req.ThinkingTokenBudget = thinkingBudget
			case ThinkingTokenBudgetFieldThinkingBudget:
				req.ThinkingBudget = thinkingBudget
			case ThinkingTokenBudgetFieldThinkingBudgetTokens:
				req.ThinkingBudgetTokens = thinkingBudget
			}
		}
	}
	// OpenRouter provider routing preferences. Mirrors openai-completions.ts:613
	// (no baseURL gate as of 0.79.1: applies whenever compat sets it).
	if p.compat().OpenRouterRouting != nil {
		req.Provider = p.compat().requestObject(p.compat().OpenRouterRouting, "openRouterRouting")
	}
	// Vercel AI Gateway provider routing preferences. Pi applies them to every request whose compat sets them, with no base URL gate (openai-completions.ts:992-1001).
	if p.compat().VercelGatewayRouting != nil && (p.compat().VercelGatewayRouting.Only != nil || p.compat().VercelGatewayRouting.Order != nil) {
		routing := p.compat().VercelGatewayRouting
		gatewayOptions := map[string]any{}
		if routing.Only != nil {
			gatewayOptions["only"] = routing.Only
		}
		if routing.Order != nil {
			gatewayOptions["order"] = routing.Order
		}
		req.ProviderOptions = map[string]any{"gateway": gatewayOptions}
	}
	// Prompt-cache body fields and session-affinity headers have independent provider gates.
	cacheRetention := resolveCompletionsCacheRetention(opts.CacheRetention, mergeProviderEnv(p.cfg.Env, opts.Env))
	longCache := cacheRetention == CacheRetentionLong && p.supportsLongCacheRetention()
	promptCacheKeyMode := cmp.Or(p.compat().PromptCacheKeyMode, PromptCacheKeyAuto)
	supportsPromptCacheKey := promptCacheKeyMode == PromptCacheKeyEnabled ||
		promptCacheKeyMode == PromptCacheKeyAuto && (isOpenAIDirectURL(p.cfg.BaseURL) || longCache)
	if opts.SessionID != "" && cacheRetention != CacheRetentionNone && supportsPromptCacheKey {
		sid := ClampOpenAIPromptCacheKey(opts.SessionID)
		req.PromptCacheKey = &sid
	}
	if longCache {
		req.PromptCacheRetention = new("24h")
	}
	// Request streaming usage tokens: gated on compat.supportsUsageInStreaming (default: true).
	supportsUsage := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsUsageInStreaming }, true)
	if supportsUsage {
		req.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	if cc := p.getCompatCacheControl(opts); cc != nil {
		applyAnthropicCacheControl(req.Messages, req.Tools, cc)
	}

	payload := any(req)
	// Last so model and request sampling parameters override named request fields (openai-completions.ts:1004-1007).
	if keys, samplingParams := resolveSamplingParamsInOrder(p.samplingModel(opts.IsReasoning), completionsSamplingLevel(opts), opts.SamplingParams, opts.SamplingParamsOrder); samplingParams != nil {
		assigned, err := assignSamplingParams(req, oaiRequestLiteralKeys, keys, samplingParams)
		if err != nil {
			return nil, fmt.Errorf("openai: %w", err)
		}
		payload = assigned
	}
	// Pi resolves the API key and builds the client (Azure's base URL resolver included) before it calls onPayload.
	baseURL := p.cfg.BaseURL
	if p.cfg.GetBaseURL != nil {
		if u, err := p.cfg.GetBaseURL(ctx); err != nil {
			// The resolver's own error is the rejection (azure-openai-config.ts throws it from the provider's stream setup).
			return nil, err
		} else if u != "" {
			baseURL = u
		}
	}

	apiKey := p.cfg.APIKey
	if p.cfg.GetAPIKey != nil {
		if k, err := p.cfg.GetAPIKey(ctx); err != nil {
			return nil, fmt.Errorf("%s: resolve API key: %w", p.cfg.ProviderID, err)
		} else {
			apiKey = k
		}
	}

	if opts.OnPayload != nil {
		next, err := opts.OnPayload(payload, providerEventModel(p.cfg.ModelMetadata, APIOpenAICompletions, p.cfg.ProviderID, p.cfg.Model))
		if err != nil {
			return nil, fmt.Errorf("openai: onPayload: %w", err)
		}
		if next != nil {
			payload = payloadReplacement(next)
		}
	}

	body, err := marshalSpliced(payload, requestStrings, marshalRequestBody)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	checkRequestSegments(ctx, APIOpenAICompletions, body)

	endpoint, err := openaiBuildURL(baseURL, "/chat/completions")
	if err != nil {
		return nil, err
	}
	httpReq, err := newSDKFetchRequest(ctx, http.MethodPost, endpoint, body.reader(), opts.Fetch != nil)
	if err != nil {
		return nil, err
	}
	body.prepare(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	// The OpenAI SDK's default `accept` header.
	httpReq.Header.Set("Accept", "application/json")
	// The identity headers are the lowest layer: model, provider and request headers override them (utils/identity.ts getIdentityHeaders).
	applyIdentityHeaders(httpReq.Header, ClientIdentityTarget{Provider: p.cfg.ProviderID, BaseURL: p.cfg.BaseURL}, opts.Identity)
	if apiKey != "" {
		if p.cfg.ProviderID == "cloudflare-ai-gateway" {
			httpReq.Header.Set("cf-aig-authorization", "Bearer "+apiKey)
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	for k, v := range p.cfg.ExtraHeaders {
		if strings.EqualFold(k, "Host") {
			httpReq.Host = v
		} else {
			httpReq.Header.Set(k, v)
		}
	}
	if p.cfg.DynamicHeaders != nil {
		for k, v := range p.cfg.DynamicHeaders(transcript, opts) {
			if strings.EqualFold(k, "Host") {
				httpReq.Host = v
			} else {
				httpReq.Header.Set(k, v)
			}
		}
	}
	if opts.SessionID != "" && cacheRetention != CacheRetentionNone && *p.compat().SendSessionAffinityHeaders {
		applyOpenAISessionAffinityHeaders(httpReq.Header, opts.SessionID, p.compat().SessionAffinityFormat, true)
	}
	applyProviderHeaders(httpReq, opts.Headers)

	builder := newObservedProviderBuilder(ctx, outputAPI(p.cfg.ModelMetadata, APIOpenAICompletions), p.cfg.ProviderID, p.cfg.Model)
	builder.transportCauseDetail = true
	builder.modelCost = opts.ModelCost
	builder.setProviderEventObserver(opts, providerEventModel(p.cfg.ModelMetadata, APIOpenAICompletions, p.cfg.ProviderID, p.cfg.Model))
	requestContext, abort := context.WithCancel(httpReq.Context())
	builder.abort = abort
	httpReq = httpReq.WithContext(requestContext)
	//portlint:allow asyncorder Pi's stream() starts a detached async IIFE and returns the stream (openai-completions.ts:320, :717); the stream owns this goroutine, which sends the request and runs the body pipeline as a turn of the stream's continuation executor (responseTurn in streamResponse), the ordering point with the consumer; coding's TestRPC33ObservationMatrix holds every observation of an immediate, delayed or held consumer of this API to Pi's
	go builder.runResponse(func() error { return p.streamResponse(ctx, httpReq, opts, builder, grammarProps) })
	return builder.stream, nil
}

func (p *openAIProvider) streamResponse(ctx context.Context, request *http.Request, opts StreamOptions, builder *assistantStreamBuilder, grammarProps map[string]string) error {
	var responseModel *Model
	if onResp := opts.OnResponse; onResp != nil {
		responseModel = &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: APIOpenAICompletions}}
		request = request.WithContext(withRetryErrorResponse(request.Context(), func(r *http.Response) error {
			return onResp(ctx, ProviderResponse{Status: r.StatusCode, Headers: headersToRecord(r.Header)}, responseModel)
		}))
	}
	resp, err := providerHTTPClient(p.client, opts.Fetch).Do(request)
	if err != nil {
		return openAIRequestError(ctx, err, "openai: request")
	}
	defer func() { _ = resp.Body.Close() }()
	_, builder.managed = resp.Body.(*observedResponseBody)
	return builder.responseTurn(func() error {
		if resp.StatusCode != http.StatusOK {
			if responseModel != nil {
				if obsErr := observeProviderResponse(ctx, opts, resp, responseModel); obsErr != nil {
					return obsErr
				}
			}
			body, _ := readWebText(resp.Body)
			noteFailureResponse(ctx, resp)
			return openAIHTTPError(resp.StatusCode, body)
		}
		if responseModel != nil {
			if err := observeProviderResponse(ctx, opts, resp, responseModel); err != nil {
				return err
			}
		}
		body := io.Reader(resp.Body)
		if observed, ok := resp.Body.(*observedResponseBody); ok {
			native := newNativeBodyIterator(ctx, observed, builder.turn, builder.abort)
			defer func() { _ = native.Close() }()
			body = native
		}
		builder.start()
		p.parseSSE(ctx, body, builder, grammarProps)
		return nil
	})
}

func resolveClampedThinkingBudget(level ModelThinkingLevel, custom *ThinkingBudgets, ceiling int) int {
	// Levels without a token budget of their own (xhigh, max and anything above) read the high budget.
	if clamped := ClampReasoning(level); clamped != ThinkingMinimal && clamped != ThinkingLow && clamped != ThinkingMedium {
		level = ThinkingHigh
	}
	budget := ThinkingBudgetForLevel(string(level), custom)
	return ClampThinkingBudgetToAnswerRoom(budget, ceiling)
}

func resolveChatTemplateValues(config map[string]any, reasoningOn bool, effort func() (string, bool), budget int) map[string]any {
	if len(config) == 0 {
		return nil
	}
	resolved := make(map[string]any, len(config))
	for key, value := range config {
		variable, ok := value.(map[string]any)
		if _, isArray := value.([]any); isArray {
			// resolveChatTemplateKwargValue treats every non-null object, an array included, as a placeholder.
			variable, ok = nil, true
		}
		if !ok {
			resolved[key] = value
			continue
		}
		if omit, _ := variable["omitWhenOff"].(bool); omit && !reasoningOn {
			continue
		}
		switch variable["$var"] {
		case "thinking.enabled":
			resolved[key] = reasoningOn
		case "thinking.budget":
			if reasoningOn && budget > 0 {
				resolved[key] = budget
			}
		default:
			// resolveChatTemplateKwargValue maps any other object, "thinking.effort" included, to the mapped effort.
			if value, emit := effort(); emit {
				resolved[key] = value
			}
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func resolveCompletionsCacheRetention(retention CacheRetention, env ProviderEnv) CacheRetention {
	if retention != "" {
		return retention
	}
	if getProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return CacheRetentionLong
	}
	return CacheRetentionShort
}

// supportsLongCacheRetention is the resolved compat flag (openai-completions.ts:1731 `model.compat.supportsLongCacheRetention ?? detected.supportsLongCacheRetention`); the detection lives in
// detectOpenAICompletionsCompat, which always sets it.
func (p *openAIProvider) supportsLongCacheRetention() bool {
	return p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsLongCacheRetention }, true)
}

func applyOpenAISessionAffinityHeaders(headers http.Header, sessionID string, format SessionAffinityFormat, completions bool) {
	if format == SessionAffinityOpenRouter {
		headers.Set("x-session-id", sessionID)
		return
	}
	if format == SessionAffinityOpenAI {
		if completions {
			headers.Set("session_id", sessionID)
		} else {
			// upstream: Pi c5d397835 sends session-id on Responses; nginx drops an underscored header by default and Envoy can reject it.
			headers.Set("session-id", sessionID)
		}
	}
	headers.Set("x-client-request-id", sessionID)
	if completions {
		headers.Set("x-session-affinity", sessionID)
	}
}

func (p *openAIProvider) parseSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder, grammarProps map[string]string) {
	// Track partial tool calls by index
	type partialTool struct {
		streamIndex int
		wireIndex   *int
		id          string
		name        string
	}
	partialsByWireIndex := map[int]*partialTool{}
	partialsByID := map[string]*partialTool{}
	nextStreamIndex := 0
	var streamedReasoningDetails []*openAIReasoningDetail
	thinkingContentIndex := -1
	textContentIndex := -1
	ensureThinkingBlock := func(signature string) int {
		if thinkingContentIndex < 0 {
			thinkingContentIndex = builder.thinkingBlockStart()
			builder.thinkingBlockSignature(thinkingContentIndex, signature)
			builder.touch()
		}
		return thinkingContentIndex
	}
	// reasoning_details are replay metadata: they accumulate in memory and are serialized once, when the thinking
	// block ends or the stream fails (openai-completions.ts applyStreamedReasoningDetails).
	applyStreamedReasoningDetails := func() {
		if streamedReasoningDetails != nil && thinkingContentIndex >= 0 {
			builder.thinkingBlockSignature(thinkingContentIndex, marshalOpenAIReasoningDetails(streamedReasoningDetails))
		}
	}
	finishStreamedBlocks := func() {
		builder.activeText = textContentIndex
		applyStreamedReasoningDetails()
		if thinkingContentIndex >= 0 {
			builder.finishBlocksWithThinking(thinkingContentIndex)
		} else {
			builder.finishBlocks()
		}
		textContentIndex = -1
		thinkingContentIndex = -1
	}
	fail := func(reason StopReason, err error) {
		applyStreamedReasoningDetails()
		builder.failUnfinished(reason, err)
	}
	done := func(reason StopReason) {
		finishStreamedBlocks()
		builder.done(reason, nil, builder.partial.ErrorMessage)
	}
	hasFinishReason := false
	var finishReason string
	finishReasonNotString := false
	supportsFinishReason := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsFinishReason }, true)
	openRouter := isOpenRouterModel(p.cfg.ProviderID, p.cfg.BaseURL)
	var nativeFinishReason string
	inferredStopReason := func() StopReason {
		if nextStreamIndex > 0 {
			return StopReasonToolUse
		}
		return StopReasonStop
	}

	var suspend func()
	if builder.turn != nil {
		// The decoder's awaits between two records read and write only its own state, so they are made at the provider's next await or observable step, in the same row (deferContinuation): observeValidProviderEvent, a body read or cancel, or the settle below.
		suspend = func() { deferContinuation(builder.turn) }
	}
	// The decoder publishes the message before it reads the next record only when the record changed it outside a push: each direct change
	// below touches the builder.
	decoder := newOpenAIStreamDecoder(r, builder.publishPending, suspend)
	decoder.decodeChunks()
	decoder.wrapNormalizeChatCompletionStream()
	for decoder.Next() {
		data := decoder.Event().Data

		var thrown string
		// The decoder decoded the common chunk while it checked the record; the chunk is valid until the next record.
		chunk, decodedFast := decoder.Chunk()
		if !decodedFast {
			chunk = oaiChunk{}
			text := jsjson.NormalizeIntegers([]byte(data))
			if err := jsstring.UnmarshalKeepingSurrogates(text, &chunk); err != nil {
				normalized, read, normalizable := []byte(nil), openAIChunkRead{}, false
				if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
					normalized, read, normalizable = normalizeOpenAIChunkJS(text)
				}
				chunk = oaiChunk{}
				if !normalizable || json.Unmarshal(normalized, &chunk) != nil {
					builder.settleDeferred()
					fail(StopReasonError, fmt.Errorf("openai-completions: invalid SSE JSON: %w", err))
					return
				}
				thrown = read.thrown
				if thrown == nullChoiceFinishReason {
					builder.settleDeferred()
					fail(StopReasonError, errors.New(thrown))
					return
				}
				if read.finishReason != nil && len(chunk.Choices) > 0 {
					chunk.Choices[0].FinishReason, chunk.Choices[0].finishReasonNotString = *read.finishReason, true
				}
			}
		}
		// The stream decoder accepted the record as valid JSON before yielding it: with json.Valid, or by the chunk decode, which reads only valid JSON
		// and answered validJSONParsesAsValue for it.
		var observeErr error
		if decodedFast {
			observeErr = builder.observeDecodedOpenAICompletionsProviderEvent(data, decoder.ChunkParsesAsValue(), decoder.ChunkHasAllFinishReasons())
		} else {
			observeErr = builder.observeOpenAICompletionsProviderEvent(data)
		}
		if observeErr != nil {
			fail(StopReasonError, observeErr)
			return
		}

		responseModel := openAIChunkResponseModel(chunk.Model, p.cfg.Model)
		if chunk.ID != "" && builder.partial.ResponseID == "" || responseModel != "" && builder.partial.ResponseModel == "" {
			builder.touch()
		}
		responseID := chunk.ID
		if decodedFast && responseID != "" && builder.partial.ResponseID == "" {
			// The fast decode's strings are substrings of the record. The message keeps its own copy of the id, so it does not hold the record's line buffer.
			responseID = strings.Clone(responseID)
		}
		builder.setResponseMetadata(responseID, responseModel, "", "", nil)

		if errorMessage, ok := openAIStreamErrorMessage(chunk.Error); ok {
			fail(StopReasonError, errors.New(errorMessage))
			return
		}

		// Usage replaces the current object only when this chunk supplies it; content-bearing usage chunks still process their choices.
		if chunk.Usage != nil {
			builder.setUsage(parseChunkUsage(chunk.Usage, builder.modelCost, openRouter))
			builder.touch()
		} else if len(chunk.Choices) > 0 && chunk.Choices[0].Usage != nil {
			// Fallback: some providers (e.g., Moonshot) return usage in
			// choice.usage instead of the standard chunk.usage.
			builder.setUsage(parseChunkUsage(chunk.Choices[0].Usage, builder.modelCost, openRouter))
			builder.touch()
		}

		for _, choice := range chunk.Choices[:min(1, len(chunk.Choices))] {
			if openRouter {
				if native := readOpenRouterNativeFinishReason(&choice); native != "" {
					if decodedFast {
						nativeFinishReason = strings.Clone(native)
					} else {
						nativeFinishReason = native
					}
				}
			}
			if choice.FinishReason != "" || choice.finishReasonNotString {
				hasFinishReason = true
				finishReason, finishReasonNotString = choice.FinishReason, choice.finishReasonNotString
				if decodedFast {
					// The message keeps its own copy of the raw finish reason, as it does of the id.
					finishReason = strings.Clone(finishReason)
				}
				builder.setResponseMetadata("", "", finishReason, "", nil)
				stopReason, errorMessage := mapChunkFinishReason(finishReason, finishReasonNotString)
				builder.partial.StopReason = stopReason
				if errorMessage != "" {
					builder.partial.ErrorMessage = errorMessage
				}
				if nativeFinishReason != "" {
					applyOpenRouterNativeFinishReason(builder.partial, finishReason, nativeFinishReason)
				}
				builder.touch()
			}
			delta := choice.Delta
			// Pi 63035734: reasoning and reasoning_details come before content and tool calls, so a chunk that carries both orders thinking before text.
			for _, reasoning := range []struct {
				field string
				value string
			}{
				{field: "reasoning_content", value: delta.ReasoningContent},
				{field: "reasoning", value: delta.Reasoning},
				{field: "reasoning_text", value: delta.ReasoningText},
			} {
				if reasoning.value != "" {
					signature := reasoning.field
					if p.cfg.ProviderID == "opencode-go" && signature == "reasoning" {
						signature = "reasoning_content"
					}
					contentIndex := ensureThinkingBlock(signature)
					builder.thinkingBlockDelta(contentIndex, reasoning.value)
					break
				}
			}
			for _, raw := range delta.ReasoningDetails {
				var appended bool
				streamedReasoningDetails, appended = appendOpenAIReasoningDetail(streamedReasoningDetails, raw)
				if !appended {
					continue
				}
				ensureThinkingBlock("")
				builder.touch()
			}
			if delta.Content != "" {
				if textContentIndex < 0 {
					textContentIndex = builder.textBlockStart()
				}
				builder.textBlockDelta(textContentIndex, delta.Content)
			}
			for _, tc := range delta.ToolCalls {
				functionName, functionArguments := "", ""
				if tc.Function != nil {
					functionName, functionArguments = tc.Function.Name, tc.Function.Arguments
				} else if tc.Custom != nil {
					functionName = tc.Custom.Name
				}
				var pt *partialTool
				if tc.Index != nil {
					pt = partialsByWireIndex[*tc.Index]
				}
				if pt == nil && tc.ID != "" {
					pt = partialsByID[tc.ID]
				}
				if pt == nil {
					pt = &partialTool{streamIndex: nextStreamIndex}
					nextStreamIndex++
				}
				if tc.Index != nil && pt.wireIndex == nil {
					pt.wireIndex = new(*tc.Index)
					partialsByWireIndex[*tc.Index] = pt
				}
				if tc.ID != "" {
					if pt.id == "" {
						pt.id = tc.ID
						if decodedFast {
							// The call keeps its own copy of the id and of the name, as the message does of the response id.
							pt.id = strings.Clone(tc.ID)
						}
					}
					partialsByID[tc.ID] = pt
				}
				if pt.name == "" {
					pt.name = functionName
					if decodedFast {
						pt.name = strings.Clone(functionName)
					}
				}
				// A call is a custom (grammar) call from the first delta that says so, or at once when it is created by one.
				var custom *customToolInput
				if tc.Custom != nil && tc.Function == nil {
					property := grammarProps[pt.name]
					if property == "" {
						property = "input"
					}
					custom = newCustomToolInput(property, "")
				}
				builder.toolCallStart(streamToolCallDelta{index: pt.streamIndex, id: pt.id, name: pt.name, custom: custom})
				if custom != nil {
					builder.convertToCustomToolCall(pt.streamIndex, custom)
				}
				argsDelta := ""
				if state := builder.toolCalls[pt.streamIndex]; state.custom == nil {
					if functionArguments != "" {
						argsDelta = functionArguments
					}
				} else if tc.Custom != nil && tc.Custom.Input != "" {
					var err error
					argsDelta, _, err = builder.appendCustomToolCallInput(pt.streamIndex, tc.Custom.Input)
					if err != nil {
						fail(StopReasonError, err)
						return
					}
				}
				builder.toolCallDelta(streamToolCallDelta{index: pt.streamIndex, id: pt.id, name: pt.name, argumentsDelta: argsDelta, skipEmpty: true})
			}
		}
		if thrown != "" {
			fail(StopReasonError, errors.New(thrown))
			return
		}
	}
	// The decoder's last awaits come before the abort check and the end of the message.
	builder.settleDeferred()
	if err := decoder.Err(); err != nil && !errors.Is(err, io.EOF) {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		fail(reason, err)
		return
	}
	finishStreamedBlocks()
	if err := ctx.Err(); err != nil {
		fail(StopReasonAborted, errors.New("Request was aborted"))
		return
	}
	// The SDK consumes DONE and drains its body before provider finalization. A missing finish reason is checked after block ends, as in the upstream producer.
	finishStreamedBlocks()
	switch {
	case hasFinishReason:
		if nativeFinishReason != "" {
			flagOpenRouterEmptyStop(builder.partial, nativeFinishReason)
		}
		if builder.partial.StopReason == StopReasonError {
			msg := builder.partial.ErrorMessage
			if msg == "" {
				msg = "Provider returned an error stop reason"
			}
			fail(StopReasonError, errors.New(msg))
			return
		}
		done(builder.partial.StopReason)
	case !supportsFinishReason:
		builder.partial.StopReason = inferredStopReason()
		if nativeFinishReason != "" {
			flagOpenRouterEmptyStop(builder.partial, nativeFinishReason)
		}
		if builder.partial.StopReason == StopReasonError {
			fail(StopReasonError, errors.New(builder.partial.ErrorMessage))
			return
		}
		done(builder.partial.StopReason)
	default:
		fail(StopReasonError, errors.New("Stream ended without finish_reason"))
	}
}

// openAIChunkResponseModel is the response model a chunk reports, "" when it reports none, one that is not a string, or the configured model. A model without escapes is compared in place, so the configured model costs no allocation.
func openAIChunkResponseModel(raw json.RawMessage, configured string) string {
	if model, ok := simpleJSONString(raw); ok {
		if string(model) == configured {
			return ""
		}
		return string(model)
	}
	var model string
	if json.Unmarshal(raw, &model) != nil || model == configured {
		return ""
	}
	return model
}

// mapChunkFinishReason is mapOAIFinishReason for a chunk's finish_reason; one that was not a string matches no case of mapStopReason's === switch, whatever
// its String() form.
func mapChunkFinishReason(reason string, notString bool) (StopReason, string) {
	if notString {
		return StopReasonError, "Provider finish_reason: " + reason
	}
	return mapOAIFinishReason(reason)
}

// mapOAIFinishReason maps OpenAI finish_reason values to canonical stop
// reasons and preserves provider failures for the assistant error boundary.
func mapOAIFinishReason(reason string) (StopReason, string) {
	switch reason {
	case "", "stop", "end":
		return StopReasonStop, ""
	case "length":
		return StopReasonLength, ""
	case "tool_calls", "function_call":
		return StopReasonToolUse, ""
	default:
		return StopReasonError, "Provider finish_reason: " + reason
	}
}

// completionsRole is the role name convertMessages tracks in lastRole.
func completionsRole(message Message) string {
	switch message.(type) {
	case SystemMessage:
		return "system"
	case UserMessage:
		return "user"
	case AssistantMessage:
		return "assistant"
	}
	return "toolResult"
}

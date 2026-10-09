package ai

// Ports packages/ai/src/api/openai-responses.ts.
// Ports packages/ai/src/api/openai-responses-shared.ts.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// ─── Config ──────────────────────────────────────────────────────────────────

// openAIResponsesMinOutputTokens is the floor OpenAI Responses accepts for
// max_output_tokens; values below 16 are rejected (earendil-works/pi#6265).
const openAIResponsesMinOutputTokens = 16

// OpenAIResponsesConfig configures the OpenAI Responses API provider.
type OpenAIResponsesConfig struct {
	api API
	// requestModel selects a deployment on the wire without changing logical model identity or replay capabilities.
	requestModel string
	// azure resolves the Azure endpoint and deployment per request (NewAzureOpenAIResponsesProvider).
	azure *azureResponsesEndpoint
	// BaseURL is the API base (default: https://api.openai.com/v1).
	BaseURL string
	// APIKey is the bearer token.
	APIKey string
	// Model is the logical model identity used for response metadata and replay capabilities.
	Model string
	// ModelMetadata supplies the selected model's thinking map and input capabilities.
	ModelMetadata *Model
	// ProviderID is the provider label (e.g. "openai", "github-copilot").
	ProviderID string
	// ExtraHeaders are added to every request.
	ExtraHeaders   map[string]string
	SamplingParams map[string]any
	// SamplingParamsByThinkingLevel overrides SamplingParams for the effective thinking level.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel
	// ThinkingLevelMap supplies an explicit map when selected ModelMetadata is absent.
	ThinkingLevelMap ThinkingLevelMap
	// APIKeyHeader is the header name used for the API key.
	// Default: Authorization.
	APIKeyHeader string
	// APIKeyPrefix is prepended when APIKeyHeader is used.
	// Default: Bearer . Empty string sends the raw key.
	APIKeyPrefix string
	// BaseURLIsEndpoint means BaseURL/GetBaseURL already resolves to the
	// final Responses endpoint and should not have /responses appended.
	BaseURLIsEndpoint bool
	// IsReasoning indicates this model supports extended reasoning.
	IsReasoning bool

	// GetAPIKey dynamically obtains the bearer token. Takes precedence over APIKey.
	GetAPIKey func(ctx context.Context) (string, error)
	// GetBaseURL dynamically resolves the base URL. Takes precedence over BaseURL.
	GetBaseURL func(ctx context.Context) (string, error)
	// DynamicHeaders returns per-request headers.
	DynamicHeaders func(transcript TranscriptContext, opts StreamOptions) map[string]string
	// Env holds provider-scoped environment overrides that take precedence over
	// the process environment for provider configuration (PI_CACHE_RETENTION,
	// Cloudflare base-URL placeholders). Mirrors upstream StreamOptions.env.
	Env ProviderEnv
	// Compat controls Responses-API-specific compat behavior.
	Compat *OpenAIResponsesCompat
	// StrictModeDefault is the provider-level default for supportsStrictMode when
	// the model compat does not set it. Upstream defaults false for the generic
	// openai-responses path and true for azure/codex.
	StrictModeDefault bool
	// StrictToolNull makes a strict-mode function tool default its strict field to
	// JSON null instead of false. Codex passes strict:null; other providers false.
	StrictToolNull bool
	// SkipServiceTierPricing leaves the response's service tier out of the
	// cost. Upstream Azure passes processResponsesStream no
	// applyServiceTierPricing; OpenAI and Codex apply it.
	SkipServiceTierPricing bool

	// ignoreSSEErrorObjects selects Codex's own SSE parser semantics. Unlike
	// openai-node, Codex does not special-case a top-level error object.
	ignoreSSEErrorObjects bool
	// Codex selects the ChatGPT Codex request and transport protocol rather
	// than the generic Responses protocol.
	Codex bool
}

type openAIResponsesProvider struct {
	cfg    OpenAIResponsesConfig
	client *http.Client
}

// NewOpenAIResponsesProvider creates a Provider backed by the OpenAI Responses API.
func NewOpenAIResponsesProvider(cfg OpenAIResponsesConfig) Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "openai"
	}
	if cfg.APIKeyHeader == "" {
		cfg.APIKeyHeader = "Authorization"
	}
	if cfg.APIKeyPrefix == "" && cfg.APIKeyHeader == "Authorization" {
		cfg.APIKeyPrefix = "Bearer "
	}
	return &openAIResponsesProvider{cfg: cfg, client: streamingHTTPClient()}
}

const chatGPTUsageURL = "https://chatgpt.com/settings/usage"

// withChatGPTUsageLink points a Sign in with ChatGPT user at their usage page: the subscription's usage limit is shared with other apps (openai-responses.ts stream catch).
func withChatGPTUsageLink(message string) string {
	if strings.Contains(message, "subscription_sharing_usage_limit_exceeded") {
		return message + "\nCheck your ChatGPT usage: " + chatGPTUsageURL
	}
	return message
}

// isChatGPTSignIn reports whether apiKey is a Sign in with ChatGPT access token: OpenAI API keys start with `sk-`, so a different credential sent directly to OpenAI is one.
// It rejects max_output_tokens, temperature and the prompt cache retention fields (openai-responses.ts isChatGPTSignIn).
func (p *openAIResponsesProvider) isChatGPTSignIn(apiKey string) bool {
	return p.cfg.ProviderID == "openai" && p.cfg.BaseURL == "https://api.openai.com/v1" && apiKey != "" && !strings.HasPrefix(apiKey, "sk-")
}

func (p *openAIResponsesProvider) ID() string   { return p.cfg.ProviderID }
func (p *openAIResponsesProvider) Close() error { return nil }

func (p *openAIResponsesProvider) api() API {
	if p.cfg.api != "" {
		return p.cfg.api
	}
	if p.cfg.Codex {
		return APIOpenAICodexResponses
	}
	return APIOpenAIResponses
}

// responsesCompat holds the resolved request flags. Long cache retention defaults to true; strict mode uses the provider default.
type responsesCompat struct {
	SupportsLongCacheRetention      bool
	SupportsStrictMode              bool
	SupportsOpenAIGrammarTools      bool
	SupportsMidConvoSystemMessages  bool
	SupportsAdditionalTools         bool
	SupportsExplicitPromptCacheMode bool
	SupportsToolSearch              bool
}

func getResponsesCompat(compat *OpenAIResponsesCompat, strictModeDefault bool) responsesCompat {
	out := responsesCompat{
		SupportsLongCacheRetention: true,
		SupportsStrictMode:         strictModeDefault,
	}
	if compat == nil {
		return out
	}
	if compat.SupportsLongCacheRetention != nil {
		out.SupportsLongCacheRetention = *compat.SupportsLongCacheRetention
	}
	if compat.SupportsStrictMode != nil {
		out.SupportsStrictMode = *compat.SupportsStrictMode
	}
	if compat.SupportsOpenAIGrammarTools != nil {
		out.SupportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
	}
	out.SupportsMidConvoSystemMessages = compat.SupportsMidConvoSystemMessages != nil && *compat.SupportsMidConvoSystemMessages
	out.SupportsAdditionalTools = compat.SupportsAdditionalTools != nil && *compat.SupportsAdditionalTools
	out.SupportsExplicitPromptCacheMode = compat.SupportsExplicitPromptCacheMode != nil && *compat.SupportsExplicitPromptCacheMode
	out.SupportsToolSearch = compat.SupportsToolSearch != nil && *compat.SupportsToolSearch
	return out
}

func getPromptCacheRetention(compat responsesCompat, cacheRetention string) *string {
	if cacheRetention != "long" || !compat.SupportsLongCacheRetention || compat.SupportsExplicitPromptCacheMode {
		return nil
	}
	ret := "24h"
	return &ret
}

// getPromptCacheOptions selects explicit writes or the supported long TTL for capable Responses models.
func getPromptCacheOptions(compat responsesCompat, retention string) map[string]string {
	if !compat.SupportsExplicitPromptCacheMode {
		return nil
	}
	if retention == "none" {
		return map[string]string{"mode": "explicit"}
	}
	if retention == "long" && compat.SupportsLongCacheRetention {
		return map[string]string{"ttl": "30m"}
	}
	return nil
}

// ─── Request wire types ──────────────────────────────────────────────────────

// ResponsesInputItem is one element of the Responses API "input" array.
// Upstream: ResponseInput (union of message | function_call | function_call_output | reasoning).
type ResponsesInputItem struct {
	// Common
	Type string `json:"type,omitempty"`
	Role string `json:"role,omitempty"`

	// For type=message (user/assistant/system/developer)
	Content json.RawMessage `json:"content,omitempty"`
	Status  string          `json:"status,omitempty"`
	ID      string          `json:"id,omitempty"`
	Phase   string          `json:"phase,omitempty"`

	// For type=function_call (a JSON string) and type=tool_search_call (an object)
	CallID    string          `json:"call_id,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// For type=tool_search_call / tool_search_output: "client"
	Execution string `json:"execution,omitempty"`
	// For type=additional_tools / tool_search_output
	Tools []ResponsesTool `json:"tools,omitempty"`

	// For type=custom_tool_call (grammar tools): the raw string input.
	Input string `json:"input,omitempty"`

	// For type=function_call_output
	Output json.RawMessage `json:"output,omitempty"`

	// For type=reasoning (opaque replay)
	Summary          json.RawMessage `json:"summary,omitempty"`
	EncryptedContent json.RawMessage `json:"encrypted_content,omitempty"`
}

// ResponsesTool is one entry of the Responses API "tools" array. A function tool
// carries parameters (+ strict when the model supports strict mode); a grammar
// (custom) tool carries a format block instead. Mirrors upstream OpenAITool.
type ResponsesTool struct {
	Type        string         `json:"type"` // "function" | "custom"
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Strict is a tri-state: absent (nil), null, true, or false. The default
	// varies by provider (generic omits it; codex sends null; strict-mode models
	// send false), so it is a raw JSON token rather than *bool.
	Strict json.RawMessage      `json:"strict,omitempty"`
	Format *ResponsesToolFormat `json:"format,omitempty"`
	// DeferLoading marks a tool loaded by a synthetic tool_search_output.
	DeferLoading bool `json:"defer_loading,omitempty"`
}

// respToolSearchArguments are the synthetic tool_search_call arguments.
type respToolSearchArguments struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// ResponsesToolFormat is a grammar (custom) tool's constrained-sampling format.
// Mirrors upstream OpenAITool custom.format.
type ResponsesToolFormat struct {
	Type       string `json:"type"`   // "grammar"
	Syntax     string `json:"syntax"` // "lark" | "regex"
	Definition string `json:"definition"`
}

type respReasoning struct {
	Effort           string   `json:"effort"`
	Summary          string   `json:"summary,omitempty"`
	EncryptedContent []string `json:"encrypted_content,omitempty"`
}

type respRequest struct {
	Model                string            `json:"model"`
	Input                json.RawMessage   `json:"input"`
	Stream               bool              `json:"stream"`
	Instructions         string            `json:"instructions,omitempty"`
	Tools                []ResponsesTool   `json:"tools,omitempty"`
	ToolChoice           any               `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	Text                 map[string]string `json:"text,omitempty"`
	MaxOutputTokens      int               `json:"max_output_tokens,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	ServiceTier          string            `json:"service_tier,omitempty"`
	Store                *bool             `json:"store,omitempty"`
	Reasoning            *respReasoning    `json:"reasoning,omitempty"`
	Include              []string          `json:"include,omitempty"`
	PromptCacheKey       string            `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string            `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   map[string]string `json:"prompt_cache_options,omitempty"`
}

// ─── SSE event types ─────────────────────────────────────────────────────────

// respSSEEvent is the envelope for all SSE events from the Responses API.
type respSSEEvent struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index,omitempty"`

	// response.created / response.completed / response.failed
	Response *respResponseObj `json:"response,omitempty"`

	// response.output_item.added / response.output_item.done
	Item json.RawMessage `json:"item,omitempty"`

	// response.output_text.delta / response.function_call_arguments.delta
	Delta string `json:"delta,omitempty"`

	// response.content_part.added
	Part json.RawMessage `json:"part,omitempty"`

	// response.function_call_arguments.done
	Arguments string `json:"arguments,omitempty"`

	// response.custom_tool_call_input.done: full grammar tool input string
	Input string `json:"input,omitempty"`

	// error object emitted inside the SSE data channel
	StreamError json.RawMessage `json:"error,omitempty"`

	// error event
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type respResponseObj struct {
	ID                string           `json:"id,omitempty"`
	Model             string           `json:"model,omitempty"`
	Output            []respOutputItem `json:"output,omitempty"`
	Status            string           `json:"status,omitempty"`
	EndTurn           *bool            `json:"end_turn,omitempty"`
	Usage             *respUsage       `json:"usage,omitempty"`
	ServiceTier       string           `json:"service_tier,omitempty"`
	Error             *respError       `json:"error,omitempty"`
	IncompleteDetails *respIncomplete  `json:"incomplete_details,omitempty"`
}

type respUsage struct {
	InputTokens         int                `json:"input_tokens"`
	OutputTokens        int                `json:"output_tokens"`
	TotalTokens         int                `json:"total_tokens"`
	InputTokensDetails  *respTokenDetail   `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *respOutputDetails `json:"output_tokens_details,omitempty"`
}

type respTokenDetail struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type respOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

func parseResponsesUsage(usage *respUsage) *Usage {
	if usage == nil {
		return nil
	}
	cached := 0
	cacheWrite := 0
	if usage.InputTokensDetails != nil {
		cached = usage.InputTokensDetails.CachedTokens
		cacheWrite = usage.InputTokensDetails.CacheWriteTokens
	}
	var reasoning *int
	if usage.OutputTokensDetails != nil {
		reasoning = new(usage.OutputTokensDetails.ReasoningTokens)
	}
	return &Usage{
		Input:       max(usage.InputTokens-cached-cacheWrite, 0),
		Output:      usage.OutputTokens,
		Reasoning:   reasoning,
		CacheRead:   cached,
		CacheWrite:  cacheWrite,
		TotalTokens: usage.TotalTokens,
	}
}

type respError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type respIncomplete struct {
	Reason string `json:"reason,omitempty"`
}

// respOutputItem represents an output item from the stream.
type respOutputItem struct {
	Type             string              `json:"type"` // "reasoning" | "message" | "function_call"
	ID               string              `json:"id,omitempty"`
	CallID           string              `json:"call_id,omitempty"`
	Name             string              `json:"name,omitempty"`
	Namespace        string              `json:"namespace,omitempty"`
	Arguments        string              `json:"arguments,omitempty"`
	EncryptedContent string              `json:"encrypted_content,omitempty"`
	Content          []respOutputContent `json:"content,omitempty"`
	Summary          []respOutputContent `json:"summary,omitempty"`
	Status           string              `json:"status,omitempty"`
	Phase            string              `json:"phase,omitempty"`
	Input            *string             `json:"input,omitempty"` // for custom_tool_call items
}

type respOutputContent struct {
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// ─── Message conversion ──────────────────────────────────────────────────────
// Mirrors upstream convertResponsesMessages in openai-responses-shared.ts.

// resolveResponsesModel resolves the one catalog entry backing this responses
// provider, using a single precedence for every capability lookup. Upstream
// carries a single resolved `model` object and reads both image support and the
// reasoning/thinking limits off it; pig reconstructs that entry from the
// provider config here so image gating (ToCapabilities) and thinking clamping
// (ToModel) never resolve two different entries. Codex models are cataloged
// under the `openai-codex/` prefix, so a codex-responses provider tries that
// first; every provider then falls back to ProviderID/Model and bare Model.
func (p *openAIResponsesProvider) resolveResponsesModel() (*GeneratedModel, bool) {
	if p.cfg.ProviderID == string(APIOpenAICodexResponses) {
		if generated, ok := LookupModel("openai-codex/" + p.cfg.Model); ok {
			return generated, true
		}
	}
	if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		return generated, true
	}
	if generated, ok := LookupModel(p.cfg.Model); ok {
		return generated, true
	}
	return nil, false
}

// samplingModel is the model view resolveSamplingParams reads: reasoning support and the thinking map clamp the level, and the configured defaults and per-level overrides are merged.
func (p *openAIResponsesProvider) samplingModel(opts StreamOptions) *Model {
	resolved := p.resolvedModel()
	return &Model{
		ProviderMeta:                  ProviderMetadata{Reasoning: p.cfg.IsReasoning || opts.IsReasoning},
		ThinkingLevelMap:              resolved.ThinkingLevelMap,
		SamplingParams:                p.cfg.SamplingParams,
		SamplingParamsByThinkingLevel: p.cfg.SamplingParamsByThinkingLevel,
	}
}

// responsesSamplingLevel is the thinking level whose sampling overrides apply: the request effort, else the simple reasoning level, else medium for a summary-only request, else off (openai-responses.ts buildParams).
func responsesSamplingLevel(opts StreamOptions) ModelThinkingLevel {
	switch {
	case opts.ReasoningEffort != "":
		return ModelThinkingLevel(opts.ReasoningEffort)
	case opts.Thinking != "":
		return ModelThinkingLevel(opts.Thinking)
	case opts.ReasoningSummary != "":
		return ThinkingMedium
	}
	return ThinkingOff
}

// resolvedModel uses selected metadata when supplied, otherwise the catalog or an unknown-model reasoning default.
func (p *openAIResponsesProvider) resolvedModel() *Model {
	if p.cfg.ModelMetadata != nil {
		return p.cfg.ModelMetadata
	}
	model := &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}}
	if generated, ok := p.resolveResponsesModel(); ok {
		model = generated.ToModel()
	}
	if p.cfg.ThinkingLevelMap != nil {
		model.ThinkingLevelMap = cloneThinkingLevelMap(p.cfg.ThinkingLevelMap)
		model.Capabilities.MaxThinking = thinkingMaxLevel(true, model.ThinkingLevelMap)
	}
	return model
}

// modelSupportsImages reports whether the configured responses model accepts
// image input, mirroring upstream's `model.input.includes("image")` gate. It
// reads image support off the same entry resolveResponsesModel backs the
// reasoning clamp with, so a codex or non-codex provider never gates images on a
// different catalog entry than the one that drives thinking.
func (p *openAIResponsesProvider) modelSupportsImages() bool {
	if p.cfg.ModelMetadata != nil {
		if p.cfg.ModelMetadata.Input != nil {
			return slices.Contains(p.cfg.ModelMetadata.Input, "image")
		}
		return p.cfg.ModelMetadata.Capabilities.SupportsImages
	}
	generated, ok := p.resolveResponsesModel()
	if !ok {
		return false
	}
	return generated.ToCapabilities().SupportsImages
}

// convertResponsesToolResultOutput ports upstream openai-responses-shared.ts
// convertToolResultOutput. Empty tool results become the "(no tool output)"
// placeholder; image-only results with no image-capable model become
// "(see attached image)"; when the model accepts image input and images are
// present the output is an input_text/input_image array so tool-result images
// ride inside the function_call_output rather than being dropped.
func convertResponsesToolResultOutput(content []ToolResultMessageContent, modelSupportsImage bool) json.RawMessage {
	var text strings.Builder
	var images []ImageContent
	for _, block := range content {
		switch block := block.(type) {
		case TextContent:
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(block.Text)
		case ImageContent:
			images = append(images, block)
		}
	}
	hasText := text.Len() > 0
	if len(images) == 0 || !modelSupportsImage {
		var s string
		switch {
		case hasText:
			s = text.String()
		case len(images) > 0:
			s = "(see attached image)"
		default:
			s = "(no tool output)"
		}
		raw, _ := json.Marshal(sanitizeSurrogates(s))
		return raw
	}
	parts := make([]map[string]string, 0, len(images)+1)
	if hasText {
		parts = append(parts, map[string]string{"type": "input_text", "text": sanitizeSurrogates(text.String())})
	}
	for _, image := range images {
		url := "data:" + image.MimeType + ";base64," + image.Data
		parts = append(parts, map[string]string{"type": "input_image", "detail": "auto", "image_url": url})
	}
	raw, _ := json.Marshal(parts)
	return raw
}

// ConvertResponsesMessagesOptions mirrors upstream ConvertResponsesMessagesOptions (openai-responses-shared.ts:124).
type ConvertResponsesMessagesOptions struct {
	// IncludeSystemPrompt nil is true: the transcript's leading system message becomes the first input item.
	IncludeSystemPrompt *bool
	// GrammarToolInputProperties maps a grammar tool's name to the argument property its custom input carries.
	GrammarToolInputProperties map[string]string
	// SupportsMidConvoSystemMessages sends later system messages in place; otherwise they fold into the leading prompt.
	SupportsMidConvoSystemMessages bool
	SupportsAdditionalTools        bool
	SupportsToolSearch             bool
	ToolOptions                    ConvertResponsesToolsOptions
}

// responsesMessageConverter holds what convertResponsesMessages reads from its model and options.
type responsesMessageConverter struct {
	providerID, modelID      string
	api                      API
	instructionRole          string
	supportsImages           bool
	allowedToolCallProviders map[string]bool
	options                  ConvertResponsesMessagesOptions
}

// responsesInstructionRole is upstream's instructionRole: developer for reasoning models unless compat sets supportsDeveloperRole
// false, otherwise system.
func responsesInstructionRole(isReasoning bool, supportsDeveloperRole *bool) string {
	if isReasoning && (supportsDeveloperRole == nil || *supportsDeveloperRole) {
		return "developer"
	}
	return "system"
}

// instructionRole is the role of a system message's text for this provider.
func (p *openAIResponsesProvider) instructionRole() string {
	var supportsDeveloperRole *bool
	if p.cfg.Compat != nil {
		supportsDeveloperRole = p.cfg.Compat.SupportsDeveloperRole
	}
	return responsesInstructionRole(p.cfg.IsReasoning, supportsDeveloperRole)
}

// messageConverter is the converter for this provider's configured model and compat flags.
func (p *openAIResponsesProvider) messageConverter(compat responsesCompat, grammarProps map[string]string) responsesMessageConverter {
	allowed := responsesToolCallProviders
	if p.api() == APIAzureOpenAIResponses {
		allowed = azureToolCallProviders
	}
	return responsesMessageConverter{
		providerID:               p.cfg.ProviderID,
		modelID:                  p.cfg.Model,
		api:                      p.api(),
		instructionRole:          p.instructionRole(),
		supportsImages:           p.modelSupportsImages(),
		allowedToolCallProviders: allowed,
		options: ConvertResponsesMessagesOptions{
			GrammarToolInputProperties:     grammarProps,
			SupportsMidConvoSystemMessages: compat.SupportsMidConvoSystemMessages,
			SupportsAdditionalTools:        compat.SupportsAdditionalTools,
			SupportsToolSearch:             compat.SupportsToolSearch,
			ToolOptions:                    p.responsesToolOptions(compat),
		},
	}
}

// systemToolAdditions mirrors upstream appendSystemToolAdditions: when the
// transcript anchors additions, a later system message's added tools load in
// place as an additional_tools item, or else as a synthetic client
// tool_search_call and tool_search_output pair.
func (c responsesMessageConverter) systemToolAdditions(message SystemMessage, seed string, anchorsAdditions bool) ([]ResponsesInputItem, error) {
	if !anchorsAdditions || len(message.ToolsAdded) == 0 || (!c.options.SupportsAdditionalTools && !c.options.SupportsToolSearch) {
		return nil, nil
	}
	options := c.options.ToolOptions
	if c.options.SupportsAdditionalTools {
		tools, err := ConvertResponsesTools(message.ToolsAdded, options)
		if err != nil {
			return nil, err
		}
		return []ResponsesInputItem{{Type: "additional_tools", Role: "developer", Tools: tools}}, nil
	}
	options.ToolSearchResult = true
	tools, err := ConvertResponsesTools(message.ToolsAdded, options)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(message.ToolsAdded))
	for i, tool := range message.ToolsAdded {
		names[i] = tool.Name
	}
	callID := "pi_tool_load_" + ShortHash(seed+":"+strings.Join(names, ","))
	arguments, _ := json.Marshal(respToolSearchArguments{Query: strings.Join(names, " "), Limit: len(names)})
	return []ResponsesInputItem{
		{Type: "tool_search_call", CallID: callID, Execution: "client", Status: "completed", Arguments: arguments},
		{Type: "tool_search_output", CallID: callID, Execution: "client", Status: "completed", Tools: tools},
	}, nil
}

func (p *openAIResponsesProvider) convertMessages(messages []Message, grammarProps map[string]string) ([]ResponsesInputItem, error) {
	return p.convertAnchoredMessages(messages, grammarProps, false)
}

// convertAnchoredMessages converts the conversation after the leading system
// message; anchorsAdditions is ResolveTranscriptTools over the whole
// transcript.
func (p *openAIResponsesProvider) convertAnchoredMessages(messages []Message, grammarProps map[string]string, anchorsAdditions bool) ([]ResponsesInputItem, error) {
	return p.messageConverter(getResponsesCompat(p.cfg.Compat, p.cfg.StrictModeDefault), grammarProps).convertAnchored(messages, anchorsAdditions)
}

// convertAnchored converts the conversation after the leading system message.
func (c responsesMessageConverter) convertAnchored(messages []Message, anchorsAdditions bool) ([]ResponsesInputItem, error) {
	items := []ResponsesInputItem{}
	messageIndex := 0
	supportsImages := c.supportsImages
	normalizedToolIDs := make(map[string]string)
	for _, message := range messages {
		switch message := message.(type) {
		case SystemMessage:
			additions, err := c.systemToolAdditions(message, fmt.Sprintf("system:%d", messageIndex), anchorsAdditions)
			if err != nil {
				return nil, err
			}
			items = append(items, additions...)
			if text := RenderSystemMessageUpdate(message); text != "" {
				content, _ := json.Marshal(sanitizeSurrogates(text))
				items = append(items, ResponsesInputItem{Role: c.instructionRole, Content: content})
			}
		case UserMessage:
			var parts []map[string]string
			switch content := message.Content.(type) {
			case UserText:
				parts = append(parts, map[string]string{"type": "input_text", "text": sanitizeSurrogates(string(content))})
			case UserContentBlocks:
				for _, block := range content {
					switch block := block.(type) {
					case TextContent:
						parts = append(parts, map[string]string{"type": "input_text", "text": sanitizeSurrogates(block.Text)})
					case ImageContent:
						parts = append(parts, map[string]string{"type": "input_image", "detail": "auto", "image_url": "data:" + block.MimeType + ";base64," + block.Data})
					}
				}
			}
			if len(parts) == 0 {
				continue
			}
			content, _ := json.Marshal(parts)
			items = append(items, ResponsesInputItem{Role: "user", Content: content})
		case ToolResultMessage:
			callID := message.ToolCallID
			if normalized, exists := normalizedToolIDs[callID]; exists {
				callID = normalized
			}
			if before, _, ok := strings.Cut(callID, "|"); ok {
				callID = before
			}
			resultType := "function_call_output"
			if _, isGrammar := c.options.GrammarToolInputProperties[message.ToolName]; isGrammar {
				resultType = "custom_tool_call_output"
			}
			items = append(items, ResponsesInputItem{Type: resultType, CallID: callID, Output: convertResponsesToolResultOutput(message.Content, supportsImages)})
		case AssistantMessage:
			sameProviderAPI := message.Provider == c.providerID && message.API == c.api
			sameModel := sameProviderAPI && message.Model == c.modelID
			initialItemCount := len(items)
			textBlockIndex := 0
			for _, block := range message.Content {
				switch block := block.(type) {
				case ThinkingContent:
					if block.ThinkingSignature != "" {
						var reasoning ResponsesInputItem
						if json.Unmarshal([]byte(block.ThinkingSignature), &reasoning) == nil && reasoning.Type == "reasoning" {
							items = append(items, reasoning)
						}
					}
				case TextContent:
					text := sanitizeSurrogates(block.Text)
					id := fmt.Sprintf("msg_pi_%d", messageIndex)
					if textBlockIndex > 0 {
						id = fmt.Sprintf("msg_pi_%d_%d", messageIndex, textBlockIndex)
					}
					phase := ""
					if signedID, signedPhase, ok := parseResponsesTextSignature(block.TextSignature); ok {
						if signedID != "" {
							id = signedID
							if len(id) > 64 {
								id = "msg_" + ShortHash(id)
							}
						}
						phase = signedPhase
					}
					textBlockIndex++
					content, _ := json.Marshal([]map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}})
					items = append(items, ResponsesInputItem{Type: "message", Role: "assistant", Content: content, Status: "completed", ID: id, Phase: phase})
				case ToolCall:
					normalized := block.ID
					if !sameModel {
						normalized = normalizeResponsesToolCallID(block.ID, c.allowedToolCallProviders, c.providerID, !sameProviderAPI)
					}
					normalizedToolIDs[block.ID] = normalized
					callID, itemID, _ := strings.Cut(normalized, "|")
					property, isGrammar := c.options.GrammarToolInputProperties[block.Name]
					// A different-model replay omits the item id to avoid reasoning pairing validation. An id that does not
					// match the replayed item type is dropped too: function_call ids must be fc_* and custom_tool_call ids
					// ctc_*, foreign ids are normalized to fc_*, and a call can switch type when grammar tool support differs.
					itemIDPrefix := "fc_"
					if isGrammar {
						itemIDPrefix = "ctc_"
					}
					if (sameProviderAPI && !sameModel) || !strings.HasPrefix(itemID, itemIDPrefix) {
						itemID = ""
					}
					namespace := ""
					if sameModel {
						namespace = block.Namespace
					}
					if isGrammar {
						input, err := getGrammarToolInput(block.Name, block.Arguments, property)
						if err != nil {
							return nil, err
						}
						items = append(items, ResponsesInputItem{Type: "custom_tool_call", ID: itemID, CallID: callID, Name: block.Name, Namespace: namespace, Input: sanitizeSurrogates(input)})
					} else {
						arguments, _ := block.ArgumentsJSON()
						encoded, _ := json.Marshal(string(arguments))
						items = append(items, ResponsesInputItem{Type: "function_call", ID: itemID, CallID: callID, Name: block.Name, Namespace: namespace, Arguments: encoded})
					}
				}
			}
			if len(items) == initialItemCount {
				continue
			}
		}
		messageIndex++
	}
	return items, nil
}

// prepareResponsesTranscript resolves the transcript's system updates, transforms its messages for the model and resolves which
// tools the request carries (convertResponsesMessages: resolveTranscript, transformMessages, resolveTranscriptTools).
func prepareResponsesTranscript(model *Model, transcript TranscriptContext, supportsMidConvoSystemMessages, supportsToolAdditions bool) ([]Message, TranscriptTools) {
	messages := TransformMessages(ResolveTranscript(transcript, supportsMidConvoSystemMessages).Messages(), model, nil)
	return messages, ResolveTranscriptTools(messages, supportsToolAdditions)
}

// withLeadingSystem puts the transcript's leading system message, when it has text, before the converted conversation.
func (c responsesMessageConverter) withLeadingSystem(messages []Message, items []ResponsesInputItem) []ResponsesInputItem {
	systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))])
	if systemPrompt == "" {
		return items
	}
	content, _ := json.Marshal(sanitizeSurrogates(systemPrompt))
	return append([]ResponsesInputItem{{Role: c.instructionRole, Content: content}}, items...)
}

// ConvertResponsesMessages converts a transcript to the Responses API "input" array for model: the leading system message (unless
// options.IncludeSystemPrompt is false), user, assistant and tool-result messages, with tool-call ids normalized for the providers
// in allowedToolCallProviders.
// Ports packages/ai/src/api/openai-responses-shared.ts convertResponsesMessages.
func ConvertResponsesMessages(model *Model, transcript TranscriptContext, allowedToolCallProviders map[string]bool, options ConvertResponsesMessagesOptions) ([]ResponsesInputItem, error) {
	messages, transcriptTools := prepareResponsesTranscript(model, transcript, options.SupportsMidConvoSystemMessages, options.SupportsAdditionalTools || options.SupportsToolSearch)
	var supportsDeveloperRole *bool
	if model.ProviderMeta.Compat != nil {
		supportsDeveloperRole = model.ProviderMeta.Compat.SupportsDeveloperRole
	}
	converter := responsesMessageConverter{
		providerID:               model.ProviderMeta.ProviderID,
		modelID:                  model.ID,
		api:                      model.ProviderMeta.API,
		instructionRole:          responsesInstructionRole(model.ProviderMeta.Reasoning, supportsDeveloperRole),
		supportsImages:           slices.Contains(model.Input, "image"),
		allowedToolCallProviders: allowedToolCallProviders,
		options:                  options,
	}
	items, err := converter.convertAnchored(WithoutInitialSystemMessage(messages), transcriptTools.AnchorsAdditions)
	if err != nil {
		return nil, err
	}
	if options.IncludeSystemPrompt == nil || *options.IncludeSystemPrompt {
		items = converter.withLeadingSystem(messages, items)
	}
	return items, nil
}

// responsesToolCallProviders lists the openai-responses providers whose models
// emit pipe-joined {call_id}|{item_id} tool-call ids that need normalization.
// Mirrors upstream openai-responses.ts OPENAI_TOOL_CALL_PROVIDERS.
var responsesToolCallProviders = map[string]bool{
	"openai":       true,
	"openai-codex": true,
	"opencode":     true,
}

// azureToolCallProviders mirrors upstream azure-openai-responses.ts AZURE_TOOL_CALL_PROVIDERS.
var azureToolCallProviders = map[string]bool{
	"openai":       true,
	"openai-codex": true,
	"opencode":     true,
	"azure":        true,
}

// codexResponseStatuses is the closed status set mapCodexEvents preserves.
// Unknown Codex statuses become omitted before the shared response parser maps
// them, so they stop successfully instead of becoming protocol errors.
var codexResponseStatuses = map[string]bool{
	"completed":   true,
	"incomplete":  true,
	"failed":      true,
	"cancelled":   true,
	"queued":      true,
	"in_progress": true,
}

// normalizeResponsesIDPart mirrors upstream openai-responses-shared.ts
// normalizeIdPart: sanitize to [A-Za-z0-9_-], cap at 64, drop trailing '_'.
func normalizeResponsesIDPart(part string) string {
	sanitized := toolCallIDForbidden.ReplaceAllString(part, "_")
	if len(sanitized) > 64 {
		sanitized = sanitized[:64]
	}
	return strings.TrimRight(sanitized, "_")
}

// buildForeignResponsesItemID mirrors upstream buildForeignResponsesItemId:
// fc_<shortHash>, capped at 64. ShortHash is byte-identical to upstream shortHash.
func buildForeignResponsesItemID(itemID string) string {
	normalized := "fc_" + ShortHash(itemID)
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return normalized
}

// normalizeResponsesToolCallID normalizes cross-model IDs for supported target providers. Source provider/API identity, not an item prefix, determines whether the item needs a foreign namespace.
func normalizeResponsesToolCallID(id string, allowed map[string]bool, providerID string, foreign bool) string {
	// upstream: packages/ai/src/api/openai-responses-shared.ts:normalizeToolCallId
	if !allowed[providerID] {
		return normalizeResponsesIDPart(id)
	}
	before, after, ok := strings.Cut(id, "|")
	if !ok {
		return normalizeResponsesIDPart(id)
	}
	callID := normalizeResponsesIDPart(before)
	var itemID string
	if foreign {
		itemID = buildForeignResponsesItemID(after)
	} else {
		itemID = normalizeResponsesIDPart(after)
	}
	if !strings.HasPrefix(itemID, "fc_") {
		itemID = normalizeResponsesIDPart("fc_" + itemID)
	}
	return callID + "|" + itemID
}

// ResponsesStrictDefault is the strict field of a function tool that no constrained-sampling request decides: absent from the
// options or false emits false, true emits true and null emits null (convertResponsesTools options.strict: boolean | null).
type ResponsesStrictDefault int

const (
	ResponsesStrictFalse ResponsesStrictDefault = iota
	ResponsesStrictTrue
	ResponsesStrictNull
)

// ConvertResponsesToolsOptions mirrors upstream ConvertResponsesToolsOptions (openai-responses-shared.ts:132).
type ConvertResponsesToolsOptions struct {
	// Strict is the strict field of a function tool that no constrained-sampling request decides.
	Strict ResponsesStrictDefault
	// SupportsStrictMode nil is true, as `options?.supportsStrictMode ?? true`.
	SupportsStrictMode *bool
	// SupportsOpenAIGrammarTools nil is false.
	SupportsOpenAIGrammarTools bool
	// ToolSearchResult marks every tool as loaded by a tool_search_output (defer_loading).
	ToolSearchResult bool
}

// ConvertResponsesTools maps tools to the Responses API "tools" array. A grammar constrained-sampling tool becomes a custom tool
// with a format block; a json_schema constrained tool requests strict mode when supported; any other function tool takes the
// default strict value and emits it only when the model supports strict mode.
// Ports packages/ai/src/api/openai-responses-shared.ts convertResponsesTools.
func ConvertResponsesTools(tools []ToolSchema, options ConvertResponsesToolsOptions) ([]ResponsesTool, error) {
	supportsStrictMode := options.SupportsStrictMode == nil || *options.SupportsStrictMode
	out := make([]ResponsesTool, 0, len(tools))
	for _, t := range tools {
		grammar, err := resolveGrammarConstrainedSampling(t, options.SupportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			out = append(out, ResponsesTool{
				Type:         "custom",
				Name:         t.Name,
				Description:  t.Description,
				Format:       &ResponsesToolFormat{Type: "grammar", Syntax: grammar.Format, Definition: grammar.Definition},
				DeferLoading: options.ToolSearchResult,
			})
			continue
		}
		constrained, err := resolveJSONSchemaStrictSampling(t, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		strict := options.Strict
		if constrained != nil && *constrained {
			strict = ResponsesStrictTrue
		}
		parameters, err := getJSONSchemaToolParameters(t, new(strict == ResponsesStrictTrue))
		if err != nil {
			return nil, err
		}
		ft := ResponsesTool{
			Type:         "function",
			Name:         t.Name,
			Description:  t.Description,
			Parameters:   parameters,
			DeferLoading: options.ToolSearchResult,
		}
		if supportsStrictMode {
			switch strict {
			case ResponsesStrictTrue:
				ft.Strict = json.RawMessage("true")
			case ResponsesStrictNull:
				ft.Strict = json.RawMessage("null")
			default:
				ft.Strict = json.RawMessage("false")
			}
		}
		out = append(out, ft)
	}
	return out, nil
}

// responsesToolOptions are the convertResponsesTools options this provider passes: its compat flags and, for Codex, strict: null.
func (p *openAIResponsesProvider) responsesToolOptions(compat responsesCompat) ConvertResponsesToolsOptions {
	options := ConvertResponsesToolsOptions{SupportsStrictMode: &compat.SupportsStrictMode, SupportsOpenAIGrammarTools: compat.SupportsOpenAIGrammarTools}
	if p.cfg.StrictToolNull {
		options.Strict = ResponsesStrictNull
	}
	return options
}

// ─── Stream ──────────────────────────────────────────────────────────────────

// Stream returns before HTTP setup settles. Successful setup admits start before waiting for body data.
func (p *openAIResponsesProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if p.cfg.azure != nil {
		p = p.cfg.azure.resolve(p, opts)
	}
	ctx = withProviderRequestOptions(ctx, opts)
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("openai-responses: invalid transcript: %w", err)
	}
	compat := getResponsesCompat(p.cfg.Compat, p.cfg.StrictModeDefault)
	target := &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{API: p.api(), ProviderID: p.cfg.ProviderID}, Input: []string{"text"}}
	if p.modelSupportsImages() {
		target.Input = append(target.Input, "image")
	}
	messages, transcriptTools := prepareResponsesTranscript(target, transcript, compat.SupportsMidConvoSystemMessages, compat.SupportsAdditionalTools || compat.SupportsToolSearch)
	tools := transcriptTools.RequestTools
	grammarProps, err := createGrammarToolInputProperties(GetDeclaredTools(messages), compat.SupportsOpenAIGrammarTools)
	if err != nil {
		return nil, err
	}
	converter := p.messageConverter(compat, grammarProps)
	msgs, err := converter.convertAnchored(WithoutInitialSystemMessage(messages), transcriptTools.AnchorsAdditions)
	if err != nil {
		return nil, err
	}
	if p.cfg.ProviderID == string(APIOpenAICodexResponses) && opts.Thinking != "" {
		opts.Thinking = ClampThinkingLevel(p.resolvedModel(), ModelThinkingLevel(opts.Thinking)).ReasoningOption()
	}
	if !p.cfg.Codex {
		msgs = converter.withLeadingSystem(messages, msgs)
	}

	// upstream resolves the API key before it builds the request: Sign in with ChatGPT changes the request fields.
	apiKey := p.cfg.APIKey
	if p.cfg.GetAPIKey != nil {
		if k, err := p.cfg.GetAPIKey(ctx); err != nil {
			return nil, fmt.Errorf("%s: resolve API key: %w", p.cfg.ProviderID, err)
		} else {
			apiKey = k
		}
	}

	inputJSON, marshalErr := json.Marshal(msgs)
	if marshalErr != nil {
		return nil, fmt.Errorf("openai-responses: marshal input: %w", marshalErr)
	}

	req := respRequest{
		Model:  cmp.Or(p.cfg.requestModel, p.cfg.Model),
		Input:  inputJSON,
		Stream: true,
	}
	store := false
	req.Store = &store
	req.ServiceTier = opts.ServiceTier
	if p.cfg.Codex {
		req.Instructions = "You are a helpful assistant."
		if initial := GetInitialSystemMessage(messages); initial != nil {
			if prompt := GetCurrentSystemPrompt([]Message{*initial}); prompt != "" {
				req.Instructions = sanitizeSurrogates(prompt)
			}
		}
		req.Text = map[string]string{"verbosity": cmp.Or(opts.TextVerbosity, "low")}
		req.ToolChoice = "auto"
		parallel := true
		req.ParallelToolCalls = &parallel
		req.Include = []string{"reasoning.encrypted_content"}
	}

	// upstream: packages/ai/src/api/openai-responses.ts:buildParams
	// Azure shares the same presence-based string/object tool-choice contract.
	if opts.ToolChoice != nil {
		req.ToolChoice = opts.ToolChoice
	}
	if len(tools) > 0 {
		convertedTools, err := ConvertResponsesTools(tools, p.responsesToolOptions(compat))
		if err != nil {
			return nil, err
		}
		req.Tools = convertedTools
	}
	omitUnsupportedFields := p.isChatGPTSignIn(apiKey)
	if !p.cfg.Codex && !omitUnsupportedFields && opts.MaxTokens > 0 && (p.cfg.Compat == nil || p.cfg.Compat.SupportsMaxOutputTokens == nil || *p.cfg.Compat.SupportsMaxOutputTokens) {
		// OpenAI Responses rejects max_output_tokens below 16 (earendil-works/pi#6265).
		req.MaxOutputTokens = max(opts.MaxTokens, openAIResponsesMinOutputTokens)
	}
	if !omitUnsupportedFields && (opts.TemperatureSet || opts.Temperature != 0) {
		req.Temperature = new(opts.Temperature)
	}

	// An omitted effort remains omitted; the model's off mapping controls the default request.
	// upstream: packages/ai/src/api/openai-responses.ts:buildParams
	if p.cfg.IsReasoning || opts.IsReasoning {
		model := p.resolvedModel()
		if model.Capabilities.MaxThinking == "" {
			model = new(*model)
			model.Capabilities.MaxThinking = ThinkingLevelHigh
		}
		clamped := ModelThinkingLevel(opts.Thinking)
		if opts.ReasoningEffort != "" {
			clamped = ModelThinkingLevel(opts.ReasoningEffort)
		} else if clamped != ThinkingOff && clamped != "" {
			clamped = ClampThinkingLevel(model, clamped)
		}
		switch {
		case clamped != ThinkingOff && clamped != "":
			effort := string(clamped)
			if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; ok && mapped != nil {
				effort = *mapped
			}
			req.Reasoning = &respReasoning{
				Effort:  effort,
				Summary: cmp.Or(opts.ReasoningSummary, "auto"),
			}
			req.Include = []string{"reasoning.encrypted_content"}
		case opts.ReasoningSummary != "":
			// A summary-only request asks for medium effort, unmapped (openai-responses.ts buildParams reasoningEffort).
			req.Reasoning = &respReasoning{
				Effort:  string(ThinkingMedium),
				Summary: opts.ReasoningSummary,
			}
			req.Include = []string{"reasoning.encrypted_content"}
		case p.cfg.ProviderID != "github-copilot":
			if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(ThinkingOff)]; !ok || mapped != nil {
				effort := "none"
				if mapped != nil {
					effort = *mapped
				}
				req.Reasoning = &respReasoning{Effort: effort}
			}
		}
		// xAI returns encrypted reasoning that must be replayed; request the
		// include even when thinking is off. Mirrors upstream openai-responses.ts.
		if p.cfg.ProviderID == "xai" {
			req.Include = []string{"reasoning.encrypted_content"}
		}
	}

	// Prompt caching.
	cacheRetention := string(opts.CacheRetention)
	if !p.cfg.Codex {
		cacheRetention = string(resolveCompletionsCacheRetention(opts.CacheRetention, mergeProviderEnv(p.cfg.Env, opts.Env)))
	}
	if opts.SessionID != "" && cacheRetention != "none" {
		req.PromptCacheKey = ClampOpenAIPromptCacheKey(opts.SessionID)
	}
	// Retention and write policy do not depend on a session-affinity key.
	if !p.cfg.Codex && !omitUnsupportedFields {
		if ret := getPromptCacheRetention(compat, cacheRetention); ret != nil {
			req.PromptCacheRetention = *ret
		}
		req.PromptCacheOptions = getPromptCacheOptions(compat, cacheRetention)
	}

	payload := any(req)
	// Last so model and request sampling parameters override named request fields (openai-responses.ts:383-385 buildParams).
	// openai-codex-responses.ts buildRequestBody applies no sampling parameters.
	var samplingParams map[string]any
	if !p.cfg.Codex {
		samplingParams = ResolveSamplingParams(p.samplingModel(opts), responsesSamplingLevel(opts), opts.SamplingParams)
	}
	if samplingParams != nil {
		encoded, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("openai-responses: marshal sampling base: %w", err)
		}
		var merged map[string]any
		if err := json.Unmarshal(encoded, &merged); err != nil {
			return nil, fmt.Errorf("openai-responses: decode sampling base: %w", err)
		}
		maps.Copy(merged, samplingParams)
		payload = merged
	}
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(payload, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if err != nil {
			return nil, fmt.Errorf("openai-responses: onPayload: %w", err)
		}
		if next != nil {
			payload = next
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai-responses: marshal request: %w", err)
	}

	baseURL := p.cfg.BaseURL
	if p.cfg.GetBaseURL != nil {
		if u, err := p.cfg.GetBaseURL(ctx); err != nil {
			return nil, fmt.Errorf("%s: resolve base URL: %w", p.cfg.ProviderID, err)
		} else if u != "" {
			baseURL = u
		}
	}
	baseURL, err = ResolveCloudflareBaseURL(p.cfg.ProviderID, baseURL, mergeProviderEnv(p.cfg.Env, opts.Env))
	if err != nil {
		return nil, fmt.Errorf("%s: resolve base URL: %w", p.cfg.ProviderID, err)
	}

	accountID := ""
	if p.cfg.Codex {
		accountID = CodexAccountID(apiKey)
		if accountID == "" {
			return nil, errors.New("openai-codex-responses: failed to extract accountId from token")
		}
	}

	endpointURL := strings.TrimRight(baseURL, "/")
	if !p.cfg.BaseURLIsEndpoint {
		endpointURL += "/responses"
	}
	if p.cfg.Codex && opts.Transport != TransportSSE {
		cacheSessionID := ""
		if cacheRetention != "none" {
			cacheSessionID = opts.SessionID
		}
		webSocketStream, err := p.startCodexWebSocket(ctx, endpointURL, body, apiKey, accountID, cacheSessionID, grammarProps, opts)
		if err != nil {
			return nil, err
		}
		if webSocketStream != nil {
			return webSocketStream, nil
		}
	}
	sseBody := body
	if p.cfg.Codex {
		sseBody = encodeZstdRawFrame(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpointURL, bytes.NewReader(sseBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", PiUserAgent())
	if p.cfg.Codex {
		httpReq.Header.Set("Accept", "text/event-stream")
		// pig divergence (D26): PiG names itself as the Codex originator. Like the User-Agent it is a default: model and caller headers override it (openai-codex-responses.ts buildBaseCodexHeaders, #10429).
		httpReq.Header.Set("originator", pigidentity.CodexOriginator)
	}
	if apiKey != "" {
		if p.cfg.ProviderID == "cloudflare-ai-gateway" {
			httpReq.Header.Set("cf-aig-authorization", "Bearer "+apiKey)
		} else {
			httpReq.Header.Set(p.cfg.APIKeyHeader, p.cfg.APIKeyPrefix+apiKey)
		}
	}
	if p.cfg.Codex {
		httpReq.Header.Set("chatgpt-account-id", accountID)
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
	if opts.SessionID != "" && cacheRetention != "none" {
		if p.cfg.Codex {
			sessionID := ClampOpenAIPromptCacheKey(opts.SessionID)
			httpReq.Header.Set("session-id", sessionID)
			httpReq.Header.Set("x-client-request-id", sessionID)
		} else {
			format := SessionAffinityOpenAI
			if p.cfg.ProviderID == "openrouter" || strings.Contains(p.cfg.BaseURL, "openrouter.ai") {
				format = SessionAffinityOpenRouter
			}
			if p.cfg.Compat != nil && p.cfg.Compat.SessionAffinityFormat != "" {
				format = p.cfg.Compat.SessionAffinityFormat
			}
			applyOpenAISessionAffinityHeaders(httpReq.Header, opts.SessionID, format, false)
		}
	}
	applyProviderHeaders(httpReq, opts.Headers)
	if p.cfg.Codex {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("chatgpt-account-id", accountID)
		httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Content-Encoding", "zstd")
		if opts.SessionID != "" && cacheRetention != "none" {
			sessionID := ClampOpenAIPromptCacheKey(opts.SessionID)
			httpReq.Header.Set("session-id", sessionID)
			httpReq.Header.Set("x-client-request-id", sessionID)
		}
	}
	builder := newObservedProviderBuilder(ctx, p.api(), p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	builder.requestServiceTier = requestServiceTierOf(opts)
	builder.setProviderEventObserver(opts, providerEventModel(p.cfg.ModelMetadata, p.api(), p.cfg.ProviderID, p.cfg.Model))
	if p.api() == APIOpenAIResponses {
		builder.errorMessage = withChatGPTUsageLink
	}
	requestContext, abort := context.WithCancel(httpReq.Context())
	builder.abort = abort
	httpReq = httpReq.WithContext(requestContext)
	go builder.runResponse(func() error { return p.streamResponse(ctx, httpReq, opts, builder, grammarProps) })
	return builder.stream, nil
}

func (p *openAIResponsesProvider) streamResponse(ctx context.Context, httpReq *http.Request, opts StreamOptions, builder *assistantStreamBuilder, grammarProps map[string]string) error {
	var resp *http.Response
	var err error
	var releaseRequest context.CancelFunc
	if p.cfg.Codex {
		resp, releaseRequest, err = p.doCodexSSERequest(httpReq, opts)
	} else {
		resp, err = providerHTTPClient(p.client, opts.Fetch).Do(httpReq)
	}
	if err != nil {
		if p.cfg.Codex {
			return err
		}
		return openAIRequestError(ctx, err, "openai-responses: request")
	}
	closeBody := sync.OnceFunc(func() {
		_ = resp.Body.Close()
		if releaseRequest != nil {
			releaseRequest()
		}
	})
	defer closeBody()
	_, builder.managed = resp.Body.(*observedResponseBody)
	// Codex SSE always runs as an executor turn: a body without a readiness owner is treated as always pending.
	builder.managed = builder.managed || p.cfg.Codex
	return builder.responseTurn(func() error {
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			if p.cfg.Codex {
				return fmt.Errorf("openai-responses: HTTP %d: %s", resp.StatusCode, string(b))
			}
			name := p.cfg.ProviderID
			// upstream: packages/ai/src/api/openai-responses.ts:stream
			if name == "openai" {
				name = "OpenAI"
			}
			// upstream: packages/ai/src/api/azure-openai-responses.ts:formatAzureOpenAIError
			if p.api() == APIAzureOpenAIResponses {
				name = "Azure OpenAI"
			}
			return openAIHTTPError(resp.StatusCode, b, name+" API error")
		}
		if !p.cfg.Codex {
			err := observeProviderResponse(ctx, opts, resp, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: p.api()}})
			if builder.turn != nil {
				// upstream: packages/ai/src/api/azure-openai-responses.ts:128, openai-responses.ts:177: `await options?.onResponse?.(...)` awaits the hook's result (one reaction even when no hook exists) before pushing start.
				suspendContinuation(builder.turn)
			}
			if err != nil {
				return err
			}
		}
		if p.cfg.Codex {
			if _, observed := resp.Body.(*observedResponseBody); !observed {
				stopAbort := context.AfterFunc(ctx, closeBody)
				defer stopAbort()
			}
			// fetch() resolves and `await options?.onResponse?.()` returns before start (openai-codex-responses.ts:400-414,471-473).
			for range codexFetchResolutionTicks + 1 {
				suspendContinuation(builder.turn)
			}
			reader := newCodexBodyReader(ctx, resp.Body, closeBody, builder.turn, builder.abort)
			defer reader.close()
			builder.start()
			p.parseResponses(ctx, newCodexSSEDecoder(ctx, reader, builder), builder, grammarProps)
			return nil
		}
		if !builder.managed {
			stopAbort := context.AfterFunc(ctx, closeBody)
			defer stopAbort()
		}
		bodyReader := io.Reader(resp.Body)
		if observed, ok := resp.Body.(*observedResponseBody); ok {
			native := newNativeBodyIterator(ctx, observed, builder.turn, builder.abort)
			defer func() { _ = native.Close() }()
			bodyReader = native
		}
		// Awaits in Pi's order after headers (openai-responses.ts:177-186, azure-openai-responses.ts:128-136):
		//  1. `await options?.onResponse?.(...)`: one reaction, modeled above.
		//  2. `stream.push({ type: "start" })`, synchronous.
		//  3. `await processResponsesStream(...)`: its only await is the `for await` over the SDK Stream (openai-responses-shared.ts:598), modeled by
		//     the native body iterator's first read and the openAIStreamDecoder generator chain (openai 6.40.0 core/streaming.mjs:28-48,191-245).
		builder.start()
		p.parseResponsesSSE(ctx, bodyReader, builder, grammarProps)
		return nil
	})
}

// getServiceTierCostMultiplier mirrors OpenAI and Codex service-tier pricing. Only OpenAI prices the "fast" tier, which GPT-6 models report for
// Fast mode (openai-responses.ts getServiceTierCostMultiplier); Codex bills it at the default rate (openai-codex-responses.ts getServiceTierCostMultiplier).
func getServiceTierCostMultiplier(modelID, serviceTier string, codex bool) float64 {
	switch serviceTier {
	case "flex":
		return 0.5
	case "fast":
		if codex {
			return 1
		}
		fallthrough
	case "priority":
		if modelID == "gpt-5.5" {
			return 2.5
		}
		return 2
	default:
		return 1
	}
}

// applyServiceTierPricing scales an already priced usage by the tier multiplier.
func applyServiceTierPricing(usage *Usage, serviceTier, modelID string, codex bool) {
	multiplier := getServiceTierCostMultiplier(modelID, serviceTier, codex)
	if multiplier == 1 {
		return
	}
	// float64() keeps each product rounded before the sum (no fused multiply-add).
	usage.Cost.Input = float64(usage.Cost.Input * multiplier)
	usage.Cost.Output = float64(usage.Cost.Output * multiplier)
	usage.Cost.CacheRead = float64(usage.Cost.CacheRead * multiplier)
	usage.Cost.CacheWrite = float64(usage.Cost.CacheWrite * multiplier)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

// ─── SSE parsing ─────────────────────────────────────────────────────────────
// Mirrors upstream processResponsesStream in openai-responses-shared.ts.

func (p *openAIResponsesProvider) parseResponsesSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder, grammarProps map[string]string) {
	var decoder providerSSEDecoder
	if p.cfg.Codex {
		raw := newSSEDecoder(r)
		raw.beforeNext = func() { builder.publish() }
		decoder = raw
	} else {
		var suspend func()
		if builder.turn != nil {
			suspend = func() { suspendContinuation(builder.turn) }
		}
		decoder = newOpenAIStreamDecoder(r, func() { builder.publish() }, suspend)
	}
	p.parseResponses(ctx, decoder, builder, grammarProps)
}

// OpenAIResponsesStreamOptions is openai-responses-shared.ts OpenAIResponsesStreamOptions: what a Responses adapter injects into
// processResponsesStream.
type OpenAIResponsesStreamOptions struct {
	// OnProviderStreamEvent observes each wire event before it is mapped, with the model processResponsesStream was given; its error ends
	// the stream. Nil observes nothing. It is StreamOptions.OnProviderStreamEvent (openai-responses-shared.ts:111).
	OnProviderStreamEvent func(ctx context.Context, data any, model *Model) error
	// ServiceTier is the request's service tier, which prices a response that reports none.
	ServiceTier string
	// GrammarToolInputProperties names the input property of each grammar tool by tool name.
	GrammarToolInputProperties map[string]string
	// ResolveServiceTier picks the tier that prices a response from the response's tier and the request's; nil picks the response's
	// tier, then the request's.
	ResolveServiceTier func(responseServiceTier, requestServiceTier string) string
	// ApplyServiceTierPricing scales the response's priced usage by its tier; nil leaves the usage at the default rate.
	ApplyServiceTierPricing func(usage *Usage, serviceTier string)

	// transport is how Go reads the stream: Codex frames and their termination are not part of the shared function upstream.
	transport responsesStreamTransport
	// model is processResponsesStream's model argument, the one OnProviderStreamEvent receives.
	model *Model
}

// responsesStreamTransport holds the Codex-only stream handling that openai-codex-responses.ts keeps outside processResponsesStream.
type responsesStreamTransport struct {
	codex                 bool
	ignoreSSEErrorObjects bool
	finishCodexSSE        func(ctx context.Context, builder *assistantStreamBuilder, decoder providerSSEDecoder, sawTerminal bool)
}

// parseResponses is processResponsesStream over an already constructed record decoder.
func (p *openAIResponsesProvider) parseResponses(ctx context.Context, decoder providerSSEDecoder, builder *assistantStreamBuilder, grammarProps map[string]string) {
	options := &OpenAIResponsesStreamOptions{
		OnProviderStreamEvent:      builder.providerEventOption,
		model:                      builder.providerEventModel,
		ServiceTier:                builder.requestServiceTier,
		GrammarToolInputProperties: grammarProps,
		transport: responsesStreamTransport{
			codex: p.cfg.Codex, ignoreSSEErrorObjects: p.cfg.ignoreSSEErrorObjects,
			finishCodexSSE: p.finishCodexSSE,
		},
	}
	if p.cfg.Codex {
		options.ResolveServiceTier = resolveCodexServiceTier
	}
	if !p.cfg.SkipServiceTierPricing {
		options.ApplyServiceTierPricing = func(usage *Usage, serviceTier string) {
			applyServiceTierPricing(usage, serviceTier, p.cfg.Model, p.cfg.Codex)
		}
	}
	processResponsesStream(ctx, decoder, builder, options)
}

// resolveCodexServiceTier is openai-codex-responses.ts resolveCodexServiceTier: a Codex response that reports "default" for a flex or
// priority request is priced at the requested tier.
func resolveCodexServiceTier(responseServiceTier, requestServiceTier string) string {
	if responseServiceTier == "default" && (requestServiceTier == "flex" || requestServiceTier == "priority") {
		return requestServiceTier
	}
	return cmp.Or(responseServiceTier, requestServiceTier)
}

// processResponsesStream maps an OpenAI Responses event stream onto builder (openai-responses-shared.ts processResponsesStream).
func processResponsesStream(ctx context.Context, decoder providerSSEDecoder, builder *assistantStreamBuilder, options *OpenAIResponsesStreamOptions) {
	grammarProps := options.GrammarToolInputProperties
	codex := options.transport.codex
	builder.providerEvent = nil
	if options.OnProviderStreamEvent != nil {
		builder.providerEvent = func(data any) error { return options.OnProviderStreamEvent(ctx, data, options.model) }
	}
	// Track current item state for multi-event sequences.
	type currentState struct {
		itemType     string
		contentIndex int
		toolIndex    int
		toolID       string
		toolName     string
		argsJSON     strings.Builder
		customProp   string
		customIn     string
		grammarBuf   *grammarToolInputJSONBuffer
	}
	states := make(map[int]*currentState)
	reasoningBlocksByID := make(map[string]int)
	nextToolIndex := 0
	createState := func(outputIndex int, item respOutputItem) *currentState {
		if item.Type == "message" && item.Phase == "final_answer" {
			builder.partial.StopReason = StopReasonStop
		}
		if state := states[outputIndex]; state != nil {
			return state
		}
		state := &currentState{itemType: item.Type, contentIndex: -1, toolIndex: -1}
		switch item.Type {
		case "reasoning":
			state.contentIndex = builder.thinkingBlockStart()
		case "message":
			state.contentIndex = builder.textBlockStart()
		case "function_call":
			state.toolIndex = nextToolIndex
			nextToolIndex++
			state.toolID = item.CallID + "|" + item.ID
			state.toolName = item.Name
			state.argsJSON.WriteString(item.Arguments)
			builder.toolCallStart(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, namespace: item.Namespace,
				initialJSON: item.Arguments, scratch: toolCallScratch{hasPartialJson: true, partialJson: item.Arguments}})
		case "custom_tool_call":
			state.toolIndex = nextToolIndex
			nextToolIndex++
			state.toolID = item.CallID + "|" + item.ID
			state.toolName = item.Name
			state.customProp = grammarProps[item.Name]
			if state.customProp == "" {
				state.customProp = "input"
			}
			if item.Input != nil {
				state.customIn = *item.Input
			}
			state.grammarBuf = &grammarToolInputJSONBuffer{}
			builder.toolCallStart(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, namespace: item.Namespace,
				initialArguments: JsonObject{state.customProp: state.customIn}, scratch: toolCallScratch{customInput: true, property: state.customProp}})
		default:
			return nil
		}
		states[outputIndex] = state
		return state
	}

	sawTerminal := false
	_, codexSSE := decoder.(*codexSSEDecoder)
	for decoder.Next() {
		if codex && !codexSSE && ctx.Err() != nil {
			builder.failUnfinished(StopReasonAborted, errors.New("Request was aborted"))
			return
		}
		data := decoder.Event().Data
		if data == "[DONE]" {
			break
		}

		var event respSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: invalid SSE JSON: %w", err))
			return
		}
		// Codex observes the raw event before mapping it (mapCodexEvents). processResponsesStream still awaits its own, empty, observer for it.
		if codex {
			builder.awaitProviderEvent()
		} else if err := builder.observeProviderEvent([]byte(data)); err != nil {
			builder.failUnfinished(StopReasonError, err)
			return
		}

		if !options.transport.ignoreSSEErrorObjects {
			if errorMessage, ok := openAIStreamErrorMessage(event.StreamError); ok {
				builder.failUnfinished(StopReasonError, errors.New(errorMessage))
				return
			}
		}

		switch event.Type {
		case "response.created":
			if event.Response != nil {
				builder.partial.ResponseID = event.Response.ID
			}

		case "response.output_item.added":
			var item respOutputItem
			if err := json.Unmarshal(event.Item, &item); err != nil {
				continue
			}
			// output_item.added always opens a new block and replaces the slot at its index (openai-responses-shared.ts createSlot); only output_item.done reuses one.
			// An item type without a block leaves the slot in place.
			switch item.Type {
			case "reasoning", "message", "function_call", "custom_tool_call":
				delete(states, event.OutputIndex)
			}
			createState(event.OutputIndex, item)

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "reasoning" {
				builder.thinkingBlockDelta(state.contentIndex, event.Delta)
			}

		case "response.reasoning_summary_part.done":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "reasoning" {
				builder.thinkingBlockDelta(state.contentIndex, "\n\n")
			}

		case "response.output_text.delta", "response.refusal.delta":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "message" {
				builder.textBlockDelta(state.contentIndex, event.Delta)
			}

		case "response.function_call_arguments.delta":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "function_call" {
				state.argsJSON.WriteString(event.Delta)
				builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, argumentsDelta: event.Delta})
			}

		case "response.function_call_arguments.done":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "function_call" {
				previous := state.argsJSON.String()
				if after, ok := strings.CutPrefix(event.Arguments, previous); ok && after != "" {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, argumentsDelta: after})
				}
				state.argsJSON.Reset()
				state.argsJSON.WriteString(event.Arguments)
				builder.setToolCallFinal(state.toolIndex, "", event.Arguments)
			}

		case "response.custom_tool_call_input.delta":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "custom_tool_call" {
				next := state.customIn + event.Delta
				delta, ok, err := appendGrammarToolInputJSONDelta(state.grammarBuf, state.customProp, next, false)
				state.customIn = next
				if err != nil {
					builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: %w", err))
					return
				}
				builder.setCustomToolCallInput(state.toolIndex, state.customProp, next, *state.grammarBuf)
				if ok {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, argumentsDelta: delta})
				}
			}

		case "response.custom_tool_call_input.done":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "custom_tool_call" {
				delta, ok, err := appendGrammarToolInputJSONDelta(state.grammarBuf, state.customProp, event.Input, true)
				state.customIn = event.Input
				if err != nil {
					builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: %w", err))
					return
				}
				builder.setCustomToolCallInput(state.toolIndex, state.customProp, event.Input, *state.grammarBuf)
				if ok {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, argumentsDelta: delta})
				}
			}

		case "response.output_item.done":
			var item respOutputItem
			if err := json.Unmarshal(event.Item, &item); err != nil {
				continue
			}
			state := createState(event.OutputIndex, item)
			if state == nil || state.itemType != item.Type {
				continue
			}
			switch item.Type {
			case "reasoning":
				finalText := joinResponseItemText(item.Summary)
				if finalText == "" {
					finalText = joinResponseItemText(item.Content)
				}
				if finalText == "" {
					finalText = builder.partial.Content[state.contentIndex].(ThinkingContent).Thinking
				}
				builder.thinkingBlockEnd(state.contentIndex, finalText, string(event.Item))
				reasoningBlocksByID[item.ID] = state.contentIndex
			case "message":
				var finalText strings.Builder
				for _, part := range item.Content {
					if part.Type == "output_text" {
						finalText.WriteString(part.Text)
					} else {
						finalText.WriteString(part.Refusal)
					}
				}
				builder.textBlockEnd(state.contentIndex, finalText.String(), encodeResponsesTextSignature(item.ID, item.Phase))
			case "function_call":
				arguments := item.Arguments
				if arguments == "" {
					arguments = state.argsJSON.String()
				}
				if arguments == "" {
					arguments = "{}"
				}
				builder.setToolCallFinal(state.toolIndex, item.Namespace, arguments)
				builder.endToolCall(state.toolIndex)
			case "custom_tool_call":
				finalInput := state.customIn
				if item.Input != nil {
					finalInput = *item.Input
				}
				delta, ok, err := appendGrammarToolInputJSONDelta(state.grammarBuf, state.customProp, finalInput, true)
				if err != nil {
					builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: %w", err))
					return
				}
				builder.setCustomToolCallInput(state.toolIndex, state.customProp, finalInput, *state.grammarBuf)
				if ok {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, argumentsDelta: delta})
				}
				if item.Namespace != "" {
					index := builder.toolCalls[state.toolIndex].contentIndex
					block := builder.partial.Content[index].(ToolCall)
					block.Namespace = item.Namespace
					builder.partial.Content[index] = block
				}
				builder.endToolCall(state.toolIndex)
			}
			delete(states, event.OutputIndex)

		case "response.completed", "response.incomplete", "response.done":
			if event.Type == "response.done" && !codex {
				continue
			}
			if event.Response == nil {
				builder.failUnfinished(StopReasonError, errors.New("openai-responses: terminal response is missing response metadata"))
				return
			}
			for _, item := range event.Response.Output {
				contentIndex, ok := reasoningBlocksByID[item.ID]
				if !ok || item.Type != "reasoning" || item.EncryptedContent == "" {
					continue
				}
				block := builder.partial.Content[contentIndex].(ThinkingContent)
				var stored map[string]any
				if json.Unmarshal([]byte(block.ThinkingSignature), &stored) != nil {
					continue
				}
				if existing, _ := stored["encrypted_content"].(string); existing != "" {
					continue
				}
				stored["encrypted_content"] = item.EncryptedContent
				if encoded, err := json.Marshal(stored); err == nil {
					block.ThinkingSignature = string(encoded)
					builder.partial.Content[contentIndex] = block
				}
			}
			var usage *Usage
			if u := event.Response.Usage; u != nil {
				cached := 0
				cacheWrite := 0
				if u.InputTokensDetails != nil {
					cached = u.InputTokensDetails.CachedTokens
					cacheWrite = u.InputTokensDetails.CacheWriteTokens
				}
				reasoning := 0
				if u.OutputTokensDetails != nil {
					reasoning = u.OutputTokensDetails.ReasoningTokens
				}
				usage = &Usage{
					Input:       max(u.InputTokens-cached-cacheWrite, 0),
					Output:      u.OutputTokens,
					Reasoning:   &reasoning,
					CacheRead:   cached,
					CacheWrite:  cacheWrite,
					TotalTokens: u.TotalTokens,
				}
				builder.calculateCost(usage)
				if options.ApplyServiceTierPricing != nil {
					var serviceTier string
					if options.ResolveServiceTier != nil {
						serviceTier = options.ResolveServiceTier(event.Response.ServiceTier, options.ServiceTier)
					} else {
						serviceTier = cmp.Or(event.Response.ServiceTier, options.ServiceTier)
					}
					options.ApplyServiceTierPricing(usage, serviceTier)
				}
				builder.setUsage(usage)
			}
			rawStopReason := event.Response.Status
			if codex && !codexResponseStatuses[rawStopReason] {
				rawStopReason = ""
				event.Response.Status = ""
			}
			incompleteReason := ""
			if event.Response.IncompleteDetails != nil {
				incompleteReason = event.Response.IncompleteDetails.Reason
				if incompleteReason != "" {
					rawStopReason += "." + incompleteReason
				}
			}
			stopReason, errorMessage := mapRespStatus(event.Response.Status, incompleteReason)
			if stopReason == StopReasonStop && responseBuilderHasToolCall(builder) {
				stopReason = StopReasonToolUse
			}
			if event.Response.ID != "" {
				builder.partial.ResponseID = event.Response.ID
			}
			builder.setResponseMetadata("", "", rawStopReason, "", event.Response.EndTurn)
			builder.partial.StopReason = stopReason
			builder.partial.ErrorMessage = errorMessage
			sawTerminal = true
			if codex && !codexSSE {
				if unfinished := unfinishedToolCallError(builder); unfinished != nil {
					builder.failUnfinished(StopReasonError, unfinished)
				} else if stopReason == StopReasonError {
					builder.failUnfinished(stopReason, errors.New(errorMessage))
				} else {
					builder.done(stopReason, nil, "")
				}
				return
			}

		case "response.failed":
			msg := "Unknown error (no error details in response)"
			if event.Response != nil {
				builder.setResponseMetadata(event.Response.ID, "", event.Response.Status, "", nil)
				builder.setUsage(parseResponsesUsage(event.Response.Usage))
				if e := event.Response.Error; e != nil {
					code := cmp.Or(e.Code, "unknown")
					message := cmp.Or(e.Message, "no message")
					msg = code + ": " + message
				} else if d := event.Response.IncompleteDetails; d != nil && d.Reason != "" {
					msg = "incomplete: " + d.Reason
				}
			}
			builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: %s", msg))
			return

		case "error":
			builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: error %s: %s", event.Code, event.Message))
			return
		}
	}

	if codexSSE {
		options.transport.finishCodexSSE(ctx, builder, decoder, sawTerminal)
		return
	}
	if codex && ctx.Err() != nil {
		builder.failUnfinished(StopReasonAborted, errors.New("Request was aborted"))
		return
	}
	if err := decoder.Err(); err != nil && !errors.Is(err, io.EOF) {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		builder.failUnfinished(reason, err)
		return
	}
	if sawTerminal {
		if err := unfinishedToolCallError(builder); err != nil {
			builder.failUnfinished(StopReasonError, err)
			return
		}
		if builder.turn != nil {
			// processResponsesStream resolves before the outer Responses producer checks cancellation and publishes done.
			suspendContinuation(builder.turn)
		}
		switch {
		case ctx.Err() != nil:
			builder.failUnfinished(StopReasonAborted, errors.New("Request was aborted"))
		case builder.partial.StopReason == StopReasonError:
			builder.failUnfinished(StopReasonError, errors.New(builder.partial.ErrorMessage))
		default:
			builder.done(builder.partial.StopReason, nil, "")
		}
		return
	}
	if err := ctx.Err(); err != nil {
		if codex {
			err = errors.New("Request was aborted")
		} else {
			err = errors.New("OpenAI Responses stream ended before a terminal response event")
		}
		builder.failUnfinished(StopReasonAborted, err)
		return
	}
	builder.failUnfinished(StopReasonError, errors.New("OpenAI Responses stream ended before a terminal response event"))
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func joinResponseItemText(parts []respOutputContent) string {
	values := make([]string, len(parts))
	for index, part := range parts {
		values[index] = part.Text
	}
	return strings.Join(values, "\n\n")
}

// TextSignatureV1 is the JSON payload an OpenAI Responses text block keeps in TextContent.TextSignature (types.ts TextSignatureV1). V is always 1; Phase is "commentary" or "final_answer" when set.
type TextSignatureV1 struct {
	V     int    `json:"v"`
	ID    string `json:"id"`
	Phase string `json:"phase,omitempty"`
}

func parseResponsesTextSignature(signature string) (id, phase string, ok bool) {
	if signature == "" {
		return "", "", false
	}
	if strings.HasPrefix(signature, "{") {
		var parsed struct {
			V     int     `json:"v"`
			ID    *string `json:"id"`
			Phase string  `json:"phase"`
		}
		if json.Unmarshal([]byte(signature), &parsed) == nil && parsed.V == 1 && parsed.ID != nil {
			if parsed.Phase != "commentary" && parsed.Phase != "final_answer" {
				parsed.Phase = ""
			}
			return *parsed.ID, parsed.Phase, true
		}
	}
	return signature, "", true
}

func encodeResponsesTextSignature(id, phase string) string {
	signature := TextSignatureV1{V: 1, ID: id}
	if phase == "commentary" || phase == "final_answer" {
		signature.Phase = phase
	}
	encoded, _ := json.Marshal(signature)
	return string(encoded)
}

// unfinishedToolCallError refuses to hand over a completed tool-use message that holds a tool call whose output_item.done never arrived: its arguments may be cut off or mixed up,
// for example when a non-compliant server omits output_index. Finished calls have their scratch buffers cleared (openai-responses-shared.ts processResponsesStream).
func unfinishedToolCallError(builder *assistantStreamBuilder) error {
	if builder.partial.StopReason != StopReasonToolUse {
		return nil
	}
	for _, block := range builder.partial.Content {
		if call, ok := block.(ToolCall); ok && (call.scratch.hasPartialJson || call.scratch.customInput) {
			return fmt.Errorf("OpenAI Responses stream completed with an unfinished tool call: %s (%s)", call.Name, call.ID)
		}
	}
	return nil
}

func responseBuilderHasToolCall(builder *assistantStreamBuilder) bool {
	for _, block := range builder.partial.Content {
		if _, ok := block.(ToolCall); ok {
			return true
		}
	}
	return false
}

// mapRespStatus maps OpenAI Responses API response status to canonical
// stop reason. Mirrors upstream openai-responses-shared.ts mapStopReason.
func mapRespStatus(status, incompleteReason string) (StopReason, string) {
	switch status {
	case "", "completed", "in_progress", "queued":
		return StopReasonStop, ""
	case "incomplete":
		if incompleteReason == "max_output_tokens" {
			return StopReasonLength, ""
		}
		if incompleteReason == "" {
			return StopReasonError, "Response incomplete without a provider reason"
		}
		return StopReasonError, "Response incomplete: " + incompleteReason
	case "failed", "cancelled":
		return StopReasonError, "openai-responses: response " + status
	default:
		return StopReasonError, "openai-responses: unhandled stop reason: " + status
	}
}

// requestServiceTierOf is the requested tier that prices a response without its own tier: the serviceTier option, not a
// sampling parameter that overrode the request field (openai-responses.ts:196, openai-codex-responses.ts:674).
func requestServiceTierOf(opts StreamOptions) string {
	return opts.ServiceTier
}

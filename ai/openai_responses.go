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
	"net/http"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
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

// chatGPTWorkspaceLimitHint is openai-responses.ts CHATGPT_WORKSPACE_LIMIT_HINT.
const chatGPTWorkspaceLimitHint = "You've reached your ChatGPT workspace's usage limit. Ask your workspace admin to increase it at admin.openai.com → Workspace → Usage limits."

// withChatGPTUsageLink points a Sign in with ChatGPT user at their usage page: the subscription's usage limit is shared with other apps.
// On ChatGPT Business plans the workspace admin also sets a per-user limit, and the hint names the admin (openai-responses.ts stream catch, Pi cedefc5326).
func withChatGPTUsageLink(message string) string {
	if strings.Contains(message, "subscription_sharing_usage_limit_exceeded") {
		return message + "\nCheck your ChatGPT usage: " + chatGPTUsageURL
	}
	if strings.Contains(message, "subscription_sharing_workplace_individual_usage_limit_exceeded") {
		return message + "\n" + chatGPTWorkspaceLimitHint
	}
	return message
}

// configurationUpdateRequestEffort is the fixed reasoning.effort while configuration_update items carry the thinking
// level (openai-responses.ts CONFIGURATION_UPDATE_REQUEST_EFFORT).
const configurationUpdateRequestEffort = "medium"

// configurationUpdateEffort is upstream getConfigurationUpdateEffort: the mapped effort to send in configuration_update
// items, or "" to send it as reasoning.effort. It needs the model's supportsMidConvoEffort compat and a requested level;
// Sign in with ChatGPT rejects the items.
func (p *openAIResponsesProvider) configurationUpdateEffort(compat responsesCompat, opts StreamOptions, apiKey string) string {
	// Only the openai-responses module reads it; the Azure and Codex modules do not.
	if p.api() != APIOpenAIResponses || !compat.SupportsMidConvoEffort || p.isChatGPTSignIn(apiKey) {
		return ""
	}
	model := p.resolvedModel()
	level := ModelThinkingLevel(opts.ReasoningEffort)
	if level == "" && opts.Thinking != "" {
		level = ClampThinkingLevel(model, ModelThinkingLevel(opts.Thinking))
	}
	if level == "" || level == ThinkingOff {
		return ""
	}
	if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
		return *mapped
	}
	return string(level)
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
	SupportsMidConvoEffort          bool
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
	out.SupportsMidConvoEffort = compat.SupportsMidConvoEffort != nil && *compat.SupportsMidConvoEffort
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

	// For type=configuration_update
	Reasoning *respReasoningEffort `json:"reasoning,omitempty"`
}

// respReasoningEffort is the reasoning member of a configuration_update item.
type respReasoningEffort struct {
	Effort string `json:"effort"`
}

// ResponsesTool is one entry of the Responses API "tools" array. A function tool
// carries parameters (+ strict when the model supports strict mode); a grammar
// (custom) tool carries a format block instead. Mirrors upstream OpenAITool.
type ResponsesTool struct {
	parameterOrder schemaObjectOrder
	Type           string         `json:"type"` // "function" | "custom"
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	// Strict is a tri-state: absent (nil), null, true, or false. The default
	// varies by provider (generic omits it; codex sends null; strict-mode models
	// send false), so it is a raw JSON token rather than *bool.
	Strict json.RawMessage      `json:"strict,omitempty"`
	Format *ResponsesToolFormat `json:"format,omitempty"`
	// DeferLoading marks a tool loaded by a synthetic tool_search_output.
	DeferLoading bool `json:"defer_loading,omitempty"`
}

// MarshalJSON writes a tool's members in the order convertResponsesTools creates them: strict is assigned after the literal that holds
// defer_loading (openai-responses-shared.ts:386-393), and the parameters keep the tool schema's own member order.
func (t ResponsesTool) MarshalJSON() ([]byte, error) {
	wire, err := t.wire()
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}

// responsesToolWire is the value ResponsesTool's MarshalJSON writes.
type responsesToolWire struct {
	Type         string               `json:"type"`
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Parameters   json.RawMessage      `json:"parameters,omitempty"`
	Format       *ResponsesToolFormat `json:"format,omitempty"`
	DeferLoading bool                 `json:"defer_loading,omitempty"`
	Strict       json.RawMessage      `json:"strict,omitempty"`
}

func (t ResponsesTool) wire() (responsesToolWire, error) {
	var parameters json.RawMessage
	if t.Parameters != nil {
		encoded, err := marshalToolParameters(t.Parameters, t.parameterOrder)
		if err != nil {
			return responsesToolWire{}, err
		}
		parameters = encoded
	}
	return responsesToolWire{t.Type, t.Name, t.Description, parameters, t.Format, t.DeferLoading, t.Strict}, nil
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

// respRequest is the openai-responses and openai-codex-responses request body. JSON.stringify writes each body in the order its builder
// creates the keys: the fields follow openai-responses.ts buildParams (:330-381), and MarshalJSON writes a Codex body in
// openai-codex-responses.ts buildRequestBody's order (:553-596).
type respRequest struct {
	Model                string            `json:"model"`
	Input                json.RawMessage   `json:"input"`
	Stream               bool              `json:"stream"`
	PromptCacheKey       string            `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string            `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   map[string]string `json:"prompt_cache_options,omitempty"`
	Store                *bool             `json:"store,omitempty"`
	MaxOutputTokens      int               `json:"max_output_tokens,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	ServiceTier          string            `json:"service_tier,omitempty"`
	Tools                []ResponsesTool   `json:"tools,omitempty"`
	ToolChoice           any               `json:"tool_choice,omitempty"`
	Reasoning            *respReasoning    `json:"reasoning,omitempty"`
	Include              []string          `json:"include,omitempty"`
	// Instructions, Text and ParallelToolCalls are Codex members.
	Instructions      string            `json:"instructions,omitempty"`
	Text              map[string]string `json:"text,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`

	// codex selects buildRequestBody's member order.
	codex bool
}

// MarshalJSON writes the body in the member order of the builder that made it.
func (req respRequest) MarshalJSON() ([]byte, error) {
	return marshalStringify(req.wire())
}

// wire is the value MarshalJSON writes.
func (req respRequest) wire() any {
	if !req.codex {
		type fields respRequest
		return fields(req)
	}
	return struct {
		Model                string            `json:"model"`
		Store                *bool             `json:"store,omitempty"`
		Stream               bool              `json:"stream"`
		Instructions         string            `json:"instructions,omitempty"`
		Input                json.RawMessage   `json:"input"`
		Text                 map[string]string `json:"text,omitempty"`    //portlint:allow emptydrop always the one-member {verbosity} object (openai-codex-responses.ts:560, openai_responses.go:1295)
		Include              []string          `json:"include,omitempty"` //portlint:allow emptydrop always ["reasoning.encrypted_content"] (openai-codex-responses.ts:561)
		PromptCacheKey       string            `json:"prompt_cache_key,omitempty"`
		ToolChoice           any               `json:"tool_choice,omitempty"`
		ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
		Temperature          *float64          `json:"temperature,omitempty"`
		ServiceTier          string            `json:"service_tier,omitempty"`
		Tools                []ResponsesTool   `json:"tools,omitempty"` //portlint:allow emptydrop Pi sets tools only when requestTools is non-empty (openai-codex-responses.ts:575-581)
		Reasoning            *respReasoning    `json:"reasoning,omitempty"`
		MaxOutputTokens      int               `json:"max_output_tokens,omitempty"`
		PromptCacheRetention string            `json:"prompt_cache_retention,omitempty"`
		PromptCacheOptions   map[string]string `json:"prompt_cache_options,omitempty"` //portlint:allow emptydrop getPromptCacheOptions returns a non-empty object or undefined (openai-responses.ts:106-112)
	}{req.Model, req.Store, req.Stream, req.Instructions, req.Input, req.Text, req.Include, req.PromptCacheKey, req.ToolChoice, req.ParallelToolCalls,
		req.Temperature, req.ServiceTier, req.Tools, req.Reasoning, req.MaxOutputTokens, req.PromptCacheRetention, req.PromptCacheOptions}
}

// ─── SSE event types ─────────────────────────────────────────────────────────

// respSSEEvent is the envelope for all SSE events from the Responses API.
type respSSEEvent struct {
	Type        string           `json:"type"`
	OutputIndex responsesSlotKey `json:"output_index"`

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
	Code    json.RawMessage `json:"code,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
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

// applyCodexReasoning is openai-codex-responses.ts buildRequestBody's reasoning: a given effort is mapped through the model's levels ("none" through
// its off entry) and sent unless that maps to null, with the summary defaulting to "auto"; with no effort a reasoning model whose off is not null sends
// its off effort. A summary alone requests nothing.
func (p *openAIResponsesProvider) applyCodexReasoning(req *respRequest, opts StreamOptions) {
	model := p.resolvedModel()
	levels := model.ThinkingLevelMap
	// streamSimple's reasoning level (already clamped to the model) is the effort unless it is off.
	requested := cmp.Or(opts.ReasoningEffort, strings.TrimPrefix(string(opts.Thinking), string(ThinkingOff)))
	if requested != "" {
		var effort *string
		if requested == "none" {
			effort = new("none")
			if off, ok := levels[ThinkingOff]; ok {
				effort = off
			}
		} else if mapped := levels[ModelThinkingLevel(requested)]; mapped != nil {
			effort = mapped
		} else {
			effort = new(requested)
		}
		if effort != nil {
			req.Reasoning = &respReasoning{Effort: *effort, Summary: cmp.Or(opts.ReasoningSummary, "auto")}
		}
		return
	}
	if off, ok := levels[ThinkingOff]; model.ProviderMeta.Reasoning && (!ok || off != nil) {
		effort := "none"
		if off != nil {
			effort = *off
		}
		req.Reasoning = &respReasoning{Effort: effort}
	}
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
	// first is the only text block's text, kept as it is, so the string cache finds the same string every request by its identity.
	var first string
	var images []ImageContent
	textBlocks := 0
	for _, block := range content {
		switch block := block.(type) {
		case TextContent:
			switch textBlocks {
			case 0:
				first = block.Text
			case 1:
				text.WriteString(first)
				fallthrough
			default:
				text.WriteByte('\n')
				text.WriteString(block.Text)
			}
			textBlocks++
		case ImageContent:
			images = append(images, block)
		}
	}
	joined := first
	if textBlocks > 1 {
		joined = text.String()
	}
	// Whitespace-only text counts as no text (openai-responses-shared.ts hasText: textResult.trim(), Pi 4dd2b149).
	hasText := trimJSWhitespace(joined) != ""
	if len(images) == 0 || !modelSupportsImage {
		var s string
		switch {
		case hasText:
			s = joined
		case len(images) > 0:
			s = "(see attached image)"
		default:
			s = "(no tool output)"
		}
		s = sanitizeSurrogates(s)
		//portlint:allow utf16units requestSpliceMinBytes is the Go-only request-string cache's threshold in bytes (request_body_splice.go:21), not a JS length
		if requestStrings != nil && len(s) >= requestSpliceMinBytes {
			// The encoding is the cache's own bytes, so a later request that carries it needs no new encoding and stands for it with a token.
			if quoted, valid := requestStrings.quoteValid(s); valid {
				return quoted
			}
		}
		raw, _ := json.Marshal(s)
		return raw
	}
	parts := make([]any, 0, len(images)+1)
	if hasText {
		parts = append(parts, responsesInputText{Type: "input_text", Text: sanitizeSurrogates(joined)})
	}
	for _, image := range images {
		parts = append(parts, newResponsesInputImage(image))
	}
	raw, _ := json.Marshal(parts)
	return raw
}

// quotedArgumentsJSON is the JSON string of a function call's arguments text (openai-responses-shared.ts writes `arguments:
// JSON.stringify(block.arguments)`). Large arguments are quoted by the request string cache straight from the bytes ArgumentsJSON
// keeps, so a later request finds the same quoted bytes without encoding them again and the body stands for them with a token.
func quotedArgumentsJSON(arguments []byte) json.RawMessage {
	//portlint:allow utf16units requestSpliceMinBytes is the Go-only request-string cache's threshold in bytes (request_body_splice.go:21), not a JS length
	if requestStrings != nil && len(arguments) >= requestSpliceMinBytes {
		if quoted, ok := requestStrings.quoteValid(keptArgumentsString(arguments)); ok {
			return quoted
		}
	}
	encoded, _ := json.Marshal(string(arguments))
	return encoded
}

// keptArgumentsString is the bytes ArgumentsJSON returned as a string, without a copy. Those bytes are never modified (a kept encoding is
// shared by every later request, and a fresh one is clipped and owned by the caller), so the string is immutable as Go requires, and a
// later request that sends the same kept arguments sends the same string: the request string cache finds it by identity.
func keptArgumentsString(arguments []byte) string {
	return unsafe.String(unsafe.SliceData(arguments), len(arguments)) //nolint:gosec // G103: the argument bytes are immutable once kept (clipped and owned by the caller), so the aliased string cannot change
}

// responsesInputText is a ResponseInputText content part with its members in the order openai-responses-shared.ts writes them (:98, :238-241).
type responsesInputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesInputImage is a ResponseInputImage content part with its members in the order openai-responses-shared.ts writes them (:101-105,
// :243-247).
type responsesInputImage struct {
	Type     string `json:"type"`
	Detail   string `json:"detail"`
	ImageURL string `json:"image_url"`
}

func newResponsesInputImage(image ImageContent) responsesInputImage {
	return responsesInputImage{Type: "input_image", Detail: "auto", ImageURL: "data:" + image.MimeType + ";base64," + image.Data}
}

// responsesOutputText is a replayed assistant output_text part (openai-responses-shared.ts:285).
type responsesOutputText struct {
	Type        string     `json:"type"`
	Text        string     `json:"text"`
	Annotations []struct{} `json:"annotations"`
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
	// ConfigurationUpdateEffort is the effort of the reply being generated. When set, the thinking level goes in
	// configuration_update items instead of a changing reasoning.effort: one before every earlier reply of this model
	// with its recorded level (ProviderThinkingLevel), and one with this effort at the end, so earlier input never
	// changes (openai-responses-shared.ts convertResponsesMessages configurationUpdateEffort).
	ConfigurationUpdateEffort string
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
	isReasoning := p.cfg.IsReasoning
	if p.cfg.Codex {
		// openai-codex-responses.ts passes the selected model: its reasoning flag, not an unconditional one, picks the role.
		isReasoning = p.resolvedModel().ProviderMeta.Reasoning
	}
	return responsesInstructionRole(isReasoning, supportsDeveloperRole)
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

// ResponsesToolSupportOptions are the compat flags GetResponsesToolSupport reads.
type ResponsesToolSupportOptions struct {
	SupportsMidConvoSystemMessages bool
	SupportsAdditionalTools        bool
	SupportsToolSearch             bool
}

// GetResponsesToolSupport is how a Responses transport expresses tool changes.
//
// additional_tools items accumulate: a later item adds tools and replaces same-name definitions from earlier items, but it
// cannot replace a top-level tool and cannot remove any tool (verified with strict schemas on the OpenAI, Codex, and Copilot
// backends, October 2026). Like Codex, every tool is therefore declared in additional_tools items, starting with one at the
// head of the input, and removals are announced as text. Synthetic tool search can only add tools. Without mid-conversation
// system messages there is nothing to anchor.
// Ports packages/ai/src/api/openai-responses-shared.ts getResponsesToolSupport.
func GetResponsesToolSupport(options ResponsesToolSupportOptions) TranscriptToolSupport {
	if options.SupportsMidConvoSystemMessages && options.SupportsAdditionalTools {
		return TranscriptToolSupportChanges
	}
	if options.SupportsToolSearch {
		return TranscriptToolSupportAdditions
	}
	return TranscriptToolSupportNone
}

// toolSupport is the converter's options as GetResponsesToolSupport reads them.
func (c responsesMessageConverter) toolSupport() TranscriptToolSupport {
	return GetResponsesToolSupport(ResponsesToolSupportOptions{
		SupportsMidConvoSystemMessages: c.options.SupportsMidConvoSystemMessages,
		SupportsAdditionalTools:        c.options.SupportsAdditionalTools,
		SupportsToolSearch:             c.options.SupportsToolSearch,
	})
}

// renderIncrementalToolsNotice precedes every additional_tools item after the first. Without it, models read the latest item as
// the complete tool set once a removal was announced; Codex precedes its incremental tool batches with the same guidance.
// Restating the tools that are still unavailable keeps smaller models from calling them again after later loads.
func renderIncrementalToolsNotice(unavailable []string) string {
	const notice = "This is an incremental tools update. Previously declared tools remain available unless explicitly marked unavailable. A tool declared again here is available again, and its latest definition replaces the earlier one."
	if len(unavailable) == 0 {
		return notice
	}
	return notice + "\nStill unavailable: " + strings.Join(unavailable, ", ") + "."
}

func renderToolRemovalNotice(names []string) string {
	var notice strings.Builder
	notice.WriteString("The following tools are no longer available. Do not call them:")
	for _, name := range names {
		notice.WriteString("\n- ")
		notice.WriteString(name)
	}
	return notice.String()
}

// responsesToolChanges is the state convertAnchored keeps across a transcript's system messages.
type responsesToolChanges struct {
	// declaredAdditionalTools is set once an additional_tools item was written.
	declaredAdditionalTools bool
	// unavailable lists, in the order announced, the tools announced as removed and not declared again since.
	unavailable []string
}

// systemToolAdditions mirrors upstream appendSystemToolAdditions: a system message's added tools load in place as an
// additional_tools item (preceded by the incremental notice after the first), or else as a synthetic client tool_search_call and
// tool_search_output pair.
func (c responsesMessageConverter) systemToolAdditions(state *responsesToolChanges, message SystemMessage, seed string) ([]ResponsesInputItem, error) {
	for _, tool := range message.ToolsAdded {
		state.unavailable = slices.DeleteFunc(state.unavailable, func(name string) bool { return name == tool.Name })
	}
	if len(message.ToolsAdded) == 0 {
		return nil, nil
	}
	switch c.toolSupport() {
	case TranscriptToolSupportChanges:
		tools, err := ConvertResponsesTools(message.ToolsAdded, c.options.ToolOptions)
		if err != nil {
			return nil, err
		}
		var items []ResponsesInputItem
		if state.declaredAdditionalTools {
			content, _ := json.Marshal(renderIncrementalToolsNotice(state.unavailable))
			items = append(items, ResponsesInputItem{Role: c.instructionRole, Content: content})
		}
		state.declaredAdditionalTools = true
		return append(items, ResponsesInputItem{Type: "additional_tools", Role: "developer", Tools: tools}), nil
	case TranscriptToolSupportAdditions:
		options := c.options.ToolOptions
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
	return nil, nil
}

// systemMessageInstruction is a system message's text for the instruction item: the leading message's prompt or a later
// message's update, followed by the announcement of its true removals. A redefinition is a removal plus an addition of the same
// name; only true removals are announced. The removals join the unavailable tools after this message's additions, whose notice
// precedes this removal notice.
func (state *responsesToolChanges) systemMessageInstruction(message SystemMessage, isLeading, anchorsToolChanges bool) string {
	var first string
	if isLeading {
		first = GetSystemMessageText(message)
	} else {
		first = RenderSystemMessageUpdate(message)
	}
	if !anchorsToolChanges || len(message.ToolsRemoved) == 0 {
		return first
	}
	var removed []string
	for _, tool := range message.ToolsRemoved {
		if !slices.ContainsFunc(message.ToolsAdded, func(added ToolSchema) bool { return added.Name == tool.Name }) {
			removed = append(removed, tool.Name)
		}
	}
	if len(removed) == 0 {
		return first
	}
	for _, name := range removed {
		if !slices.Contains(state.unavailable, name) {
			state.unavailable = append(state.unavailable, name)
		}
	}
	if first == "" {
		return renderToolRemovalNotice(removed)
	}
	return first + "\n\n" + renderToolRemovalNotice(removed)
}

// convertMessages converts messages as a whole transcript with every tool declared at the request's top level.
func (p *openAIResponsesProvider) convertMessages(messages []Message, grammarProps map[string]string) ([]ResponsesInputItem, error) {
	return p.messageConverter(getResponsesCompat(p.cfg.Compat, p.cfg.StrictModeDefault), grammarProps).convertAnchored(messages, ResolveTranscriptTools(messages, TranscriptToolSupportNone), true)
}

// convertAnchored converts the whole transcript, the leading system message included. transcriptTools is
// ResolveTranscriptTools over it: the first FoldedSystemMessages system messages leave their tool changes to the request's
// tool list, every later one carries its own in place. The leading system message's text is written only with
// includeSystemPrompt; its tools are declared either way.
func (c responsesMessageConverter) convertAnchored(messages []Message, transcriptTools TranscriptTools, includeSystemPrompt bool) ([]ResponsesInputItem, error) {
	items := []ResponsesInputItem{}
	messageIndex := 0
	systemIndex := 0
	supportsImages := c.supportsImages
	var changes responsesToolChanges
	for sourceIndex, message := range messages {
		_, isSystem := message.(SystemMessage)
		isLeadingSystemMessage := sourceIndex == 0 && isSystem
		switch message := message.(type) {
		case SystemMessage:
			// transformMessages keeps system messages in order, so their position matches the transcript.
			anchorsToolChanges := systemIndex >= transcriptTools.FoldedSystemMessages
			systemIndex++
			if anchorsToolChanges {
				additions, err := c.systemToolAdditions(&changes, message, fmt.Sprintf("system:%d", messageIndex))
				if err != nil {
					return nil, err
				}
				items = append(items, additions...)
			}
			if !isLeadingSystemMessage || includeSystemPrompt {
				if text := changes.systemMessageInstruction(message, isLeadingSystemMessage, anchorsToolChanges); text != "" {
					content, _ := json.Marshal(sanitizeSurrogates(text))
					items = append(items, ResponsesInputItem{Role: c.instructionRole, Content: content})
				}
			}
		case UserMessage:
			var parts []any
			switch content := message.Content.(type) {
			case UserText:
				parts = append(parts, responsesInputText{Type: "input_text", Text: sanitizeSurrogates(string(content))})
			case UserContentBlocks:
				for _, block := range content {
					switch block := block.(type) {
					case TextContent:
						parts = append(parts, responsesInputText{Type: "input_text", Text: sanitizeSurrogates(block.Text)})
					case ImageContent:
						parts = append(parts, newResponsesInputImage(block))
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
					content, _ := json.Marshal([]responsesOutputText{{Type: "output_text", Text: text, Annotations: []struct{}{}}})
					items = append(items, ResponsesInputItem{Type: "message", Role: "assistant", Content: content, Status: "completed", ID: id, Phase: phase})
				case ToolCall:
					callID, itemID, _ := strings.Cut(block.ID, "|")
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
						items = append(items, ResponsesInputItem{Type: "function_call", ID: itemID, CallID: callID, Name: block.Name, Namespace: namespace, Arguments: quotedArgumentsJSON(arguments)})
					}
				}
			}
			if len(items) == initialItemCount {
				continue
			}
			if c.options.ConfigurationUpdateEffort != "" && sameModel && message.ProviderThinkingLevel != "" {
				items = slices.Insert(items, initialItemCount, configurationUpdateItem(message.ProviderThinkingLevel))
			}
		}
		if !isLeadingSystemMessage {
			messageIndex++
		}
	}
	if c.options.ConfigurationUpdateEffort != "" {
		items = append(items, configurationUpdateItem(c.options.ConfigurationUpdateEffort))
	}
	return items, nil
}

// configurationUpdateItem is a configuration_update input item that sets the reasoning effort from that point on.
func configurationUpdateItem(effort string) ResponsesInputItem {
	return ResponsesInputItem{Type: "configuration_update", Reasoning: &respReasoningEffort{Effort: effort}}
}

// responsesToolCallNormalizer is convertResponsesMessages' normalizeToolCallId, which transformMessages applies to another model's tool call ids.
func responsesToolCallNormalizer(allowedToolCallProviders map[string]bool) func(string, *Model, AssistantMessage) string {
	return func(id string, target *Model, source AssistantMessage) string {
		return normalizeResponsesToolCallID(id, allowedToolCallProviders, target.ProviderMeta.ProviderID, source.Provider != target.ProviderMeta.ProviderID || source.API != target.ProviderMeta.API)
	}
}

// prepareResponsesTranscript resolves the transcript's system updates, transforms its messages for the model and resolves which
// tools the request carries (convertResponsesMessages: resolveTranscript, transformMessages, resolveTranscriptTools).
func prepareResponsesTranscript(model *Model, transcript TranscriptContext, allowedToolCallProviders map[string]bool, supportsMidConvoSystemMessages bool, toolSupport TranscriptToolSupport) ([]Message, TranscriptTools) {
	resolved := resolvedMessages(transcript, supportsMidConvoSystemMessages)
	messages := omitEmptyResponsesCallIDs(TransformMessages(resolved, model, responsesToolCallNormalizer(allowedToolCallProviders)))
	return messages, ResolveTranscriptTools(resolved, toolSupport)
}

// omitEmptyResponsesCallIDs omits, with the tool results that name it, a tool call whose {call_id}|{item_id} id still has an empty
// call_id part after ID normalization. pig divergence (D129): Pi replays it as `call_id: ""`, which the API rejects on every later
// request (pi-mono #10299). Only the Responses replay splits the id this way: other targets normalize the whole id ("|fc_1" becomes
// "_fc_1" for Chat Completions and Anthropic) or send it as is (Gemini), and replay it as Pi does.
func omitEmptyResponsesCallIDs(messages []Message) []Message {
	emptyCallPart := func(id string) bool { return strings.HasPrefix(id, "|") }
	found := slices.ContainsFunc(messages, func(message Message) bool {
		assistant, ok := message.(AssistantMessage)
		return ok && slices.ContainsFunc(assistant.Content, func(block AssistantContentBlock) bool {
			call, isCall := block.(ToolCall)
			return isCall && emptyCallPart(call.ID)
		})
	})
	if !found {
		return messages
	}
	blanked := make([]Message, len(messages))
	for i, message := range messages {
		switch message := message.(type) {
		case AssistantMessage:
			content := slices.Clone(message.Content)
			for j, block := range content {
				if call, ok := block.(ToolCall); ok && emptyCallPart(call.ID) {
					call.ID = ""
					content[j] = call
				}
			}
			message.Content = content
			blanked[i] = message
		case ToolResultMessage:
			if emptyCallPart(message.ToolCallID) {
				message.ToolCallID = ""
			}
			blanked[i] = message
		default:
			blanked[i] = message
		}
	}
	// The D120 tool flow omits a call with an empty id and the results that name it.
	return prepareProviderToolFlow(TranscriptContext{messages: blanked}).messages
}

// ConvertResponsesMessages converts a transcript to the Responses API "input" array for model: the leading system message (unless
// options.IncludeSystemPrompt is false), user, assistant and tool-result messages, with tool-call ids normalized for the providers
// in allowedToolCallProviders. Tools are declared as GetResponsesToolSupport says: in additional_tools items, in synthetic tool
// search items, or not at all (the request's tool list carries them).
// Ports packages/ai/src/api/openai-responses-shared.ts convertResponsesMessages.
func ConvertResponsesMessages(model *Model, transcript TranscriptContext, allowedToolCallProviders map[string]bool, options ConvertResponsesMessagesOptions) ([]ResponsesInputItem, error) {
	toolSupport := GetResponsesToolSupport(ResponsesToolSupportOptions{
		SupportsMidConvoSystemMessages: options.SupportsMidConvoSystemMessages,
		SupportsAdditionalTools:        options.SupportsAdditionalTools,
		SupportsToolSearch:             options.SupportsToolSearch,
	})
	messages, transcriptTools := prepareResponsesTranscript(model, transcript, allowedToolCallProviders, options.SupportsMidConvoSystemMessages, toolSupport)
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
	return converter.convertAnchored(messages, transcriptTools, options.IncludeSystemPrompt == nil || *options.IncludeSystemPrompt)
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

// responsesTemplatePart is a string member of a streamed output item as Pi's template literal `${item.call_id}|${item.id}` interpolates it:
// an absent member is "undefined" and null is "null".
type responsesTemplatePart string

func (p *responsesTemplatePart) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = "null"
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = responsesTemplatePart(value)
	return nil
}

// responsesStreamedToolCallID is the `${item.call_id}|${item.id}` id of a streamed function_call or custom_tool_call item
// (openai-responses-shared.ts createSlot). pig divergence (D129): a call streamed with an empty call_id gets the generated id D120
// gives any id-less call, because an empty call_id replays as `call_id: ""` and the API rejects every later request (pi-mono #10299).
func responsesStreamedToolCallID(rawItem json.RawMessage, name string) string {
	parts := struct {
		CallID responsesTemplatePart `json:"call_id"`
		ID     responsesTemplatePart `json:"id"`
	}{CallID: "undefined", ID: "undefined"}
	// The item already decoded as a respOutputItem, whose call_id and id are strings, so this decode cannot fail.
	_ = json.Unmarshal(rawItem, &parts)
	if parts.CallID == "" {
		parts.CallID = responsesTemplatePart(generatedToolCallID(name))
	}
	return string(parts.CallID) + "|" + string(parts.ID)
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
		toolParameters, err := getJSONSchemaToolParameters(t, new(strict == ResponsesStrictTrue))
		if err != nil {
			return nil, err
		}
		parameters, order := toolParameters.Schema, toolParameters.order
		ft := ResponsesTool{
			Type:           "function",
			Name:           t.Name,
			Description:    t.Description,
			Parameters:     parameters,
			parameterOrder: order,
			DeferLoading:   options.ToolSearchResult,
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
	// pig divergence (D48): the agent loop asks for the tool-flow repair.
	if opts.RepairToolFlow {
		transcript = transcript.withRepairedToolFlow()
	}
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("openai-responses: invalid transcript: %w", err)
	}
	compat := getResponsesCompat(p.cfg.Compat, p.cfg.StrictModeDefault)
	target := &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{API: p.api(), ProviderID: p.cfg.ProviderID}, Input: []string{"text"}}
	if p.modelSupportsImages() {
		target.Input = append(target.Input, "image")
	}
	toolSupport := GetResponsesToolSupport(ResponsesToolSupportOptions{
		SupportsMidConvoSystemMessages: compat.SupportsMidConvoSystemMessages,
		SupportsAdditionalTools:        compat.SupportsAdditionalTools,
		SupportsToolSearch:             compat.SupportsToolSearch,
	})
	messages, transcriptTools := prepareResponsesTranscript(target, transcript, p.messageConverter(compat, nil).allowedToolCallProviders, compat.SupportsMidConvoSystemMessages, toolSupport)
	// Pi's stream functions pass buildParams the transcript after resolveTranscript and before transformMessages, so the request
	// tools and the anchored system messages resolve over the same messages: a transcript whose first message is an aborted
	// assistant has no initial system message there, though transformMessages drops that assistant.
	tools := transcriptTools.RequestTools
	grammarProps, err := createGrammarToolInputProperties(GetDeclaredTools(messages), compat.SupportsOpenAIGrammarTools)
	if err != nil {
		return nil, err
	}
	if p.cfg.Codex && opts.Thinking != "" {
		opts.Thinking = ClampThinkingLevel(p.resolvedModel(), ModelThinkingLevel(opts.Thinking)).ReasoningOption()
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
	configurationUpdateEffort := p.configurationUpdateEffort(compat, opts, apiKey)
	converter := p.messageConverter(compat, grammarProps)
	converter.options.ConfigurationUpdateEffort = configurationUpdateEffort
	// Codex sends the leading system message as `instructions`; its tools stay in the input either way.
	msgs, err := converter.convertAnchored(messages, transcriptTools, !p.cfg.Codex)
	if err != nil {
		return nil, err
	}

	inputJSON, marshalErr := json.Marshal(msgs)
	if marshalErr != nil {
		return nil, fmt.Errorf("openai-responses: marshal input: %w", marshalErr)
	}
	// The payload hook gets the real input. The body is built from the same input with a token where each cached string is, so the strings are not scanned again.
	inputSplice := &stringSplice{cache: requestStrings, nonce: newSpliceNonce()}
	var inputTokens json.RawMessage
	if p.cfg.Codex {
		inputSplice.cache = nil
	} else if tokenized, changed := inputSplice.tokens(msgs); changed {
		if inputTokens, marshalErr = json.Marshal(tokenized); marshalErr != nil {
			return nil, fmt.Errorf("openai-responses: marshal input: %w", marshalErr)
		}
	}

	req := respRequest{
		Model:  cmp.Or(p.cfg.requestModel, p.cfg.Model),
		Input:  inputJSON,
		Stream: true,
		codex:  p.cfg.Codex,
	}
	store := false
	req.Store = &store
	req.ServiceTier = opts.ServiceTier
	if p.cfg.Codex {
		req.Instructions = "You are a helpful assistant."
		// openai-codex-responses.ts buildParams reads the initial system message of the resolved transcript, before transformMessages drops an errored or aborted turn ahead of it.
		if initial := borrowedInitialSystemMessage(resolvedMessages(transcript, compat.SupportsMidConvoSystemMessages)); initial != nil {
			if prompt := GetCurrentSystemPrompt([]Message{*initial}); prompt != "" {
				req.Instructions = prompt
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
	if p.cfg.Codex {
		p.applyCodexReasoning(&req, opts)
	} else if p.cfg.IsReasoning || opts.IsReasoning {
		model := p.resolvedModel()
		if model.Capabilities.MaxThinking == "" {
			model = new(*model)
			model.Capabilities.MaxThinking = ThinkingLevelHigh
		}
		clamped := ModelThinkingLevel(opts.Thinking)
		if opts.ReasoningEffort != "" {
			clamped = ModelThinkingLevel(opts.ReasoningEffort)
		} else if clamped != "" {
			// streamSimple clamps every given level, "off" too: a model whose off is null takes the next supported level.
			clamped = ClampThinkingLevel(model, clamped)
		}
		switch {
		case clamped != ThinkingOff && clamped != "":
			effort := string(clamped)
			if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; ok && mapped != nil {
				effort = *mapped
			}
			if configurationUpdateEffort != "" {
				// The level travels in configuration_update items; reasoning.effort stays fixed so the prompt cache holds.
				effort = configurationUpdateRequestEffort
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
	if !p.cfg.Codex {
		if keys, samplingParams := resolveSamplingParamsInOrder(p.samplingModel(opts), responsesSamplingLevel(opts), opts.SamplingParams); samplingParams != nil {
			assigned, err := assignSamplingParams(req, respRequestLiteralKeys, keys, samplingParams)
			if err != nil {
				return nil, fmt.Errorf("openai-responses: %w", err)
			}
			payload = assigned
		}
	}
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(payload, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if err != nil {
			return nil, fmt.Errorf("openai-responses: onPayload: %w", err)
		}
		if next != nil {
			payload = payloadReplacement(next)
		}
	}

	var body *requestBody
	if request, own := payload.(respRequest); own && inputTokens != nil && len(request.Input) == len(inputJSON) && len(inputJSON) > 0 && &request.Input[0] == &inputJSON[0] {
		request.Input = inputTokens
		skeleton, err := marshalRequestBody(request)
		if err != nil {
			return nil, fmt.Errorf("openai-responses: marshal request: %w", err)
		}
		body, _ = inputSplice.join(skeleton)
	}
	if body == nil {
		whole, err := marshalRequestBody(payload)
		if err != nil {
			return nil, fmt.Errorf("openai-responses: marshal request: %w", err)
		}
		body = &requestBody{segments: [][]byte{whole}, size: len(whole)}
	}
	checkRequestSegments(ctx, APIOpenAIResponses, body)

	baseURL := p.cfg.BaseURL
	if p.cfg.GetBaseURL != nil {
		if u, err := p.cfg.GetBaseURL(ctx); err != nil {
			// The resolver's own error is the rejection (azure-openai-config.ts throws it from the provider's stream setup).
			return nil, err
		} else if u != "" {
			baseURL = u
		}
	}

	accountID := ""
	if p.cfg.Codex {
		accountID = CodexAccountID(apiKey)
		if accountID == "" {
			return nil, errors.New("Failed to extract accountId from token")
		}
	}

	endpointURL := strings.TrimRight(baseURL, "/")
	if !p.cfg.BaseURLIsEndpoint && !p.cfg.Codex {
		var err error
		if endpointURL, err = sdkBuildURL(baseURL, "/responses"); err != nil {
			return nil, err
		}
	} else if !p.cfg.BaseURLIsEndpoint {
		endpointURL += "/responses"
	}
	// webSocketFallback is the transport failure diagnostic of a Codex WebSocket attempt that fell back to SSE; the SSE output carries it.
	var webSocketFallback []AssistantMessageDiagnostic
	if p.cfg.Codex && opts.Transport != TransportSSE {
		cacheSessionID := ""
		if cacheRetention != "none" {
			cacheSessionID = opts.SessionID
		}
		webSocketStream, fallback, err := p.startCodexWebSocket(ctx, endpointURL, body.Bytes(), apiKey, accountID, cacheSessionID, grammarProps, opts)
		if err != nil {
			return nil, err
		}
		if webSocketStream != nil {
			return webSocketStream, nil
		}
		if fallback != nil {
			webSocketFallback = []AssistantMessageDiagnostic{codexTransportFailureDiagnostic(fallback, opts.Transport, false, body.size)}
		}
	}
	sseBody := body.reader()
	if p.cfg.Codex {
		sseBody = bytes.NewReader(compressCodexRequestBody(body.Bytes()))
	}
	// Codex calls fetch itself (openai-codex-responses.ts:400) and reports its TypeError; the openai SDK, Azure's included, reports a connection error.
	newRequest := newSDKFetchRequest
	if p.cfg.Codex {
		newRequest = newFetchRequest
	}
	httpReq, err := newRequest(ctx, http.MethodPost, endpointURL, sseBody, opts.Fetch != nil)
	if err != nil {
		return nil, err
	}
	body.prepare(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	applyIdentityHeaders(httpReq.Header, ClientIdentityTarget{Provider: p.cfg.ProviderID, BaseURL: p.cfg.BaseURL}, opts.Identity)
	// The OpenAI SDK's default `accept` header; the Codex transport asks for an event stream.
	httpReq.Header.Set("Accept", "application/json")
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
	// azure-openai-responses.ts createClient sends no session affinity headers (:155-170); the session id reaches only prompt_cache_key.
	if opts.SessionID != "" && cacheRetention != "none" && p.api() != APIAzureOpenAIResponses {
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
	// Only the openai-responses module copies model.api (openai-responses.ts:140); the Azure and Codex modules write their own literal.
	api := p.api()
	if api == APIOpenAIResponses {
		api = outputAPI(p.cfg.ModelMetadata, api)
	}
	builder := newObservedProviderBuilder(ctx, api, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	builder.requestServiceTier = requestServiceTierOf(opts)
	builder.setResponseMetadata("", "", "", configurationUpdateEffort, nil)
	builder.setProviderEventObserver(opts, providerEventModel(p.cfg.ModelMetadata, p.api(), p.cfg.ProviderID, p.cfg.Model))
	builder.partial.Diagnostics = webSocketFallback
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
			b, _ := readWebText(resp.Body)
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
		raw.beforeNext = builder.publishPending
		decoder = raw
	} else {
		var suspend func()
		if builder.turn != nil {
			suspend = func() { suspendContinuation(builder.turn) }
		}
		decoder = newOpenAIStreamDecoder(r, builder.publishPending, suspend)
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
	// providerEventWanted is the request's ProviderStreamEventWanted for OnProviderStreamEvent, or nil.
	providerEventWanted func() bool
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
		providerEventWanted:        builder.providerEventWanted,
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
	builder.providerEvent, builder.providerEventWanted = nil, options.providerEventWanted
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
		// argsNotString is set while the slot's partialJson is still the truthy non-string `arguments` of its added item; a delta or an arguments.done event makes it a string.
		argsNotString bool
		customProp    string
		customIn      string
		// customGrown holds customIn's bytes, so a delta appends to them instead of copying them.
		customGrown accumulatedString
		grammarBuf  *grammarToolInputJSONBuffer
	}
	states := make(map[responsesSlotKey]*currentState)
	reasoningBlocksByID := make(map[string]int)
	nextToolIndex := 0
	createState := func(outputIndex responsesSlotKey, item respOutputItem, rawItem json.RawMessage) *currentState {
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
			state.toolID = responsesStreamedToolCallID(rawItem, item.Name)
			state.toolName = item.Name
			state.argsJSON.WriteString(item.Arguments)
			builder.toolCallStart(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, namespace: item.Namespace,
				initialJSON: item.Arguments, scratch: toolCallScratch{hasPartialJson: true, partialJson: item.Arguments}})
		case "custom_tool_call":
			state.toolIndex = nextToolIndex
			nextToolIndex++
			state.toolID = responsesStreamedToolCallID(rawItem, item.Name)
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
	// The decoders publish the message before the next record only when it is marked changed. A record whose handler may change it outside a
	// push marks it after the handler has run (the loop's post statement runs on `continue` too); the delta handlers change it only by pushing.
	var eventType string
	for ; decoder.Next(); markResponsesRecord(builder, eventType) {
		eventType = ""
		if codex && !codexSSE && ctx.Err() != nil {
			builder.failUnfinished(StopReasonAborted, errors.New("Request was aborted"))
			return
		}
		data := decoder.Event().Data
		if data == "[DONE]" {
			break
		}

		// processResponsesStream awaits the observer with any parsed value and then reads `event.type` (openai-responses-shared.ts:599-601): a null
		// throws there, and a number, string, boolean or array matches no branch.
		if top := strings.TrimLeft(data, " \t\r\n"); !codex && top != "" && top[0] != '{' && json.Valid([]byte(top)) {
			if err := builder.observeProviderEvent([]byte(data)); err != nil {
				builder.failUnfinished(StopReasonError, err)
				return
			}
			if top[0] == 'n' {
				builder.failUnfinished(StopReasonError, errors.New("Cannot read properties of null (reading 'type')"))
				return
			}
			continue
		}
		var event respSSEEvent
		var read responsesEventRead
		if fast, ok := parseResponsesDeltaFast(data); ok {
			// A delta of the common shape decodes without reflection; json.Unmarshal agrees wherever it answers true.
			event = fast
		} else {
			text := jsjson.NormalizeIntegers([]byte(data))
			if err := json.Unmarshal(text, &event); err != nil {
				normalized, normalizable := []byte(nil), false
				// mapCodexEvents yields every event but its terminal and error ones unchanged into processResponsesStream (openai-codex-responses.ts:743-788),
				// so a Codex event reads its members as openai-responses does.
				if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
					normalized, read, normalizable = normalizeResponsesEventJS(text)
				}
				event = respSSEEvent{}
				if !normalizable || json.Unmarshal(normalized, &event) != nil {
					builder.failUnfinished(StopReasonError, fmt.Errorf("openai-responses: invalid SSE JSON: %w", err))
					return
				}
			} else if event.Delta == "" && responsesDeltaEvents[event.Type] || event.Arguments == "" && event.Type == "response.function_call_arguments.done" {
				// The typed decode reads a null or absent member as "", where Pi's handler reads null or undefined.
				readResponsesEmptyMember(data, &event, &read)
			}
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

		eventType = event.Type
		switch event.Type {
		case "response.created":
			if event.Response == nil {
				// openai-responses-shared.ts:602 reads event.response.id of an undefined response; the V8 TypeError text is the message.
				builder.failUnfinished(StopReasonError, errors.New("Cannot read properties of undefined (reading 'id')"))
				return
			}
			builder.partial.ResponseID = event.Response.ID

		case "response.output_item.added":
			var item respOutputItem
			rawItem, _, err := decodeResponsesItem(event.Item, false, &item)
			if err != nil {
				builder.failUnfinished(StopReasonError, err)
				return
			}
			// output_item.added always opens a new block and replaces the slot at its index (openai-responses-shared.ts createSlot); only output_item.done reuses one.
			// An item type without a block leaves the slot in place.
			switch item.Type {
			case "reasoning", "message", "function_call", "custom_tool_call":
				delete(states, event.OutputIndex)
			}
			// createSlot stores `partialJson: item.arguments || ""` as it is; a delta then appends to its string form.
			if state := createState(event.OutputIndex, item, rawItem); state != nil && item.Type == "function_call" {
				state.argsNotString = responsesArgumentsNotString(event.Item)
			}

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
				state.argsNotString = false
				state.argsJSON.WriteString(event.Delta)
				builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, argumentsDelta: event.Delta})
			}

		case "response.function_call_arguments.done":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "function_call" {
				if read.thrown != nil {
					builder.failUnfinished(StopReasonError, read.thrown)
					return
				}
				state.argsNotString = false
				previous := state.argsJSON.String()
				if after, ok := strings.CutPrefix(event.Arguments, previous); ok && after != "" {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, argumentsDelta: after})
				}
				state.argsJSON.Reset()
				state.argsJSON.WriteString(event.Arguments)
				builder.setToolCallFinal(state.toolIndex, "", event.Arguments, false)
			}

		case "response.custom_tool_call_input.delta":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "custom_tool_call" {
				next := state.customGrown.append(state.customIn, event.Delta)
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
			rawItem, thrown, err := decodeResponsesItem(event.Item, true, &item)
			if err != nil {
				builder.failUnfinished(StopReasonError, err)
				return
			}
			state := createState(event.OutputIndex, item, rawItem)
			if state == nil || state.itemType != item.Type {
				continue
			}
			// parseToolCallArguments(item.arguments || slot.block.partialJson) also reaches a non-string partialJson the added item left (:741).
			if thrown == nil && item.Type == "function_call" && item.Arguments == "" && state.argsNotString {
				thrown = errors.New("json?.trim is not a function")
			}
			if thrown != nil {
				builder.failUnfinished(StopReasonError, thrown)
				return
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
				// thinkingSignature = JSON.stringify(item): the parsed item written back, not the bytes the server sent.
				signature := string(event.Item)
				if canonical, err := jsonstringify.Canonicalize(event.Item); err == nil {
					signature = string(canonical)
				}
				builder.thinkingBlockEnd(state.contentIndex, finalText, signature)
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
				builder.textBlockEnd(state.contentIndex, finalText.String(), responsesTextSignature(event.Item, &item))
			case "function_call":
				arguments := item.Arguments
				if arguments == "" {
					arguments = state.argsJSON.String()
				}
				if arguments == "" {
					arguments = "{}"
				}
				builder.setToolCallFinal(state.toolIndex, item.Namespace, arguments, true)
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
				// finalizeResponse reads response.output of an undefined response (openai-responses-shared.ts:556); the V8 TypeError text is the message.
				builder.failUnfinished(StopReasonError, errors.New("Cannot read properties of undefined (reading 'output')"))
				return
			}
			if read.thrown != nil {
				// backfillReasoningSignatures iterates response.output first.
				builder.failUnfinished(StopReasonError, read.thrown)
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
				// JSON.stringify({...storedItem, encrypted_content}): the member keeps its position when stored already had a falsy one, else it goes last.
				quoted, _ := json.Marshal(item.EncryptedContent)
				spliced := strings.TrimSpace(block.ThinkingSignature)
				spliced = strings.TrimSuffix(spliced, "}")
				if strings.TrimSpace(strings.TrimPrefix(spliced, "{")) != "" {
					spliced += ","
				}
				if canonical, err := jsonstringify.Canonicalize([]byte(spliced + `"encrypted_content":` + string(quoted) + "}")); err == nil {
					block.ThinkingSignature = string(canonical)
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
			if read.status != nil {
				// finalizeResponse records the id and usage, then mapStopReason throws for a status no case matches.
				if event.Response.ID != "" {
					builder.partial.ResponseID = event.Response.ID
				}
				builder.setResponseMetadata("", "", *read.status, "", nil)
				builder.failUnfinished(StopReasonError, errors.New("Unhandled stop reason: "+*read.status))
				return
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
			// Only the Codex event mapper records end_turn (openai-codex-responses.ts:775-778); openai-responses ignores it.
			var endTurn *bool
			if codex {
				endTurn = event.Response.EndTurn
			}
			builder.setResponseMetadata("", "", rawStopReason, "", endTurn)
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
				// openai-responses-shared.ts:748-749: a failed response records only its status; usage and id stay unset.
				builder.setResponseMetadata("", "", event.Response.Status, "", nil)
				if e := event.Response.Error; e != nil {
					code := cmp.Or(e.Code, "unknown")
					message := cmp.Or(e.Message, "no message")
					msg = code + ": " + message
				} else if d := event.Response.IncompleteDetails; d != nil && d.Reason != "" {
					msg = "incomplete: " + d.Reason
				}
			}
			builder.failUnfinished(StopReasonError, errors.New(msg))
			return

		case "error":
			builder.failUnfinished(StopReasonError, fmt.Errorf("Error Code %s: %s", jsStateString(event.Code), jsStateString(event.Message)))
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

// markResponsesRecord marks the message changed after a record unless its handler changes the message only by pushing: the deltas of text,
// refusal, reasoning and function-call arguments, and the end of a reasoning summary part.
func markResponsesRecord(builder *assistantStreamBuilder, eventType string) {
	switch eventType {
	case "response.output_text.delta", "response.refusal.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta",
		"response.reasoning_summary_part.done", "response.function_call_arguments.delta":
		return
	}
	builder.touch()
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
		// `parsed.v === 1 && typeof parsed.id === "string"` (openai-responses-shared.ts:59-76): a phase of any other value is dropped, not a parse failure.
		var parsed struct {
			V     json.RawMessage `json:"v"`
			ID    json.RawMessage `json:"id"`
			Phase json.RawMessage `json:"phase"`
		}
		var id string
		if json.Unmarshal([]byte(signature), &parsed) == nil && len(parsed.V) > 0 && (parsed.V[0] == '-' || parsed.V[0] >= '0' && parsed.V[0] <= '9') && jsnumber.FromJSON(parsed.V) == 1 &&
			json.Unmarshal(parsed.ID, &id) == nil && len(parsed.ID) > 0 && parsed.ID[0] == '"' {
			var phase string
			if json.Unmarshal(parsed.Phase, &phase) != nil || phase != "commentary" && phase != "final_answer" {
				phase = ""
			}
			return id, phase, true
		}
	}
	return signature, "", true
}

// encodeResponsesTextSignature is encodeTextSignatureV1 (openai-responses-shared.ts:53-57) for a string id and phase: `if (phase)` keeps any non-empty phase.
func encodeResponsesTextSignature(id, phase string) string {
	signature := TextSignatureV1{V: 1, ID: id, Phase: phase}
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
		return StopReasonError, "An unknown error occurred"
	default:
		return StopReasonError, "Unhandled stop reason: " + status
	}
}

// requestServiceTierOf is the requested tier that prices a response without its own tier: the serviceTier option, not a
// sampling parameter that overrode the request field (openai-responses.ts:196, openai-codex-responses.ts:674).
func requestServiceTierOf(opts StreamOptions) string {
	return opts.ServiceTier
}

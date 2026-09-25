package ai

// Covers OpenAI, OpenRouter, Groq, Cerebras, Fireworks, xAI, Ollama, vLLM, llama.cpp,
// and any other OpenAI-compatible endpoint.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

// OpenAIConfig configures an OpenAI-compatible provider.
type OpenAIConfig struct {
	// BaseURL is the API base (default: https://api.openai.com/v1).
	BaseURL string
	// APIKey is the bearer token. May be empty for local endpoints.
	APIKey string
	// Model is the model name sent in the request.
	Model string
	// ProviderID is the provider label (e.g. "openai", "openrouter", "ollama").
	ProviderID string
	// ExtraHeaders are added to every request.
	ExtraHeaders   map[string]string
	SamplingParams map[string]any
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
	OpenRouterRouting  map[string]any `json:"openRouterRouting,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chatTemplateKwargs,omitempty"`
	VLLMPriority       *float64       `json:"vllmPriority,omitempty"`
	// VercelGatewayRouting contains Vercel gateway routing config.
	VercelGatewayRouting            map[string]any `json:"vercelGatewayRouting,omitempty"`
	SupportsLongCacheRetention      *bool          `json:"supportsLongCacheRetention,omitempty"`
	SendSessionIdHeader             *bool          `json:"sendSessionIdHeader,omitempty"`
	SupportsEagerToolInputStreaming *bool          `json:"supportsEagerToolInputStreaming,omitempty"`
	SupportsCacheControlOnTools     *bool          `json:"supportsCacheControlOnTools,omitempty"`
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
	// + x-session-affinity (Completions) / session_id + x-client-request-id
	// (Responses); "openai-nosession" omits session_id; "openrouter" sends
	// x-session-id. Empty = auto-detect (openrouter base URL => "openrouter",
	// else "openai"). Replaces the pre-0.81 sendSessionIdHeader flag.
	SessionAffinityFormat SessionAffinityFormat `json:"sessionAffinityFormat,omitempty"`
	// SupportsToolSearch indicates the model supports client-executed tool
	// search for deferred tools (OpenAI Responses). Default: false.
	SupportsToolSearch      *bool `json:"supportsToolSearch,omitempty"`
	SupportsMaxOutputTokens *bool `json:"supportsMaxOutputTokens,omitempty"`
	// SupportsToolReferences indicates the provider supports deferred tools
	// loaded by tool_reference blocks in tool results (Anthropic Messages).
	// Default: true for first-party Anthropic except Haiku / pre-Claude-4.5.
	// ChatTemplateArgs are provider-specific vLLM/Baseten chat-template values.
	ChatTemplateArgs               map[string]any                  `json:"chatTemplateArgs,omitempty"`
	SupportsThinkingTokenBudget    *bool                           `json:"supportsThinkingTokenBudget,omitempty"`
	SupportsAdditionalTools        *bool                           `json:"supportsAdditionalTools,omitempty"`
	SupportsMidConvoEffort         *bool                           `json:"supportsMidConvoEffort,omitempty"`
	SupportsMidConvoSystemMessages *bool                           `json:"supportsMidConvoSystemMessages,omitempty"`
	SupportsMidConvoToolAdditions  *bool                           `json:"supportsMidConvoToolAdditions,omitempty"`
	SupportsMidConvoToolChanges    *bool                           `json:"supportsMidConvoToolChanges,omitempty"`
	AllowedFallbackModels          []AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
}

func ptrBool(v bool) *bool { return new(v) }

type openAIProvider struct {
	cfg    OpenAIConfig
	client *http.Client
}

// NewOpenAIProvider creates a Provider backed by the OpenAI Completions API.
func NewOpenAIProvider(cfg OpenAIConfig) Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "openai"
	}
	return &openAIProvider{cfg: cfg, client: newStreamingHTTPClient(cfg.Insecure)}
}

func (p *openAIProvider) ID() string { return p.cfg.ProviderID }

// systemPromptRole returns "developer" for reasoning models on supported endpoints,
// "system" otherwise. Mirrors upstream openai-completions.ts:683-684:
//
//	const useDeveloperRole = model.reasoning && compat.supportsDeveloperRole;
func (p *openAIProvider) systemPromptRole(isReasoning bool) string {
	if !isReasoning {
		return "system" // non-reasoning models always use "system"
	}
	if c := p.cfg.Compat; c != nil && c.SupportsDeveloperRole != nil {
		if *c.SupportsDeveloperRole {
			return "developer"
		}
		return "system"
	}
	// Auto-detect: OpenAI direct endpoints support developer role for reasoning models.
	if isOpenAIDirectURL(p.cfg.BaseURL) {
		return "developer"
	}
	return "system"
}

// compatBool returns the compat flag value, or the default if not set.
func (p *openAIProvider) compatBool(getter func(*OpenAICompat) *bool, defaultVal bool) bool {
	if c := p.cfg.Compat; c != nil {
		if v := getter(c); v != nil {
			return *v
		}
	}
	return defaultVal
}

// maxTokensField returns "max_completion_tokens" (OpenAI standard) or "max_tokens" (compat).
// Mirrors upstream detectCompat defaults: max_completion_tokens for standard, max_tokens for chutes.ai etc.
func (p *openAIProvider) maxTokensField() string {
	if c := p.cfg.Compat; c != nil && c.MaxTokensField != "" {
		return c.MaxTokensField
	}
	if upstreamUsesMaxTokens(p.cfg.ProviderID, p.cfg.BaseURL) {
		return "max_tokens"
	}
	return "max_completion_tokens"
}

// cacheControlFormat returns the cache control format ("anthropic" or "").
func (p *openAIProvider) cacheControlFormat() string {
	if c := p.cfg.Compat; c != nil {
		return c.CacheControlFormat
	}
	return ""
}

// convertMessagesWithCompat wraps convertMessages with compat-aware transformations:
// - requiresAssistantAfterToolResult: inserts empty assistant messages
// - requiresToolResultName: adds name field to tool result messages
// - requiresThinkingAsText: converts thinking blocks to <thinking> text
// Mirrors upstream openai-completions.ts convertMessages (lines 680-870).
func (p *openAIProvider) convertMessagesWithCompat(msgs []Message, grammarProps map[string]string, instructionRole string, anchorsAdditions bool) ([]oaiMessage, error) {
	requiresAssistant := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresAssistantAfterToolResult }, false)
	requiresName := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresToolResultName }, false)
	requiresThinkingAsText := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresThinkingAsText }, false)
	requiresReasoningContent := p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresReasoningContentOnAssistantMessages }, false)
	out, err := convertCompletionsMessages(msgs, completionsConvertOptions{
		supportsImages:  p.modelSupportsImages(),
		grammarProps:    grammarProps,
		thinkingAsText:  requiresThinkingAsText,
		instructionRole: instructionRole,
		systemTools: func(message SystemMessage) ([]oaiTool, error) {
			if !anchorsAdditions || len(message.ToolsAdded) == 0 {
				return nil, nil
			}
			return p.convertTools(message.ToolsAdded)
		},
	})
	if err != nil {
		return nil, err
	}
	normalizeToolCallIDs(out, p.cfg.ProviderID)
	if p.cfg.ProviderID == "opencode-go" {
		for index := range out {
			if out[index].Role == "assistant" && out[index].Reasoning != "" && out[index].ReasoningContent == nil {
				out[index].ReasoningContent = new(out[index].Reasoning)
				out[index].Reasoning = ""
			}
		}
	}

	if !requiresAssistant && !requiresName && !requiresThinkingAsText && !requiresReasoningContent {
		return out, nil
	}

	// Post-process message transformations.
	var result []oaiMessage
	for i, m := range out {
		// requiresAssistantAfterToolResult: if this is a user message and the
		// previous message was a tool result, insert an empty assistant message.
		if requiresAssistant && m.Role == "user" && i > 0 && out[i-1].Role == "tool" {
			result = append(result, oaiMessage{Role: "assistant", Content: ""})
		}
		// requiresToolResultName: add ToolName to tool messages.
		if requiresName && m.Role == "tool" && m.ToolName == "" {
			// Find the tool name from the preceding assistant's tool_calls.
			for j := i - 1; j >= 0; j-- {
				if out[j].Role == "assistant" && len(out[j].ToolCalls) > 0 {
					for _, tc := range out[j].ToolCalls {
						if tc.ID == m.ToolCallID {
							switch {
							case tc.Function != nil:
								m.ToolName = tc.Function.Name
							case tc.Custom != nil:
								m.ToolName = tc.Custom.Name
							}
							break
						}
					}
					break
				}
			}
		}
		if requiresReasoningContent && m.Role == "assistant" && m.ReasoningContent == nil {
			empty := ""
			m.ReasoningContent = &empty
		}
		result = append(result, m)
	}
	return result, nil
}

func (p *openAIProvider) Close() error { return nil }

// ─── Request / response wire types ───────────────────────────────────────────

type oaiMessage struct {
	Role             string               `json:"role"`
	Content          any                  `json:"content"` // string | []oaiContentPart
	ToolCallID       string               `json:"tool_call_id,omitempty"`
	ToolName         string               `json:"name,omitempty"` // for tool results when requiresToolResultName=true
	ToolCalls        []oaiRequestToolCall `json:"tool_calls,omitempty"`
	ReasoningDetails []json.RawMessage    `json:"reasoning_details,omitempty"`
	ReasoningContent *string              `json:"reasoning_content,omitempty"`
	Reasoning        string               `json:"reasoning,omitempty"`
	ReasoningText    string               `json:"reasoning_text,omitempty"`
	// Tools makes a tool-bearing system message that loads tools in place
	// (Kimi mid-conversation tool additions).
	Tools []oaiTool `json:"tools,omitempty"`
}

// MarshalJSON sends a tool-bearing system message as {role, tools} without
// content. Mirrors upstream KimiToolSystemMessageParam.
func (message oaiMessage) MarshalJSON() ([]byte, error) {
	type wire oaiMessage
	if message.Tools == nil {
		return json.Marshal(wire(message))
	}
	return json.Marshal(struct {
		Role  string    `json:"role"`
		Tools []oaiTool `json:"tools"`
	}{message.Role, message.Tools})
}

// oaiCacheControl mirrors Anthropic's cache_control annotation.
type oaiCacheControl struct {
	Type string `json:"type"` // "ephemeral"
	TTL  string `json:"ttl,omitempty"`
}

type oaiContentPart struct {
	CacheControl *oaiCacheControl `json:"cache_control,omitempty"`
	Type         string           `json:"type"`
	Text         string           `json:"text,omitempty"`
	ImageURL     *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
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

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function" | "custom"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// Custom carries a streamed grammar tool call. Its Input is a raw grammar
	// string that is reconstructed into JSON arguments. Mirrors
	// openai-completions.ts StreamingToolCallDelta.custom.
	Custom *struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"custom,omitempty"`
	Index *int `json:"index"`
}

type oaiTool struct {
	CacheControl *oaiCacheControl `json:"cache_control,omitempty"`
	Type         string           `json:"type"` // "function" | "custom"
	Function     *oaiToolFunction `json:"function,omitempty"`
	Custom       *oaiCustomTool   `json:"custom,omitempty"`
}

type oaiToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      *bool          `json:"strict,omitempty"`
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

type oaiRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	// Tools is a pointer so the request can express the three states upstream
	// separates (openai-completions.ts:729-737): a populated array, an explicit
	// empty array required by Anthropic-via-proxy backends once the conversation
	// carries tool history, and the omitted field. A plain slice with omitempty
	// cannot represent the empty-but-present case.
	Tools      *[]oaiTool `json:"tools,omitempty"`
	ToolChoice any        `json:"tool_choice,omitempty"`
	// Provider/ProviderOptions carry OpenRouter / Vercel AI Gateway routing
	// preferences. Mirrors openai-completions.ts:613-627.
	Provider        any      `json:"provider,omitempty"`
	ProviderOptions any      `json:"providerOptions,omitempty"`
	Stream          bool     `json:"stream"`
	Priority        *float64 `json:"priority,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	Thinking        any      `json:"thinking,omitempty"`
	// MaxTokens and MaxCompletionTokens are mutually exclusive: one is set
	// based on compat.maxTokensField. Default: "max_completion_tokens" for
	// api.openai.com, "max_tokens" for everything else.
	MaxTokens           int `json:"max_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	// Store enables server-side storage of the conversation. Gated on compat.supportsStore.
	Store *bool `json:"store,omitempty"`
	// ReasoningEffort maps to upstream reasoning_effort for models that support
	// extended reasoning (o1, o3, claude via Copilot, etc.).
	// Values: "low", "medium", "high" (or absent for no extended reasoning).
	// Mirrors upstream openai-completions.ts:511 reasoning_effort wiring.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Reasoning       any    `json:"reasoning,omitempty"`
	// EnableThinking / ChatTemplateKwargs carry the qwen and qwen-chat-template
	// thinking formats. Mirrors openai-completions.ts:559-564.
	EnableThinking      *bool `json:"enable_thinking,omitempty"`
	ChatTemplateKwargs  any   `json:"chat_template_kwargs,omitempty"`
	ChatTemplateArgs    any   `json:"chat_template_args,omitempty"`
	ThinkingTokenBudget int   `json:"thinking_token_budget,omitempty"`
	// PromptCacheKey enables server-side prompt caching (OpenAI). Value is
	// the session ID; repeated calls with the same key skip re-processing
	// the unchanged prefix. Mirrors upstream openai-completions.ts:449.
	PromptCacheKey *string `json:"prompt_cache_key,omitempty"`
	// PromptCacheRetention controls how long cached prompts are kept.
	// Value "24h" for long retention. Mirrors upstream :450.
	PromptCacheRetention *string `json:"prompt_cache_retention,omitempty"`
	StreamOptions        *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
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
	Delta        oaiDelta `json:"delta"`
	FinishReason string   `json:"finish_reason"`
	Index        int      `json:"index"`
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
}

// parseChunkUsage mirrors upstream openai-completions.ts parseChunkUsage.
// cached_tokens counts cache reads; a separately reported cache_write_tokens
// is not subtracted from it, and both leave the uncached input.
func parseChunkUsage(raw *oaiUsage, cost ModelCost) *Usage {
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
	reasoning := 0
	if raw.CompletionTokensDetails != nil {
		reasoning = raw.CompletionTokensDetails.ReasoningTokens
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
	return usage
}

type oaiChunk struct {
	Choices []oaiChunkChoice `json:"choices"`
	Usage   *oaiUsage        `json:"usage"`
	Error   json.RawMessage  `json:"error,omitempty"`
}

// ─── Message conversion ───────────────────────────────────────────────────────

// qwenReasoningEffort mirrors the qwen thinkingFormat branch of
// openai-completions.ts: enable_thinking always, plus reasoning_effort (mapped
// through thinkingLevelMap) when reasoning is on and the model supports it.
func qwenReasoningEffort(reasoningOn, supportsReasoningEffort bool, mappedEffort func() string) string {
	if !reasoningOn || !supportsReasoningEffort {
		return ""
	}
	return mappedEffort()
}

// thinkingToReasoningEffort maps a ThinkingLevel to an OpenAI-compatible
// reasoning_effort string using the model's thinkingLevelMap when present.
// Returns "" when the requested level maps to disabled reasoning so callers
// can omit the field entirely.
func thinkingToReasoningEffort(model *Model, level ThinkingLevel) string {
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
	supportsImages  bool
	grammarProps    map[string]string
	thinkingAsText  bool
	instructionRole string
	// systemTools converts the tools a later system message loads in place,
	// or returns none when the transcript does not anchor additions.
	systemTools func(SystemMessage) ([]oaiTool, error)
}

// convertCompletionsMessages converts the conversation after the leading
// system message. A later system message becomes a tool-bearing system
// message for its anchored additions, then its update text in the
// instruction role.
func convertCompletionsMessages(messages []Message, options completionsConvertOptions) ([]oaiMessage, error) {
	supportsImages, grammarProps, thinkingAsText := options.supportsImages, options.grammarProps, options.thinkingAsText
	out := make([]oaiMessage, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		switch message := messages[index].(type) {
		case SystemMessage:
			if options.systemTools != nil {
				tools, err := options.systemTools(message)
				if err != nil {
					return nil, err
				}
				if len(tools) > 0 {
					out = append(out, oaiMessage{Role: "system", Tools: tools})
				}
			}
			if text := RenderSystemMessageUpdate(message); text != "" {
				out = append(out, oaiMessage{Role: options.instructionRole, Content: text})
			}
		case UserMessage:
			switch content := message.Content.(type) {
			case UserText:
				out = append(out, oaiMessage{Role: "user", Content: string(content)})
			case UserContentBlocks:
				var parts []oaiContentPart
				for _, block := range content {
					switch block := block.(type) {
					case TextContent:
						// Pi drops empty text parts so image-only messages stay valid for
						// compatible providers; the message is dropped only when empty.
						if block.Text != "" {
							parts = append(parts, oaiContentPart{Type: "text", Text: block.Text})
						}
					case ImageContent:
						parts = append(parts, oaiContentPart{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:" + block.MimeType + ";base64," + block.Data}})
					}
				}
				if len(parts) > 0 {
					out = append(out, oaiMessage{Role: "user", Content: parts})
				}
			}
		case AssistantMessage:
			converted := oaiMessage{Role: "assistant", Content: ""}
			var textParts []oaiContentPart
			var thinking []ThinkingContent
			var legacyReasoningDetails []json.RawMessage
			for _, block := range message.Content {
				switch block := block.(type) {
				case TextContent:
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, oaiContentPart{Type: "text", Text: block.Text})
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
						arguments, _ := json.Marshal(block.Arguments)
						converted.ToolCalls = append(converted.ToolCalls, oaiRequestToolCall{ID: block.ID, Type: "function", Function: &oaiRequestToolCallFunction{Name: block.Name, Arguments: string(arguments)}})
					}
					if detail := parseLegacyOpenAIReasoningDetail(block.ThoughtSignature); detail != nil {
						legacyReasoningDetails = append(legacyReasoningDetails, detail)
					}
				}
			}
			var thinkingText []string
			var nonEmptyThinking []ThinkingContent
			for _, block := range thinking {
				if strings.TrimSpace(block.Thinking) != "" {
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
			var assistantText strings.Builder
			for _, part := range textParts {
				assistantText.WriteString(part.Text)
			}
			if thinkingAsText && len(thinkingText) > 0 {
				converted.Content = append([]oaiContentPart{{Type: "text", Text: strings.Join(thinkingText, "\n\n")}}, textParts...)
			} else {
				converted.Content = assistantText.String()
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
			if converted.Content != "" || len(converted.ToolCalls) > 0 || len(converted.ReasoningDetails) > 0 || converted.Reasoning != "" || converted.ReasoningContent != nil || converted.ReasoningText != "" {
				out = append(out, converted)
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
				if textResult == "" {
					if hasImages {
						textResult = "(see attached image)"
					} else {
						textResult = "(no tool output)"
					}
				}
				out = append(out, oaiMessage{Role: "tool", Content: sanitizeSurrogates(textResult), ToolCallID: result.ToolCallID})
				index++
			}
			index--
			if len(images) > 0 {
				parts := append([]oaiContentPart{{Type: "text", Text: "Attached image(s) from tool result:"}}, images...)
				out = append(out, oaiMessage{Role: "user", Content: parts})
			}
		}
	}
	return out, nil
}

type openAIReasoningDetail struct {
	fields []openAIReasoningDetailField
	typ    string
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
	requiredRaw := values[required]
	if bytes.Equal(requiredRaw, []byte("null")) || json.Unmarshal(requiredRaw, &requiredValue) != nil {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	detail := &openAIReasoningDetail{typ: typ}
	positions := map[string]int{}
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		name, ok := nameToken.(string)
		if !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		if position, exists := positions[name]; exists {
			detail.fields[position].value = value
			continue
		}
		positions[name] = len(detail.fields)
		detail.fields = append(detail.fields, openAIReasoningDetailField{name: name, value: value})
	}
	return detail, true
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

func (detail *openAIReasoningDetail) setString(name, value string) {
	detail.setField(name, marshalOpenAIJSONString(value))
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
	last.setString(contentField, last.stringField(contentField)+detail.stringField(contentField))
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
	if signature == "" || !json.Valid([]byte(signature)) {
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

// detectCompat auto-detects compat flags from provider name and base URL.
// Mirrors upstream openai-completions.ts detectCompat (lines 990-1035).
// Covers: cerebras, xAI, chutes.ai, deepseek, zai, opencode, groq, openrouter, ollama.
func detectCompat(providerID, baseURL string) *OpenAICompat {
	isZai := providerID == "zai" || strings.Contains(baseURL, "api.z.ai")
	isTogether := providerID == "together" || strings.Contains(baseURL, "api.together.ai") || strings.Contains(baseURL, "api.together.xyz")
	isMoonshot := providerID == "moonshotai" || providerID == "moonshotai-cn" || strings.Contains(baseURL, "api.moonshot.")
	isCloudflareWorkersAI := providerID == "cloudflare-workers-ai" || strings.Contains(baseURL, "api.cloudflare.com")
	isCloudflareAIGateway := providerID == "cloudflare-ai-gateway" || strings.Contains(baseURL, "gateway.ai.cloudflare.com")
	isDeepSeek := providerID == "deepseek" || strings.Contains(baseURL, "deepseek.com")
	isNonStandard := providerID == "cerebras" || strings.Contains(baseURL, "cerebras.ai") ||
		providerID == "xai" || strings.Contains(baseURL, "api.x.ai") ||
		isTogether ||
		strings.Contains(baseURL, "chutes.ai") ||
		isDeepSeek ||
		isZai ||
		isMoonshot ||
		providerID == "opencode" || strings.Contains(baseURL, "opencode.ai") ||
		isCloudflareWorkersAI || isCloudflareAIGateway
	isGroq := providerID == "groq" || strings.Contains(baseURL, "groq.com")
	isOllama := providerID == "ollama" || strings.Contains(baseURL, ":11434")
	isOpenRouter := providerID == "openrouter" || strings.Contains(baseURL, "openrouter.ai")

	if isOllama || isNonStandard || isGroq {
		f := false
		t := true
		maxField := "max_tokens"
		if upstreamUsesMaxTokens(providerID, baseURL) {
			maxField = "max_tokens"
		}
		thinkingFmt := "openai"
		switch {
		case isDeepSeek:
			thinkingFmt = "deepseek"
		case isZai:
			thinkingFmt = "zai"
		case isTogether:
			thinkingFmt = "together"
		}
		supportsReasoningEffort := !isOllama && !isMoonshot && !isTogether && !isCloudflareAIGateway
		supportsStrictMode := false // Upstream detectCompat enables strict mode only through model compat.
		supportsLongCacheRetention := !isTogether && !isCloudflareWorkersAI && !isCloudflareAIGateway
		compat := &OpenAICompat{
			SupportsDeveloperRole:    &f,
			SupportsReasoningEffort:  new(supportsReasoningEffort),
			SupportsStore:            &f,
			SupportsUsageInStreaming: &t,
			MaxTokensField:           maxField,
			ThinkingFormat:           thinkingFmt,
			RequiresReasoningContentOnAssistantMessages: new(isDeepSeek),
			SupportsStrictMode:                          new(supportsStrictMode),
			SupportsLongCacheRetention:                  new(supportsLongCacheRetention),
		}
		return compat
	}
	if isOpenRouter {
		t := true
		return &OpenAICompat{
			SupportsDeveloperRole:    &t,
			SupportsReasoningEffort:  &t,
			SupportsStore:            &t,
			SupportsUsageInStreaming: &t,
			MaxTokensField:           "max_completion_tokens",
			ThinkingFormat:           "openrouter",
			SupportsStrictMode:       new(false),
		}
	}
	return nil
}

// DetectCompat is exported for use by model resolution. Returns nil for OpenAI-direct (defaults suffice).
func DetectCompat(providerID, baseURL string) *OpenAICompat {
	return detectCompat(providerID, baseURL)
}

var toolCallIDForbidden = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// shortHash32 ports upstream packages/ai/src/utils/hash.ts shortHash: a 32-bit
// non-cryptographic hash rendered as two base36 numbers. Iterating UTF-16 code
// units with uint32 arithmetic (matching JS Math.imul low-32-bit wrap and `>>>`)
// keeps the output byte-identical to upstream for the tool-call ids it hashes.
func shortHash32(s string) string {
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
		hash := shortHash32(id)
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

// normalizeToolCallIDs rewrites tool-call and tool-result ids in place so each
// call and its matching result stay consistent (the same original id maps to the
// same normalized id). Mirrors upstream applying normalizeToolCallId across the
// whole message array via transformMessages.
func normalizeToolCallIDs(msgs []oaiMessage, providerID string) {
	for i := range msgs {
		for j := range msgs[i].ToolCalls {
			msgs[i].ToolCalls[j].ID = normalizeCompletionsToolCallID(msgs[i].ToolCalls[j].ID, providerID)
		}
		if msgs[i].ToolCallID != "" {
			msgs[i].ToolCallID = normalizeCompletionsToolCallID(msgs[i].ToolCallID, providerID)
		}
	}
}

// hasToolHistory reports whether the conversation already references tools via a
// tool result or an assistant tool call. Mirrors upstream openai-completions.ts
// hasToolHistory: some OpenAI-compatible proxies (Anthropic via LiteLLM) require
// the tools param to be present, even empty, once the history references tools.
func hasToolHistory(messages []Message) bool {
	for _, message := range messages {
		switch message := message.(type) {
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
func (p *openAIProvider) resolveRequestTools(messages []Message, tools []ToolSchema) (*[]oaiTool, error) {
	if len(tools) > 0 {
		converted, err := p.convertTools(tools)
		if err != nil {
			return nil, err
		}
		return &converted, nil
	}
	if hasToolHistory(messages) {
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
			Parameters:  t.Parameters,
		}
		if supportsStrict {
			strict, err := resolveJSONSchemaStrictSampling(t, supportsStrict)
			if err != nil {
				return nil, err
			}
			parameters, err := getJSONSchemaToolParameters(t, strict)
			if err != nil {
				return nil, err
			}
			fn.Parameters = parameters
			v := strict != nil && *strict
			fn.Strict = &v
		}
		out = append(out, oaiTool{Type: "function", Function: fn})
	}
	return out, nil
}

// ─── Stream ───────────────────────────────────────────────────────────────────

func (p *openAIProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("openai: invalid transcript: %w", err)
	}
	supportsMidConversation := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsMidConvoSystemMessages }, false)
	resolved := ResolveTranscript(transcript, supportsMidConversation)
	messages := resolved.Messages()
	conversation := WithoutInitialSystemMessage(messages)
	// Kimi loads later tool additions in tool-bearing system messages; the
	// request tools then hold only the initial tools.
	transcriptTools := ResolveTranscriptTools(messages, supportsMidConversation && p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsMidConvoToolAdditions }, false))
	tools := transcriptTools.RequestTools
	// grammarProps maps a grammar tool's name to the single string property that
	// carries its constrained input. It is derived once from every declared tool and
	// threaded into message replay, tool conversion, and stream reconstruction so
	// custom (grammar) tool calls round-trip. Mirrors openai-completions.ts:229.
	supportsGrammar := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsOpenAIGrammarTools }, false)
	grammarProps, err := createGrammarToolInputProperties(GetDeclaredTools(messages), supportsGrammar)
	if err != nil {
		return nil, err
	}
	msgs, err := p.convertMessagesWithCompat(conversation, grammarProps, p.systemPromptRole(opts.IsReasoning), transcriptTools.AnchorsAdditions)
	if err != nil {
		return nil, err
	}
	if opts.IsReasoning && p.compatBool(func(c *OpenAICompat) *bool { return c.RequiresReasoningContentOnAssistantMessages }, false) {
		for i := range msgs {
			if msgs[i].Role == "assistant" && msgs[i].ReasoningContent == nil {
				empty := ""
				msgs[i].ReasoningContent = &empty
			}
		}
	}
	if systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))]); systemPrompt != "" {
		role := p.systemPromptRole(opts.IsReasoning)
		msgs = append([]oaiMessage{{Role: role, Content: systemPrompt}}, msgs...)
	}

	req := oaiRequest{
		Model:    p.cfg.Model,
		Messages: msgs,
		Stream:   true,
	}
	requestTools, err := p.resolveRequestTools(messages, tools)
	if err != nil {
		return nil, err
	}
	req.Tools = requestTools
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
	if p.cfg.Compat != nil && p.cfg.Compat.VLLMPriority != nil {
		priority := *p.cfg.Compat.VLLMPriority
		req.Priority = &priority
	}
	// Upstream sends store: false whenever compat.supportsStore holds, which by
	// default is every provider except the non-standard ones. PiG used to send
	// store: true, which asks OpenAI to retain the conversation.
	if p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsStore }, !upstreamNonStandard(p.cfg.ProviderID, p.cfg.BaseURL)) {
		f := false
		req.Store = &f
	}
	// Wire thinking/reasoning level when the model supports extended reasoning.
	// Gated on compat.supportsReasoningEffort (default: true for OpenAI direct, false otherwise).
	if opts.IsReasoning {
		model := &Model{Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh}}
		if p.cfg.ProviderID == "openrouter" {
			if generated, ok := LookupModel(p.cfg.Model); ok {
				model = generated.ToModel()
			} else if generated, ok := LookupModel(strings.TrimPrefix(p.cfg.Model, "openrouter/")); ok {
				model = generated.ToModel()
			}
		} else if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
			model = generated.ToModel()
		} else if generated, ok := LookupModel(p.cfg.Model); ok {
			model = generated.ToModel()
		} else {
			model.ID = p.cfg.Model
		}
		// Clamp the requested thinking level to the model's supported range.
		// Exception: when the caller explicitly requests ThinkingOff, honor it
		// unconditionally: don't clamp UP to the lowest supported level.
		// Some catalog entries map off→nil (disabled) which would otherwise
		// cause ClampThinkingLevel to promote "off" to "minimal", sending a
		// reasoning_effort value that some APIs reject.
		clamped := opts.Thinking
		if clamped != ThinkingOff {
			clamped = ClampThinkingLevel(model, opts.Thinking)
		}
		thinkingFormat := "openai"
		if p.cfg.Compat != nil && p.cfg.Compat.ThinkingFormat != "" {
			thinkingFormat = p.cfg.Compat.ThinkingFormat
		}
		supportsRE := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsReasoningEffort }, isOpenAIDirectURL(p.cfg.BaseURL))
		reasoningOn := clamped != ThinkingOff && clamped != ""
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
		// Mirrors openai-completions.ts:555-615 thinkingFormat dispatch.
		switch thinkingFormat {
		case "zai":
			if reasoningOn {
				req.Thinking = map[string]any{"type": "enabled"}
			} else {
				req.Thinking = map[string]any{"type": "disabled"}
			}
			// 0.79.5: also send reasoning_effort when supported. Uses the same
			// clamped-level mapping as deepseek/together (mapped ?? raw). pig clamps
			// away from null-mapped levels, so upstream's null-skip guard can't be
			// reached here.
			if reasoningOn && supportsRE {
				req.ReasoningEffort = mappedEffort()
			}
		case "qwen":
			req.EnableThinking = &reasoningOn
			req.ReasoningEffort = qwenReasoningEffort(reasoningOn, supportsRE, mappedEffort)
		case "qwen-chat-template":
			req.ChatTemplateKwargs = map[string]any{"enable_thinking": reasoningOn, "preserve_thinking": true}
		case "chat-template":
			if p.cfg.Compat != nil {
				req.ChatTemplateKwargs = resolveChatTemplateValues(p.cfg.Compat.ChatTemplateKwargs, reasoningOn, mappedEffort, offEffort, opts.ThinkingBudgets, clamped)
			}
		case "baseten":
			resolved := make(map[string]any, len(p.cfg.Compat.ChatTemplateArgs))
			for key, value := range p.cfg.Compat.ChatTemplateArgs {
				variable, isVariable := value.(map[string]any)
				if !isVariable {
					resolved[key] = value
					continue
				}
				if omit, _ := variable["omitWhenOff"].(bool); omit && !reasoningOn {
					continue
				}
				switch variable["$var"] {
				case "thinking.enabled":
					resolved[key] = reasoningOn
				case "thinking.effort":
					if reasoningOn {
						resolved[key] = mappedEffort()
					} else if value, emit := offEffort(); emit {
						resolved[key] = value
					}
				}
			}
			if len(resolved) > 0 {
				req.ChatTemplateArgs = resolved
			}
			if supportsRE {
				if reasoningOn {
					req.ReasoningEffort = mappedEffort()
				} else if value, emit := offEffort(); emit {
					req.ReasoningEffort = value
				}
			}
		case "deepseek":
			if reasoningOn {
				req.Thinking = map[string]any{"type": "enabled"}
			} else if _, emit := offEffort(); emit {
				// 0.79.5: suppress thinking:{disabled} when thinkingLevelMap.off is
				// explicitly null; still send it when off is absent or a value.
				req.Thinking = map[string]any{"type": "disabled"}
			}
			if reasoningOn && supportsRE {
				req.ReasoningEffort = mappedEffort()
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
			}
		case "together":
			req.Reasoning = map[string]any{"enabled": reasoningOn}
			if reasoningOn && supportsRE {
				req.ReasoningEffort = mappedEffort()
			}
		case "string-thinking":
			if reasoningOn {
				req.Thinking = mappedEffort()
			} else if v, emit := offEffort(); emit {
				req.Thinking = v
			}
		default: // "openai"
			if reasoningOn && supportsRE {
				req.ReasoningEffort = mappedEffort()
			} else if !reasoningOn && supportsRE {
				if v, ok := model.ThinkingLevelMap[ModelThinkingLevel(ThinkingOff)]; ok && v != nil {
					req.ReasoningEffort = *v
				}
			}
		}

		if reasoningOn && p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsThinkingTokenBudget }, false) {
			budgets := DefaultThinkingBudgets()
			if opts.ThinkingBudgets != nil {
				if opts.ThinkingBudgets.Minimal > 0 {
					budgets.Minimal = opts.ThinkingBudgets.Minimal
				}
				if opts.ThinkingBudgets.Low > 0 {
					budgets.Low = opts.ThinkingBudgets.Low
				}
				if opts.ThinkingBudgets.Medium > 0 {
					budgets.Medium = opts.ThinkingBudgets.Medium
				}
				if opts.ThinkingBudgets.High > 0 {
					budgets.High = opts.ThinkingBudgets.High
				}
			}
			budget := budgets.High
			switch clamped {
			case ThinkingMinimal:
				budget = budgets.Minimal
			case ThinkingLow:
				budget = budgets.Low
			case ThinkingMedium:
				budget = budgets.Medium
			}
			ceiling := req.MaxTokens
			if ceiling == 0 {
				ceiling = req.MaxCompletionTokens
			}
			if ceiling == 0 {
				ceiling = model.Capabilities.MaxOutputTokens
			}
			budget = min(budget, max(0, ceiling-1024))
			if budget > 0 {
				req.ThinkingTokenBudget = budget
			}
		}
	}
	// OpenRouter provider routing preferences. Mirrors openai-completions.ts:613
	// (no baseURL gate as of 0.79.1: applies whenever compat sets it).
	if p.cfg.Compat != nil && p.cfg.Compat.OpenRouterRouting != nil {
		req.Provider = p.cfg.Compat.OpenRouterRouting
	}
	// Vercel AI Gateway provider routing preferences (only/order). Gated on the
	// Vercel gateway host. Mirrors openai-completions.ts:618-627.
	if p.cfg.Compat != nil && p.cfg.Compat.VercelGatewayRouting != nil && strings.Contains(p.cfg.BaseURL, "ai-gateway.vercel.sh") {
		routing := p.cfg.Compat.VercelGatewayRouting
		only, hasOnly := routing["only"]
		order, hasOrder := routing["order"]
		if hasOnly || hasOrder {
			gatewayOptions := map[string]any{}
			if hasOnly {
				gatewayOptions["only"] = only
			}
			if hasOrder {
				gatewayOptions["order"] = order
			}
			req.ProviderOptions = map[string]any{"gateway": gatewayOptions}
		}
	}
	// Prompt caching: send session ID as cache key. Gated on compat.sendSessionAffinityHeaders
	// OR direct OpenAI URL. Mirrors upstream openai-completions.ts:449-450.
	cacheRetention := getProviderEnvValue("PI_CACHE_RETENTION", mergeProviderEnv(p.cfg.Env, opts.Env))
	sendCache := p.compatBool(func(c *OpenAICompat) *bool { return c.SendSessionAffinityHeaders }, isOpenAIDirectURL(p.cfg.BaseURL))
	if opts.SessionID != "" && sendCache && cacheRetention != "none" {
		sid := ClampOpenAIPromptCacheKey(opts.SessionID)
		req.PromptCacheKey = &sid
		req.PromptCacheRetention = p.promptCacheRetention(opts.Env)
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
	if len(p.cfg.SamplingParams) > 0 || len(opts.SamplingParams) > 0 {
		encoded, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("openai: marshal sampling base: %w", err)
		}
		var merged map[string]any
		if err := json.Unmarshal(encoded, &merged); err != nil {
			return nil, fmt.Errorf("openai: decode sampling base: %w", err)
		}
		maps.Copy(merged, p.cfg.SamplingParams)
		maps.Copy(merged, opts.SamplingParams)
		payload = merged
	}
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(payload, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if err != nil {
			return nil, fmt.Errorf("openai: onPayload: %w", err)
		}
		if next != nil {
			payload = next
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	baseURL := p.cfg.BaseURL
	if p.cfg.GetBaseURL != nil {
		if u, err := p.cfg.GetBaseURL(ctx); err != nil {
			return nil, fmt.Errorf("%s: resolve base URL: %w", p.cfg.ProviderID, err)
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

	baseURL, err = ResolveCloudflareBaseURL(p.cfg.ProviderID, baseURL, mergeProviderEnv(p.cfg.Env, opts.Env))
	if err != nil {
		return nil, fmt.Errorf("%s: resolve base URL: %w", p.cfg.ProviderID, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", PiUserAgent())
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
	applyProviderHeaders(httpReq, opts.Headers)

	resp, err := p.client.Do(httpReq) //nolint:bodyclose // body closed via defer in SSE goroutine below
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(b))
	}

	builder := newAssistantStreamBuilder(ctx, APIOpenAICompletions, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	go func() {
		defer func() { _ = resp.Body.Close() }()
		p.parseSSE(ctx, resp.Body, builder, grammarProps)
	}()
	return builder.stream, nil
}

func resolveChatTemplateValues(config map[string]any, reasoningOn bool, effort func() string, offEffort func() (string, bool), budgets *ThinkingBudgets, level ThinkingLevel) map[string]any {
	if len(config) == 0 {
		return nil
	}
	resolved := make(map[string]any, len(config))
	thinkingBudgets := DefaultThinkingBudgets()
	if budgets != nil {
		if budgets.Minimal > 0 {
			thinkingBudgets.Minimal = budgets.Minimal
		}
		if budgets.Low > 0 {
			thinkingBudgets.Low = budgets.Low
		}
		if budgets.Medium > 0 {
			thinkingBudgets.Medium = budgets.Medium
		}
		if budgets.High > 0 {
			thinkingBudgets.High = budgets.High
		}
	}
	budget := thinkingBudgets.High
	switch level {
	case ThinkingMinimal:
		budget = thinkingBudgets.Minimal
	case ThinkingLow:
		budget = thinkingBudgets.Low
	case ThinkingMedium:
		budget = thinkingBudgets.Medium
	}
	for key, value := range config {
		variable, ok := value.(map[string]any)
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
		case "thinking.effort":
			if reasoningOn {
				resolved[key] = effort()
			} else if value, emit := offEffort(); emit {
				resolved[key] = value
			}
		case "thinking.budget":
			if reasoningOn {
				resolved[key] = budget
			}
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func (p *openAIProvider) promptCacheRetention(requestEnv ProviderEnv) *string {
	cacheRetention := getProviderEnvValue("PI_CACHE_RETENTION", mergeProviderEnv(p.cfg.Env, requestEnv))
	if cacheRetention != "long" {
		return nil
	}
	supportsLong := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsLongCacheRetention }, isOpenAIDirectURL(p.cfg.BaseURL))
	if !supportsLong {
		return nil
	}
	ret := "24h"
	return &ret
}

func (p *openAIProvider) parseSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder, grammarProps map[string]string) {
	// Track partial tool calls by index
	type partialTool struct {
		streamIndex int
		wireIndex   *int
		id          string
		name        string
		args        strings.Builder
		// customProp/customIn/grammarBuf reconstruct a streamed grammar tool
		// call's raw input into JSON arguments. Mirrors openai-completions.ts
		// StreamingToolCallBlock.customInput.
		customProp string
		customIn   string
		grammarBuf *grammarToolInputJSONBuffer
	}
	partials := map[int]*partialTool{}
	partialsByWireIndex := map[int]*partialTool{}
	partialsByID := map[string]*partialTool{}
	nextStreamIndex := 0
	var streamedReasoningDetails []*openAIReasoningDetail
	thinkingContentIndex := -1
	ensureThinkingBlock := func(signature string) int {
		if thinkingContentIndex < 0 {
			thinkingContentIndex = builder.thinkingBlockStart()
			builder.thinkingBlockSignature(thinkingContentIndex, signature)
		}
		return thinkingContentIndex
	}
	finishStreamedBlocks := func() {
		if thinkingContentIndex < 0 {
			return
		}
		builder.finishBlocksWithThinking(thinkingContentIndex)
		thinkingContentIndex = -1
	}
	fail := func(reason StopReason, err error) {
		finishStreamedBlocks()
		builder.fail(reason, err)
	}
	done := func(reason StopReason, usage *Usage, errorMessage string) {
		finishStreamedBlocks()
		builder.done(reason, usage, errorMessage)
	}
	// closeGrammarBuffers finalizes any open grammar tool-call buffers, emitting
	// the trailing JSON framing so replay carries complete {"prop":"value"}
	// arguments. Mirrors openai-completions.ts finishBlock closing customInput.
	// Returns false after surfacing an error so the caller stops the stream.
	closeGrammarBuffers := func() bool {
		for _, idx := range slices.Sorted(maps.Keys(partials)) {
			pt := partials[idx]
			if pt.grammarBuf == nil || pt.grammarBuf.Closed {
				continue
			}
			argsDelta, emit, err := appendGrammarToolInputJSONDelta(pt.grammarBuf, pt.customProp, pt.customIn, true)
			if err != nil {
				fail(StopReasonError, err)
				return false
			}
			if emit {
				builder.toolCallDelta(streamToolCallDelta{index: idx, id: pt.id, name: pt.name, argumentsDelta: argsDelta})
			}
		}
		return true
	}
	hasFinishReason := false
	var finishReason string
	var pendingUsage *Usage
	supportsFinishReason := p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsFinishReason }, true)
	inferredStopReason := func() StopReason {
		if len(partials) > 0 {
			return StopReasonToolUse
		}
		return StopReasonStop
	}

	decoder := newSSEDecoder(r)
	for decoder.Next() {
		if err := ctx.Err(); err != nil {
			fail(StopReasonAborted, err)
			return
		}
		data := decoder.Event().Data
		if data == "[DONE]" {
			if !hasFinishReason && supportsFinishReason {
				fail(StopReasonError, errors.New("Stream ended without finish_reason"))
				return
			}
			stopReason, errorMessage := inferredStopReason(), ""
			if hasFinishReason {
				stopReason, errorMessage = mapOAIFinishReason(finishReason)
			}
			if stopReason == StopReasonError {
				fail(StopReasonError, errors.New(errorMessage))
				return
			}
			if !closeGrammarBuffers() {
				return
			}
			done(stopReason, pendingUsage, errorMessage)
			return
		}

		var chunk oaiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			fail(StopReasonError, fmt.Errorf("openai-completions: invalid SSE JSON: %w", err))
			return
		}

		if errorMessage, ok := openAIStreamErrorMessage(chunk.Error); ok {
			fail(StopReasonError, errors.New(errorMessage))
			return
		}

		// Record usage when present, but keep processing this chunk's
		// choices. Some providers (e.g. gemini-3.5-flash via github-copilot)
		// attach usage to content-bearing chunks; returning here would drop
		// the rest of the stream and emit nothing. Termination is driven by
		// finish_reason / [DONE] / stream end, matching upstream
		// openai-completions.ts. The usage rides out on the terminal event.
		if chunk.Usage != nil {
			pendingUsage = parseChunkUsage(chunk.Usage, builder.modelCost)
		} else if len(chunk.Choices) > 0 && chunk.Choices[0].Usage != nil {
			// Fallback: some providers (e.g., Moonshot) return usage in
			// choice.usage instead of the standard chunk.usage.
			pendingUsage = parseChunkUsage(chunk.Choices[0].Usage, builder.modelCost)
		}
		builder.setUsage(pendingUsage)

		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				hasFinishReason = true
				finishReason = choice.FinishReason
			}
			delta := choice.Delta
			if delta.Content != "" {
				builder.textDelta(delta.Content)
			}
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
			for _, tc := range delta.ToolCalls {
				var pt *partialTool
				if tc.Index != nil {
					pt = partialsByWireIndex[*tc.Index]
				}
				if pt == nil && tc.ID != "" {
					pt = partialsByID[tc.ID]
				}
				if pt == nil {
					pt = &partialTool{streamIndex: nextStreamIndex}
					partials[nextStreamIndex] = pt
					nextStreamIndex++
				}
				if tc.Index != nil && pt.wireIndex == nil {
					pt.wireIndex = new(*tc.Index)
					partialsByWireIndex[*tc.Index] = pt
				}
				if tc.ID != "" {
					pt.id = tc.ID
					partialsByID[tc.ID] = pt
				}
				if tc.Custom != nil {
					// Grammar tool call: reconstruct JSON arguments from the raw
					// input, framing it as {"<prop>":"<input>"}. Mirrors
					// openai-completions.ts appendCustomToolCallInput.
					if tc.Custom.Name != "" {
						pt.name = tc.Custom.Name
					}
					if pt.grammarBuf == nil {
						prop := grammarProps[pt.name]
						if prop == "" {
							prop = "input"
						}
						pt.customProp = prop
						pt.grammarBuf = &grammarToolInputJSONBuffer{}
					}
					next := pt.customIn + tc.Custom.Input
					argsDelta, emit, err := appendGrammarToolInputJSONDelta(pt.grammarBuf, pt.customProp, next, false)
					pt.customIn = next
					if err != nil {
						fail(StopReasonError, err)
						return
					}
					if emit {
						pt.args.WriteString(argsDelta)
						builder.toolCallDelta(streamToolCallDelta{index: pt.streamIndex, id: tc.ID, name: tc.Custom.Name, argumentsDelta: argsDelta})
					}
				} else {
					if tc.Function.Name != "" {
						pt.name = tc.Function.Name
					}
					pt.args.WriteString(tc.Function.Arguments)
					builder.toolCallDelta(streamToolCallDelta{
						index: pt.streamIndex, id: tc.ID, name: tc.Function.Name, argumentsDelta: tc.Function.Arguments,
					})
				}
			}
			for _, raw := range delta.ReasoningDetails {
				var appended bool
				streamedReasoningDetails, appended = appendOpenAIReasoningDetail(streamedReasoningDetails, raw)
				if !appended {
					continue
				}
				contentIndex := ensureThinkingBlock("")
				builder.thinkingBlockSignature(contentIndex, marshalOpenAIReasoningDetails(streamedReasoningDetails))
			}
		}
	}
	if err := decoder.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		fail(StopReasonError, err)
		return
	}
	if err := ctx.Err(); err != nil {
		fail(StopReasonAborted, err)
		return
	}
	// Stream ended (EOF) without a [DONE] sentinel. Some providers (e.g.
	// together) close the connection after a final chunk that carries
	// finish_reason and usage rather than sending [DONE]. Emit the terminal
	// event here so the stop reason and accumulated usage are not lost,
	// matching upstream which finalizes output on stream end.
	switch {
	case hasFinishReason:
		stopReason, errorMessage := mapOAIFinishReason(finishReason)
		if stopReason == StopReasonError {
			fail(StopReasonError, errors.New(errorMessage))
			return
		}
		if !closeGrammarBuffers() {
			return
		}
		done(stopReason, pendingUsage, errorMessage)
	case !supportsFinishReason:
		if !closeGrammarBuffers() {
			return
		}
		done(inferredStopReason(), pendingUsage, "")
	default:
		fail(StopReasonError, errors.New("Stream ended without finish_reason"))
	}
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

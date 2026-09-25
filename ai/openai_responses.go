package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/openai-responses.ts
// and .upstream/current/packages/ai/src/providers/openai-responses-shared.ts.

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
	"strings"
)

// ─── Config ──────────────────────────────────────────────────────────────────

// openAIResponsesMinOutputTokens is the floor OpenAI Responses accepts for
// max_output_tokens; values below 16 are rejected (earendil-works/pi#6265).
const openAIResponsesMinOutputTokens = 16

// OpenAIResponsesConfig configures the OpenAI Responses API provider.
type OpenAIResponsesConfig struct {
	// BaseURL is the API base (default: https://api.openai.com/v1).
	BaseURL string
	// APIKey is the bearer token.
	APIKey string
	// Model is the model name sent in the request.
	Model string
	// ProviderID is the provider label (e.g. "openai", "github-copilot").
	ProviderID string
	// ExtraHeaders are added to every request.
	ExtraHeaders   map[string]string
	SamplingParams map[string]any
	// APIKeyHeader is the header name used for the API key.
	// Default: Authorization.
	APIKeyHeader string
	// APIKeyPrefix is prepended when APIKeyHeader is used.
	// Default: Bearer . Empty string sends the raw key.
	APIKeyPrefix string
	// BaseURLIsEndpoint means BaseURL/GetBaseURL already resolves to the
	// final Responses endpoint and should not have /responses appended.
	BaseURLIsEndpoint bool
	forceUserAgent    bool
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

func (p *openAIResponsesProvider) ID() string   { return p.cfg.ProviderID }
func (p *openAIResponsesProvider) Close() error { return nil }

// responsesCompat is the resolved subset of OpenAIResponsesCompat the request
// builder reads. SendSessionIdHeader and SupportsLongCacheRetention default
// true; the others default false, mirroring upstream openai-responses.ts
// (`?? false`).
type responsesCompat struct {
	SendSessionIdHeader            bool
	SupportsLongCacheRetention     bool
	SupportsStrictMode             bool
	SupportsOpenAIGrammarTools     bool
	SupportsMidConvoSystemMessages bool
	SupportsAdditionalTools        bool
	SupportsToolSearch             bool
}

func getResponsesCompat(compat *OpenAIResponsesCompat, strictModeDefault bool) responsesCompat {
	out := responsesCompat{
		SendSessionIdHeader:        true,
		SupportsLongCacheRetention: true,
		SupportsStrictMode:         strictModeDefault,
	}
	if compat == nil {
		return out
	}
	if compat.SendSessionIdHeader != nil {
		out.SendSessionIdHeader = *compat.SendSessionIdHeader
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
	out.SupportsToolSearch = compat.SupportsToolSearch != nil && *compat.SupportsToolSearch
	return out
}

func getPromptCacheRetention(compat responsesCompat, cacheRetention string) *string {
	if cacheRetention != "long" || !compat.SupportsLongCacheRetention {
		return nil
	}
	ret := "24h"
	return &ret
}

// ─── Request wire types ──────────────────────────────────────────────────────

// respInputItem is one element of the Responses API "input" array.
// Upstream: ResponseInput (union of message | function_call | function_call_output | reasoning).
type respInputItem struct {
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
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// For type=tool_search_call / tool_search_output: "client"
	Execution string `json:"execution,omitempty"`
	// For type=additional_tools / tool_search_output
	Tools []respTool `json:"tools,omitempty"`

	// For type=custom_tool_call (grammar tools): the raw string input.
	Input string `json:"input,omitempty"`

	// For type=function_call_output
	Output json.RawMessage `json:"output,omitempty"`

	// For type=reasoning (opaque replay)
	Summary          json.RawMessage `json:"summary,omitempty"`
	EncryptedContent json.RawMessage `json:"encrypted_content,omitempty"`
}

// respTool is one entry of the Responses API "tools" array. A function tool
// carries parameters (+ strict when the model supports strict mode); a grammar
// (custom) tool carries a format block instead. Mirrors upstream OpenAITool.
type respTool struct {
	Type        string         `json:"type"` // "function" | "custom"
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Strict is a tri-state: absent (nil), null, true, or false. The default
	// varies by provider (generic omits it; codex sends null; strict-mode models
	// send false), so it is a raw JSON token rather than *bool.
	Strict json.RawMessage `json:"strict,omitempty"`
	Format *respToolFormat `json:"format,omitempty"`
	// DeferLoading marks a tool loaded by a synthetic tool_search_output.
	DeferLoading bool `json:"defer_loading,omitempty"`
}

// respToolSearchArguments are the synthetic tool_search_call arguments.
type respToolSearchArguments struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// respToolFormat is a grammar (custom) tool's constrained-sampling format.
// Mirrors upstream OpenAITool custom.format.
type respToolFormat struct {
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
	Tools                []respTool        `json:"tools,omitempty"`
	ToolChoice           string            `json:"tool_choice,omitempty"`
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
	Input            string              `json:"input,omitempty"` // for custom_tool_call items
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

// resolvedModel returns the reasoning/thinking view of the resolved catalog
// entry, falling back to a synthetic high-thinking model when the config names
// no known model (matching upstream's default when the SDK is handed an unknown
// model id).
func (p *openAIResponsesProvider) resolvedModel() *Model {
	if generated, ok := p.resolveResponsesModel(); ok {
		return generated.ToModel()
	}
	return &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh}}
}

// modelSupportsImages reports whether the configured responses model accepts
// image input, mirroring upstream's `model.input.includes("image")` gate. It
// reads image support off the same entry resolveResponsesModel backs the
// reasoning clamp with, so a codex or non-codex provider never gates images on a
// different catalog entry than the one that drives thinking.
func (p *openAIResponsesProvider) modelSupportsImages() bool {
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

// instructionRole mirrors upstream convertResponsesMessages instructionRole:
// developer for reasoning models unless compat sets supportsDeveloperRole
// false, otherwise system.
func (p *openAIResponsesProvider) instructionRole() string {
	if p.cfg.IsReasoning && (p.cfg.Compat == nil || p.cfg.Compat.SupportsDeveloperRole == nil || *p.cfg.Compat.SupportsDeveloperRole) {
		return "developer"
	}
	return "system"
}

// systemToolAdditions mirrors upstream appendSystemToolAdditions: when the
// transcript anchors additions, a later system message's added tools load in
// place as an additional_tools item, or else as a synthetic client
// tool_search_call and tool_search_output pair.
func (p *openAIResponsesProvider) systemToolAdditions(message SystemMessage, seed string, anchorsAdditions bool) ([]respInputItem, error) {
	compat := getResponsesCompat(p.cfg.Compat, p.cfg.StrictModeDefault)
	if !anchorsAdditions || len(message.ToolsAdded) == 0 || (!compat.SupportsAdditionalTools && !compat.SupportsToolSearch) {
		return nil, nil
	}
	tools, err := p.convertTools(message.ToolsAdded, compat.SupportsStrictMode, compat.SupportsOpenAIGrammarTools)
	if err != nil {
		return nil, err
	}
	if compat.SupportsAdditionalTools {
		return []respInputItem{{Type: "additional_tools", Role: "developer", Tools: tools}}, nil
	}
	names := make([]string, len(message.ToolsAdded))
	for i, tool := range message.ToolsAdded {
		names[i] = tool.Name
		tools[i].DeferLoading = true
	}
	callID := "pi_tool_load_" + shortHash32(seed+":"+strings.Join(names, ","))
	arguments, _ := json.Marshal(respToolSearchArguments{Query: strings.Join(names, " "), Limit: len(names)})
	return []respInputItem{
		{Type: "tool_search_call", CallID: callID, Execution: "client", Status: "completed", Arguments: arguments},
		{Type: "tool_search_output", CallID: callID, Execution: "client", Status: "completed", Tools: tools},
	}, nil
}

func (p *openAIResponsesProvider) convertMessages(messages []Message, grammarProps map[string]string) ([]respInputItem, error) {
	return p.convertAnchoredMessages(messages, grammarProps, false)
}

// convertAnchoredMessages converts the conversation after the leading system
// message; anchorsAdditions is ResolveTranscriptTools over the whole
// transcript.
func (p *openAIResponsesProvider) convertAnchoredMessages(messages []Message, grammarProps map[string]string, anchorsAdditions bool) ([]respInputItem, error) {
	var items []respInputItem
	messageIndex := 0
	supportsImages := p.modelSupportsImages()
	for _, message := range messages {
		switch message := message.(type) {
		case SystemMessage:
			additions, err := p.systemToolAdditions(message, fmt.Sprintf("system:%d", messageIndex), anchorsAdditions)
			if err != nil {
				return nil, err
			}
			items = append(items, additions...)
			if text := RenderSystemMessageUpdate(message); text != "" {
				content, _ := json.Marshal(sanitizeSurrogates(text))
				items = append(items, respInputItem{Role: p.instructionRole(), Content: content})
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
			items = append(items, respInputItem{Role: "user", Content: content})
		case ToolResultMessage:
			callID := normalizeResponsesToolCallID(message.ToolCallID, p.cfg.ProviderID)
			if before, _, ok := strings.Cut(callID, "|"); ok {
				callID = before
			}
			resultType := "function_call_output"
			if _, isGrammar := grammarProps[message.ToolName]; isGrammar {
				resultType = "custom_tool_call_output"
			}
			items = append(items, respInputItem{Type: resultType, CallID: callID, Output: convertResponsesToolResultOutput(message.Content, supportsImages)})
		case AssistantMessage:
			initialItemCount := len(items)
			textBlockIndex := 0
			for _, block := range message.Content {
				switch block := block.(type) {
				case ThinkingContent:
					if block.ThinkingSignature != "" {
						var reasoning respInputItem
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
								id = "msg_" + shortHash32(id)
							}
						}
						phase = signedPhase
					}
					textBlockIndex++
					content, _ := json.Marshal([]map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}})
					items = append(items, respInputItem{Type: "message", Role: "assistant", Content: content, Status: "completed", ID: id, Phase: phase})
				case ToolCall:
					normalized := normalizeResponsesToolCallID(block.ID, p.cfg.ProviderID)
					callID, itemID, _ := strings.Cut(normalized, "|")
					if property, isGrammar := grammarProps[block.Name]; isGrammar {
						input, err := getGrammarToolInput(block.Name, block.Arguments, property)
						if err != nil {
							return nil, err
						}
						items = append(items, respInputItem{Type: "custom_tool_call", ID: itemID, CallID: callID, Name: block.Name, Input: sanitizeSurrogates(input)})
					} else {
						arguments, _ := json.Marshal(block.Arguments)
						encoded, _ := json.Marshal(string(arguments))
						items = append(items, respInputItem{Type: "function_call", ID: itemID, CallID: callID, Name: block.Name, Arguments: encoded})
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

// responsesToolCallProviders lists the openai-responses providers whose models
// emit pipe-joined {call_id}|{item_id} tool-call ids that need normalization.
// Mirrors upstream openai-responses.ts OPENAI_TOOL_CALL_PROVIDERS.
var responsesToolCallProviders = map[string]bool{
	"openai":       true,
	"openai-codex": true,
	"opencode":     true,
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
// fc_<shortHash>, capped at 64. shortHash32 is byte-identical to upstream shortHash.
func buildForeignResponsesItemID(itemID string) string {
	normalized := "fc_" + shortHash32(itemID)
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return normalized
}

// normalizeResponsesToolCallID ports upstream openai-responses-shared.ts
// normalizeToolCallId. For an allowed provider it splits {call_id}|{item_id},
// sanitizes the call part, and reduces the item part to a Codex-safe fc_ id
// (the Responses API rejects item ids that are not fc_-prefixed and bounded).
// Upstream distinguishes same-provider vs foreign tool calls via source.provider,
// which pig's Message model does not carry; a native openai-responses item id is
// always fc_-prefixed while a foreign provider's (e.g. github-copilot
// call_xxx|<base64>) never is, so on well-formed history the fc_ prefix decides
// foreign-vs-same exactly as upstream. The two can differ only on poisoned or
// migrated history: a same-origin item id that is not fc_-prefixed is hashed here
// but sanitize-and-preserved upstream (and the reverse for a foreign id that is
// already fc_-prefixed). Both remain valid, deterministically paired, and are
// never persisted, so there is no observable or interop difference (not a
// divergence). Locked by TestResponsesToolCallID_SameOriginNonFcItemIsHashed.
// Non-allowed providers keep pig's existing raw id (no normalization applied).
func normalizeResponsesToolCallID(id, providerID string) string {
	if !responsesToolCallProviders[providerID] {
		return id
	}
	before, after, ok := strings.Cut(id, "|")
	if !ok {
		return normalizeResponsesIDPart(id)
	}
	callID := normalizeResponsesIDPart(before)
	var itemID string
	if strings.HasPrefix(after, "fc_") {
		itemID = normalizeResponsesIDPart(after)
	} else {
		itemID = buildForeignResponsesItemID(after)
	}
	if !strings.HasPrefix(itemID, "fc_") {
		itemID = normalizeResponsesIDPart("fc_" + itemID)
	}
	return callID + "|" + itemID
}

// convertTools maps tools to the Responses API "tools" array. A grammar
// constrained-sampling tool becomes a custom tool with a format block; a
// json_schema constrained tool requests strict mode when supported; a plain
// tool emits strict only when the model supports strict mode (default false,
// so strict is omitted for most models). Mirrors upstream convertResponsesTools.
func (p *openAIResponsesProvider) convertTools(tools []ToolSchema, supportsStrictMode, supportsGrammar bool) ([]respTool, error) {
	out := make([]respTool, 0, len(tools))
	for _, t := range tools {
		grammar, err := resolveGrammarConstrainedSampling(t, supportsGrammar)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			out = append(out, respTool{
				Type:        "custom",
				Name:        t.Name,
				Description: t.Description,
				Format: &respToolFormat{
					Type:       "grammar",
					Syntax:     grammar.Format,
					Definition: grammar.Definition,
				},
			})
			continue
		}
		strict, err := resolveJSONSchemaStrictSampling(t, supportsStrictMode)
		if err != nil {
			return nil, err
		}
		parameters, err := getJSONSchemaToolParameters(t, strict)
		if err != nil {
			return nil, err
		}
		ft := respTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  parameters,
		}
		if supportsStrictMode {
			switch {
			case strict != nil && *strict:
				ft.Strict = json.RawMessage("true")
			case p.cfg.StrictToolNull:
				ft.Strict = json.RawMessage("null")
			default:
				ft.Strict = json.RawMessage("false")
			}
		}
		out = append(out, ft)
	}
	return out, nil
}

// ─── Stream ──────────────────────────────────────────────────────────────────

func (p *openAIResponsesProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("openai-responses: invalid transcript: %w", err)
	}
	compat := getResponsesCompat(p.cfg.Compat, p.cfg.StrictModeDefault)
	resolved := ResolveTranscript(transcript, compat.SupportsMidConvoSystemMessages)
	messages := resolved.Messages()
	conversation := WithoutInitialSystemMessage(messages)
	transcriptTools := ResolveTranscriptTools(messages, compat.SupportsAdditionalTools || compat.SupportsToolSearch)
	tools := transcriptTools.RequestTools
	grammarProps, err := createGrammarToolInputProperties(GetDeclaredTools(messages), compat.SupportsOpenAIGrammarTools)
	if err != nil {
		return nil, err
	}
	msgs, err := p.convertAnchoredMessages(conversation, grammarProps, transcriptTools.AnchorsAdditions)
	if err != nil {
		return nil, err
	}
	originalThinking := opts.Thinking
	if p.cfg.ProviderID == string(APIOpenAICodexResponses) && opts.Thinking != ThinkingOff && opts.Thinking != "" {
		opts.Thinking = ClampThinkingLevel(p.resolvedModel(), opts.Thinking)
	}
	defer func() { opts.Thinking = originalThinking }()
	if !p.cfg.Codex {
		if systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))]); systemPrompt != "" {
			sysContent, _ := json.Marshal(sanitizeSurrogates(systemPrompt))
			sys := respInputItem{Role: p.instructionRole(), Content: sysContent}
			msgs = append([]respInputItem{sys}, msgs...)
		}
	}

	inputJSON, marshalErr := json.Marshal(msgs)
	if marshalErr != nil {
		return nil, fmt.Errorf("openai-responses: marshal input: %w", marshalErr)
	}

	req := respRequest{
		Model:  p.cfg.Model,
		Input:  inputJSON,
		Stream: true,
	}
	store := false
	req.Store = &store
	if p.cfg.Codex {
		req.Instructions = "You are a helpful assistant."
		if initial := GetInitialSystemMessage(messages); initial != nil {
			if prompt := GetCurrentSystemPrompt([]Message{*initial}); prompt != "" {
				req.Instructions = sanitizeSurrogates(prompt)
			}
		}
		req.Text = map[string]string{"verbosity": "low"}
		req.ToolChoice = "auto"
		if toolChoice, ok := opts.ToolChoice.(string); ok && toolChoice != "" {
			req.ToolChoice = toolChoice
		}
		parallel := true
		req.ParallelToolCalls = &parallel
		req.Include = []string{"reasoning.encrypted_content"}
	}

	if len(tools) > 0 {
		convertedTools, err := p.convertTools(tools, compat.SupportsStrictMode, compat.SupportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		req.Tools = convertedTools
	}
	if !p.cfg.Codex && opts.MaxTokens > 0 && (p.cfg.Compat == nil || p.cfg.Compat.SupportsMaxOutputTokens == nil || *p.cfg.Compat.SupportsMaxOutputTokens) {
		// OpenAI Responses rejects max_output_tokens below 16 (earendil-works/pi#6265).
		req.MaxOutputTokens = max(opts.MaxTokens, openAIResponsesMinOutputTokens)
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		req.Temperature = new(opts.Temperature)
	}

	// Reasoning/thinking support.
	// Mirrors upstream openai-responses.ts buildParams reasoning block.
	if p.cfg.IsReasoning || opts.IsReasoning {
		model := p.resolvedModel()
		clamped := opts.Thinking
		if !p.cfg.Codex || (clamped != ThinkingOff && clamped != "") {
			clamped = ClampThinkingLevel(model, clamped)
		}
		if clamped != ThinkingOff && clamped != "" {
			effort := string(clamped)
			if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(clamped)]; ok && mapped != nil {
				effort = *mapped
			}
			req.Reasoning = &respReasoning{
				Effort:  effort,
				Summary: "auto",
			}
			req.Include = []string{"reasoning.encrypted_content"}
		} else if p.cfg.ProviderID != "github-copilot" {
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
	if cacheRetention == "" && !p.cfg.Codex {
		cacheRetention = getProviderEnvValue("PI_CACHE_RETENTION", mergeProviderEnv(p.cfg.Env, opts.Env))
	}
	if opts.SessionID != "" && cacheRetention != "none" {
		req.PromptCacheKey = ClampOpenAIPromptCacheKey(opts.SessionID)
		if !p.cfg.Codex {
			if ret := getPromptCacheRetention(compat, cacheRetention); ret != nil {
				req.PromptCacheRetention = *ret
			}
		}
	}

	payload := any(req)
	if len(p.cfg.SamplingParams) > 0 || len(opts.SamplingParams) > 0 {
		encoded, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("openai-responses: marshal sampling base: %w", err)
		}
		var merged map[string]any
		if err := json.Unmarshal(encoded, &merged); err != nil {
			return nil, fmt.Errorf("openai-responses: decode sampling base: %w", err)
		}
		maps.Copy(merged, p.cfg.SamplingParams)
		maps.Copy(merged, opts.SamplingParams)
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

	apiKey := p.cfg.APIKey
	if p.cfg.GetAPIKey != nil {
		if k, err := p.cfg.GetAPIKey(ctx); err != nil {
			return nil, fmt.Errorf("%s: resolve API key: %w", p.cfg.ProviderID, err)
		} else {
			apiKey = k
		}
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
			cacheSessionID = ClampOpenAIPromptCacheKey(opts.SessionID)
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
	if opts.SessionID != "" && cacheRetention != "none" {
		sessionID := ClampOpenAIPromptCacheKey(opts.SessionID)
		if p.cfg.Codex {
			httpReq.Header.Set("session-id", sessionID)
		} else if compat.SendSessionIdHeader {
			httpReq.Header.Set("session_id", sessionID)
		}
		httpReq.Header.Set("x-client-request-id", sessionID)
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
	if p.cfg.Codex {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("chatgpt-account-id", accountID)
		httpReq.Header.Set("originator", "pi")
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
	if p.cfg.forceUserAgent {
		// pig divergence (D65): Codex reapplies PiG's product identity after
		// model and request headers, preserving upstream precedence.
		httpReq.Header.Set("User-Agent", PiUserAgent())
	}

	resp, err := p.client.Do(httpReq) //nolint:bodyclose // body closed via defer in SSE goroutine below
	if err != nil {
		return nil, fmt.Errorf("openai-responses: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("openai-responses: HTTP %d: %s", resp.StatusCode, string(b))
	}

	api := APIOpenAIResponses
	if p.cfg.Codex {
		api = APIOpenAICodexResponses
	}
	builder := newAssistantStreamBuilder(ctx, api, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	go func() {
		defer func() { _ = resp.Body.Close() }()
		bodyReader := io.Reader(resp.Body)
		if p.cfg.Codex {
			bodyReader = newCodexMappedSSEReader(ctx, resp.Body)
		}
		p.parseResponsesSSE(ctx, bodyReader, builder, grammarProps)
	}()
	return builder.stream, nil
}

// getServiceTierCostMultiplier mirrors openai-responses.ts and
// openai-codex-responses.ts. PiG sends no request service tier, so both
// upstream resolvers reduce to the tier the response reports.
func getServiceTierCostMultiplier(modelID, serviceTier string) float64 {
	switch serviceTier {
	case "flex":
		return 0.5
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
func applyServiceTierPricing(usage *Usage, serviceTier, modelID string) {
	multiplier := getServiceTierCostMultiplier(modelID, serviceTier)
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
			builder.toolCallStart(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, namespace: item.Namespace})
		case "custom_tool_call":
			state.toolIndex = nextToolIndex
			nextToolIndex++
			state.toolID = item.CallID + "|" + item.ID
			state.toolName = item.Name
			state.customProp = grammarProps[item.Name]
			if state.customProp == "" {
				state.customProp = "input"
			}
			state.customIn = item.Input
			state.grammarBuf = &grammarToolInputJSONBuffer{}
			builder.toolCallStart(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, namespace: item.Namespace})
		default:
			return nil
		}
		states[outputIndex] = state
		return state
	}

	decoder := newSSEDecoder(r)
	for decoder.Next() {
		if err := ctx.Err(); err != nil {
			builder.fail(StopReasonAborted, err)
			return
		}
		data := decoder.Event().Data
		if data == "[DONE]" {
			break
		}

		var event respSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			builder.fail(StopReasonError, fmt.Errorf("openai-responses: invalid SSE JSON: %w", err))
			return
		}

		if !p.cfg.ignoreSSEErrorObjects {
			if errorMessage, ok := openAIStreamErrorMessage(event.StreamError); ok {
				builder.fail(StopReasonError, errors.New(errorMessage))
				return
			}
		}

		switch event.Type {
		case "response.created":
			if event.Response != nil {
				builder.setResponseMetadata(event.Response.ID, event.Response.Model, "", "", nil)
			}

		case "response.output_item.added":
			var item respOutputItem
			if err := json.Unmarshal(event.Item, &item); err != nil {
				continue
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
					state.argsJSON.WriteString(after)
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, argumentsDelta: after})
				}
			}

		case "response.custom_tool_call_input.delta":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "custom_tool_call" {
				next := state.customIn + event.Delta
				delta, ok, err := appendGrammarToolInputJSONDelta(state.grammarBuf, state.customProp, next, false)
				state.customIn = next
				if err != nil {
					builder.fail(StopReasonError, fmt.Errorf("openai-responses: %w", err))
					return
				}
				if ok {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, argumentsDelta: delta})
				}
			}

		case "response.custom_tool_call_input.done":
			if state := states[event.OutputIndex]; state != nil && state.itemType == "custom_tool_call" {
				delta, ok, err := appendGrammarToolInputJSONDelta(state.grammarBuf, state.customProp, event.Input, true)
				state.customIn = event.Input
				if err != nil {
					builder.fail(StopReasonError, fmt.Errorf("openai-responses: %w", err))
					return
				}
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
				finalInput := cmp.Or(item.Input, state.customIn)
				delta, ok, err := appendGrammarToolInputJSONDelta(state.grammarBuf, state.customProp, finalInput, true)
				if err != nil {
					builder.fail(StopReasonError, fmt.Errorf("openai-responses: %w", err))
					return
				}
				if ok {
					builder.toolCallDelta(streamToolCallDelta{index: state.toolIndex, id: state.toolID, name: state.toolName, namespace: item.Namespace, argumentsDelta: delta})
				}
				builder.endToolCall(state.toolIndex)
			}
			delete(states, event.OutputIndex)

		case "response.completed", "response.incomplete", "response.done":
			if event.Type == "response.done" && !p.cfg.Codex {
				continue
			}
			if event.Response == nil {
				builder.fail(StopReasonError, errors.New("openai-responses: terminal response is missing response metadata"))
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
				if !p.cfg.SkipServiceTierPricing {
					applyServiceTierPricing(usage, event.Response.ServiceTier, p.cfg.Model)
				}
				builder.setUsage(usage)
			}
			rawStopReason := event.Response.Status
			if p.cfg.Codex && !codexResponseStatuses[rawStopReason] {
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
			builder.setResponseMetadata(event.Response.ID, event.Response.Model, rawStopReason, "", event.Response.EndTurn)
			if stopReason == StopReasonError {
				builder.fail(stopReason, errors.New(errorMessage))
			} else {
				builder.done(stopReason, usage, "")
			}
			return

		case "response.failed":
			msg := "Unknown error (no error details in response)"
			if event.Response != nil {
				builder.setResponseMetadata(event.Response.ID, event.Response.Model, event.Response.Status, "", nil)
				builder.setUsage(parseResponsesUsage(event.Response.Usage))
				if e := event.Response.Error; e != nil {
					code := cmp.Or(e.Code, "unknown")
					message := cmp.Or(e.Message, "no message")
					msg = code + ": " + message
				} else if d := event.Response.IncompleteDetails; d != nil && d.Reason != "" {
					msg = "incomplete: " + d.Reason
				}
			}
			builder.fail(StopReasonError, fmt.Errorf("openai-responses: %s", msg))
			return

		case "error":
			builder.fail(StopReasonError, fmt.Errorf("openai-responses: error %s: %s", event.Code, event.Message))
			return
		}
	}

	if err := decoder.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		builder.fail(StopReasonError, err)
		return
	}
	if err := ctx.Err(); err != nil {
		builder.fail(StopReasonAborted, err)
		return
	}
	builder.fail(StopReasonError, errors.New("OpenAI Responses stream ended before a terminal response event"))
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// sanitizeSurrogates removes unpaired UTF-16 surrogates from a string.
// Mirrors upstream sanitizeSurrogates from sanitize-unicode.ts.
func sanitizeSurrogates(s string) string {
	// Go strings are valid UTF-8; unpaired surrogates can't exist.
	// This is a no-op but kept for upstream structural fidelity.
	return s
}

func joinResponseItemText(parts []respOutputContent) string {
	values := make([]string, len(parts))
	for index, part := range parts {
		values[index] = part.Text
	}
	return strings.Join(values, "\n\n")
}

func parseResponsesTextSignature(signature string) (id, phase string, ok bool) {
	if signature == "" {
		return "", "", false
	}
	if strings.HasPrefix(signature, "{") {
		var parsed struct {
			Version int     `json:"v"`
			ID      *string `json:"id"`
			Phase   string  `json:"phase"`
		}
		if json.Unmarshal([]byte(signature), &parsed) == nil && parsed.Version == 1 && parsed.ID != nil {
			if parsed.Phase != "commentary" && parsed.Phase != "final_answer" {
				parsed.Phase = ""
			}
			return *parsed.ID, parsed.Phase, true
		}
	}
	return signature, "", true
}

func encodeResponsesTextSignature(id, phase string) string {
	signature := struct {
		Version int    `json:"v"`
		ID      string `json:"id"`
		Phase   string `json:"phase,omitempty"`
	}{Version: 1, ID: id}
	if phase == "commentary" || phase == "final_answer" {
		signature.Phase = phase
	}
	encoded, _ := json.Marshal(signature)
	return string(encoded)
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

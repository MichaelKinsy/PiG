package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/anthropic.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"strings"
)

// AnthropicConfig configures the Anthropic Messages API provider.
type AnthropicConfig struct {
	// APIKey is the Anthropic API key, sent as X-Api-Key. A subscription
	// (sk-ant-oat) token is sent as an Authorization bearer token with the
	// Claude Code identity instead. When APIKey is empty, the anthropic
	// provider sends ANTHROPIC_AUTH_TOKEN as an Authorization bearer header.
	APIKey string
	// Model is the model name sent in the request (e.g. "claude-sonnet-4-20250514").
	Model string
	// ProviderID is the provider label (default: "anthropic").
	ProviderID string
	// BaseURL is the API base (default: https://api.anthropic.com).
	BaseURL string
	// ExtraHeaders are added to every request.
	ExtraHeaders map[string]string
	// Env holds provider-scoped environment overrides that take precedence over
	// the process environment when resolving provider configuration such as
	// PI_CACHE_RETENTION. Mirrors the upstream StreamOptions.env threaded into
	// the provider.
	Env ProviderEnv
	// Compat controls Anthropic-API-specific capability quirks for proxied
	// Anthropic-compatible providers.
	Compat *AnthropicMessagesCompat

	// GetAPIKey dynamically obtains the auth token. Takes precedence over
	// APIKey. Called once per Stream() invocation. Used by GitHub Copilot
	// to auto-refresh the bearer token.
	GetAPIKey func(ctx context.Context) (string, error)
	// GetBaseURL dynamically resolves the base URL. Takes precedence over
	// BaseURL. Used by GitHub Copilot to extract proxy-ep from the
	// refreshed token.
	GetBaseURL func(ctx context.Context) (string, error)
	// DynamicHeaders returns per-request headers (e.g. Copilot X-Initiator).
	// They apply only with UseBearerAuth, the Copilot client.
	DynamicHeaders func(transcript TranscriptContext, opts StreamOptions) map[string]string
	// UseBearerAuth selects the Copilot client: the token is sent as
	// Authorization: Bearer instead of X-Api-Key, with DynamicHeaders and
	// without the Claude Code identity.
	UseBearerAuth bool
}

type anthropicProvider struct {
	cfg    AnthropicConfig
	client *http.Client
}

// NewAnthropicProvider creates a Provider backed by the Anthropic Messages API.
func NewAnthropicProvider(cfg AnthropicConfig) Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "anthropic"
	}
	return &anthropicProvider{cfg: cfg, client: streamingHTTPClient()}
}

func (p *anthropicProvider) ID() string { return p.cfg.ProviderID }

func (p *anthropicProvider) Close() error { return nil }

// ─── Request wire types ──────────────────────────────────────────────────────

type anthSystemBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

type anthCacheControl struct {
	Type string `json:"type"` // "ephemeral"
	TTL  string `json:"ttl,omitempty"`
}

type anthRequest struct {
	Model     string        `json:"model"`
	Messages  []anthMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream"`
	// Betas is visible to OnPayload as upstream params.betas. The wire body
	// omits it and sends it as the anthropic-beta header instead.
	Betas      []string          `json:"betas,omitempty"`
	System     []anthSystemBlock `json:"system,omitempty"`
	Tools      []anthTool        `json:"tools,omitempty"`
	ToolChoice any               `json:"tool_choice,omitempty"`
	Metadata   *anthMetadata     `json:"metadata,omitempty"`
	Thinking   *anthThinking     `json:"thinking,omitempty"`
	// OutputConfig controls output quality knobs like thinking effort.
	// Used with adaptive thinking (Opus 4.6+, Sonnet 4.6) to set effort level.
	// Mirrors upstream anthropic.ts output_config.
	OutputConfig *anthOutputConfig `json:"output_config,omitempty"`
	// Temperature is incompatible with extended thinking.
	Temperature *float64 `json:"temperature,omitempty"`
}

type anthMetadata struct {
	UserID string `json:"user_id"`
}

type anthThinking struct {
	Type         string            `json:"type"`                    // "enabled" | "adaptive" | "disabled"
	BudgetTokens int               `json:"budget_tokens,omitempty"` // for "enabled"
	Display      string            `json:"display,omitempty"`
	BlockBinding *anthBlockBinding `json:"block_binding,omitempty"`
}

type anthBlockBinding struct {
	PrefixMismatchBehavior string `json:"prefix_mismatch_behavior"`
}

type anthOutputConfig struct {
	Effort string `json:"effort"` // "low" | "medium" | "high" | "xhigh" | "max"
}

type anthMessage struct {
	Role         string            `json:"role"`
	Content      any               `json:"content"` // string | []anthContentBlock
	OutputConfig *anthOutputConfig `json:"output_config,omitempty"`
}

// anthContentBlock is the union type for Anthropic content blocks.
// Fields are selectively present depending on Type.
type anthContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // string | []block for tool_result
	IsError   *bool  `json:"is_error,omitempty"`
	// image (base64 source: {type, media_type, data})
	Source any `json:"source,omitempty"`
	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// redacted_thinking
	Data string `json:"data,omitempty"`
	// tool_addition / tool_removal
	Tool *anthToolReference `json:"tool,omitempty"`
	// cache_control
	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

// anthToolReference names a declared tool in a tool_addition or tool_removal
// block.
type anthToolReference struct {
	Type string `json:"type"` // "tool_reference"
	Name string `json:"name"`
}

type anthTool struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Strict           *bool             `json:"strict,omitempty"`
	InputSchema      map[string]any    `json:"input_schema"`
	CacheControl     *anthCacheControl `json:"cache_control,omitempty"`
	EagerInputStream any               `json:"eager_input_streaming,omitempty"`
	// DeferLoading declares a tool that stays inactive until a tool_addition
	// block surfaces it.
	DeferLoading bool `json:"defer_loading,omitempty"`
}

// deferredToolPlaceholder mirrors upstream DEFERRED_TOOL_PLACEHOLDER: a stable
// deferred tool declared whenever native tool changes are in use. Anthropic adds
// hidden prompt scaffolding as soon as any tool has defer_loading; declaring
// the placeholder from the first request keeps that scaffolding in the cached
// prefix. It is never activated.
func deferredToolPlaceholder() anthTool {
	return anthTool{
		Name:         "__pi_deferred_placeholder__",
		Description:  "Reserved placeholder. Never available. Never call this.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
		DeferLoading: true,
	}
}

// ─── SSE response types ──────────────────────────────────────────────────────

type anthEventMessageStart struct {
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheCreation            *struct {
				Ephemeral1H int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation,omitempty"`
			OutputTokensDetails *struct {
				ThinkingTokens int `json:"thinking_tokens"`
			} `json:"output_tokens_details,omitempty"`
		} `json:"usage"`
	} `json:"message"`
}

type anthEventContentBlockStart struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type  string `json:"type"` // "text" | "thinking" | "redacted_thinking" | "tool_use"
		ID    string `json:"id,omitempty"`
		Name  string `json:"name,omitempty"`
		Text  string `json:"text,omitempty"`
		Input any    `json:"input,omitempty"`
		Data  string `json:"data,omitempty"` // redacted_thinking
	} `json:"content_block"`
}

type anthEventContentBlockDelta struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"` // "text_delta" | "thinking_delta" | "input_json_delta" | "signature_delta"
		Text        string `json:"text,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		Signature   string `json:"signature,omitempty"`
	} `json:"delta"`
}

type anthEventContentBlockStop struct {
	Index int `json:"index"`
}

type anthEventMessageDelta struct {
	Delta struct {
		StopReason  string `json:"stop_reason"`
		StopDetails *struct {
			Type        string `json:"type"`
			Explanation string `json:"explanation"`
		} `json:"stop_details"`
	} `json:"delta"`
	Usage struct {
		InputTokens              *int `json:"input_tokens"`
		OutputTokens             *int `json:"output_tokens"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
		OutputTokensDetails      *struct {
			ThinkingTokens *int `json:"thinking_tokens"`
		} `json:"output_tokens_details,omitempty"`
	} `json:"usage"`
}

type anthErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ─── Message conversion ──────────────────────────────────────────────────────

// anthToolCallIDRe matches characters NOT in the Anthropic allowed set.
// Anthropic requires tool_use_id to match ^[a-zA-Z0-9_-]+$ (max 64 chars).
// Cross-provider IDs (e.g. OpenAI Responses "call_xxx|item_xxx") must be
// normalized before sending to the Anthropic Messages API.
// Mirrors upstream anthropic.ts normalizeToolCallId.
var anthToolCallIDRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func normalizeAnthropicToolCallID(id string) string {
	normalized := anthToolCallIDRe.ReplaceAllString(id, "_")
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return normalized
}

// anthToolResultContent mirrors upstream convertContentBlocks (pi-ai
// anthropic.ts): a tool result's content is a plain string when there are no
// images, otherwise an array of text+image blocks. Anthropic requires image
// blocks to use the "source" base64 shape. The prior code dropped b.Images
// entirely, so read-tool image output never reached the model: it saw only
// the "[Image: ...]" placeholder text.
func anthToolResultContent(content []ToolResultMessageContent) any {
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
	if len(images) == 0 {
		return text.String()
	}
	blocks := make([]anthContentBlock, 0, 1+len(images))
	if strings.TrimSpace(text.String()) != "" {
		blocks = append(blocks, anthContentBlock{Type: "text", Text: text.String()})
	} else {
		blocks = append(blocks, anthContentBlock{Type: "text", Text: "(see attached image)"})
	}
	for _, image := range images {
		blocks = append(blocks, anthContentBlock{
			Type: "image",
			Source: map[string]any{
				"type":       "base64",
				"media_type": image.MimeType,
				"data":       image.Data,
			},
		})
	}
	return blocks
}

// applyConversationCacheControl places a cache breakpoint on the last block of
// the final user or system message so the conversation prefix is cached across
// turns. Mirrors the end of upstream anthropic-messages.ts convertMessages.
func applyConversationCacheControl(msgs []anthMessage, cc *anthCacheControl) {
	if cc == nil || len(msgs) == 0 {
		return
	}
	last := &msgs[len(msgs)-1]
	if last.Role != "user" && last.Role != "system" {
		return
	}
	switch content := last.Content.(type) {
	case []anthContentBlock:
		if n := len(content); n > 0 {
			switch content[n-1].Type {
			case "text", "image", "tool_result", "tool_addition", "tool_removal":
				content[n-1].CacheControl = cc
			}
		}
	case string:
		last.Content = []anthContentBlock{{Type: "text", Text: content, CacheControl: cc}}
	}
}

// anthSystemUpdateBlocks mirrors the system branch of upstream convertMessages:
// the rendered update text, then, with native tool changes, a tool_removal
// block per removed tool and a tool_addition block per added tool.
func anthSystemUpdateBlocks(message SystemMessage, isOAuthToken, nativeToolChanges bool) []anthContentBlock {
	var blocks []anthContentBlock
	if update := RenderSystemMessageUpdate(message); update != "" {
		blocks = append(blocks, anthContentBlock{Type: "text", Text: update})
	}
	if !nativeToolChanges {
		return blocks
	}
	reference := func(name string) *anthToolReference {
		if isOAuthToken {
			name = toClaudeCodeName(name)
		}
		return &anthToolReference{Type: "tool_reference", Name: name}
	}
	for _, tool := range message.ToolsRemoved {
		blocks = append(blocks, anthContentBlock{Type: "tool_removal", Tool: reference(tool.Name)})
	}
	for _, tool := range message.ToolsAdded {
		blocks = append(blocks, anthContentBlock{Type: "tool_addition", Tool: reference(tool.Name)})
	}
	return blocks
}

type anthConvertedMessages struct {
	messages        []anthMessage
	assistantLevels map[int]string
}

// anthConvertMessages mirrors upstream convertMessages. Later system messages
// are held back and emitted directly before the next assistant message, or at
// the end: Anthropic requires tool_result blocks to follow their tool_use
// immediately, so an update between them would be rejected.
func anthConvertMessages(messages []Message, isOAuthToken, allowEmptySignature, nativeToolChanges bool) []anthMessage {
	return anthConvertMessagesDetailed(messages, isOAuthToken, allowEmptySignature, "", nativeToolChanges).messages
}

// anthConvertMessagesDetailed also records native effort for replayable
// assistant turns when the managed-effort provider identity matches.
func anthConvertMessagesDetailed(messages []Message, isOAuthToken, allowEmptySignature bool, managedProvider string, nativeToolChanges bool) anthConvertedMessages {
	out := make([]anthMessage, 0, len(messages))
	assistantLevels := map[int]string{}
	var pendingSystem []anthMessage
	for index := 0; index < len(messages); index++ {
		switch message := messages[index].(type) {
		case SystemMessage:
			if blocks := anthSystemUpdateBlocks(message, isOAuthToken, nativeToolChanges); len(blocks) > 0 {
				pendingSystem = append(pendingSystem, anthMessage{Role: "system", Content: blocks})
			}
		case UserMessage:
			switch content := message.Content.(type) {
			case UserText:
				if strings.TrimSpace(string(content)) != "" {
					out = append(out, anthMessage{Role: "user", Content: string(content)})
				}
			case UserContentBlocks:
				var blocks []anthContentBlock
				for _, content := range content {
					switch content := content.(type) {
					case TextContent:
						if strings.TrimSpace(content.Text) != "" {
							blocks = append(blocks, anthContentBlock{Type: "text", Text: content.Text})
						}
					case ImageContent:
						blocks = append(blocks, anthContentBlock{Type: "image", Source: map[string]any{
							"type": "base64", "media_type": content.MimeType, "data": content.Data,
						}})
					}
				}
				if len(blocks) > 0 {
					out = append(out, anthMessage{Role: "user", Content: blocks})
				}
			}
		case AssistantMessage:
			out = append(out, pendingSystem...)
			pendingSystem = nil
			var blocks []anthContentBlock
			for _, content := range message.Content {
				switch content := content.(type) {
				case TextContent:
					if strings.TrimSpace(content.Text) != "" {
						blocks = append(blocks, anthContentBlock{Type: "text", Text: content.Text})
					}
				case ThinkingContent:
					if content.Redacted {
						blocks = append(blocks, anthContentBlock{Type: "redacted_thinking", Data: content.ThinkingSignature})
						continue
					}
					signature := strings.TrimSpace(content.ThinkingSignature)
					if strings.TrimSpace(content.Thinking) == "" && signature == "" {
						continue
					}
					if signature != "" || allowEmptySignature {
						blocks = append(blocks, anthContentBlock{Type: "thinking", Thinking: content.Thinking, Signature: content.ThinkingSignature})
					} else {
						blocks = append(blocks, anthContentBlock{Type: "text", Text: content.Thinking})
					}
				case ToolCall:
					arguments := content.Arguments
					if arguments == nil {
						arguments = JsonObject{}
					}
					name := content.Name
					if isOAuthToken {
						name = toClaudeCodeName(name)
					}
					blocks = append(blocks, anthContentBlock{Type: "tool_use", ID: normalizeAnthropicToolCallID(content.ID), Name: name, Input: arguments})
				}
			}
			if len(blocks) > 0 {
				messageIndex := len(out)
				out = append(out, anthMessage{Role: "assistant", Content: blocks})
				if managedProvider != "" && message.API == APIAnthropicMessages && message.Provider == managedProvider && isAnthropicEffort(message.ProviderThinkingLevel) {
					assistantLevels[messageIndex] = message.ProviderThinkingLevel
				}
			}
		case ToolResultMessage:
			var results []anthContentBlock
			for index < len(messages) {
				result, ok := messages[index].(ToolResultMessage)
				if !ok {
					break
				}
				isError := result.IsError
				results = append(results, anthContentBlock{
					Type: "tool_result", ToolUseID: normalizeAnthropicToolCallID(result.ToolCallID),
					Content: anthToolResultContent(result.Content), IsError: &isError,
				})
				index++
			}
			index--
			out = append(out, anthMessage{Role: "user", Content: results})
		}
	}
	out = append(out, pendingSystem...)
	return anthConvertedMessages{messages: out, assistantLevels: assistantLevels}
}

func isAnthropicEffort(value string) bool {
	switch value {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func insertAnthropicThinkingLevelMessages(converted anthConvertedMessages, activeEffort string) []anthMessage {
	messages := make([]anthMessage, 0, len(converted.messages)+len(converted.assistantLevels)+1)
	for index, message := range converted.messages {
		if historicalEffort := converted.assistantLevels[index]; historicalEffort != "" {
			messages = append(messages, anthMessage{Role: "system", Content: []anthContentBlock{}, OutputConfig: &anthOutputConfig{Effort: historicalEffort}})
		}
		messages = append(messages, message)
	}
	return append(messages, anthMessage{Role: "system", Content: []anthContentBlock{}, OutputConfig: &anthOutputConfig{Effort: activeEffort}})
}

// stripThinkingSignatures returns a copy of msgs with every assistant thinking
// block downgraded so no thinking signature is sent to the provider: normal
// thinking becomes a text block (reasoning text preserved as context), and
// redacted thinking is dropped (it is opaque and carries only the signature).
// Used to retry a request the provider rejected with a stale thinking-block
// signature. See the D37 recovery in Stream.
func stripThinkingSignatures(messages []Message) []Message {
	out := cloneMessages(messages)
	for index, item := range out {
		message, ok := item.(AssistantMessage)
		if !ok {
			continue
		}
		content := make([]AssistantContentBlock, 0, len(message.Content))
		for _, block := range message.Content {
			thinking, ok := block.(ThinkingContent)
			if !ok {
				content = append(content, block)
				continue
			}
			if thinking.Redacted || strings.TrimSpace(thinking.Thinking) == "" {
				continue
			}
			content = append(content, TextContent{Text: thinking.Thinking})
		}
		message.Content = content
		out[index] = message
	}
	return out
}

// isThinkingSignatureError reports whether an Anthropic error body describes a
// rejected thinking-block signature (e.g. "Invalid `signature` in `thinking`
// block"). Matching on both tokens avoids firing on unrelated signature or
// thinking errors.
func isThinkingSignatureError(body []byte) bool {
	var errResp anthErrorResponse
	if json.Unmarshal(body, &errResp) != nil {
		return false
	}
	msg := strings.ToLower(errResp.Error.Message)
	return strings.Contains(msg, "signature") && strings.Contains(msg, "thinking")
}

// anthHTTPError formats a non-200 Anthropic response, preferring the parsed
// error envelope and falling back to the raw body.
func anthHTTPError(providerID string, status int, body []byte) error {
	var errResp anthErrorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		return fmt.Errorf("%s: HTTP %d: %s: %s", providerID, status, errResp.Error.Type, errResp.Error.Message)
	}
	return fmt.Errorf("%s: HTTP %d: %s", providerID, status, string(body))
}

func anthConvertTools(tools []ToolSchema, isOAuthToken, supportsEagerToolInputStreaming, supportsStrictTools bool, cacheControl *anthCacheControl) ([]anthTool, error) {
	out := make([]anthTool, len(tools))
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictTools)
		if err != nil {
			return nil, err
		}
		parameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		schema := map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		}
		if strict != nil && *strict {
			maps.Copy(schema, parameters)
		} else {
			if props, ok := parameters["properties"]; ok {
				schema["properties"] = props
			}
			if required, ok := parameters["required"]; ok {
				schema["required"] = required
			}
		}
		name := tool.Name
		if isOAuthToken {
			name = toClaudeCodeName(name)
		}
		out[i] = anthTool{
			Name:        name,
			Description: tool.Description,
			Strict:      strict,
			InputSchema: schema,
		}
		if supportsEagerToolInputStreaming {
			out[i].EagerInputStream = true
		}
		if cacheControl != nil && i == len(tools)-1 {
			out[i].CacheControl = cacheControl
		}
	}
	return out, nil
}

// modelAllowsEmptySignature returns true when the model's compat marks empty
// thinking signatures as acceptable, so thinking blocks with an empty signature
// are preserved instead of converted to text.
// Mirrors upstream: model.compat?.allowEmptySignature === true.
func modelAllowsEmptySignature(model *Model) bool {
	return model != nil && model.ProviderMeta.Compat != nil &&
		model.ProviderMeta.Compat.AllowEmptySignature != nil && *model.ProviderMeta.Compat.AllowEmptySignature
}

// modelUsesAdaptiveThinking returns true when the model's metadata says
// it uses adaptive thinking (compat.forceAdaptiveThinking). Replaces the
// former string-based supportsAdaptiveThinking that matched model IDs.
// Mirrors upstream v0.75.5: model.compat?.forceAdaptiveThinking === true.
func modelUsesAdaptiveThinking(model *Model) bool {
	return model != nil && model.ProviderMeta.Compat != nil &&
		model.ProviderMeta.Compat.ForceAdaptiveThinking != nil && *model.ProviderMeta.Compat.ForceAdaptiveThinking
}

// mapThinkingLevelToEffort maps a ThinkingLevel to an Anthropic effort string
// for adaptive thinking models. Uses thinkingLevelMap when present.
// Mirrors upstream anthropic.ts mapThinkingLevelToEffort.
func mapThinkingLevelToEffort(model *Model, level ThinkingLevel) string {
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(level)]; ok && mapped != nil {
			return *mapped
		}
	}
	switch level {
	case ThinkingMinimal, ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh:
		return "high"
	default:
		return "high"
	}
}

func anthropicActiveEffort(model *Model, level ThinkingLevel) string {
	if level == "" || level == ThinkingOff {
		return "high"
	}
	if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(level)]; ok && mapped != nil && isAnthropicEffort(*mapped) {
		return *mapped
	}
	if level == ThinkingXHigh {
		return "xhigh"
	}
	if level == ThinkingMax {
		return "max"
	}
	effort := mapThinkingLevelToEffort(model, level)
	if isAnthropicEffort(effort) {
		return effort
	}
	return "high"
}

// anthThinkingResult holds both the thinking config and optional output_config
// for the Anthropic request, plus an adjusted MaxTokens that accounts for
// the thinking budget. Mirrors upstream adjustMaxTokensForThinking.
type anthThinkingResult struct {
	Thinking     *anthThinking
	OutputConfig *anthOutputConfig
	MaxTokens    int // adjusted max_tokens; 0 = no change
}

// thinkingToAnthropicConfig builds the thinking field for the Anthropic request.
// Returns nil when thinking should be omitted.
// For adaptive-thinking models, returns type "adaptive"
// with effort via output_config. For older models, returns type "enabled" with
// budget_tokens. Mirrors upstream anthropic.ts buildParams thinking config.
func thinkingToAnthropicConfig(model *Model, maxTokens int, level ThinkingLevel) *anthThinkingResult {
	// Explicit thinking-off (upstream thinkingEnabled === false): never clamp off
	// up to an enabled level. Reasoning models send {type:"disabled"} unless
	// thinkingLevelMap.off is explicitly null (Fable 5 rejects the disabled
	// payload); non-reasoning models and the unset level omit thinking entirely.
	// Mirrors upstream anthropic.ts:982 (#5567).
	if level == ThinkingOff {
		if model != nil && model.Capabilities.MaxThinking != "" && !anthThinkingOffIsNull(model) {
			return &anthThinkingResult{Thinking: &anthThinking{Type: "disabled"}}
		}
		return nil
	}
	if level == "" {
		return nil
	}
	clamped := ClampThinkingLevel(model, level)
	switch clamped {
	case ThinkingOff, "":
		return nil
	}

	// Adaptive thinking: model.compat.forceAdaptiveThinking == true.
	// Mirrors upstream: params.thinking = {type: "adaptive", display: "summarized"}
	// with optional output_config = {effort: level}.
	if modelUsesAdaptiveThinking(model) {
		result := &anthThinkingResult{
			Thinking: &anthThinking{Type: "adaptive", Display: "summarized"},
		}
		effort := mapThinkingLevelToEffort(model, clamped)
		if effort != "" {
			result.OutputConfig = &anthOutputConfig{Effort: effort}
		}
		return result
	}

	// Budget-based thinking for older models.
	// Mirrors upstream adjustMaxTokensForThinking (simple-options.ts:26-53).
	// Default budgets by level match upstream defaultBudgets.
	var budget int
	// upstream: ai/src/api/simple-options.ts:DEFAULT_THINKING_BUDGETS
	switch clamped {
	case ThinkingMinimal:
		budget = 1024
	case ThinkingLow:
		budget = 2048
	case ThinkingMedium:
		budget = 8192
	case ThinkingHigh, ThinkingXHigh, ThinkingMax:
		budget = 16384
	default:
		budget = 1024
	}

	// Upstream: maxTokens = min(baseMaxTokens + thinkingBudget, modelMaxTokens)
	// Then: if maxTokens <= thinkingBudget, thinkingBudget = max(0, maxTokens - 1024)
	modelMax := maxTokens // fallback if no model cap
	if model != nil && model.Capabilities.MaxOutputTokens > 0 {
		modelMax = model.Capabilities.MaxOutputTokens
	}
	adjustedMax := min(maxTokens+budget, modelMax)
	if adjustedMax <= budget {
		budget = max(0, adjustedMax-1024)
	}

	return &anthThinkingResult{
		Thinking:  &anthThinking{Type: "enabled", BudgetTokens: budget, Display: "summarized"},
		MaxTokens: adjustedMax,
	}
}

// anthThinkingOffIsNull reports whether the model's thinkingLevelMap explicitly
// maps "off" to null. Upstream uses `thinkingLevelMap?.off !== null` to gate the
// {type:"disabled"} payload; this is the inverted check (Fable 5 sets off=null
// because it rejects the disabled payload). Mirrors anthropic.ts:982.
func anthThinkingOffIsNull(model *Model) bool {
	if model == nil {
		return false
	}
	v, ok := model.ThinkingLevelMap[ThinkingOff]
	return ok && v == nil
}

// ─── Stream ──────────────────────────────────────────────────────────────────

// anthropicParams is a built request plus the inputs the D37 retry and the
// SSE parser reuse.
type anthropicParams struct {
	request             anthRequest
	conversation        []Message
	tools               []ToolSchema
	cacheControl        *anthCacheControl
	allowEmptySignature bool
	nativeToolChanges   bool
	managedProvider     string
	activeEffort        string
}

func (params anthropicParams) convertMessages(messages []Message, isOAuthToken bool) []anthMessage {
	converted := anthConvertMessagesDetailed(messages, isOAuthToken, params.allowEmptySignature, params.managedProvider, params.nativeToolChanges)
	applyConversationCacheControl(converted.messages, params.cacheControl)
	if params.activeEffort != "" {
		return insertAnthropicThinkingLevelMessages(converted, params.activeEffort)
	}
	return converted.messages
}

func (p *anthropicProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("anthropic: invalid transcript: %w", err)
	}
	model := p.resolveModel()
	baseURL, apiKey, err := p.resolveEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	requestEnv := mergeProviderEnv(p.cfg.Env, opts.Env)
	baseURL, err = ResolveCloudflareBaseURL(p.cfg.ProviderID, baseURL, requestEnv)
	if err != nil {
		return nil, err
	}
	modelHeaders := anthropicHeadersFromMap(p.cfg.ExtraHeaders)
	// Upstream Models.applyAuth passes resolved auth headers, then model
	// headers, then request headers as the request's options.headers.
	optionsHeaders := mergeAnthropicHeaders(p.authTokenHeaders(apiKey, requestEnv), modelHeaders, anthropicHeadersFromProviderHeaders(opts.Headers))
	var dynamicHeaders anthropicHeaders
	if p.cfg.UseBearerAuth && p.cfg.DynamicHeaders != nil {
		dynamicHeaders = anthropicHeadersFromMap(p.cfg.DynamicHeaders(transcript, opts))
	}
	client := p.createClient(apiKey, optionsHeaders, dynamicHeaders, p.sessionAffinityHeaders(opts.SessionID, requestEnv))
	params, err := p.buildParams(model, transcript, client.isOAuthToken, modelHeaders, optionsHeaders, opts, requestEnv)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build params: %w", err)
	}

	sendOnce := func() (*http.Response, error) {
		return p.send(ctx, baseURL, client, params.request, opts)
	}
	resp, err := sendOnce() //nolint:bodyclose // closed on the error paths below and via defer in the SSE goroutine on success
	if err != nil {
		return nil, fmt.Errorf(p.cfg.ProviderID+": request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// pig divergence (D37): retry once without rejected thinking signatures.
		if resp.StatusCode == http.StatusBadRequest && isThinkingSignatureError(b) {
			params.request.Messages = params.convertMessages(stripThinkingSignatures(params.conversation), client.isOAuthToken)
			resp, err = sendOnce() //nolint:bodyclose // closed just below on non-200 and via defer in the SSE goroutine on success
			if err != nil {
				return nil, fmt.Errorf(p.cfg.ProviderID+": request: %w", err)
			}
			if resp.StatusCode != http.StatusOK {
				b2, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				return nil, anthHTTPError(p.cfg.ProviderID, resp.StatusCode, b2)
			}
		} else {
			return nil, anthHTTPError(p.cfg.ProviderID, resp.StatusCode, b)
		}
	}

	builder := newAssistantStreamBuilder(ctx, APIAnthropicMessages, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	if params.activeEffort != "" {
		builder.setResponseMetadata("", "", "", params.activeEffort, nil)
	}
	go func() {
		defer func() { _ = resp.Body.Close() }()
		p.parseAnthropicSSE(ctx, resp.Body, builder, anthropicStreamNames{isOAuthToken: client.isOAuthToken, currentTools: params.tools})
	}()
	return builder.stream, nil
}

func (p *anthropicProvider) resolveModel() *Model {
	model := &Model{ID: p.cfg.Model}
	if generated, ok := LookupModel(p.ID() + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	}
	// Apply config-level compat to the model when the generated registry
	// had none (custom/proxy providers, test mode).
	if model.ProviderMeta.Compat == nil && p.cfg.Compat != nil {
		model.ProviderMeta.Compat = p.cfg.Compat
	}
	return model
}

// fallbackUsageCost prices a response served by an allowed fallback model at
// that model's rates. Mirrors the usageModel choice in upstream
// anthropic-messages.ts message_start handling.
func (p *anthropicProvider) fallbackUsageCost(cost ModelCost, responseModel string) ModelCost {
	if responseModel == "" {
		return cost
	}
	compat := p.resolveModel().ProviderMeta.Compat
	if compat == nil {
		return cost
	}
	for _, fallback := range compat.AllowedFallbackModels {
		if fallback.Provider == p.cfg.ProviderID && fallback.Model == responseModel {
			return fallback.Cost
		}
	}
	return cost
}

// resolveEndpoint resolves the dynamic base URL and API key once per Stream;
// only the request body changes across the D37 retry.
func (p *anthropicProvider) resolveEndpoint(ctx context.Context) (string, string, error) {
	baseURL := p.cfg.BaseURL
	if p.cfg.GetBaseURL != nil {
		u, err := p.cfg.GetBaseURL(ctx)
		if err != nil {
			return "", "", fmt.Errorf(p.cfg.ProviderID+": GetBaseURL: %w", err)
		}
		if u != "" {
			baseURL = u
		}
	}
	apiKey := p.cfg.APIKey
	if p.cfg.GetAPIKey != nil {
		k, err := p.cfg.GetAPIKey(ctx)
		if err != nil {
			return "", "", fmt.Errorf(p.cfg.ProviderID+": GetAPIKey: %w", err)
		}
		apiKey = k
	}
	return baseURL, apiKey, nil
}

// sessionAffinityHeaders returns the session affinity header for providers that
// accept one while prompt caching is enabled.
func (p *anthropicProvider) sessionAffinityHeaders(sessionID string, env ProviderEnv) anthropicHeaders {
	var headers anthropicHeaders
	if sessionID == "" || getProviderEnvValue("PI_CACHE_RETENTION", env) == "none" {
		return headers
	}
	sendSessionAffinity := false
	if compat := p.cfg.Compat; compat != nil && compat.SendSessionAffinityHeaders != nil {
		sendSessionAffinity = *compat.SendSessionAffinityHeaders
	} else if p.cfg.ProviderID == "fireworks" || (p.cfg.ProviderID == "cloudflare-ai-gateway" && strings.Contains(p.cfg.BaseURL, "anthropic")) {
		sendSessionAffinity = true
	}
	if sendSessionAffinity {
		headers.set("x-session-affinity", sessionID)
	}
	return headers
}

// cacheControl mirrors upstream getCacheControl: the breakpoint applies to the
// system prompt, tools, and conversation history.
func (p *anthropicProvider) cacheControl(env ProviderEnv) *anthCacheControl {
	cacheRetention := getProviderEnvValue("PI_CACHE_RETENTION", env)
	if cacheRetention == "none" {
		return nil
	}
	cc := &anthCacheControl{Type: "ephemeral"}
	supportsLong := true
	if compat := p.cfg.Compat; compat != nil && compat.SupportsLongCacheRetention != nil {
		supportsLong = *compat.SupportsLongCacheRetention
	}
	if cacheRetention == "long" && supportsLong {
		cc.TTL = "1h"
	}
	return cc
}

func (p *anthropicProvider) supportsEagerToolInputStreaming() bool {
	if compat := p.cfg.Compat; compat != nil && compat.SupportsEagerToolInputStreaming != nil {
		return *compat.SupportsEagerToolInputStreaming
	}
	return true
}

// anthSystemBlocks mirrors upstream buildParams system: an OAuth-token request
// leads with the Claude Code identity block, and every block carries the cache
// breakpoint.
func anthSystemBlocks(systemPrompt string, isOAuthToken bool, cc *anthCacheControl) []anthSystemBlock {
	var blocks []anthSystemBlock
	if isOAuthToken {
		blocks = append(blocks, anthSystemBlock{Type: "text", Text: claudeCodeSystemPrompt, CacheControl: cc})
	}
	if systemPrompt != "" {
		blocks = append(blocks, anthSystemBlock{Type: "text", Text: systemPrompt, CacheControl: cc})
	}
	return blocks
}

// anthropicCompatFlag reads an opt-in model compat flag (default false).
func anthropicCompatFlag(model *Model, flag func(*ModelCompat) *bool) bool {
	if compat := model.ProviderMeta.Compat; compat != nil {
		if value := flag(compat); value != nil {
			return *value
		}
	}
	return false
}

// anthropicRequestTools mirrors the tools branch of upstream buildParams. With
// native tool changes, the initial tools stay active with the cache breakpoint
// on the last one, then the deferred placeholder, then every later declaration
// deferred; removed tools stay declared, so the list only grows. Otherwise the
// current tool list is sent.
func anthropicRequestTools(messages []Message, params anthropicParams, initialTools []ToolSchema, isOAuthToken, supportsEager, supportsStrict bool, cacheControl *anthCacheControl) ([]anthTool, error) {
	if !params.nativeToolChanges {
		if len(params.tools) == 0 {
			return nil, nil
		}
		return anthConvertTools(params.tools, isOAuthToken, supportsEager, supportsStrict, cacheControl)
	}
	initialNames := make(map[string]bool, len(initialTools))
	for _, tool := range initialTools {
		initialNames[tool.Name] = true
	}
	var laterTools []ToolSchema
	for _, tool := range GetDeclaredTools(messages) {
		if !initialNames[tool.Name] {
			laterTools = append(laterTools, tool)
		}
	}
	tools, err := anthConvertTools(initialTools, isOAuthToken, supportsEager, supportsStrict, cacheControl)
	if err != nil {
		return nil, err
	}
	tools = append(tools, deferredToolPlaceholder())
	deferredTools, err := anthConvertTools(laterTools, isOAuthToken, supportsEager, supportsStrict, nil)
	if err != nil {
		return nil, err
	}
	for _, tool := range deferredTools {
		tool.DeferLoading = true
		tools = append(tools, tool)
	}
	return tools, nil
}

// buildParams mirrors upstream buildParams for the request fields PiG sends.
func (p *anthropicProvider) buildParams(model *Model, transcript TranscriptContext, isOAuthToken bool, modelHeaders, optionsHeaders anthropicHeaders, opts StreamOptions, env ProviderEnv) (anthropicParams, error) {
	supportsMidConversation := anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoSystemMessages })
	messages := ResolveTranscript(transcript, supportsMidConversation).Messages()
	systemPrompt := ""
	var initialTools []ToolSchema
	if initialSystem := GetInitialSystemMessage(messages); initialSystem != nil {
		systemPrompt = GetCurrentSystemPrompt([]Message{*initialSystem})
		initialTools = initialSystem.ToolsAdded
	}
	// Native tool changes reference tools by name, so a redefined name cannot be
	// expressed, and Anthropic rejects a tool list where every tool is deferred,
	// so an initial active tool must anchor the deferred ones.
	params := anthropicParams{
		conversation:        WithoutInitialSystemMessage(messages),
		tools:               GetCurrentTools(messages),
		cacheControl:        p.cacheControl(env),
		allowEmptySignature: modelAllowsEmptySignature(model),
		nativeToolChanges: supportsMidConversation &&
			anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoToolChanges }) &&
			len(initialTools) > 0 && !HasToolRedefinitions(messages),
	}
	if anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoEffort }) {
		params.managedProvider = p.cfg.ProviderID
		params.activeEffort = anthropicActiveEffort(model, opts.Thinking)
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		if model.Capabilities.MaxOutputTokens > 0 {
			maxTokens = model.Capabilities.MaxOutputTokens
		} else {
			maxTokens = 8192
		}
	}
	supportsEager := p.supportsEagerToolInputStreaming()
	thinkingEnabled := opts.Thinking != ThinkingOff && opts.Thinking != ""
	req := anthRequest{
		Model:     p.cfg.Model,
		Messages:  params.convertMessages(params.conversation, isOAuthToken),
		MaxTokens: maxTokens,
		Stream:    true,
		Betas: getBetaFeatures(modelHeaders, optionsHeaders, anthropicBetaInputs{
			isOAuthToken:                    isOAuthToken,
			hasTools:                        len(params.tools) > 0,
			supportsEagerToolInputStreaming: supportsEager,
			reasoning:                       model.ProviderMeta.Reasoning,
			thinkingEnabled:                 thinkingEnabled,
			forceAdaptiveThinking:           modelUsesAdaptiveThinking(model),
			supportsMidConvoEffort:          params.activeEffort != "",
			nativeToolChanges:               params.nativeToolChanges,
		}),
		System: anthSystemBlocks(systemPrompt, isOAuthToken, params.cacheControl),
	}
	var toolCacheControl *anthCacheControl
	if compat := p.cfg.Compat; compat == nil || compat.SupportsCacheControlOnTools == nil || *compat.SupportsCacheControlOnTools {
		toolCacheControl = params.cacheControl
	}
	supportsStrictTools := anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsStrictTools })
	var err error
	req.Tools, err = anthropicRequestTools(messages, params, initialTools, isOAuthToken, supportsEager, supportsStrictTools, toolCacheControl)
	if err != nil {
		return anthropicParams{}, err
	}
	if toolChoice, ok := opts.ToolChoice.(string); ok {
		req.ToolChoice = map[string]any{"type": toolChoice}
	} else if opts.ToolChoice != nil {
		req.ToolChoice = opts.ToolChoice
	}
	if userID, ok := opts.Metadata["user_id"].(string); ok {
		req.Metadata = &anthMetadata{UserID: userID}
	}
	p.applySampling(&req, model, maxTokens, thinkingEnabled, opts.Thinking, opts.Temperature, opts.TemperatureSet || opts.Temperature != 0, params.activeEffort != "")
	params.request = req
	return params, nil
}

// applySampling sets thinking, output_config, the thinking-adjusted
// max_tokens, and temperature.
func (p *anthropicProvider) applySampling(req *anthRequest, model *Model, maxTokens int, thinkingEnabled bool, level ThinkingLevel, temperature float64, temperatureSet, managedEffort bool) {
	if managedEffort {
		req.Thinking = &anthThinking{
			Type: "adaptive", Display: "summarized",
			BlockBinding: &anthBlockBinding{PrefixMismatchBehavior: "drop_block"},
		}
		req.OutputConfig = &anthOutputConfig{Effort: "high"}
		return
	}
	if thinkingResult := thinkingToAnthropicConfig(model, maxTokens, level); thinkingResult != nil {
		req.Thinking = thinkingResult.Thinking
		req.OutputConfig = thinkingResult.OutputConfig
		// Budget-based thinking may adjust max_tokens upward to
		// accommodate the budget (upstream adjustMaxTokensForThinking).
		if thinkingResult.MaxTokens > 0 {
			req.MaxTokens = thinkingResult.MaxTokens
		}
	}
	// Temperature: upstream sets it only when thinking is NOT enabled (off or
	// unset) and the model supports it: independent of the thinking field, so an
	// off request can carry both temperature and thinking:{type:"disabled"}.
	// Temperature is incompatible with extended thinking and unsupported on
	// some models (e.g. Claude Opus 4.7+).
	if thinkingEnabled || !temperatureSet {
		return
	}
	if compat := model.ProviderMeta.Compat; compat != nil && compat.SupportsTemperature != nil && !*compat.SupportsTemperature {
		return
	}
	req.Temperature = new(temperature)
}

// send runs OnPayload on the current request, then issues the streaming POST.
func (p *anthropicProvider) send(ctx context.Context, baseURL string, client anthropicClient, req anthRequest, opts StreamOptions) (*http.Response, error) {
	payload := any(req)
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(req, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if err != nil {
			return nil, fmt.Errorf(p.cfg.ProviderID+": onPayload: %w", err)
		}
		if next != nil {
			payload = next
		}
	}
	body, betas, err := anthropicWireBody(payload)
	if err != nil {
		return nil, fmt.Errorf(p.cfg.ProviderID+": marshal request: %w", err)
	}
	// Upstream calls client.beta.messages.create (anthropic-messages.ts:1065,
	// 588), which the Anthropic SDK's beta namespace routes to
	// /v1/messages?beta=true rather than /v1/messages. This is the single
	// send path for every Anthropic-flavored caller: direct API key, OAuth,
	// and the github-copilot Anthropic proxy (NewCopilotProvider's
	// APIAnthropicMessages branch), so all three inherit the fix here.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyAnthropicRequestHeaders(httpReq, client, betas)
	return p.client.Do(httpReq)
}

// ─── SSE parsing ─────────────────────────────────────────────────────────────

// anthropicStreamNames maps streamed tool names back from Claude Code casing
// for OAuth-token requests.
type anthropicStreamNames struct {
	isOAuthToken bool
	currentTools []ToolSchema
}

func (names anthropicStreamNames) toolName(name string) string {
	if names.isOAuthToken {
		return fromClaudeCodeName(name, names.currentTools)
	}
	return name
}

func unmarshalAnthropicSSEEvent(event serverSentEvent, target any) error {
	if err := unmarshalJSONWithRepair(event.Data, target); err != nil {
		return fmt.Errorf("Could not parse Anthropic SSE event %s: %w; data=%s; raw=%s", event.Event, err, event.Data, strings.Join(event.Raw, `\n`))
	}
	return nil
}

func (p *anthropicProvider) parseAnthropicSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder, names anthropicStreamNames) {
	// Track active blocks by their Anthropic index.
	type activeBlock struct {
		blockType string // "text" | "thinking" | "tool_use"
		redacted  bool   // true for redacted_thinking
		seqIdx    int    // 0-based sequential tool call index
		toolID    string
		toolName  string
		toolArgs  strings.Builder
		signature strings.Builder
	}
	blocks := map[int]*activeBlock{}
	toolCallSeqIdx := 0 // 0-based sequential index for tool calls only

	// Upstream mutates output.usage as events arrive, retaining billed usage on errors.
	usage := &builder.partial.Usage
	usageCost := builder.modelCost
	var stopReason StopReason // provider-reported stop reason
	var errMsg string         // refusal explanation, surfaced on EventDone (#5666)
	sawMessageStart := false

	decoder := newSSEDecoder(r)

	for decoder.Next() {
		if err := ctx.Err(); err != nil {
			builder.fail(StopReasonAborted, err)
			return
		}
		event := decoder.Event()
		currentEvent := event.Event
		data := event.Data

		if currentEvent == "ping" {
			continue
		}
		if currentEvent == "error" {
			builder.fail(StopReasonError, fmt.Errorf("anthropic SSE error: %s", data))
			return
		}

		switch currentEvent {
		case "message_start":
			sawMessageStart = true
			var ev anthEventMessageStart
			if err := unmarshalAnthropicSSEEvent(event, &ev); err != nil {
				builder.fail(StopReasonError, err)
				return
			}
			responseModel := ""
			if ev.Message.Model != p.cfg.Model {
				responseModel = ev.Message.Model
			}
			builder.setResponseMetadata(ev.Message.ID, responseModel, "", "", nil)
			usageCost = p.fallbackUsageCost(builder.modelCost, responseModel)
			usage.Input = ev.Message.Usage.InputTokens
			usage.Output = ev.Message.Usage.OutputTokens
			usage.CacheRead = ev.Message.Usage.CacheReadInputTokens
			usage.CacheWrite = ev.Message.Usage.CacheCreationInputTokens
			cacheWrite1h := 0
			if ev.Message.Usage.CacheCreation != nil {
				cacheWrite1h = ev.Message.Usage.CacheCreation.Ephemeral1H
			}
			usage.CacheWrite1h = &cacheWrite1h
			if ev.Message.Usage.OutputTokensDetails != nil {
				usage.Reasoning = new(ev.Message.Usage.OutputTokensDetails.ThinkingTokens)
			}
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
			calculateUsageCost(usageCost, usage)

		case "content_block_start":
			var ev anthEventContentBlockStart
			if err := unmarshalAnthropicSSEEvent(event, &ev); err != nil {
				builder.fail(StopReasonError, err)
				return
			}
			ab := &activeBlock{blockType: ev.ContentBlock.Type}
			blocks[ev.Index] = ab

			switch ev.ContentBlock.Type {
			case "text":
				builder.textStart()
			case "thinking":
				builder.thinkingStart(false, "", "")
			case "redacted_thinking":
				ab.blockType = "thinking"
				ab.redacted = true
				ab.signature.WriteString(ev.ContentBlock.Data)
				builder.thinkingStart(true, "[Reasoning redacted]", ev.ContentBlock.Data)
			case "tool_use":
				ab.toolID = ev.ContentBlock.ID
				ab.toolName = names.toolName(ev.ContentBlock.Name)
				idx := toolCallSeqIdx
				toolCallSeqIdx++
				ab.seqIdx = idx
				builder.toolCallStart(streamToolCallDelta{index: idx, id: ab.toolID, name: ab.toolName})
			}

		case "content_block_delta":
			var ev anthEventContentBlockDelta
			if err := unmarshalAnthropicSSEEvent(event, &ev); err != nil {
				builder.fail(StopReasonError, err)
				return
			}
			ab := blocks[ev.Index]
			if ab == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				builder.textDelta(ev.Delta.Text)
			case "thinking_delta":
				builder.thinkingDelta(ev.Delta.Thinking, false)
			case "input_json_delta":
				ab.toolArgs.WriteString(ev.Delta.PartialJSON)
				builder.toolCallDelta(streamToolCallDelta{
					index: ab.seqIdx, id: ab.toolID, name: ab.toolName, argumentsDelta: ev.Delta.PartialJSON,
				})
			case "signature_delta":
				ab.signature.WriteString(ev.Delta.Signature)
				builder.thinkingSignature(ab.signature.String())
			}

		case "content_block_stop":
			var ev anthEventContentBlockStop
			if err := unmarshalAnthropicSSEEvent(event, &ev); err != nil {
				builder.fail(StopReasonError, err)
				return
			}
			ab := blocks[ev.Index]
			if ab == nil {
				continue
			}
			switch ab.blockType {
			case "text":
				builder.endText()
			case "thinking":
				builder.thinkingSignature(ab.signature.String())
				builder.endThinking()
			case "tool_use":
				builder.endToolCall(ab.seqIdx)
			}
			delete(blocks, ev.Index)

		case "message_delta":
			var ev anthEventMessageDelta
			if err := unmarshalAnthropicSSEEvent(event, &ev); err != nil {
				builder.fail(StopReasonError, err)
				return
			}
			// Capture stop reason for the terminal event.
			if ev.Delta.StopReason != "" {
				builder.setResponseMetadata("", "", ev.Delta.StopReason, "", nil)
				explanation := ""
				if ev.Delta.StopDetails != nil {
					explanation = ev.Delta.StopDetails.Explanation
				}
				mapped, message, err := mapAnthStopReason(ev.Delta.StopReason, explanation)
				if err != nil {
					builder.fail(StopReasonError, err)
					return
				}
				stopReason = mapped
				if message != "" {
					errMsg = message
				}
			}
			// Update usage: only override fields present (non-nil).
			if ev.Usage.InputTokens != nil {
				usage.Input = *ev.Usage.InputTokens
			}
			if ev.Usage.OutputTokens != nil {
				usage.Output = *ev.Usage.OutputTokens
			}
			if ev.Usage.CacheReadInputTokens != nil {
				usage.CacheRead = *ev.Usage.CacheReadInputTokens
			}
			if ev.Usage.CacheCreationInputTokens != nil {
				usage.CacheWrite = *ev.Usage.CacheCreationInputTokens
			}
			if ev.Usage.OutputTokensDetails != nil && ev.Usage.OutputTokensDetails.ThinkingTokens != nil {
				usage.Reasoning = new(*ev.Usage.OutputTokensDetails.ThinkingTokens)
			}
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
			calculateUsageCost(usageCost, usage)

		case "message_stop":
			var payload any
			if err := unmarshalAnthropicSSEEvent(event, &payload); err != nil {
				builder.fail(StopReasonError, err)
				return
			}
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
			if stopReason == "" {
				builder.fail(StopReasonError, errors.New("Anthropic stream ended without a stop reason"))
				return
			}
			if stopReason == StopReasonError {
				builder.fail(stopReason, errors.New(errMsg))
			} else {
				builder.done(stopReason, usage, errMsg)
			}
			return
		}
	}

	if err := decoder.Err(); err != nil && ctx.Err() == nil {
		builder.fail(StopReasonError, err)
		return
	}
	if err := ctx.Err(); err != nil {
		builder.fail(StopReasonAborted, err)
		return
	}
	// message_stop returns from the loop, so reaching here after
	// message_start means the stream ended before message_stop.
	if sawMessageStart {
		builder.fail(StopReasonError, errors.New("Anthropic stream ended before message_stop"))
		return
	}

	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	builder.fail(StopReasonError, errors.New("Anthropic stream ended without a stop reason"))
}

// mapAnthStopReason maps Anthropic stop_reason values to canonical terminal
// state and provider error text. Unknown values are rejected so API additions
// do not silently become successful turns.
func mapAnthStopReason(reason, refusalExplanation string) (StopReason, string, error) {
	switch reason {
	case "end_turn", "pause_turn", "stop_sequence":
		return StopReasonStop, "", nil
	case "max_tokens":
		return StopReasonLength, "", nil
	case "tool_use":
		return StopReasonToolUse, "", nil
	case "refusal":
		if refusalExplanation == "" {
			refusalExplanation = "The model refused to complete the request"
		}
		return StopReasonError, refusalExplanation, nil
	case "sensitive":
		return StopReasonError, "Provider stopped with: sensitive", nil
	default:
		return "", "", fmt.Errorf("Unhandled stop reason: %s", reason)
	}
}

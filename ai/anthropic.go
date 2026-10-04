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
	"slices"
	"strings"
	"time"
	"unicode/utf16"
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
	// ModelMetadata retains the caller-selected model's capabilities and API metadata instead of resolving an ID through the catalog.
	ModelMetadata *Model
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
	Temperature *float64       `json:"temperature,omitempty"`
	Fallbacks   []anthFallback `json:"fallbacks,omitempty"`
}

type anthFallback struct {
	Model string `json:"model"`
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
	Tool *anthToolChange `json:"tool,omitempty"`
	// cache_control
	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

// Text and thinking blocks retain their required string fields, including explicit empty values.
func (block anthContentBlock) MarshalJSON() ([]byte, error) {
	type payload anthContentBlock
	if block.Type == "text" {
		return json.Marshal(struct {
			payload
			Text string `json:"text"`
		}{payload: payload(block), Text: block.Text})
	}
	if block.Type != "thinking" {
		return json.Marshal(payload(block))
	}
	// upstream: packages/ai/src/api/anthropic-messages.ts:convertMessages retains empty thinking and signature fields on thinking blocks.
	return json.Marshal(struct {
		Type         string            `json:"type"`
		Thinking     string            `json:"thinking"`
		Signature    string            `json:"signature"`
		CacheControl *anthCacheControl `json:"cache_control,omitempty"`
	}{block.Type, block.Thinking, block.Signature, block.CacheControl})
}

// anthToolChange is the tool of a tool_addition or tool_removal block: a
// tool_reference naming a declared tool, or a tool_definition carrying a tool by
// value (inline-tools-2026-09-15).
type anthToolChange struct {
	Type       string    `json:"type"` // "tool_reference" | "tool_definition"
	Name       string    `json:"name,omitempty"`
	Definition *anthTool `json:"definition,omitempty"`
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
// hidden prompt scaffolding for mid-conversation tool changes; declaring the
// placeholder from the first request keeps that scaffolding in the cached
// prefix, so the first tool change does not invalidate the cache. It is never
// activated.
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
		ID                   string `json:"id"`
		Model                string `json:"model"`
		InputTransformations any    `json:"input_transformations"`
		Usage                struct {
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
		Type      string `json:"type"` // "text" | "thinking" | "redacted_thinking" | "tool_use"
		ID        string `json:"id,omitempty"`
		Name      string `json:"name,omitempty"`
		Text      string `json:"text,omitempty"`
		Thinking  string `json:"thinking,omitempty"`
		Signature string `json:"signature,omitempty"`
		Input     any    `json:"input,omitempty"`
		Data      string `json:"data,omitempty"` // redacted_thinking
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
	InputTransformations any `json:"input_transformations"`
	Delta                struct {
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
		// CacheCreation is the TTL breakdown. Vercel AI Gateway sends it in deltas, though the SDK only types it on message_start.
		CacheCreation *struct {
			Ephemeral1H *int `json:"ephemeral_1h_input_tokens"`
		} `json:"cache_creation"`
		OutputTokensDetails *struct {
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

// normalizeAnthropicToolCallID replaces non-ASCII identifier characters per UTF-16 code unit, then truncates the ASCII result to Anthropic's 64-unit limit.
// upstream: packages/ai/src/api/anthropic-messages.ts:normalizeToolCallId
func normalizeAnthropicToolCallID(id string) string {
	var normalized [64]byte
	length := 0
	for _, r := range id {
		if length == len(normalized) {
			break
		}
		value := byte('_')
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			value = byte(r)
		}
		normalized[length] = value
		length++
		if utf16.RuneLen(r) == 2 && length < len(normalized) {
			normalized[length] = '_'
			length++
		}
	}
	return string(normalized[:length])
}

// anthToolResultContent joins every text-only element with a newline. Mixed text/image results preserve block order and explicit empty text; only an image-only result receives placeholder text.
func anthToolResultContent(content []ToolResultMessageContent) any {
	hasImages := false
	for _, block := range content {
		if _, ok := block.(ImageContent); ok {
			hasImages = true
			break
		}
	}
	if !hasImages {
		var text strings.Builder
		for index, block := range content {
			if index > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(block.(TextContent).Text)
		}
		return sanitizeSurrogates(text.String())
	}
	blocks := make([]anthContentBlock, 0, len(content))
	hasText := false
	for _, block := range content {
		switch block := block.(type) {
		case TextContent:
			hasText = true
			blocks = append(blocks, anthContentBlock{Type: "text", Text: sanitizeSurrogates(block.Text)})
		case ImageContent:
			blocks = append(blocks, anthContentBlock{
				Type: "image",
				Source: map[string]any{
					"type": "base64", "media_type": block.MimeType, "data": block.Data,
				},
			})
		}
	}
	if !hasText {
		blocks = append([]anthContentBlock{{Type: "text", Text: "(see attached image)"}}, blocks...)
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

// anthToolDefinitions converts the tools of native tool_addition blocks. It is
// nil when tool changes are not native.
type anthToolDefinitions func([]ToolSchema) ([]anthTool, error)

// anthSystemUpdateBlocks mirrors the system branch of upstream convertMessages:
// the rendered update text, then, with native tool changes, a tool_removal
// block per removed tool that is not redefined in the same update and a
// tool_addition block carrying each added tool by value.
func anthSystemUpdateBlocks(message SystemMessage, isOAuthToken bool, convertToolDefinitions anthToolDefinitions) ([]anthContentBlock, error) {
	var blocks []anthContentBlock
	if update := RenderSystemMessageUpdate(message); update != "" {
		blocks = append(blocks, anthContentBlock{Type: "text", Text: sanitizeSurrogates(update)})
	}
	if convertToolDefinitions == nil {
		return blocks, nil
	}
	redefined := make(map[string]bool, len(message.ToolsAdded))
	for _, tool := range message.ToolsAdded {
		redefined[tool.Name] = true
	}
	for _, tool := range message.ToolsRemoved {
		// A new definition under the same name replaces the old one, so no removal is needed.
		if redefined[tool.Name] {
			continue
		}
		name := tool.Name
		if isOAuthToken {
			name = toClaudeCodeName(name)
		}
		blocks = append(blocks, anthContentBlock{Type: "tool_removal", Tool: &anthToolChange{Type: "tool_reference", Name: name}})
	}
	definitions, err := convertToolDefinitions(message.ToolsAdded)
	if err != nil {
		return nil, err
	}
	for i := range definitions {
		blocks = append(blocks, anthContentBlock{Type: "tool_addition", Tool: &anthToolChange{Type: "tool_definition", Definition: &definitions[i]}})
	}
	return blocks, nil
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
	var convertToolDefinitions anthToolDefinitions
	if nativeToolChanges {
		convertToolDefinitions = func(tools []ToolSchema) ([]anthTool, error) {
			return anthConvertTools(tools, isOAuthToken, false, false, nil)
		}
	}
	converted, err := anthConvertMessagesDetailed(messages, isOAuthToken, allowEmptySignature, "", convertToolDefinitions)
	if err != nil {
		return nil
	}
	return converted.messages
}

// anthConvertMessagesDetailed also records native effort for replayable assistant turns when the managed-effort provider identity matches. Empty text and signature checks use ECMAScript whitespace rules.
func anthConvertMessagesDetailed(messages []Message, isOAuthToken, allowEmptySignature bool, managedProvider string, convertToolDefinitions anthToolDefinitions) (anthConvertedMessages, error) {
	out := make([]anthMessage, 0, len(messages))
	assistantLevels := map[int]string{}
	var pendingSystem []anthMessage
	for index := 0; index < len(messages); index++ {
		switch message := messages[index].(type) {
		case SystemMessage:
			blocks, err := anthSystemUpdateBlocks(message, isOAuthToken, convertToolDefinitions)
			if err != nil {
				return anthConvertedMessages{}, err
			}
			if len(blocks) > 0 {
				pendingSystem = append(pendingSystem, anthMessage{Role: "system", Content: blocks})
			}
		case UserMessage:
			switch content := message.Content.(type) {
			case UserText:
				if trimJSWhitespace(string(content)) != "" {
					out = append(out, anthMessage{Role: "user", Content: sanitizeSurrogates(string(content))})
				}
			case UserContentBlocks:
				var blocks []anthContentBlock
				for _, content := range content {
					switch content := content.(type) {
					case TextContent:
						// Pi sanitizes each user text block before its blank filter.
						if text := sanitizeSurrogates(content.Text); trimJSWhitespace(text) != "" {
							blocks = append(blocks, anthContentBlock{Type: "text", Text: text})
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
					if trimJSWhitespace(content.Text) != "" {
						blocks = append(blocks, anthContentBlock{Type: "text", Text: sanitizeSurrogates(content.Text)})
					}
				case ThinkingContent:
					if content.Redacted {
						blocks = append(blocks, anthContentBlock{Type: "redacted_thinking", Data: content.ThinkingSignature})
						continue
					}
					signature := trimJSWhitespace(content.ThinkingSignature)
					if trimJSWhitespace(content.Thinking) == "" && signature == "" {
						continue
					}
					if signature != "" || allowEmptySignature {
						wireSignature := content.ThinkingSignature
						if signature == "" {
							wireSignature = ""
						}
						blocks = append(blocks, anthContentBlock{Type: "thinking", Thinking: sanitizeSurrogates(content.Thinking), Signature: wireSignature})
					} else {
						blocks = append(blocks, anthContentBlock{Type: "text", Text: sanitizeSurrogates(content.Thinking)})
					}
				case ToolCall:
					// ArgumentsJSON writes the model's member order; a nil Arguments is {}.
					arguments, _ := content.ArgumentsJSON()
					name := content.Name
					if isOAuthToken {
						name = toClaudeCodeName(name)
					}
					blocks = append(blocks, anthContentBlock{Type: "tool_use", ID: normalizeAnthropicToolCallID(content.ID), Name: name, Input: json.RawMessage(arguments)})
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
	return anthConvertedMessages{messages: out, assistantLevels: assistantLevels}, nil
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

// anthropicStrictUnsupportedKeywords are the keywords Anthropic strict tool use rejects with a 400 for the whole request.
// https://platform.claude.com/docs/en/build-with-claude/structured-outputs#json-schema-limitations
// upstream: packages/ai/src/api/anthropic-messages.ts:1548-1580 (ANTHROPIC_STRICT_UNSUPPORTED_KEYWORDS)
var anthropicStrictUnsupportedKeywords = []string{
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"maxItems", "uniqueItems", "minContains", "maxContains", "minProperties", "maxProperties",
}

// anthropicStrictStringFormats are the string formats strict mode accepts (ANTHROPIC_STRICT_STRING_FORMATS).
var anthropicStrictStringFormats = []string{"date-time", "time", "date", "duration", "email", "hostname", "uri", "ipv4", "ipv6", "uuid"}

// anthropicStrictUnsupportedKeyword reports the schema keywords Anthropic strict tool use rejects, so a "prefer" tool that uses one is sent non-strict.
// upstream: packages/ai/src/api/anthropic-messages.ts:1573-1577 (isAnthropicStrictUnsupportedKeyword)
func anthropicStrictUnsupportedKeyword(key string, value any) bool {
	if slices.Contains(anthropicStrictUnsupportedKeywords, key) {
		return true
	}
	switch key {
	case "minItems":
		// JavaScript compares the parsed JSON value: only the numbers 0 and 1 pass.
		number, ok := value.(float64)
		return !ok || (number != 0 && number != 1)
	case "format":
		format, ok := value.(string)
		return !ok || !slices.Contains(anthropicStrictStringFormats, format)
	}
	return false
}

func anthConvertTools(tools []ToolSchema, isOAuthToken, supportsEagerToolInputStreaming, supportsStrictTools bool, cacheControl *anthCacheControl) ([]anthTool, error) {
	out := make([]anthTool, len(tools))
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictTools, anthropicStrictUnsupportedKeyword)
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
	// upstream: packages/ai/src/api/anthropic-messages.ts:streamSimple
	// Adaptive effort maps the requested level directly; only budget-based thinking is clamped.
	if modelUsesAdaptiveThinking(model) {
		result := &anthThinkingResult{
			Thinking: &anthThinking{Type: "adaptive", Display: "summarized"},
		}
		effort := mapThinkingLevelToEffort(model, level)
		if effort != "" {
			result.OutputConfig = &anthOutputConfig{Effort: effort}
		}
		return result
	}

	clamped := ClampThinkingLevel(model, level)
	if clamped == ThinkingOff || clamped == "" {
		return nil
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
	// upstream: packages/ai/src/api/anthropic-messages.ts:buildParams applies thinkingBudgetTokens || 1024 after simple-option lowering.
	if budget == 0 {
		budget = 1024
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
	// toolDefinitions converts tools for native tool_addition blocks; nil
	// unless nativeToolChanges.
	toolDefinitions anthToolDefinitions
	managedProvider string
	activeEffort    string
}

func (params anthropicParams) convertMessages(messages []Message, isOAuthToken bool) ([]anthMessage, error) {
	converted, err := anthConvertMessagesDetailed(messages, isOAuthToken, params.allowEmptySignature, params.managedProvider, params.toolDefinitions)
	if err != nil {
		return nil, err
	}
	applyConversationCacheControl(converted.messages, params.cacheControl)
	if params.activeEffort != "" {
		return insertAnthropicThinkingLevelMessages(converted, params.activeEffort), nil
	}
	return converted.messages, nil
}

// Stream retains the assistant identity and active effort when request preparation fails, and terminates with an error event before generation starts. The request, its response and the body read run behind the returned stream, as upstream's async IIFE does; only the synchronous prefix of that IIFE runs before Stream returns.
func (p *anthropicProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	ctx = withProviderRequestOptions(ctx, opts)
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("anthropic: invalid transcript: %w", err)
	}
	model := p.resolveModel()
	// upstream: packages/ai/src/api/anthropic-messages.ts:stream
	builder := newObservedProviderBuilder(ctx, APIAnthropicMessages, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	builder.setProviderEventObserver(opts, model)
	builder.setResponseMetadata("", "", "", anthropicProviderEffort(model, opts), nil)
	request, err := p.prepareStream(ctx, transcript, opts, model)
	if err != nil {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		builder.fail(reason, err)
		return builder.stream, nil
	}
	requestContext, abort := context.WithCancel(ctx)
	builder.abort = abort
	go builder.runResponse(func() error { return p.streamResponse(ctx, requestContext, request, opts, model, builder) })
	return builder.stream, nil
}

// anthropicStreamRequest is everything the request needs after upstream's synchronous prefix: it can be sent again after a rejected thinking signature.
type anthropicStreamRequest struct {
	baseURL string
	client  anthropicClient
	params  anthropicParams
	// first is the request prepared synchronously, including the caller's payload hook.
	first *http.Request
}

func (p *anthropicProvider) prepareStream(ctx context.Context, transcript TranscriptContext, opts StreamOptions, model *Model) (*anthropicStreamRequest, error) {
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
	// headers, then request headers as the request's options.headers. The
	// auth resolver owns ANTHROPIC_AUTH_TOKEN: the API implementation never reads it
	// (anthropic-messages.ts:331-341, 614-615), so a direct stream carries only what the caller passes.
	optionsHeaders := mergeAnthropicHeaders(modelHeaders, anthropicHeadersFromProviderHeaders(opts.Headers))
	var dynamicHeaders anthropicHeaders
	if p.cfg.UseBearerAuth && p.cfg.DynamicHeaders != nil {
		dynamicHeaders = anthropicHeadersFromMap(p.cfg.DynamicHeaders(transcript, opts))
	}
	// upstream: packages/ai/src/api/anthropic-messages.ts:614-615 (getAnthropicFederation, assertRequestAuth)
	var lease *anthropicFederationLease
	if federation := getAnthropicFederation(p.cfg.ProviderID, apiKey, optionsHeaders, requestEnv); federation != nil {
		lease = acquireAnthropicFederationClient(baseURL, *federation, opts.Fetch)
	} else if !anthropicHasRequestAuth(apiKey, optionsHeaders) {
		return nil, fmt.Errorf("No API key for provider: %s", p.cfg.ProviderID)
	}
	client := p.createClient(apiKey, optionsHeaders, dynamicHeaders, p.sessionAffinityHeaders(opts.SessionID, resolveAnthropicCacheRetention(opts.CacheRetention, requestEnv)), lease)
	params, err := p.buildParams(model, transcript, client.isOAuthToken, modelHeaders, optionsHeaders, opts, requestEnv)
	if err != nil {
		lease.release()
		return nil, fmt.Errorf("anthropic: build params: %w", err)
	}
	prepared := &anthropicStreamRequest{baseURL: baseURL, client: client, params: params}
	prepared.first, err = p.prepareRequest(ctx, baseURL, client, params.request, model, opts)
	if err != nil {
		lease.release()
		return nil, err
	}
	return prepared, nil
}

// send authorizes a prepared request with the federated token when the client has one, sends it, and invalidates the token on a 401.
func (p *anthropicProvider) send(ctx, requestContext context.Context, client anthropicClient, httpRequest *http.Request, opts StreamOptions) (*http.Response, error) {
	if client.federation != nil {
		if err := client.federation.authorize(requestContext, httpRequest); err != nil {
			return nil, err
		}
	}
	resp, err := p.do(ctx, httpRequest, opts)
	if err == nil {
		client.federation.invalidateAfter401(resp.StatusCode)
	}
	return resp, err
}

// streamResponse sends the request, handles a non-success status, and runs the body pipeline on the provider's executor turn. The request context is canceled with the caller's and when the pipeline ends.
func (p *anthropicProvider) streamResponse(ctx, requestContext context.Context, request *anthropicStreamRequest, opts StreamOptions, model *Model, builder *assistantStreamBuilder) error {
	defer request.client.federation.release()
	httpRequest := request.first.WithContext(requestContext)
	resp, err := p.send(ctx, requestContext, request.client, httpRequest, opts)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// pig divergence (D37): retry once without rejected thinking signatures.
		if resp.StatusCode != http.StatusBadRequest || !isThinkingSignatureError(b) {
			return anthHTTPError(p.cfg.ProviderID, resp.StatusCode, b)
		}
		request.params.request.Messages, err = request.params.convertMessages(stripThinkingSignatures(request.params.conversation), request.client.isOAuthToken)
		if err != nil {
			return err
		}
		retry, err := p.prepareRequest(ctx, request.baseURL, request.client, request.params.request, model, opts)
		if err != nil {
			return err
		}
		resp, err = p.send(ctx, requestContext, request.client, retry.WithContext(requestContext), opts)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			b2, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return anthHTTPError(p.cfg.ProviderID, resp.StatusCode, b2)
		}
	}
	defer func() { _ = resp.Body.Close() }()
	_, builder.managed = resp.Body.(*observedResponseBody)
	return builder.responseTurn(func() error {
		if err := observeProviderResponse(ctx, opts, resp, model); err != nil { // anthropic-messages.ts:595
			return err
		}
		body := io.Reader(resp.Body)
		if observed, ok := resp.Body.(*observedResponseBody); ok {
			// iterateSseMessages reads with getReader().read(), not ReadableStream.values(); undici's body settles a read after the reactions counted here.
			native := newNativeBodyReaderWithHops(ctx, observed, builder.turn, builder.abort, anthropicBodyReadHops)
			defer func() { _ = native.Close() }()
			body = native
		}
		p.parseAnthropicSSE(ctx, body, builder, anthropicStreamNames{isOAuthToken: request.client.isOAuthToken, currentTools: request.params.tools})
		return nil
	})
}

func anthropicProviderEffort(model *Model, opts StreamOptions) string {
	if !anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoEffort }) {
		return ""
	}
	if opts.Effort != "" {
		return opts.Effort
	}
	return anthropicActiveEffort(model, opts.Thinking)
}

func (p *anthropicProvider) resolveModel() *Model {
	model := &Model{ID: p.cfg.Model}
	// upstream: packages/ai/src/api/anthropic-messages.ts:buildParams
	if p.cfg.ModelMetadata != nil {
		model = new(*p.cfg.ModelMetadata)
	} else if generated, ok := LookupModel(p.ID() + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	}
	// Configured model compat is already merged by the model runtime and takes precedence over the generated catalog.
	if p.cfg.Compat != nil {
		model.ProviderMeta.Compat = p.cfg.Compat
	}
	model.ProviderMeta.ProviderID = p.cfg.ProviderID
	model.ProviderMeta.API = APIAnthropicMessages
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
func (p *anthropicProvider) sessionAffinityHeaders(sessionID string, retention CacheRetention) anthropicHeaders {
	var headers anthropicHeaders
	if sessionID == "" || retention == CacheRetentionNone {
		return headers
	}
	// upstream: packages/ai/src/api/anthropic-messages.ts:getAnthropicCompat
	isOpenRouter := p.cfg.ProviderID == "openrouter" || strings.Contains(p.cfg.BaseURL, "openrouter.ai")
	sendSessionAffinity := isOpenRouter
	format := SessionAffinityFormat("")
	if isOpenRouter {
		format = SessionAffinityOpenRouter
	}
	if compat := p.cfg.Compat; compat != nil {
		if compat.SendSessionAffinityHeaders != nil {
			sendSessionAffinity = *compat.SendSessionAffinityHeaders
		}
		if compat.SessionAffinityFormat != "" {
			format = compat.SessionAffinityFormat
		}
	}
	if sendSessionAffinity {
		header := "x-session-affinity"
		if format == SessionAffinityOpenRouter {
			header = "x-session-id"
		}
		headers.set(header, sessionID)
	}
	return headers
}

// resolveAnthropicCacheRetention gives explicit request choices precedence; only env=long changes the short default.
func resolveAnthropicCacheRetention(retention CacheRetention, env ProviderEnv) CacheRetention {
	if retention != "" {
		return retention
	}
	if getProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return CacheRetentionLong
	}
	return CacheRetentionShort
}

// cacheControl applies the resolved retention to system, tool, and conversation breakpoints.
func (p *anthropicProvider) cacheControl(retention CacheRetention) *anthCacheControl {
	if retention == CacheRetentionNone {
		return nil
	}
	cc := &anthCacheControl{Type: "ephemeral"}
	supportsLong := true
	if compat := p.cfg.Compat; compat != nil && compat.SupportsLongCacheRetention != nil {
		supportsLong = *compat.SupportsLongCacheRetention
	}
	if retention == CacheRetentionLong && supportsLong {
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
		blocks = append(blocks, anthSystemBlock{Type: "text", Text: sanitizeSurrogates(systemPrompt), CacheControl: cc})
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
func anthropicRequestTools(params anthropicParams, initialTools []ToolSchema, isOAuthToken, supportsEager, supportsStrict bool, cacheControl *anthCacheControl) ([]anthTool, error) {
	if !params.nativeToolChanges {
		if len(params.tools) == 0 {
			return nil, nil
		}
		return anthConvertTools(params.tools, isOAuthToken, supportsEager, supportsStrict, cacheControl)
	}
	// Initial tools stay active with the cache breakpoint on the last one,
	// followed by the placeholder. The list never changes afterwards: later
	// tools are defined by value in tool_addition blocks and withdrawn by
	// tool_removal, so the cached prefix survives every tool change.
	tools, err := anthConvertTools(initialTools, isOAuthToken, supportsEager, supportsStrict, cacheControl)
	if err != nil {
		return nil, err
	}
	return append(tools, deferredToolPlaceholder()), nil
}

// buildParams mirrors upstream buildParams for the request fields PiG sends.
func (p *anthropicProvider) buildParams(model *Model, transcript TranscriptContext, isOAuthToken bool, modelHeaders, optionsHeaders anthropicHeaders, opts StreamOptions, env ProviderEnv) (anthropicParams, error) {
	supportsMidConversation := anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoSystemMessages })
	// upstream: packages/ai/src/api/anthropic-messages.ts:buildParams
	messages := TransformMessages(ResolveTranscript(transcript, supportsMidConversation).Messages(), model, func(id string, _ *Model, _ AssistantMessage) string {
		return normalizeAnthropicToolCallID(id)
	})
	systemPrompt := ""
	var initialTools []ToolSchema
	if initialSystem := GetInitialSystemMessage(messages); initialSystem != nil {
		systemPrompt = GetCurrentSystemPrompt([]Message{*initialSystem})
		initialTools = initialSystem.ToolsAdded
	}
	// Native tool changes keep the request-level tool list fixed and define
	// every later tool by value in a tool_addition block, which also expresses
	// same-name redefinitions. Anthropic rejects a tool list where every tool is
	// deferred, so an initial active tool must anchor the placeholder.
	params := anthropicParams{
		conversation:        WithoutInitialSystemMessage(messages),
		tools:               GetCurrentTools(messages),
		cacheControl:        p.cacheControl(resolveAnthropicCacheRetention(opts.CacheRetention, env)),
		allowEmptySignature: modelAllowsEmptySignature(model),
		nativeToolChanges: supportsMidConversation &&
			anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoToolChanges }) &&
			len(initialTools) > 0,
	}
	params.activeEffort = anthropicProviderEffort(model, opts)
	if params.activeEffort != "" {
		params.managedProvider = p.cfg.ProviderID
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
	supportsStrictTools := anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsStrictTools })
	if params.nativeToolChanges {
		params.toolDefinitions = func(tools []ToolSchema) ([]anthTool, error) {
			return anthConvertTools(tools, isOAuthToken, supportsEager, supportsStrictTools, nil)
		}
	}
	conversation, err := params.convertMessages(params.conversation, isOAuthToken)
	if err != nil {
		return anthropicParams{}, err
	}
	thinkingEnabled := opts.Thinking != ThinkingOff && opts.Thinking != ""
	if opts.ThinkingEnabled != nil {
		thinkingEnabled = *opts.ThinkingEnabled
	}
	var allowedFallbackModels []AnthropicAllowedFallbackModel
	if compat := model.ProviderMeta.Compat; compat != nil {
		allowedFallbackModels = compat.AllowedFallbackModels
	}
	req := anthRequest{
		Model:     p.cfg.Model,
		Messages:  conversation,
		MaxTokens: maxTokens,
		Stream:    true,
		Betas: getBetaFeatures(modelHeaders, optionsHeaders, anthropicBetaInputs{
			isOAuthToken:                    isOAuthToken,
			hasTools:                        len(params.tools) > 0,
			supportsEagerToolInputStreaming: supportsEager,
			reasoning:                       model.ProviderMeta.Reasoning,
			thinkingEnabled:                 thinkingEnabled,
			forceAdaptiveThinking:           modelUsesAdaptiveThinking(model),
			hasFallbacks:                    len(allowedFallbackModels) > 0,
			supportsMidConvoEffort:          params.activeEffort != "",
			nativeToolChanges:               params.nativeToolChanges,
		}),
		System: anthSystemBlocks(systemPrompt, isOAuthToken, params.cacheControl),
	}
	if len(allowedFallbackModels) > 0 {
		req.Fallbacks = make([]anthFallback, len(allowedFallbackModels))
		for i, fallback := range allowedFallbackModels {
			req.Fallbacks[i].Model = fallback.Model
		}
	}
	var toolCacheControl *anthCacheControl
	if compat := p.cfg.Compat; compat == nil || compat.SupportsCacheControlOnTools == nil || *compat.SupportsCacheControlOnTools {
		toolCacheControl = params.cacheControl
	}
	req.Tools, err = anthropicRequestTools(params, initialTools, isOAuthToken, supportsEager, supportsStrictTools, toolCacheControl)
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
	if opts.ThinkingEnabled != nil && model.ProviderMeta.Reasoning && params.activeEffort == "" {
		req.Thinking, req.OutputConfig, req.MaxTokens = nil, nil, maxTokens
		if *opts.ThinkingEnabled {
			if modelUsesAdaptiveThinking(model) {
				req.Thinking = &anthThinking{Type: "adaptive", Display: "summarized"}
				if opts.Effort != "" {
					req.OutputConfig = &anthOutputConfig{Effort: opts.Effort}
				}
			} else {
				budget := 1024
				if opts.ThinkingBudgetTokens != nil && *opts.ThinkingBudgetTokens != 0 {
					budget = *opts.ThinkingBudgetTokens
				}
				req.Thinking = &anthThinking{Type: "enabled", BudgetTokens: budget, Display: "summarized"}
			}
		} else if !anthThinkingOffIsNull(model) {
			req.Thinking = &anthThinking{Type: "disabled"}
		}
	}
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

// prepareRequest runs OnPayload with the selected model and current request, then builds the streaming POST. Upstream calls the payload hook synchronously, before its first await.
func (p *anthropicProvider) prepareRequest(ctx context.Context, baseURL string, client anthropicClient, req anthRequest, model *Model, opts StreamOptions) (*http.Request, error) {
	payload := any(req)
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(req, model)
		if err != nil {
			return nil, err
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
	return httpReq, nil
}

// do issues a prepared request. ctx is the caller's context: it classifies a failed send as an abort.
func (p *anthropicProvider) do(ctx context.Context, httpReq *http.Request, opts StreamOptions) (*http.Response, error) {
	response, err := providerHTTPClient(p.client, opts.Fetch).Do(httpReq)
	if err != nil {
		// Upstream retryProviderRequest preserves cancellation, while the Anthropic SDK classifies pre-header transport failures before stream's catch publishes them.
		switch {
		case ctx.Err() != nil:
			return nil, errors.New("Request aborted")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, errors.New("Request timed out.")
		default:
			if _, ok := errors.AsType[*nodeTransportError](err); ok {
				return nil, errors.New("Connection error.")
			}
		}
	}
	return response, err
}

// anthropicBodyReadHops are the promise reactions between undici's byte stream and the reader.read() promise of Node 24.19 and 26.7, measured by coding/testdata/rpc33-observation/providers/anthropic-messages/probe.mjs: a body already buffered with the headers settles after two reactions, data that arrives later after three, and the end of the body after one (two when the last bytes waited for the transport).
var anthropicBodyReadHops = bodyReadHops{buffered: 2, data: 3, end: 1, endAfterData: 2}

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

// anthropicEvent decodes the JSON of one SSE record after the generator chain has accepted it.
func decodeAnthropicEvent(sse serverSentEvent, target any) error {
	text, err := anthropicParseJSON(sse.Data)
	if err == nil {
		err = json.Unmarshal([]byte(text), target)
	}
	if err != nil {
		return &anthropicSSEParseError{sse: sse, cause: err}
	}
	return nil
}

// findAnthropicBlock is `blocks.findIndex((b) => b.index === event.index)`; it returns -1 when no open block carries the index.
func findAnthropicBlock(content []AssistantContentBlock, index int) int {
	want := scratchIndexFor(index)
	for i, block := range content {
		switch block := block.(type) {
		case TextContent:
			if block.scratch == want {
				return i
			}
		case ThinkingContent:
			if block.scratch == want {
				return i
			}
		case ToolCall:
			if block.scratch.hasIndex && block.scratch.index == index {
				return i
			}
		}
	}
	return -1
}

// parseAnthropicSSE is the body of stream()'s try block from the start event on (packages/ai/src/api/anthropic-messages.ts:596-826): it pushes start, consumes the SSE iterator until it completes, and publishes exactly one terminal event. Error-event data is surfaced verbatim.
func (p *anthropicProvider) parseAnthropicSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder, names anthropicStreamNames) {
	builder.start() // anthropic-messages.ts:596
	suspend := func() {}
	if turn := builder.turn; turn != nil {
		suspend = func() {
			builder.publishPending()
			suspendContinuation(turn)
		}
	}
	source, ok := r.(openAIStreamChunkReader)
	if !ok {
		source = &anthropicReaderSource{reader: r}
	}
	events := newAnthropicEventReader(ctx, anthropicPublishingSource{source, builder}, suspend)

	output := builder.partial
	usage := &output.Usage
	usageCost := builder.modelCost
	var inputTransformations []any

	// fail is the catch block (anthropic-messages.ts:817-826).
	fail := func(err error) {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		builder.failUnfinished(reason, err)
	}
	// throw is an exception in the loop body: the for-await closes the generator before the catch runs.
	throw := func(err error) {
		events.close()
		fail(err)
	}

	for {
		sse, ok, err := events.next()
		if err != nil {
			fail(err)
			return
		}
		if !ok {
			break
		}
		// The events reader already validated (and repaired) the payload.
		event, _ := anthropicParseJSON(sse.Data)
		if err := builder.observeProviderEvent([]byte(event)); err != nil {
			throw(err)
			return
		}
		switch sse.Event {
		case "message_start":
			var ev anthEventMessageStart
			if err := decodeAnthropicEvent(sse, &ev); err != nil {
				throw(err)
				return
			}
			output.ResponseID = ev.Message.ID
			if transformations, ok := ev.Message.InputTransformations.([]any); ok {
				inputTransformations = transformations
			}
			responseModel := ""
			if ev.Message.Model != p.cfg.Model {
				responseModel = ev.Message.Model
			}
			if responseModel != "" {
				output.ResponseModel = responseModel
			}
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
			builder.touch()

		case "content_block_start":
			var ev anthEventContentBlockStart
			if err := decodeAnthropicEvent(sse, &ev); err != nil {
				throw(err)
				return
			}
			if ev.ContentBlock.Type == "fallback" {
				if len(output.Content) > 0 {
					throw(errors.New("Anthropic performed an unsupported mid-output model fallback"))
					return
				}
				continue
			}
			index := scratchIndexFor(ev.Index)
			switch ev.ContentBlock.Type {
			case "text":
				output.Content = append(output.Content, TextContent{Text: ev.ContentBlock.Text, scratch: index})
				builder.push(TextStartEvent{ContentIndex: len(output.Content) - 1, Partial: output})
			case "thinking":
				output.Content = append(output.Content, ThinkingContent{
					Thinking: ev.ContentBlock.Thinking, ThinkingSignature: ev.ContentBlock.Signature,
					thinkingSignatureEmpty: ev.ContentBlock.Signature == "", scratch: index,
				})
				builder.push(ThinkingStartEvent{ContentIndex: len(output.Content) - 1, Partial: output})
			case "redacted_thinking":
				output.Content = append(output.Content, ThinkingContent{
					Thinking: "[Reasoning redacted]", ThinkingSignature: ev.ContentBlock.Data, Redacted: true,
					thinkingSignatureEmpty: ev.ContentBlock.Data == "", scratch: index,
				})
				builder.push(ThinkingStartEvent{ContentIndex: len(output.Content) - 1, Partial: output})
			case "tool_use":
				arguments, _ := ev.ContentBlock.Input.(map[string]any)
				if arguments == nil {
					arguments = JsonObject{}
				}
				output.Content = append(output.Content, ToolCall{
					ID: ev.ContentBlock.ID, Name: names.toolName(ev.ContentBlock.Name), Arguments: arguments,
					scratch: toolCallScratch{hasPartialJson: true, hasIndex: true, index: ev.Index},
				})
				builder.push(ToolCallStartEvent{ContentIndex: len(output.Content) - 1, Partial: output})
			}

		case "content_block_delta":
			var ev anthEventContentBlockDelta
			if err := decodeAnthropicEvent(sse, &ev); err != nil {
				throw(err)
				return
			}
			index := findAnthropicBlock(output.Content, ev.Index)
			if index < 0 {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if block, ok := output.Content[index].(TextContent); ok {
					block.Text += ev.Delta.Text
					output.Content[index] = block
					builder.push(TextDeltaEvent{ContentIndex: index, Delta: ev.Delta.Text, Partial: output})
				}
			case "thinking_delta":
				if block, ok := output.Content[index].(ThinkingContent); ok {
					block.Thinking += ev.Delta.Thinking
					output.Content[index] = block
					builder.push(ThinkingDeltaEvent{ContentIndex: index, Delta: ev.Delta.Thinking, Partial: output})
				}
			case "input_json_delta":
				if block, ok := output.Content[index].(ToolCall); ok {
					block.scratch.partialJson += ev.Delta.PartialJSON
					block.SetStreamingArguments(block.scratch.partialJson)
					output.Content[index] = block
					builder.push(ToolCallDeltaEvent{ContentIndex: index, Delta: ev.Delta.PartialJSON, Partial: output})
				}
			case "signature_delta":
				if block, ok := output.Content[index].(ThinkingContent); ok {
					block.ThinkingSignature += ev.Delta.Signature
					block.thinkingSignatureEmpty = block.ThinkingSignature == ""
					output.Content[index] = block
					builder.touch()
				}
			}

		case "content_block_stop":
			var ev anthEventContentBlockStop
			if err := decodeAnthropicEvent(sse, &ev); err != nil {
				throw(err)
				return
			}
			index := findAnthropicBlock(output.Content, ev.Index)
			if index < 0 {
				continue
			}
			switch block := output.Content[index].(type) {
			case TextContent:
				block.scratch = ""
				output.Content[index] = block
				builder.push(TextEndEvent{ContentIndex: index, Content: block.Text, Partial: output})
			case ThinkingContent:
				block.scratch = ""
				output.Content[index] = block
				builder.push(ThinkingEndEvent{ContentIndex: index, Content: block.Thinking, Partial: output})
			case ToolCall:
				// Finalize in place and strip the scratch buffer so replay only carries parsed arguments.
				block.SetStreamingArguments(block.scratch.partialJson)
				block.scratch = toolCallScratch{}
				output.Content[index] = block
				builder.push(ToolCallEndEvent{ContentIndex: index, ToolCall: block, Partial: output})
			}

		case "message_delta":
			var ev anthEventMessageDelta
			if err := decodeAnthropicEvent(sse, &ev); err != nil {
				throw(err)
				return
			}
			if transformations, ok := ev.InputTransformations.([]any); ok {
				inputTransformations = transformations
			}
			if ev.Delta.StopReason != "" {
				output.RawStopReason = ev.Delta.StopReason
				explanation := ""
				if ev.Delta.StopDetails != nil {
					explanation = ev.Delta.StopDetails.Explanation
				}
				mapped, message, err := mapAnthStopReason(ev.Delta.StopReason, explanation)
				if err != nil {
					builder.touch()
					throw(err)
					return
				}
				output.StopReason = mapped
				if message != "" {
					output.ErrorMessage = message
				}
			}
			// Only update usage fields that are present, so proxies that omit input_tokens keep message_start's.
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
			if ev.Usage.CacheCreation != nil && ev.Usage.CacheCreation.Ephemeral1H != nil {
				usage.CacheWrite1h = new(*ev.Usage.CacheCreation.Ephemeral1H)
			}
			if ev.Usage.OutputTokensDetails != nil && ev.Usage.OutputTokensDetails.ThinkingTokens != nil {
				usage.Reasoning = new(*ev.Usage.OutputTokensDetails.ThinkingTokens)
			}
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
			calculateUsageCost(usageCost, usage)
			builder.touch()

		case "message_stop":
			var payload any
			if err := decodeAnthropicEvent(sse, &payload); err != nil {
				throw(err)
				return
			}
		}
	}

	if ctx.Err() != nil {
		fail(errAnthropicRequestAborted)
		return
	}
	if output.StopReason == StopReasonPending {
		fail(errors.New("Anthropic stream ended without a stop reason"))
		return
	}
	if output.StopReason == StopReasonAborted || output.StopReason == StopReasonError {
		message := output.ErrorMessage
		if message == "" {
			message = "An unknown error occurred"
		}
		fail(errors.New(message))
		return
	}
	if len(inputTransformations) > 0 {
		transformations := make([]map[string]any, len(inputTransformations))
		for i, value := range inputTransformations {
			if value == nil {
				fail(errors.New("Cannot read properties of null (reading 'type')"))
				return
			}
			input, _ := value.(map[string]any)
			transformation := map[string]any{}
			for _, key := range []string{"type", "path", "reason"} {
				if value := input[key]; value != nil {
					transformation[key] = value
				}
			}
			transformations[i] = transformation
		}
		output.Diagnostics = append(output.Diagnostics, AssistantMessageDiagnostic{Type: "anthropic_input_transformations", Timestamp: time.Now().UnixMilli(), Details: map[string]any{"transformations": transformations}})
	}
	builder.push(DoneEvent{Reason: output.StopReason, Message: output})
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

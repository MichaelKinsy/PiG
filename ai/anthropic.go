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
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsjson"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
	Betas      []string          `json:"betas,omitempty" requestsplice:"read"`
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
	Effort AnthropicEffort `json:"effort"`
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

func (anthContentBlock) carriesRequestStrings() {}

// encodesRequestStrings: no field of the request, nor of anything in it, writes a string in another form than its own, and the request's own marshalers (anthContentBlock, anthTool) write theirs as JSON strings under the caller's options.
func (anthRequest) encodesRequestStrings() {}

// Text and thinking blocks retain their required string fields, including explicit empty values.
func (block anthContentBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(block.wire())
}

// wire is the value MarshalJSON writes.
func (block anthContentBlock) wire() any {
	type payload anthContentBlock
	if block.Type == "text" {
		return struct {
			payload
			Text string `json:"text"`
		}{payload: payload(block), Text: block.Text}
	}
	if block.Type != "thinking" {
		return payload(block)
	}
	// upstream: packages/ai/src/api/anthropic-messages.ts:convertMessages retains empty thinking and signature fields on thinking blocks.
	return struct {
		Type         string            `json:"type"`
		Thinking     string            `json:"thinking"`
		Signature    string            `json:"signature"`
		CacheControl *anthCacheControl `json:"cache_control,omitempty"`
	}{block.Type, block.Thinking, block.Signature, block.CacheControl}
}

// anthToolChange is the tool of a tool_addition or tool_removal block: a
// tool_reference naming a declared tool, or a tool_definition carrying a tool by
// value (inline-tools-2026-09-15).
type anthToolChange struct {
	Type       string    `json:"type"` // "tool_reference" | "tool_definition"
	Name       string    `json:"name,omitempty"`
	Definition *anthTool `json:"definition,omitempty"`
}

// anthTool writes its members in the order convertTools creates them: name, description, eager_input_streaming, strict, input_schema,
// cache_control (anthropic-messages.ts:1602-1609); DEFERRED_TOOL_PLACEHOLDER has defer_loading after input_schema (:204-209).
type anthTool struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	EagerInputStream any               `json:"eager_input_streaming,omitempty"`
	Strict           *bool             `json:"strict,omitempty"`
	InputSchema      map[string]any    `json:"input_schema"`
	inputSchemaOrder schemaObjectOrder // the member order of InputSchema's objects; nil writes them sorted
	// DeferLoading declares a tool that stays inactive until a tool_addition
	// block surfaces it.
	DeferLoading bool              `json:"defer_loading,omitempty"`
	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

// MarshalJSON writes InputSchema with its recorded member order.
func (t anthTool) MarshalJSON() ([]byte, error) {
	wire, err := t.wire()
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}

// anthToolWire is the value anthTool's MarshalJSON writes.
type anthToolWire struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	EagerInputStream any               `json:"eager_input_streaming,omitempty"`
	Strict           *bool             `json:"strict,omitempty"`
	InputSchema      json.RawMessage   `json:"input_schema"`
	DeferLoading     bool              `json:"defer_loading,omitempty"`
	CacheControl     *anthCacheControl `json:"cache_control,omitempty"`
}

func (t anthTool) wire() (anthToolWire, error) {
	inputSchema, err := marshalToolParameters(t.InputSchema, t.inputSchemaOrder)
	if err != nil {
		return anthToolWire{}, err
	}
	return anthToolWire{t.Name, t.Description, t.EagerInputStream, t.Strict, inputSchema, t.DeferLoading, t.CacheControl}, nil
}

// anthLegacyInputSchemaOrder is the member order of the literal { type, properties, required } (anthropic-messages.ts:1589-1593, :207).
var anthLegacyInputSchemaOrder = []string{"type", "properties", "required"}

// deferredToolPlaceholder mirrors upstream DEFERRED_TOOL_PLACEHOLDER: a stable
// deferred tool declared whenever native tool changes are in use. Anthropic adds
// hidden prompt scaffolding for mid-conversation tool changes; declaring the
// placeholder from the first request keeps that scaffolding in the cached
// prefix, so the first tool change does not invalidate the cache. It is never
// activated.
func deferredToolPlaceholder() anthTool {
	return anthTool{
		Name:             "__pi_deferred_placeholder__",
		Description:      "Reserved placeholder. Never available. Never call this.",
		InputSchema:      map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
		inputSchemaOrder: schemaObjectOrder{"": anthLegacyInputSchemaOrder},
		DeferLoading:     true,
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
			// message_start's usage has no reasoning count: Pi reads output_tokens_details only from message_delta (anthropic-messages.ts:678-688, :848-852).
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

// anthBlockIndex is a content block index that records whether the member was a number.
type anthBlockIndex struct {
	value   int
	present bool
}

func (index *anthBlockIndex) UnmarshalJSON(raw []byte) error {
	value, err := strconv.Atoi(string(raw))
	if err != nil {
		// A spelling such as 1e0 is the number 1 in JS; a non-integer or a non-number matches no block.
		number, floatErr := strconv.ParseFloat(string(raw), 64)
		if floatErr != nil || number != math.Trunc(number) || math.Abs(number) > 1<<53 {
			return nil
		}
		value = int(number)
	}
	*index = anthBlockIndex{value: value, present: true}
	return nil
}

type anthEventContentBlockDelta struct {
	// Index has no value when the record has no numeric index, so `blocks.findIndex((b) => b.index === event.index)` finds no block.
	Index anthBlockIndex `json:"index"`
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
		// OpenRouter adds billing fields to streamed message_delta usage at runtime, although its OpenAPI schema and the Anthropic SDK do not type them (anthropic-messages.ts).
		openRouterUsageAccounting
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

// anthImageBlock is convertImageBlock: a base64 image block.
func anthImageBlock(image ImageContent) anthContentBlock {
	return anthContentBlock{
		Type: "image",
		Source: map[string]any{
			"type": "base64", "media_type": image.MimeType, "data": image.Data,
		},
	}
}

// anthToolResultContent joins every text-only element with a newline. Mixed text/image results preserve block order and explicit empty text; only an image-only result receives placeholder text. The caller passes the images of an errored result as text, split out by SplitErrorToolResultImages.
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
			blocks = append(blocks, anthImageBlock(block))
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
	assistantLevels map[int]AnthropicEffort
}

// anthConvertMessages mirrors upstream convertMessages. Later system messages
// are held back and emitted directly before the next assistant message, or at
// the end: Anthropic requires tool_result blocks to follow their tool_use
// immediately, so an update between them would be rejected.
func anthConvertMessages(messages []Message, isOAuthToken, allowEmptySignature, nativeToolChanges bool) []anthMessage {
	var convertToolDefinitions anthToolDefinitions
	if nativeToolChanges {
		convertToolDefinitions = func(tools []ToolSchema) ([]anthTool, error) {
			return anthConvertTools(tools, isOAuthToken, false, false, nil, false)
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
	assistantLevels := map[int]AnthropicEffort{}
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
					// Pi: input = block.arguments ?? {} (anthropic-messages.ts:1444), in the model's member order.
					arguments, _ := content.argumentsJSONDefaulting(false)
					name := content.Name
					if isOAuthToken {
						name = toClaudeCodeName(name)
					}
					blocks = append(blocks, anthContentBlock{Type: "tool_use", ID: content.ID, Name: name, Input: json.RawMessage(arguments)})
				}
			}
			if len(blocks) > 0 {
				// An assistant message that converts to no blocks is dropped, so the held updates wait for the next one that is sent (Pi #11091).
				out = append(out, pendingSystem...)
				pendingSystem = nil
				messageIndex := len(out)
				out = append(out, anthMessage{Role: "assistant", Content: blocks})
				if managedProvider != "" && message.API == APIAnthropicMessages && message.Provider == managedProvider && isAnthropicEffort(AnthropicEffort(message.ProviderThinkingLevel)) {
					assistantLevels[messageIndex] = AnthropicEffort(message.ProviderThinkingLevel)
				}
			}
		case ToolResultMessage:
			var results []anthContentBlock
			// Anthropic rejects error tool results that contain anything but text, so their images follow all tool results of
			// the turn, which must come first in the user message (Pi #10794, anthropic-messages.ts convertMessages).
			var errorImages []anthContentBlock
			for index < len(messages) {
				result, ok := messages[index].(ToolResultMessage)
				if !ok {
					break
				}
				isError := result.IsError
				content := result.Content
				if split := SplitErrorToolResultImages(result); split != nil {
					content = make([]ToolResultMessageContent, len(split.Content))
					for position, block := range split.Content {
						content[position] = block
					}
					errorImages = append(errorImages, anthContentBlock{Type: "text", Text: split.Caption})
					for _, image := range split.Images {
						errorImages = append(errorImages, anthImageBlock(image))
					}
				}
				results = append(results, anthContentBlock{
					Type: "tool_result", ToolUseID: result.ToolCallID,
					Content: anthToolResultContent(content), IsError: &isError,
				})
				index++
			}
			index--
			out = append(out, anthMessage{Role: "user", Content: append(results, errorImages...)})
		}
	}
	out = append(out, pendingSystem...)
	return anthConvertedMessages{messages: out, assistantLevels: assistantLevels}, nil
}

func isAnthropicEffort(value AnthropicEffort) bool {
	switch value {
	case AnthropicEffortLow, AnthropicEffortMedium, AnthropicEffortHigh, AnthropicEffortXHigh, AnthropicEffortMax:
		return true
	default:
		return false
	}
}

func insertAnthropicThinkingLevelMessages(converted anthConvertedMessages, activeEffort AnthropicEffort) []anthMessage {
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

// anthHTTPError reconstructs the Anthropic SDK's thrown status error: client.js reads the body as text, parses it with safeJSON, and
// APIError.makeMessage composes "<status> <message>" from the parsed body's message, the stringified body, or the text.
func anthHTTPError(status int, body []byte) error {
	// The stream reports the SDK error's own message, with no body appended.
	return &providerError{status: new(status), message: anthHTTPErrorMessage(status, body)}
}

// anthHTTPErrorMessage is the message of the SDK error anthHTTPError reconstructs.
func anthHTTPErrorMessage(status int, body []byte) string {
	var errJSON json.RawMessage
	if json.Valid(body) && jsonValueTruthy(body) {
		if canonical, err := jsonstringify.Canonicalize(body); err == nil {
			errJSON = canonical
		}
	}
	var message string
	switch errJSON {
	case nil:
		message = string(body)
	default:
		message = string(errJSON)
		var envelope map[string]json.RawMessage
		if json.Unmarshal(errJSON, &envelope) == nil {
			if inner, ok := envelope["message"]; ok && jsonValueTruthy(inner) {
				message = string(inner)
				var text string
				if json.Unmarshal(inner, &text) == nil {
					message = text
				}
			}
		}
	}
	if message == "" {
		return fmt.Sprintf("%d status code (no body)", status)
	}
	return fmt.Sprintf("%d %s", status, message)
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

// anthropicStrictToolLimitMessages are the texts of Anthropic's 400s for a strict tool set over its limits: grammar size, tool count and union parameters.
//
// upstream: packages/ai/src/api/anthropic-messages.ts:stream (the strict tool fallback's error test, :664-677)
var anthropicStrictToolLimitMessages = []string{
	"The compiled grammar is too large",
	"Too many strict tools (",
	"Schemas contains too many parameters with union types (",
}

// anthropicStrictToolLimitsExceeded reports whether a rejected request is the APIError Pi downgrades: status 400, error type
// invalid_request_error, and an SDK message that contains one of the strict tool limit texts. It returns that message.
func anthropicStrictToolLimitsExceeded(status int, body []byte) (string, bool) {
	if status != http.StatusBadRequest {
		return "", false
	}
	var errResp anthErrorResponse
	if json.Unmarshal(body, &errResp) != nil || errResp.Error.Type != "invalid_request_error" {
		return "", false
	}
	message := anthHTTPErrorMessage(status, body)
	for _, limit := range anthropicStrictToolLimitMessages {
		if strings.Contains(message, limit) {
			return message, true
		}
	}
	return "", false
}

// anthropicMarksPreferredStrict reports whether a converted tool is strict because its constrainedSampling prefers strict: the
// definitions that make hasPreferredStrictTools true (anthropic-messages.ts:1184-1191).
func anthropicMarksPreferredStrict(tools []ToolSchema, definitions []anthTool) bool {
	for i, definition := range definitions {
		config := tools[i].ConstrainedSampling
		if definition.Strict != nil && *definition.Strict && config != nil && config.Type == ConstrainedSamplingJSONSchema && config.Strict == ConstrainedSamplingStrictPrefer {
			return true
		}
	}
	return false
}

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

// anthConvertTools is upstream convertTools. disablePreferredStrictTools sends every tool whose constrainedSampling prefers strict
// without strict, for the strict tool fallback (anthropic-messages.ts:1612-1627).
func anthConvertTools(tools []ToolSchema, isOAuthToken, supportsEagerToolInputStreaming, supportsStrictTools bool, cacheControl *anthCacheControl, disablePreferredStrictTools bool) ([]anthTool, error) {
	out := make([]anthTool, len(tools))
	for i, tool := range tools {
		var strict *bool
		if config := tool.ConstrainedSampling; !disablePreferredStrictTools || config == nil || config.Type != ConstrainedSamplingJSONSchema || config.Strict != ConstrainedSamplingStrictPrefer {
			var err error
			if strict, err = resolveJSONSchemaStrictSampling(tool, supportsStrictTools, anthropicStrictUnsupportedKeyword); err != nil {
				return nil, err
			}
		}
		toolParameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		parameters, parameterOrder := toolParameters.Schema, toolParameters.order
		schema, schemaOrder := anthInputSchema(parameters, parameterOrder, strict != nil && *strict)
		name := tool.Name
		if isOAuthToken {
			name = toClaudeCodeName(name)
		}
		out[i] = anthTool{
			Name:             name,
			Description:      tool.Description,
			Strict:           strict,
			InputSchema:      schema,
			inputSchemaOrder: schemaOrder,
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

// anthInputSchema builds a tool's input_schema and its member order as convertTools does (anthropic-messages.ts:1588-1600): the legacy
// literal { type: "object", properties: schema.properties ?? {}, required: schema.required ?? [] }, which a strict schema follows
// in a spread, so the literal's members overwrite the parameters' in place and append the ones the parameters lack.
func anthInputSchema(parameters map[string]any, parameterOrder schemaObjectOrder, strict bool) (map[string]any, schemaObjectOrder) {
	properties, required := parameters["properties"], parameters["required"]
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	order := schemaObjectOrder{}
	var schema map[string]any
	if strict {
		schema = maps.Clone(parameters)
		root := orderedSchemaKeys(parameters, parameterOrder, "")
		for _, key := range anthLegacyInputSchemaOrder {
			if _, ok := parameters[key]; !ok {
				root = append(root, key)
			}
		}
		order[""] = root
	} else {
		schema = make(map[string]any, len(anthLegacyInputSchemaOrder))
		order[""] = anthLegacyInputSchemaOrder
	}
	schema["type"], schema["properties"], schema["required"] = "object", properties, required
	for path, members := range parameterOrder {
		if path == "" || !strict && path != "/properties" && !strings.HasPrefix(path, "/properties/") {
			continue
		}
		order[path] = members
	}
	return schema, order
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

// mapThinkingLevelToEffort maps a ModelThinkingLevel to an Anthropic effort string
// for adaptive thinking models. Uses thinkingLevelMap when present.
// Mirrors upstream anthropic.ts mapThinkingLevelToEffort.
func mapThinkingLevelToEffort(model *Model, level ModelThinkingLevel) AnthropicEffort {
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[ModelThinkingLevel(level)]; ok && mapped != nil {
			return AnthropicEffort(*mapped)
		}
	}
	switch level {
	case ThinkingMinimal, ThinkingLow:
		return AnthropicEffortLow
	case ThinkingMedium:
		return AnthropicEffortMedium
	case ThinkingHigh:
		return AnthropicEffortHigh
	default:
		return AnthropicEffortHigh
	}
}

// anthropicActiveEffort is the effort streamSimple hands buildParams for a reasoning level (mapThinkingLevelToEffort, unfiltered), and "high" when there is none, which is buildParams' default.
func anthropicActiveEffort(model *Model, level ModelThinkingLevel) AnthropicEffort {
	if level == "" || level == ThinkingOff {
		return AnthropicEffortHigh
	}
	return mapThinkingLevelToEffort(model, level)
}

// anthThinkingResult holds both the thinking config and optional output_config
// for the Anthropic request, plus an adjusted MaxTokens that accounts for
// the thinking budget. Mirrors upstream adjustMaxTokensForThinking.
type anthThinkingResult struct {
	Thinking     *anthThinking
	OutputConfig *anthOutputConfig
	MaxTokens    int  // adjusted max_tokens; 0 = no change
	floored      bool // the budget was zero and became 1024
}

// thinkingToAnthropicConfig builds the thinking field for the Anthropic request.
// Returns nil when thinking should be omitted.
// For adaptive-thinking models, returns type "adaptive"
// with effort via output_config. For older models, returns type "enabled" with
// budget_tokens. Mirrors upstream anthropic.ts buildParams thinking config.
func thinkingToAnthropicConfig(model *Model, maxTokens int, level ModelThinkingLevel, custom *ThinkingBudgets) *anthThinkingResult {
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

	// Budget-based thinking for older models: thinkingBudgetForLevel(level, options.thinkingBudgets), where xhigh and max use the high budget.
	// upstream: packages/ai/src/api/simple-options.ts:thinkingBudgetForLevel
	// streamSimple passes options.reasoning to it as given; the caller's level is clamped to the model before it reaches the API module.
	budget := ThinkingBudgetForLevel(string(level), custom)

	// Upstream: maxTokens = min(baseMaxTokens + thinkingBudget, modelMaxTokens)
	// Then: if maxTokens <= thinkingBudget, thinkingBudget = clampThinkingBudgetToAnswerRoom(thinkingBudget, maxTokens)
	modelMax := maxTokens // fallback if no model cap
	if model != nil && model.Capabilities.MaxOutputTokens > 0 {
		modelMax = model.Capabilities.MaxOutputTokens
	}
	adjustedMax := min(maxTokens+budget, modelMax)
	if adjustedMax <= budget {
		budget = ClampThinkingBudgetToAnswerRoom(budget, adjustedMax)
	}
	// buildParams applies thinkingBudgetTokens || 1024 after simple-option lowering and the answer-room clamp: a zero budget (a configured zero included) becomes 1024 and is not lowered again by the context clamp.
	floored := budget == 0
	if floored {
		budget = 1024
	}
	return &anthThinkingResult{
		Thinking:  &anthThinking{Type: "enabled", BudgetTokens: budget, Display: "summarized"},
		MaxTokens: adjustedMax,
		floored:   floored,
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
	activeEffort    AnthropicEffort
	// providerThinkingLevel is the effort of the final effort marker, recorded on the reply so later requests can replay it: the active effort of a
	// managed-effort model unless thinking is disabled, and empty otherwise (buildParams' providerThinkingLevel).
	providerThinkingLevel AnthropicEffort
	// effortMarkers reports that the request sends the mid-conversation output_config markers of activeEffort.
	effortMarkers bool
	// disablePreferredStrictTools sends every "prefer" tool without strict: the request the strict tool fallback sends again.
	disablePreferredStrictTools bool
	// preferredStrict records that a tool conversion of this request, the request tools or a tool_addition block's, sent a "prefer" tool strict
	// (hasPreferredStrictTools). It is shared by the copies of params.
	preferredStrict *bool
}

// convertToolDefinitions is buildParams' convertToolDefinitions (anthropic-messages.ts:1175-1193): convertTools, recording a strict "prefer" tool.
func (params anthropicParams) convertToolDefinitions(tools []ToolSchema, isOAuthToken, supportsEager, supportsStrict bool, cacheControl *anthCacheControl) ([]anthTool, error) {
	definitions, err := anthConvertTools(tools, isOAuthToken, supportsEager, supportsStrict, cacheControl, params.disablePreferredStrictTools)
	if err != nil {
		return nil, err
	}
	if params.preferredStrict != nil && anthropicMarksPreferredStrict(tools, definitions) {
		*params.preferredStrict = true
	}
	return definitions, nil
}

func (params anthropicParams) convertMessages(messages []Message, isOAuthToken bool) ([]anthMessage, error) {
	converted, err := anthConvertMessagesDetailed(messages, isOAuthToken, params.allowEmptySignature, params.managedProvider, params.toolDefinitions)
	if err != nil {
		return nil, err
	}
	applyConversationCacheControl(converted.messages, params.cacheControl)
	if params.effortMarkers {
		return insertAnthropicThinkingLevelMessages(converted, params.activeEffort), nil
	}
	return converted.messages, nil
}

// Stream retains the assistant identity and active effort when request preparation fails, and terminates with an error event before generation starts. The request, its response and the body read run behind the returned stream, as upstream's async IIFE does; only the synchronous prefix of that IIFE runs before Stream returns.
func (p *anthropicProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	ctx = withProviderRequestOptions(ctx, opts)
	// pig divergence (D48): the agent loop asks for the tool-flow repair.
	if opts.RepairToolFlow {
		transcript = transcript.withRepairedToolFlow()
	}
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("anthropic: invalid transcript: %w", err)
	}
	model := p.resolveModel()
	// upstream: packages/ai/src/api/anthropic-messages.ts:stream
	builder := newObservedProviderBuilder(ctx, outputAPI(p.cfg.ModelMetadata, APIAnthropicMessages), p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	builder.setProviderEventObserver(opts, model)
	// buildParams records the effort of the final effort marker on the reply once it has built the request (anthropic-messages.ts:647), so a failure
	// before that leaves no providerThinkingLevel.
	recordLevel := func(level AnthropicEffort) { builder.setResponseMetadata("", "", "", string(level), nil) }
	request, err := p.prepareStream(ctx, transcript, opts, model, recordLevel)
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
	first *anthropicOutgoing
	// withoutStrict prepares the request again with every "prefer" tool sent non-strict; nil when no "prefer" tool was sent strict.
	withoutStrict func() (*anthropicOutgoing, error)
}

// anthropicOutgoing is a prepared request: the HTTP form that the provider's own client sends, and the body and betas an injected AnthropicClient receives.
type anthropicOutgoing struct {
	http   *http.Request
	body   *requestBody
	betas  *string
	params anthropicParamHeaders
}

func (p *anthropicProvider) prepareStream(ctx context.Context, transcript TranscriptContext, opts StreamOptions, model *Model, recordLevel func(AnthropicEffort)) (*anthropicStreamRequest, error) {
	// An injected client owns its endpoint and credentials (anthropic-messages.ts:607-609).
	var baseURL, apiKey string
	if opts.AnthropicClient == nil {
		var err error
		if baseURL, apiKey, err = p.resolveEndpoint(ctx); err != nil {
			return nil, err
		}
	}
	requestEnv := mergeProviderEnv(p.cfg.Env, opts.Env)
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
	// upstream: packages/ai/src/api/anthropic-messages.ts:607-609 (options.client skips client construction, federation and auth assertion; isOAuth is false) and :614-615 (getAnthropicFederation, assertRequestAuth)
	var lease *anthropicFederationLease
	var client anthropicClient
	if opts.AnthropicClient != nil {
		client = anthropicClient{injected: opts.AnthropicClient}
	} else {
		if federation := getAnthropicFederation(p.cfg.ProviderID, apiKey, optionsHeaders, requestEnv); federation != nil {
			lease = acquireAnthropicFederationClient(baseURL, *federation, opts.Fetch)
		} else if !anthropicHasRequestAuth(apiKey, optionsHeaders) {
			return nil, fmt.Errorf("No API key for provider: %s", p.cfg.ProviderID)
		}
		client = p.createClient(apiKey, opts.Identity, optionsHeaders, dynamicHeaders, p.sessionAffinityHeaders(opts.SessionID, resolveAnthropicCacheRetention(opts.CacheRetention, requestEnv)), lease)
	}
	// anthropic-messages.ts:647 options?.signal?.throwIfAborted() at the top of the request loop, after the client and auth checks: an
	// aborted request ends with the signal's reason ("This operation was aborted" for a plain abort()), not retryProviderRequest's.
	if ctx.Err() != nil {
		lease.release()
		return nil, AbortReason(ctx)
	}
	params, err := p.buildParams(model, transcript, client.isOAuthToken, modelHeaders, optionsHeaders, opts, requestEnv, false)
	if err != nil {
		lease.release()
		return nil, fmt.Errorf("anthropic: build params: %w", err)
	}
	recordLevel(params.providerThinkingLevel)
	prepared := &anthropicStreamRequest{baseURL: baseURL, client: client, params: params}
	prepared.first, err = p.prepareRequest(ctx, baseURL, client, params.request, model, opts)
	if err != nil {
		lease.release()
		return nil, err
	}
	if *params.preferredStrict {
		// upstream: anthropic-messages.ts:stream (buildParams with disablePreferredStrictTools in the request loop, :644-689)
		prepared.withoutStrict = func() (*anthropicOutgoing, error) {
			retryParams, err := p.buildParams(model, transcript, client.isOAuthToken, modelHeaders, optionsHeaders, opts, requestEnv, true)
			if err != nil {
				return nil, err
			}
			recordLevel(retryParams.providerThinkingLevel)
			prepared.params = retryParams
			return p.prepareRequest(ctx, baseURL, client, retryParams.request, model, opts)
		}
	}
	return prepared, nil
}

// send authorizes a prepared request with the federated token when the client has one, sends it, and invalidates the token on a 401.
func (p *anthropicProvider) send(ctx, requestContext context.Context, client anthropicClient, outgoing *anthropicOutgoing, opts StreamOptions) (*http.Response, error) {
	httpRequest := outgoing.http.WithContext(requestContext)
	if client.injected != nil {
		// upstream: anthropic-messages.ts:650-658 retryProviderRequest around client.beta.messages.create(params, requestOptions).asResponse()
		return RetryProviderRequest(requestContext, func() (*http.Response, error) {
			return client.injected.CreateMessage(requestContext, AnthropicCreateParams{Body: outgoing.body.Bytes(), Betas: outgoing.betas, UserProfileID: outgoing.params.userProfileID, WorkspaceID: outgoing.params.workspaceID}, AnthropicRequestOptions{TimeoutMs: opts.TimeoutMs})
		}, ProviderRetryOptions{MaxRetries: opts.MaxRetries, MaxRetryDelayMs: opts.MaxRetryDelayMs})
	}
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
	resp, err := p.send(ctx, requestContext, request.client, request.first, opts)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := readWebText(resp.Body)
		_ = resp.Body.Close()
		// pig divergence (D37): retry once without rejected thinking signatures.
		var retry *anthropicOutgoing
		strictLimitMessage, strictLimitsExceeded := anthropicStrictToolLimitsExceeded(resp.StatusCode, b)
		switch {
		case request.withoutStrict != nil && strictLimitsExceeded && ctx.Err() == nil:
			// upstream: anthropic-messages.ts:stream (a rejected strict tool set is sent once more with "prefer" tools non-strict, :664-689)
			AppendAssistantMessageDiagnostic(builder.partial, AssistantMessageDiagnostic{
				Type:      "anthropic_strict_tools_fallback",
				Timestamp: time.Now().UnixMilli(), //portlint:allow clock anthropic-messages.ts:684 `timestamp: Date.now()`; the package's clock seams belong to OAuth and backoff structs, not to diagnostics
				Error:     &DiagnosticErrorInfo{Message: strictLimitMessage},
				Details:   map[string]any{"reason": "strict_tool_limits_exceeded"},
			})
			retry, err = request.withoutStrict()
		case resp.StatusCode == http.StatusBadRequest && isThinkingSignatureError(b):
			request.params.request.Messages, err = request.params.convertMessages(stripThinkingSignatures(request.params.conversation), request.client.isOAuthToken)
			if err != nil {
				return err
			}
			retry, err = p.prepareRequest(ctx, request.baseURL, request.client, request.params.request, model, opts)
		default:
			return anthHTTPError(resp.StatusCode, b)
		}
		if err != nil {
			return err
		}
		resp, err = p.send(ctx, requestContext, request.client, retry, opts)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			b2, _ := readWebText(resp.Body)
			_ = resp.Body.Close()
			return anthHTTPError(resp.StatusCode, b2)
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

// anthropicThinkingDisplay defaults to "summarized" like options?.thinkingDisplay ?? "summarized" (anthropic-messages.ts:1238,1246).
func anthropicThinkingDisplay(opts StreamOptions) AnthropicThinkingDisplay {
	if opts.ThinkingDisplay == "" {
		return AnthropicThinkingDisplaySummarized
	}
	return opts.ThinkingDisplay
}

func anthropicProviderEffort(model *Model, opts StreamOptions) AnthropicEffort {
	if !anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoEffort }) {
		return ""
	}
	if opts.Effort != "" {
		return opts.Effort
	}
	// streamSimple derives the effort from its reasoning level only for a forceAdaptiveThinking model; any other request leaves it unset, so buildParams uses "high".
	if !modelUsesAdaptiveThinking(model) {
		return AnthropicEffortHigh
	}
	return anthropicActiveEffort(model, ModelThinkingLevel(opts.Thinking))
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
		return params.convertToolDefinitions(params.tools, isOAuthToken, supportsEager, supportsStrict, cacheControl)
	}
	// Initial tools stay active with the cache breakpoint on the last one,
	// followed by the placeholder. The list never changes afterwards: later
	// tools are defined by value in tool_addition blocks and withdrawn by
	// tool_removal, so the cached prefix survives every tool change.
	tools, err := params.convertToolDefinitions(initialTools, isOAuthToken, supportsEager, supportsStrict, cacheControl)
	if err != nil {
		return nil, err
	}
	return append(tools, deferredToolPlaceholder()), nil
}

// buildParams mirrors upstream buildParams for the request fields PiG sends. disablePreferredStrictTools is upstream's parameter of that name.
func (p *anthropicProvider) buildParams(model *Model, transcript TranscriptContext, isOAuthToken bool, modelHeaders, optionsHeaders anthropicHeaders, opts StreamOptions, env ProviderEnv, disablePreferredStrictTools bool) (anthropicParams, error) {
	supportsMidConversation := anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoSystemMessages })
	// upstream: packages/ai/src/api/anthropic-messages.ts:buildParams
	resolved := resolvedMessages(transcript, supportsMidConversation)
	messages := TransformMessages(resolved, model, func(id string, _ *Model, _ AssistantMessage) string {
		return normalizeAnthropicToolCallID(id)
	})
	afterInitial := messages
	systemPrompt := ""
	var initialTools []ToolSchema
	// The initial system message is the transcript's first message as given, not as transformMessages leaves it (it drops an errored or aborted assistant turn ahead of a mid-conversation system message); one message is sliced off the transformed list when there is one.
	if initialSystem := borrowedInitialSystemMessage(resolved); initialSystem != nil {
		systemPrompt = GetCurrentSystemPrompt([]Message{*initialSystem})
		initialTools = initialSystem.ToolsAdded
		afterInitial = afterInitial[min(1, len(afterInitial)):]
	}
	// Native tool changes keep the request-level tool list fixed and define
	// every later tool by value in a tool_addition block, which also expresses
	// same-name redefinitions. Anthropic rejects a tool list where every tool is
	// deferred, so an initial active tool must anchor the placeholder.
	params := anthropicParams{
		conversation:        afterInitial,
		tools:               borrowedCurrentTools(messages),
		cacheControl:        p.cacheControl(resolveAnthropicCacheRetention(opts.CacheRetention, env)),
		allowEmptySignature: modelAllowsEmptySignature(model),
		nativeToolChanges: supportsMidConversation &&
			anthropicCompatFlag(model, func(c *ModelCompat) *bool { return c.SupportsMidConvoToolChanges }) &&
			len(initialTools) > 0,
	}
	params.disablePreferredStrictTools, params.preferredStrict = disablePreferredStrictTools, new(bool)
	params.activeEffort = anthropicProviderEffort(model, opts)
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
			return params.convertToolDefinitions(tools, isOAuthToken, supportsEager, supportsStrictTools, nil)
		}
	}
	thinkingEnabled := opts.Thinking != ""
	if opts.ThinkingEnabled != nil {
		thinkingEnabled = *opts.ThinkingEnabled
	}
	// Effort cannot change between messages while thinking is disabled, so "off" sends no effort markers (anthropic-messages.ts:1201-1203). Off is
	// explicit: `options?.thinkingEnabled === false`, or the "off" level of streamSimple; an unset level is neither.
	explicitOff := opts.ThinkingEnabled != nil && !*opts.ThinkingEnabled || opts.ThinkingEnabled == nil && ModelThinkingLevel(opts.Thinking) == ThinkingOff
	thinkingDisabled := model.ProviderMeta.Reasoning && explicitOff && !anthThinkingOffIsNull(model)
	effortMessages := params.activeEffort != "" && !thinkingDisabled
	if effortMessages {
		params.managedProvider = p.cfg.ProviderID
		params.providerThinkingLevel = params.activeEffort
	}
	var allowedFallbackModels []AnthropicAllowedFallbackModel
	if compat := model.ProviderMeta.Compat; compat != nil {
		allowedFallbackModels = compat.AllowedFallbackModels
	}
	betas := getBetaFeatures(modelHeaders, optionsHeaders, anthropicBetaInputs{
		isOAuthToken:                    isOAuthToken,
		hasTools:                        len(params.tools) > 0,
		supportsEagerToolInputStreaming: supportsEager,
		reasoning:                       model.ProviderMeta.Reasoning,
		thinkingEnabled:                 thinkingEnabled,
		interleavedThinking:             opts.InterleavedThinking == nil || *opts.InterleavedThinking,
		forceAdaptiveThinking:           modelUsesAdaptiveThinking(model),
		hasFallbacks:                    len(allowedFallbackModels) > 0,
		supportsMidConvoEffort:          params.activeEffort != "",
		nativeToolChanges:               params.nativeToolChanges,
	})
	// pig divergence (D130): Pi #10759. An explicit anthropic-beta header can drop the managed-effort betas, so each managed body field is sent only with its beta: the mid-conversation output_config markers with mid-conversation-output-config-2026-07-01 and block_binding with thinking-binding-controls-2026-08-01 (applySampling). Pi sends both regardless and the API rejects the request with 400.
	params.effortMarkers = effortMessages && slices.Contains(betas, midConversationEffortBeta)
	conversation, err := params.convertMessages(params.conversation, isOAuthToken)
	if err != nil {
		return anthropicParams{}, err
	}
	req := anthRequest{
		Model:     p.cfg.Model,
		Messages:  conversation,
		MaxTokens: maxTokens,
		Stream:    true,
		Betas:     betas,
		System:    anthSystemBlocks(systemPrompt, isOAuthToken, params.cacheControl),
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
	if toolChoice, ok := toolChoiceName(opts.ToolChoice); ok {
		// anthropic-messages.ts:1273 `if (options?.toolChoice)`: an empty string is falsy.
		if toolChoice != "" {
			req.ToolChoice = map[string]any{"type": toolChoice}
		}
	} else if opts.ToolChoice != nil {
		req.ToolChoice = opts.ToolChoice
	}
	if userID, ok := opts.Metadata["user_id"].(string); ok {
		req.Metadata = &anthMetadata{UserID: userID}
	}
	p.applySampling(&req, model, transcript, maxTokens, thinkingEnabled, ModelThinkingLevel(opts.Thinking), opts.ThinkingBudgets, opts.Temperature, opts.TemperatureSet || opts.Temperature != 0, params.activeEffort, params.effortMarkers, thinkingDisabled, anthropicThinkingDisplay(opts))
	if opts.ThinkingEnabled != nil && model.ProviderMeta.Reasoning && params.activeEffort == "" {
		req.Thinking, req.OutputConfig, req.MaxTokens = nil, nil, maxTokens
		if *opts.ThinkingEnabled {
			if modelUsesAdaptiveThinking(model) {
				req.Thinking = &anthThinking{Type: "adaptive", Display: string(anthropicThinkingDisplay(opts))}
				if opts.Effort != "" {
					req.OutputConfig = &anthOutputConfig{Effort: opts.Effort}
				}
			} else {
				budget := 1024
				if opts.ThinkingBudgetTokens != nil && *opts.ThinkingBudgetTokens != 0 {
					budget = *opts.ThinkingBudgetTokens
				}
				req.Thinking = &anthThinking{Type: "enabled", BudgetTokens: budget, Display: string(anthropicThinkingDisplay(opts))}
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
func (p *anthropicProvider) applySampling(req *anthRequest, model *Model, transcript TranscriptContext, maxTokens int, thinkingEnabled bool, level ModelThinkingLevel, budgets *ThinkingBudgets, temperature float64, temperatureSet bool, managedEffort AnthropicEffort, effortMarkers, thinkingDisabled bool, display AnthropicThinkingDisplay) {
	if managedEffort != "" {
		if thinkingDisabled {
			// No effort is sent, so the model default applies. Anthropic rejects block_binding with disabled thinking.
			req.Thinking = &anthThinking{Type: "disabled"}
		} else {
			req.Thinking = &anthThinking{Type: "adaptive", Display: string(display)}
			// pig divergence (D130): block_binding goes only with its beta, and without the mid-conversation markers the active effort is the top-level effort.
			if slices.Contains(req.Betas, thinkingBindingControlsBeta) {
				req.Thinking.BlockBinding = &anthBlockBinding{PrefixMismatchBehavior: "drop_block"}
			}
			req.OutputConfig = &anthOutputConfig{Effort: "high"}
			if !effortMarkers {
				req.OutputConfig.Effort = managedEffort
			}
		}
		// streamSimple adjusts max_tokens for a budget-based level whatever buildParams then sends as thinking, and a managed request carries no temperature.
		if result := thinkingToAnthropicConfig(model, maxTokens, level, budgets); result != nil && result.MaxTokens > 0 {
			req.MaxTokens = ClampMaxTokensToContext(model, transcript, result.MaxTokens)
		}
		return
	}
	if thinkingResult := thinkingToAnthropicConfig(model, maxTokens, level, budgets); thinkingResult != nil {
		// buildParams adds thinking and output_config only for a reasoning model; streamSimple's max_tokens adjustment does not depend on it.
		if model.ProviderMeta.Reasoning {
			req.Thinking = thinkingResult.Thinking
			req.OutputConfig = thinkingResult.OutputConfig
		}
		// Budget-based thinking may adjust max_tokens upward to
		// accommodate the budget (upstream adjustMaxTokensForThinking).
		if thinkingResult.MaxTokens > 0 {
			// streamSimple: maxTokens = clampMaxTokensToContext(...); budget = min(adjusted.thinkingBudget, max(0, maxTokens - 1024)).
			adjusted := ClampMaxTokensToContext(model, transcript, thinkingResult.MaxTokens)
			req.MaxTokens = adjusted
			if req.Thinking != nil && req.Thinking.Type == "enabled" && !thinkingResult.floored {
				// buildParams: `options.thinkingBudgetTokens || 1024`
				budget := min(req.Thinking.BudgetTokens, max(0, adjusted-1024))
				if budget == 0 {
					budget = 1024
				}
				req.Thinking.BudgetTokens = budget
			}
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
func (p *anthropicProvider) prepareRequest(ctx context.Context, baseURL string, client anthropicClient, req anthRequest, model *Model, opts StreamOptions) (*anthropicOutgoing, error) {
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
	var betas *string
	var params anthropicParamHeaders
	var body *requestBody
	var err error
	if request, own := payload.(anthRequest); own {
		// The provider's own request is written as it is, so the encoder takes each large string's encoding from the cache as it meets the string. Its betas become the header and are not in the body, which is the request that is encoded.
		request, betas = request.wire()
		body, err = marshalSpliced(request, requestStrings, marshalRequestBody)
	} else {
		// A replacement payload is read by anthropicWireBody (its betas become the header), so it is not spliced.
		body, err = marshalSpliced(payload, nil, func(payload any) ([]byte, error) {
			wire, header, err := anthropicWireBody(payload)
			betas = header
			if err != nil {
				return wire, err
			}
			wire, params, err = anthropicSDKParams(wire)
			return wire, err
		})
	}
	if sdkError, ok := errors.AsType[*anthropicSDKError](err); ok {
		return nil, sdkError
	}
	if err != nil {
		return nil, fmt.Errorf(p.cfg.ProviderID+": marshal request: %w", err)
	}
	checkRequestSegments(ctx, APIAnthropicMessages, body)
	// Upstream calls client.beta.messages.create (anthropic-messages.ts:1065,
	// 588), which the Anthropic SDK's beta namespace routes to
	// /v1/messages?beta=true rather than /v1/messages. This is the single
	// send path for every Anthropic-flavored caller: direct API key, OAuth,
	// and the github-copilot Anthropic proxy (NewCopilotProvider's
	// APIAnthropicMessages branch), so all three inherit the fix here.
	if client.injected != nil {
		// An injected client owns its endpoint: the request only carries the body to it.
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages?beta=true", body.reader())
		if err != nil {
			return nil, err
		}
		body.prepare(httpReq)
		return &anthropicOutgoing{http: httpReq, body: body, betas: betas, params: params}, nil
	}
	endpoint, err := sdkBuildURL(baseURL, "/v1/messages?beta=true")
	if err != nil {
		return nil, err
	}
	httpReq, err := newSDKFetchRequest(ctx, http.MethodPost, endpoint, body.reader(), opts.Fetch != nil)
	if err != nil {
		return nil, err
	}
	body.prepare(httpReq)
	applyAnthropicRequestHeaders(httpReq, client, betas, params)
	return &anthropicOutgoing{http: httpReq, body: body, betas: betas, params: params}, nil
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
			// retryProviderRequest throws validateServerRetryDelayMs's error with the SDK status error's own message appended.
			if exceeded, ok := errors.AsType[*providerRetryDelayError](err); ok && exceeded.status != 0 {
				if sdkErr, ok := errors.AsType[*providerError](anthHTTPError(exceeded.status, exceeded.body)); ok {
					return nil, &nodeTransportError{message: exceeded.head + ". " + sdkErr.message, cause: err}
				}
			}
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
func decodeAnthropicEvent(sse anthropicStreamEvent, target any) error {
	if err := json.Unmarshal(jsjson.NormalizeIntegers([]byte(sse.JSON)), target); err != nil {
		return &anthropicSSEParseError{sse: sse.serverSentEvent, cause: err}
	}
	return nil
}

// decodeAnthropicDeltaEvent decodes a content_block_delta. A record with a member of an unexpected type takes a second pass that reads the members as
// anthropic-messages.ts:737-770 does: an index that is not a number matches no block, a delta type that is not a string matches no case, and the text, thinking,
// partial_json and signature members are appended with `+=`, so a value that is not a string appends its JS String() form, and a null or absent member appends
// "null" or "undefined". A null or absent delta decodes as an empty delta, for which the handler throws Pi's TypeError reading `event.delta.type`.
func decodeAnthropicDeltaEvent(sse anthropicStreamEvent, ev *anthEventContentBlockDelta) error {
	if fast, ok := decodeAnthropicDeltaFast(sse.JSON); ok {
		*ev = fast
		return nil
	}
	return decodeAnthropicDeltaEventReflect(sse, ev)
}

// decodeAnthropicDeltaEventReflect is the decode with json.Unmarshal and the second look, for every record the fast decode leaves.
func decodeAnthropicDeltaEventReflect(sse anthropicStreamEvent, ev *anthEventContentBlockDelta) error {
	text := sse.JSON
	err := json.Unmarshal([]byte(text), ev)
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		if coerced, ok := coerceAnthropicDelta([]byte(text)); ok {
			*ev, err = coerced, nil
		}
	} else if err == nil {
		// The typed decode reads a null or absent member as "", where `+=` appends "null" or "undefined": only an empty member takes the second look.
		if member := anthropicDeltaMember(ev); member != nil && *member == "" {
			if coerced, ok := coerceAnthropicDelta([]byte(text)); ok {
				*ev = coerced
			}
		}
	}
	if err != nil {
		return &anthropicSSEParseError{sse: sse.serverSentEvent, cause: err}
	}
	return nil
}

func coerceAnthropicDelta(data []byte) (anthEventContentBlockDelta, bool) {
	var ev anthEventContentBlockDelta
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ev, false
	}
	record, isObject := value.(map[string]any)
	if !isObject {
		return ev, false
	}
	if index, isNumber := record["index"].(json.Number); isNumber {
		number, err := index.Int64()
		if err != nil {
			return ev, false
		}
		ev.Index = anthBlockIndex{value: int(number), present: true}
	}
	delta, present := record["delta"]
	if !present || delta == nil {
		// The handler throws Pi's TypeError for reading `event.delta.type`.
		return ev, true
	}
	fields, isObject := delta.(map[string]any)
	if !isObject {
		return ev, true
	}
	if kind, isString := fields["type"].(string); isString {
		ev.Delta.Type = kind
	}
	for key, target := range map[string]*string{"text": &ev.Delta.Text, "thinking": &ev.Delta.Thinking, "partial_json": &ev.Delta.PartialJSON, "signature": &ev.Delta.Signature} {
		if member, present := fields[key]; present && member != nil {
			*target = jsToString(member)
		}
	}
	// The member the delta type appends is read by `+=` even when it is null or absent: String(null) and String(undefined).
	if member := anthropicDeltaMember(&ev); member != nil {
		switch value, present := fields[anthropicDeltaMemberKey[ev.Delta.Type]]; {
		case !present:
			*member = "undefined"
		case value == nil:
			*member = "null"
		}
	}
	return ev, true
}

// anthropicDeltaMemberKey names the member each content_block_delta type appends (anthropic-messages.ts:737-770).
var anthropicDeltaMemberKey = map[string]string{"text_delta": "text", "thinking_delta": "thinking", "input_json_delta": "partial_json", "signature_delta": "signature"}

// anthropicDeltaMember is the field that holds the member the delta's type appends, or nil for a type no case matches.
func anthropicDeltaMember(ev *anthEventContentBlockDelta) *string {
	switch ev.Delta.Type {
	case "text_delta":
		return &ev.Delta.Text
	case "thinking_delta":
		return &ev.Delta.Thinking
	case "input_json_delta":
		return &ev.Delta.PartialJSON
	case "signature_delta":
		return &ev.Delta.Signature
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
		// The events reader already validated (and repaired) the payload; the parsed value's type selects the branch (anthropic-messages.ts:665-815).
		var observeErr error
		if sse.valid {
			observeErr = builder.observeValidProviderEvent(sse.JSON)
		} else {
			observeErr = builder.observeProviderEvent([]byte(sse.JSON))
		}
		if err := observeErr; err != nil {
			throw(err)
			return
		}
		switch sse.Type {
		case "message_start":
			var ev anthEventMessageStart
			js, err := decodeAnthropicEventJS(sse, &ev)
			if err != nil {
				throw(err)
				return
			}
			var usageReadErr error
			if ev.Message.ID == "" || ev.Message.Usage.InputTokens == 0 && ev.Message.Usage.OutputTokens == 0 {
				// A record with no id or no token counts may lack the message or its usage, which Pi reads as `event.message.id` and `event.message.usage.x`.
				var idReadErr error
				if idReadErr, usageReadErr = anthropicMessageStartReadErrors(sse.JSON); idReadErr != nil {
					throw(idReadErr)
					return
				}
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
			if js.responseModel != "" {
				// A model that is not a string matches no fallback model, so the usage keeps the requested model's cost.
				output.ResponseModel = js.responseModel
			}
			if usageReadErr != nil {
				builder.touch()
				throw(usageReadErr)
				return
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
			usage.splitsAfterCost = true
			// anthropic-messages.ts:680-684 reads no output_tokens_details here; only message_delta sets reasoning.
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
			calculateUsageCost(usageCost, usage)
			builder.touch()

		case "content_block_start":
			var ev anthEventContentBlockStart
			if _, err := decodeAnthropicEventJS(sse, &ev); err != nil {
				throw(err)
				return
			}
			if ev.ContentBlock.Type == "" {
				if err := jsReadError(anthropicRecord(sse.JSON), "content_block", "type"); err != nil {
					throw(err)
					return
				}
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
			if err := decodeAnthropicDeltaEvent(sse, &ev); err != nil {
				throw(err)
				return
			}
			if ev.Delta.Type == "" {
				// `event.delta.type` is read before the block is looked up.
				if err := jsReadError(anthropicRecord(sse.JSON), "delta", "type"); err != nil {
					throw(err)
					return
				}
			}
			index := -1
			if ev.Index.present {
				index = findAnthropicBlock(output.Content, ev.Index.value)
			}
			if index < 0 {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if block, ok := output.Content[index].(TextContent); ok {
					block.Text = builder.appendContent(index, block.Text, ev.Delta.Text)
					output.Content[index] = block
					builder.push(TextDeltaEvent{ContentIndex: index, Delta: ev.Delta.Text, Partial: output})
				}
			case "thinking_delta":
				if block, ok := output.Content[index].(ThinkingContent); ok {
					block.Thinking = builder.appendContent(index, block.Thinking, ev.Delta.Thinking)
					output.Content[index] = block
					builder.push(ThinkingDeltaEvent{ContentIndex: index, Delta: ev.Delta.Thinking, Partial: output})
				}
			case "input_json_delta":
				if block, ok := output.Content[index].(ToolCall); ok {
					block.scratch.partialJson = builder.appendArguments(index, block.scratch.partialJson, ev.Delta.PartialJSON)
					block.setParsedArguments(builder.streamingArguments(index, block.scratch.partialJson))
					output.Content[index] = block
					builder.push(ToolCallDeltaEvent{ContentIndex: index, Delta: ev.Delta.PartialJSON, Partial: output})
				}
			case "signature_delta":
				if block, ok := output.Content[index].(ThinkingContent); ok {
					block.ThinkingSignature = builder.appendSignature(index, block.ThinkingSignature, ev.Delta.Signature)
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
				builder.release(index)
				builder.push(TextEndEvent{ContentIndex: index, Content: block.Text, Partial: output})
			case ThinkingContent:
				block.scratch = ""
				output.Content[index] = block
				builder.release(index)
				builder.push(ThinkingEndEvent{ContentIndex: index, Content: block.Thinking, Partial: output})
			case ToolCall:
				// Finalize in place and strip the scratch buffer so replay only carries parsed arguments.
				block.SetFinalArguments(block.scratch.partialJson)
				block.scratch = toolCallScratch{}
				output.Content[index] = block
				builder.release(index)
				builder.push(ToolCallEndEvent{ContentIndex: index, ToolCall: block, Partial: output})
			}

		case "message_delta":
			var ev anthEventMessageDelta
			js, err := decodeAnthropicEventJS(sse, &ev)
			if err != nil {
				throw(err)
				return
			}
			if transformations, ok := ev.InputTransformations.([]any); ok {
				inputTransformations = transformations
			}
			if ev.Delta.StopReason == "" && !js.unhandledStopReason {
				if err := jsReadError(anthropicRecord(sse.JSON), "delta", "stop_reason"); err != nil {
					throw(err)
					return
				}
			}
			if js.unhandledStopReason {
				output.RawStopReason = js.stopReasonText
				builder.touch()
				throw(fmt.Errorf("Unhandled stop reason: %s", js.stopReasonText))
				return
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
				usage.splitsAfterCost = true
			}
			if ev.Usage.OutputTokensDetails != nil && ev.Usage.OutputTokensDetails.ThinkingTokens != nil {
				usage.Reasoning = new(*ev.Usage.OutputTokensDetails.ThinkingTokens)
				usage.splitsAfterCost = true
			}
			usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
			calculateUsageCost(usageCost, usage)
			if isOpenRouterModel(p.cfg.ProviderID, p.cfg.BaseURL) {
				applyOpenRouterReportedCost(usage, ev.Usage.openRouterUsageAccounting)
			}
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
		transformations := make([]any, len(inputTransformations))
		order := make([]any, len(inputTransformations))
		for i, value := range inputTransformations {
			if value == nil {
				fail(errors.New("Cannot read properties of null (reading 'type')"))
				return
			}
			input, _ := value.(map[string]any)
			transformation := map[string]any{}
			members := orderedjson.New()
			for _, key := range []string{"type", "path", "reason"} {
				if value := input[key]; value != nil {
					transformation[key] = value
					_ = members.SetValue(key, value)
				}
			}
			transformations[i] = transformation
			order[i] = members
		}
		orderText, _ := json.Marshal(order)
		details := newDiagnosticDetails()
		details.set("transformations", transformations, orderText)
		output.Diagnostics = append(output.Diagnostics, details.attach(AssistantMessageDiagnostic{Type: "anthropic_input_transformations", Timestamp: time.Now().UnixMilli()}))
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

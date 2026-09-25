package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/mistral.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	mistralToolCallIDLength  = 9
	maxMistralErrorBodyChars = 4000
)

// MistralConfig configures the Mistral API provider.
type MistralConfig struct {
	APIKey       string
	Model        string
	ProviderID   string
	BaseURL      string
	ExtraHeaders map[string]string
	// SessionID for x-affinity header (KV-cache reuse).
	SessionID string
	// Reasoning indicates whether the model supports extended reasoning.
	Reasoning bool
}

type mistralProvider struct {
	cfg    MistralConfig
	client *http.Client
}

// NewMistralProvider creates a Provider backed by the Mistral API.
func NewMistralProvider(cfg MistralConfig) Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.mistral.ai/v1"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "mistral"
	}
	return &mistralProvider{cfg: cfg, client: streamingHTTPClientNoRetry()}
}

func (p *mistralProvider) ID() string   { return p.cfg.ProviderID }
func (p *mistralProvider) Close() error { return nil }

func mistralChatCompletionsURL(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if trimmed == "" {
		trimmed = "https://api.mistral.ai/v1"
	}
	if !strings.HasSuffix(trimmed, "/v1") {
		trimmed += "/v1"
	}
	return trimmed + "/chat/completions"
}

// ─── Request wire types ──────────────────────────────────────────────────────

type mistralRequest struct {
	Model           string           `json:"model"`
	Messages        []mistralMessage `json:"messages"`
	Stream          bool             `json:"stream"`
	Tools           []mistralTool    `json:"tools,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	ToolChoice      any              `json:"tool_choice,omitempty"`
	PromptMode      string           `json:"prompt_mode,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

type mistralMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content,omitempty"`    // string | []mistralContentChunk
	ToolCalls  any    `json:"tool_calls,omitempty"` // []mistralToolCallMsg for assistant
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

type mistralContentChunk struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
	Thinking any    `json:"thinking,omitempty"` // []map[string]string for thinking content
}

type mistralToolCallMsg struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function mistralToolCallFnMsg `json:"function"`
}

type mistralToolCallFnMsg struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type mistralTool struct {
	Type     string        `json:"type"`
	Function mistralToolFn `json:"function"`
}

type mistralToolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

// ─── Response wire types ─────────────────────────────────────────────────────

type mistralSSEChunk struct {
	ID      string             `json:"id"`
	Choices []mistralSSEChoice `json:"choices"`
	Usage   *mistralSSEUsage   `json:"usage,omitempty"`
}

type mistralSSEChoice struct {
	Delta        mistralSSEDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type mistralSSEDelta struct {
	Content   json.RawMessage      `json:"content"` // string | []mistralContentItem | null
	ToolCalls []mistralSSEToolCall `json:"tool_calls"`
}

type mistralContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking []struct {
		Text string `json:"text"`
	} `json:"thinking,omitempty"`
}

type mistralSSEToolCall struct {
	ID       string `json:"id"`
	Index    int    `json:"index"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type mistralSSEUsage struct {
	PromptTokens             int                  `json:"prompt_tokens"`
	CompletionTokens         int                  `json:"completion_tokens"`
	TotalTokens              int                  `json:"total_tokens"`
	PromptTokensDetails      *mistralCacheDetails `json:"promptTokensDetails"`
	PromptTokensDetailsSnake *mistralCacheDetails `json:"prompt_tokens_details"`
	PromptTokenDetails       *mistralCacheDetails `json:"promptTokenDetails"`
	PromptTokenDetailsSnake  *mistralCacheDetails `json:"prompt_token_details"`
	NumCachedTokens          *int                 `json:"numCachedTokens"`
	NumCachedTokensSnake     *int                 `json:"num_cached_tokens"`
}

type mistralCacheDetails struct {
	CachedTokens      *int `json:"cachedTokens"`
	CachedTokensSnake *int `json:"cached_tokens"`
}

func (usage mistralSSEUsage) cachedPromptTokens() int {
	var candidates []*int
	for _, details := range []*mistralCacheDetails{usage.PromptTokensDetails, usage.PromptTokensDetailsSnake, usage.PromptTokenDetails, usage.PromptTokenDetailsSnake} {
		if details != nil {
			candidates = append(candidates, details.CachedTokens, details.CachedTokensSnake)
		}
	}
	candidates = append(candidates, usage.NumCachedTokens, usage.NumCachedTokensSnake)
	for _, candidate := range candidates {
		if candidate != nil {
			return min(usage.PromptTokens, max(0, *candidate))
		}
	}
	return 0
}

// ─── ID normalization ────────────────────────────────────────────────────────

func deriveMistralToolCallID(id string, attempt int) string {
	var normalized strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		}
	}
	n := normalized.String()
	if attempt == 0 && len(n) == mistralToolCallIDLength {
		return n
	}
	seedBase := n
	if seedBase == "" {
		seedBase = id
	}
	seed := seedBase
	if attempt > 0 {
		seed = fmt.Sprintf("%s:%d", seedBase, attempt)
	}
	h := shortHash32(seed)
	// Filter to alnum
	var out strings.Builder
	for _, r := range h {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			if out.Len() >= mistralToolCallIDLength {
				break
			}
		}
	}
	return out.String()
}

type mistralIDNormalizer struct {
	forward map[string]string
	reverse map[string]string
}

func newMistralIDNormalizer() *mistralIDNormalizer {
	return &mistralIDNormalizer{
		forward: make(map[string]string),
		reverse: make(map[string]string),
	}
}

func (n *mistralIDNormalizer) normalize(id string) string {
	if existing, ok := n.forward[id]; ok {
		return existing
	}
	for attempt := range 1000 {
		candidate := deriveMistralToolCallID(id, attempt)
		owner, taken := n.reverse[candidate]
		if !taken || owner == id {
			n.forward[id] = candidate
			n.reverse[candidate] = id
			return candidate
		}
		_ = attempt
	}
	// Fallback (should never happen)
	return id
}

// ─── Stream ──────────────────────────────────────────────────────────────────

func (p *mistralProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("mistral: invalid transcript: %w", err)
	}
	apiKey := p.cfg.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for Mistral provider")
	}

	resolved := ResolveTranscript(transcript, false)
	messages := resolved.Messages()
	normalizer := newMistralIDNormalizer()
	msgs := p.convertMessages(WithoutInitialSystemMessage(messages), normalizer)

	if systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))]); systemPrompt != "" {
		system := mistralMessage{Role: "system", Content: sanitizeSurrogates(systemPrompt)}
		msgs = append([]mistralMessage{system}, msgs...)
	}

	req := mistralRequest{
		Model:    p.cfg.Model,
		Stream:   true,
		Messages: msgs,
	}
	tools := GetCurrentTools(messages)
	if len(tools) > 0 {
		convertedTools, err := p.convertTools(tools)
		if err != nil {
			return nil, fmt.Errorf("mistral: convert tools: %w", err)
		}
		req.Tools = convertedTools
	}
	if opts.IsReasoning {
		model := &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh}}
		if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
			model = generated.ToModel()
		} else if generated, ok := LookupModel(p.cfg.Model); ok {
			model = generated.ToModel()
		}
		reasoning := ClampThinkingLevel(model, opts.Thinking)
		if reasoning != "" && reasoning != ThinkingOff {
			if usesMistralPromptModeReasoning(p.cfg.Model, p.cfg.Reasoning) {
				req.PromptMode = "reasoning"
			}
			if usesMistralReasoningEffort(p.cfg.Model) {
				if mapped, ok := model.ThinkingLevelMap[reasoning]; ok && mapped != nil {
					req.ReasoningEffort = *mapped
				} else {
					req.ReasoningEffort = "high"
				}
			}
		}
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		req.Temperature = new(opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		m := opts.MaxTokens
		req.MaxTokens = &m
	}

	payload := any(req)
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(req, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if err != nil {
			return nil, fmt.Errorf("mistral: onPayload: %w", err)
		}
		if next != nil {
			payload = next
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("mistral: marshal request: %w", err)
	}

	url := mistralChatCompletionsURL(p.cfg.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mistral: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", PiUserAgent())
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	for k, v := range p.cfg.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}
	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = p.cfg.SessionID
	}
	if sessionID != "" && httpReq.Header.Get("x-affinity") == "" {
		httpReq.Header.Set("x-affinity", sessionID)
	}
	applyProviderHeaders(httpReq, opts.Headers)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mistral: request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(resp.Body)
		errText := truncateMistralError(string(errBody))
		return nil, fmt.Errorf("mistral API error (%d): %s", resp.StatusCode, errText)
	}

	builder := newAssistantStreamBuilder(ctx, APIMistralConversations, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	go p.consumeStream(ctx, resp.Body, builder)
	return builder.stream, nil
}

func truncateMistralError(text string) string {
	if len(text) <= maxMistralErrorBodyChars {
		return text
	}
	return fmt.Sprintf("%s... [truncated %d chars]", text[:maxMistralErrorBodyChars], len(text)-maxMistralErrorBodyChars)
}

func (p *mistralProvider) consumeStream(ctx context.Context, body io.ReadCloser, builder *assistantStreamBuilder) {
	defer func() { _ = body.Close() }()

	decoder := newSSEDecoder(body)

	var usage *Usage
	stopReason := StopReasonStop
	stopErrorMessage := ""
	hasFinishReason := false
	var currentToolID string
	var currentToolName string

	for decoder.Next() {
		if err := ctx.Err(); err != nil {
			builder.fail(StopReasonAborted, err)
			return
		}
		data := strings.TrimSpace(decoder.Event().Data)
		if data == "[DONE]" {
			break
		}

		var chunk mistralSSEChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			builder.fail(StopReasonError, fmt.Errorf("mistral: invalid SSE JSON: %w", err))
			return
		}

		if chunk.Choices == nil {
			builder.fail(StopReasonError, errors.New("Invalid Mistral streaming event"))
			return
		}

		if chunk.ID != "" {
			builder.setResponseMetadata(chunk.ID, "", "", "", nil)
		}
		if chunk.Usage != nil {
			cached := chunk.Usage.cachedPromptTokens()
			total := chunk.Usage.TotalTokens
			if total == 0 {
				total = chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens
			}
			usage = &Usage{
				Input:       max(0, chunk.Usage.PromptTokens-cached),
				Output:      chunk.Usage.CompletionTokens,
				CacheRead:   cached,
				TotalTokens: total,
			}
			builder.calculateCost(usage)
			builder.setUsage(usage)
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		delta := choice.Delta

		// Handle content
		if len(delta.Content) > 0 && string(delta.Content) != "null" {
			// Try as string first
			var textStr string
			if err := json.Unmarshal(delta.Content, &textStr); err == nil {
				if textStr != "" {
					builder.textDelta(sanitizeSurrogates(textStr))
				}
			} else {
				// Try as array of content items
				var items []mistralContentItem
				if err := json.Unmarshal(delta.Content, &items); err == nil {
					for _, item := range items {
						switch item.Type {
						case "text":
							if item.Text != "" {
								builder.textDelta(sanitizeSurrogates(item.Text))
							}
						case "thinking":
							var thinkText strings.Builder
							for _, part := range item.Thinking {
								if part.Text != "" {
									thinkText.WriteString(part.Text)
								}
							}
							if thinkText.Len() > 0 {
								builder.thinkingDelta(sanitizeSurrogates(thinkText.String()), false)
							}
						}
					}
				}
			}
		}

		// Handle tool calls
		for _, tc := range delta.ToolCalls {
			callID := tc.ID
			if callID == "" || callID == "null" {
				callID = deriveMistralToolCallID(fmt.Sprintf("toolcall:%d", tc.Index), 0)
			}

			if callID != currentToolID || tc.Function.Name != "" {
				currentToolID = callID
				currentToolName = tc.Function.Name
			}

			var argsDelta string
			if len(tc.Function.Arguments) > 0 {
				// Try as string first
				var s string
				if err := json.Unmarshal(tc.Function.Arguments, &s); err == nil {
					argsDelta = s
				} else {
					argsDelta = string(tc.Function.Arguments)
				}
			}

			builder.toolCallDelta(streamToolCallDelta{
				index: tc.Index, id: callID, name: currentToolName, argumentsDelta: argsDelta,
			})
		}

		// Handle finish reason
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			hasFinishReason = true
			builder.setResponseMetadata("", "", *choice.FinishReason, "", nil)
			stopReason, stopErrorMessage = mapMistralStopReason(*choice.FinishReason)
			if stopReason == StopReasonError {
				builder.fail(stopReason, errors.New(stopErrorMessage))
				return
			}
		}
	}

	if err := ctx.Err(); err != nil {
		builder.fail(StopReasonAborted, err)
		return
	}
	if err := decoder.Err(); err != nil {
		builder.fail(StopReasonError, err)
		return
	}
	if !hasFinishReason {
		builder.fail(StopReasonError, errors.New("Mistral stream ended without a finish reason"))
		return
	}
	builder.done(stopReason, usage, stopErrorMessage)
}

func mapMistralStopReason(reason string) (StopReason, string) {
	switch reason {
	case "stop":
		return StopReasonStop, ""
	case "length", "model_length":
		return StopReasonLength, ""
	case "tool_calls":
		return StopReasonToolUse, ""
	case "error":
		return StopReasonError, "Provider stopped with: error"
	default:
		return StopReasonError, "Provider stopped with: " + reason
	}
}

func usesMistralReasoningEffort(modelID string) bool {
	return modelID == "mistral-small-2603" || modelID == "mistral-small-latest" || modelID == "mistral-medium-3.5"
}

func usesMistralPromptModeReasoning(modelID string, reasoning bool) bool {
	return reasoning && !usesMistralReasoningEffort(modelID)
}

// ─── Message conversion ─────────────────────────────────────────────────────

func (p *mistralProvider) convertMessages(messages []Message, normalizer *mistralIDNormalizer) []mistralMessage {
	var result []mistralMessage
	for _, message := range messages {
		switch message := message.(type) {
		case SystemMessage:
			if text := RenderSystemMessageUpdate(message); strings.TrimSpace(text) != "" {
				result = append(result, mistralMessage{Role: "system", Content: sanitizeSurrogates(text)})
			}
		case UserMessage:
			result = append(result, p.convertUserMessage(message))
		case AssistantMessage:
			result = append(result, p.convertAssistantMessage(message, normalizer))
		case ToolResultMessage:
			result = append(result, p.convertToolResultMessage(message, normalizer))
		}
	}
	return result
}

func (p *mistralProvider) convertUserMessage(message UserMessage) mistralMessage {
	switch content := message.Content.(type) {
	case UserText:
		return mistralMessage{Role: "user", Content: sanitizeSurrogates(string(content))}
	case UserContentBlocks:
		var chunks []mistralContentChunk
		for _, block := range content {
			switch block := block.(type) {
			case TextContent:
				chunks = append(chunks, mistralContentChunk{Type: "text", Text: sanitizeSurrogates(block.Text)})
			case ImageContent:
				chunks = append(chunks, mistralContentChunk{Type: "image_url", ImageURL: fmt.Sprintf("data:%s;base64,%s", block.MimeType, block.Data)})
			}
		}
		if len(chunks) > 0 {
			return mistralMessage{Role: "user", Content: chunks}
		}
	}
	return mistralMessage{Role: "user", Content: "(content omitted)"}
}

func (p *mistralProvider) convertAssistantMessage(message AssistantMessage, normalizer *mistralIDNormalizer) mistralMessage {
	var content []mistralContentChunk
	var toolCalls []mistralToolCallMsg
	for _, block := range message.Content {
		switch block := block.(type) {
		case TextContent:
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, mistralContentChunk{Type: "text", Text: sanitizeSurrogates(block.Text)})
			}
		case ThinkingContent:
			if strings.TrimSpace(block.Thinking) != "" {
				content = append(content, mistralContentChunk{Type: "thinking", Thinking: []map[string]string{{"type": "text", "text": sanitizeSurrogates(block.Thinking)}}})
			}
		case ToolCall:
			arguments, _ := json.Marshal(block.Arguments)
			toolCalls = append(toolCalls, mistralToolCallMsg{
				ID: normalizer.normalize(block.ID), Type: "function",
				Function: mistralToolCallFnMsg{Name: block.Name, Arguments: string(arguments)},
			})
		}
	}
	converted := mistralMessage{Role: "assistant"}
	if len(content) > 0 {
		converted.Content = content
	}
	if len(toolCalls) > 0 {
		converted.ToolCalls = toolCalls
	}
	return converted
}

func (p *mistralProvider) convertToolResultMessage(message ToolResultMessage, normalizer *mistralIDNormalizer) mistralMessage {
	var textParts []string
	for _, block := range message.Content {
		if block, ok := block.(TextContent); ok {
			textParts = append(textParts, block.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(textParts, "\n"))
	if text == "" {
		if message.IsError {
			text = "[tool error] (no tool output)"
		} else {
			text = "(no tool output)"
		}
	} else if message.IsError {
		text = "[tool error] " + text
	}
	return mistralMessage{
		Role: "tool", Content: []mistralContentChunk{{Type: "text", Text: text}},
		ToolCallID: normalizer.normalize(message.ToolCallID), Name: message.ToolName,
	}
}

func (p *mistralProvider) convertTools(tools []ToolSchema) ([]mistralTool, error) {
	result := make([]mistralTool, len(tools))
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, true)
		if err != nil {
			return nil, err
		}
		parameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		result[i] = mistralTool{
			Type: "function",
			Function: mistralToolFn{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  parameters,
				Strict:      strict != nil && *strict,
			},
		}
	}
	return result, nil
}

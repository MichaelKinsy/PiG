package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/google.ts
// and .upstream/current/packages/ai/src/providers/google-shared.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

// toolCallCounter generates unique tool call IDs when Gemini doesn't provide one.
// Mirrors upstream google.ts module-level counter.
var toolCallCounter atomic.Int64

// GoogleConfig configures the Google Gemini provider.
type GoogleConfig struct {
	// APIKey is the Google AI API key.
	APIKey string
	// Model is the model name (e.g. "gemini-2.5-flash").
	Model string
	// ProviderID is the provider label (default: "google-generative-ai").
	ProviderID string
	// BaseURL overrides the API base (default: https://generativelanguage.googleapis.com).
	BaseURL string
	// APIVersion overrides the API version path (default: "v1beta").
	// Set to "" when BaseURL already includes the version path.
	APIVersion string
	// ExtraHeaders are added to every request.
	ExtraHeaders map[string]string
}

type googleProvider struct {
	cfg    GoogleConfig
	client *http.Client
}

// NewGoogleProvider creates a Provider backed by the Google Gemini API.
func NewGoogleProvider(cfg GoogleConfig) Provider {
	hasExplicitBaseURL := cfg.BaseURL != ""
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://generativelanguage.googleapis.com"
	}
	if cfg.APIVersion == "" && !hasExplicitBaseURL {
		cfg.APIVersion = "v1beta"
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "google-generative-ai"
	}
	return &googleProvider{cfg: cfg, client: streamingHTTPClient()}
}

func (p *googleProvider) ID() string { return p.cfg.ProviderID }

func (p *googleProvider) Close() error { return nil }

// ─── Request wire types ──────────────────────────────────────────────────────

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDecl        `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	// Text distinguishes an empty text part from a part with no text field.
	Text             *string             `json:"text,omitempty"`
	Thought          *bool               `json:"thought,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse `json:"functionResponse,omitempty"`
	InlineData       *geminiInlineData   `json:"inlineData,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

type geminiFuncResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
	Parts    []geminiPart   `json:"parts,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description"`
	Parameters           map[string]any `json:"parameters,omitempty"`
	ParametersJSONSchema map[string]any `json:"parametersJsonSchema,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFuncCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFuncCallingConfig struct {
	Mode string `json:"mode"` // "AUTO", "NONE", "ANY"
}

type geminiGenerationConfig struct {
	Temperature     *float64              `json:"temperature,omitempty"`
	MaxOutputTokens *int                  `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	IncludeThoughts *bool  `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

// GoogleThinkingLevel mirrors the upstream exported thinking-level union used
// by both the direct Gemini and Vertex providers.
type GoogleThinkingLevel string

const (
	GoogleThinkingLevelUnspecified GoogleThinkingLevel = "THINKING_LEVEL_UNSPECIFIED"
	GoogleThinkingLevelMinimal     GoogleThinkingLevel = "MINIMAL"
	GoogleThinkingLevelLow         GoogleThinkingLevel = "LOW"
	GoogleThinkingLevelMedium      GoogleThinkingLevel = "MEDIUM"
	GoogleThinkingLevelHigh        GoogleThinkingLevel = "HIGH"
)

// ─── SSE response types ──────────────────────────────────────────────────────

type geminiStreamChunk struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ResponseID    string               `json:"responseId,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

type geminiCandidate struct {
	Content      *geminiContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
}

// ─── Model helpers ───────────────────────────────────────────────────────────

var (
	gemini3ProRe   = regexp.MustCompile(`(?i)gemini-3(?:\.\d+)?-pro`)
	gemini3FlashRe = regexp.MustCompile(`(?i)gemini-3(?:\.\d+)?-flash`)
	geminiMajorRe  = regexp.MustCompile(`(?i)^gemini(?:-live)?-([0-9]+)`)
	gemma4Re       = regexp.MustCompile(`(?i)gemma-?4`)
)

func isGemini3Pro(modelID string) bool   { return gemini3ProRe.MatchString(modelID) }
func isGemini3Flash(modelID string) bool { return gemini3FlashRe.MatchString(modelID) }
func isGemma4(modelID string) bool       { return gemma4Re.MatchString(modelID) }

// ─── Message conversion ──────────────────────────────────────────────────────
// Mirrors upstream google-shared.ts convertMessages.

// isValidThoughtSignature reports whether sig is a valid base64 thought
// signature. Google APIs carry the signature as TYPE_BYTES, so it must be
// non-empty, its length a multiple of 4, and match ^[A-Za-z0-9+/]+={0,2}$.
// Mirrors google-shared.ts isValidThoughtSignature.
func isValidThoughtSignature(sig string) bool {
	if sig == "" || len(sig)%4 != 0 {
		return false
	}
	pad := 0
	for i := 0; i < len(sig); i++ {
		c := sig[i]
		switch {
		case c == '=':
			pad++
		case pad > 0:
			return false // '=' padding must be trailing only
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/':
		default:
			return false
		}
	}
	return pad <= 2 && pad < len(sig)
}

var geminiMajorVersionRE = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)

// getGeminiMajorVersion parses the leading major version from a Gemini model id
// (^gemini(-live)?-N...). Mirrors google-shared.ts getGeminiMajorVersion.
func getGeminiMajorVersion(modelID string) (int, bool) {
	m := geminiMajorVersionRE.FindStringSubmatch(strings.ToLower(modelID))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// requiresToolCallId reports whether a model reached via the Google APIs needs
// explicit tool call ids echoed in functionCall/functionResponse. Mirrors
// google-shared.ts:72: claude-*, gpt-oss-*, or a Gemini major version >= 3.
func requiresToolCallId(modelID string) bool {
	if strings.HasPrefix(modelID, "claude-") || strings.HasPrefix(modelID, "gpt-oss-") {
		return true
	}
	if major, ok := getGeminiMajorVersion(modelID); ok {
		return major >= 3
	}
	return false
}

// normalizeToolCallId sanitizes a tool call id to [A-Za-z0-9_-] and truncates it
// to 64 chars when the model requires ids; otherwise it is returned unchanged.
// Mirrors google-shared.ts:101. It is applied identically to the functionCall
// id and the paired functionResponse id, which share the same source string, so
// the normalized values match and the model can pair them.
func normalizeToolCallId(modelID, id string) string {
	if !requiresToolCallId(modelID) {
		return id
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func geminiConvertMessages(messages []Message, providerID, modelID string, supportsImages bool) []geminiContent {
	var contents []geminiContent
	for index := 0; index < len(messages); index++ {
		switch message := messages[index].(type) {
		case SystemMessage:
			if text := RenderSystemMessageUpdate(message); strings.TrimSpace(text) != "" {
				contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{{Text: new(text)}}})
			}
		case UserMessage:
			var parts []geminiPart
			switch content := message.Content.(type) {
			case UserText:
				parts = append(parts, geminiPart{Text: new(string(content))})
			case UserContentBlocks:
				for _, block := range content {
					switch block := block.(type) {
					case TextContent:
						parts = append(parts, geminiPart{Text: new(block.Text)})
					case ImageContent:
						parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: block.MimeType, Data: block.Data}})
					}
				}
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "user", Parts: parts})
			}
		case AssistantMessage:
			sameProviderAndModel := message.Provider == providerID && message.Model == modelID
			var parts []geminiPart
			for _, block := range message.Content {
				switch block := block.(type) {
				case TextContent:
					signature := ""
					if sameProviderAndModel && isValidThoughtSignature(block.TextSignature) {
						signature = block.TextSignature
					}
					if strings.TrimSpace(block.Text) != "" || signature != "" {
						parts = append(parts, geminiPart{Text: new(block.Text), ThoughtSignature: signature})
					}
				case ThinkingContent:
					if sameProviderAndModel {
						signature := ""
						if isValidThoughtSignature(block.ThinkingSignature) {
							signature = block.ThinkingSignature
						}
						if strings.TrimSpace(block.Thinking) == "" && signature == "" {
							continue
						}
						thought := true
						parts = append(parts, geminiPart{Text: new(block.Thinking), Thought: &thought, ThoughtSignature: signature})
					} else if strings.TrimSpace(block.Thinking) != "" {
						parts = append(parts, geminiPart{Text: new(block.Thinking)})
					}
				case ToolCall:
					call := &geminiFunctionCall{Name: block.Name, Args: block.Arguments}
					if requiresToolCallId(modelID) {
						call.ID = normalizeToolCallId(modelID, block.ID)
					}
					part := geminiPart{FunctionCall: call}
					if sameProviderAndModel && isValidThoughtSignature(block.ThoughtSignature) {
						part.ThoughtSignature = block.ThoughtSignature
					}
					parts = append(parts, part)
				}
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "model", Parts: parts})
			}
		case ToolResultMessage:
			for index < len(messages) {
				result, ok := messages[index].(ToolResultMessage)
				if !ok {
					break
				}
				var textParts []string
				var imageParts []geminiPart
				if supportsImages {
					for _, block := range result.Content {
						switch block := block.(type) {
						case TextContent:
							textParts = append(textParts, block.Text)
						case ImageContent:
							imageParts = append(imageParts, geminiPart{InlineData: &geminiInlineData{MimeType: block.MimeType, Data: block.Data}})
						}
					}
				} else {
					for _, block := range result.Content {
						if block, ok := block.(TextContent); ok {
							textParts = append(textParts, block.Text)
						}
					}
				}
				text := strings.Join(textParts, "\n")
				if text == "" && len(imageParts) > 0 {
					text = "(see attached image)"
				}
				payload := map[string]any{"output": text}
				if result.IsError {
					payload = map[string]any{"error": text}
				}
				response := &geminiFuncResponse{Name: result.ToolName, Response: payload}
				if supportsMultimodalFunctionResponse(modelID) {
					response.Parts = imageParts
				}
				if requiresToolCallId(modelID) {
					response.ID = normalizeToolCallId(modelID, result.ToolCallID)
				}
				responsePart := geminiPart{FunctionResponse: response}
				last := len(contents) - 1
				if last >= 0 && contents[last].Role == "user" && len(contents[last].Parts) > 0 && contents[last].Parts[0].FunctionResponse != nil {
					contents[last].Parts = append(contents[last].Parts, responsePart)
				} else {
					contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{responsePart}})
				}
				if len(imageParts) > 0 && !supportsMultimodalFunctionResponse(modelID) {
					contents = append(contents, geminiContent{Role: "user", Parts: append([]geminiPart{{Text: new("Tool result image:")}}, imageParts...)})
				}
				index++
			}
			index--
		}
	}
	return contents
}

func geminiConvertTools(tools []ToolSchema, supportsStrictMode bool) ([]geminiToolDecl, bool, error) {
	if len(tools) == 0 {
		return nil, false, nil
	}
	decls := make([]geminiFuncDecl, len(tools))
	usesStrictMode := false
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictMode)
		if err != nil {
			return nil, false, err
		}
		parameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, false, err
		}
		usesStrictMode = usesStrictMode || strict != nil && *strict
		decls[i] = geminiFuncDecl{
			Name:                 tool.Name,
			Description:          tool.Description,
			ParametersJSONSchema: parameters,
		}
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}, usesStrictMode, nil
}

func supportsGoogleStrictToolSampling(modelID string) bool {
	match := geminiMajorRe.FindStringSubmatch(strings.ToLower(modelID))
	if len(match) < 2 {
		return false
	}
	major, err := strconv.Atoi(match[1])
	return err == nil && major >= 3
}

// ─── Thinking config helpers ─────────────────────────────────────────────────
// Mirrors upstream google.ts getThinkingLevel, getGoogleBudget,
// getDisabledThinkingConfig.

func buildGeminiThinkingConfig(model *Model, level ThinkingLevel, isReasoning bool) *geminiThinkingConfig {
	if model == nil {
		model = &Model{}
	}
	clamped := ClampThinkingLevel(model, level)
	if clamped == ThinkingOff || clamped == "" {
		if !isReasoning {
			return nil
		}
		// Reasoning model with thinking disabled: use lowest supported level.
		return geminiDisabledThinkingConfig(model.ID)
	}

	// Use thinkingLevel for Gemini 3 / Gemma 4 models.
	if isGemini3Pro(model.ID) || isGemini3Flash(model.ID) || isGemma4(model.ID) {
		t := true
		return &geminiThinkingConfig{
			IncludeThoughts: &t,
			ThinkingLevel:   geminiThinkingLevel(clamped, model.ID),
		}
	}

	// Use thinkingBudget for Gemini 2.x models.
	t := true
	budget := geminiThinkingBudget(clamped, model.ID)
	return &geminiThinkingConfig{
		IncludeThoughts: &t,
		ThinkingBudget:  &budget,
	}
}

func geminiDisabledThinkingConfig(modelID string) *geminiThinkingConfig {
	if isGemini3Pro(modelID) {
		return &geminiThinkingConfig{ThinkingLevel: string(GoogleThinkingLevelLow)}
	}
	if isGemini3Flash(modelID) || isGemma4(modelID) {
		return &geminiThinkingConfig{ThinkingLevel: string(GoogleThinkingLevelMinimal)}
	}
	zero := 0
	return &geminiThinkingConfig{ThinkingBudget: &zero}
}

func geminiThinkingLevel(level ThinkingLevel, modelID string) string {
	if isGemini3Pro(modelID) {
		switch level {
		case ThinkingMinimal, ThinkingLow:
			return string(GoogleThinkingLevelLow)
		default:
			return string(GoogleThinkingLevelHigh)
		}
	}
	if isGemma4(modelID) {
		switch level {
		case ThinkingMinimal, ThinkingLow:
			return string(GoogleThinkingLevelMinimal)
		default:
			return string(GoogleThinkingLevelHigh)
		}
	}
	// Default (Gemini 3 Flash)
	switch level {
	case ThinkingMinimal:
		return string(GoogleThinkingLevelMinimal)
	case ThinkingLow:
		return string(GoogleThinkingLevelLow)
	case ThinkingMedium:
		return string(GoogleThinkingLevelMedium)
	default:
		return string(GoogleThinkingLevelHigh)
	}
}

func supportsMultimodalFunctionResponse(modelID string) bool {
	match := geminiMajorRe.FindStringSubmatch(strings.ToLower(modelID))
	if len(match) < 2 {
		return true
	}
	major, err := strconv.Atoi(match[1])
	return err != nil || major >= 3
}

func geminiThinkingBudget(level ThinkingLevel, modelID string) int {
	type budgets struct {
		minimal, low, medium, high int
	}

	var b budgets
	switch {
	case strings.Contains(modelID, "2.5-pro"):
		b = budgets{128, 2048, 8192, 32768}
	case strings.Contains(modelID, "2.5-flash-lite"):
		b = budgets{512, 2048, 8192, 24576}
	case strings.Contains(modelID, "2.5-flash"):
		b = budgets{128, 2048, 8192, 24576}
	default:
		return -1 // dynamic
	}

	switch level {
	case ThinkingMinimal:
		return b.minimal
	case ThinkingLow:
		return b.low
	case ThinkingMedium:
		return b.medium
	default:
		return b.high
	}
}

// ─── Stream ──────────────────────────────────────────────────────────────────

func (p *googleProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("google: invalid transcript: %w", err)
	}
	model := &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh}}
	if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	} else if generated, ok := LookupModel(p.cfg.Model); ok {
		model = generated.ToModel()
	}
	resolved := CollapseSystemMessages(transcript)
	messages := resolved.Messages()
	contents := geminiConvertMessages(WithoutInitialSystemMessage(messages), p.cfg.ProviderID, p.cfg.Model, model.Capabilities.SupportsImages)

	req := geminiRequest{
		Contents: contents,
	}

	if systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))]); systemPrompt != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: new(systemPrompt)}},
		}
	}

	tools := GetCurrentTools(messages)
	if len(tools) > 0 {
		convertedTools, usesStrictMode, err := geminiConvertTools(tools, supportsGoogleStrictToolSampling(model.ID))
		if err != nil {
			return nil, fmt.Errorf("google: convert tools: %w", err)
		}
		req.Tools = convertedTools
		mode := "AUTO"
		if usesStrictMode {
			mode = "VALIDATED"
		}
		req.ToolConfig = &geminiToolConfig{
			FunctionCallingConfig: &geminiFuncCallingConfig{Mode: mode},
		}
	}

	genConfig := &geminiGenerationConfig{}
	hasGenConfig := false
	if opts.TemperatureSet || opts.Temperature != 0 {
		genConfig.Temperature = new(opts.Temperature)
		hasGenConfig = true
	}
	if opts.MaxTokens > 0 {
		mt := opts.MaxTokens
		genConfig.MaxOutputTokens = &mt
		hasGenConfig = true
	}

	tc := buildGeminiThinkingConfig(model, opts.Thinking, opts.IsReasoning)
	if tc != nil {
		genConfig.ThinkingConfig = tc
		hasGenConfig = true
	}

	if hasGenConfig {
		req.GenerationConfig = genConfig
	}

	payload := any(req)
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(req, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if err != nil {
			return nil, fmt.Errorf("google: onPayload: %w", err)
		}
		if next != nil {
			payload = next
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("google: marshal request: %w", err)
	}

	// Build URL: {baseURL}/{apiVersion}/models/{model}:streamGenerateContent?alt=sse&key={apiKey}
	var urlBuf strings.Builder
	urlBuf.WriteString(p.cfg.BaseURL)
	if p.cfg.APIVersion != "" {
		urlBuf.WriteByte('/')
		urlBuf.WriteString(p.cfg.APIVersion)
	}
	urlBuf.WriteString("/models/")
	urlBuf.WriteString(p.cfg.Model)
	urlBuf.WriteString(":streamGenerateContent?alt=sse")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, urlBuf.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", PiUserAgent())
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("x-goog-api-key", p.cfg.APIKey)
	}
	for k, v := range p.cfg.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}
	applyProviderHeaders(httpReq, opts.Headers)

	resp, err := p.client.Do(httpReq) //nolint:bodyclose // body closed via defer in SSE goroutine below
	if err != nil {
		return nil, fmt.Errorf("google: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("google: HTTP %d: %s", resp.StatusCode, string(b))
	}

	builder := newAssistantStreamBuilder(ctx, APIGoogleGenerativeAI, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	go func() {
		defer func() { _ = resp.Body.Close() }()
		p.parseGeminiSSE(ctx, resp.Body, builder)
	}()
	return builder.stream, nil
}

// ─── SSE parsing ─────────────────────────────────────────────────────────────
// Gemini streams SSE with `data: {JSON}` lines. Each chunk is a
// GenerateContentResponse with candidates[].content.parts[].

type googleSSEReader struct {
	reader io.Reader
}

func (reader googleSSEReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		if streamErr := googleRawChunkError(buffer[:count]); streamErr != nil {
			return 0, streamErr
		}
	}
	return count, err
}

func googleRawChunkError(chunk []byte) error {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(chunk, &envelope) != nil || len(envelope.Error) == 0 {
		return nil
	}
	var detail struct {
		Code   json.RawMessage `json:"code"`
		Status json.RawMessage `json:"status"`
	}
	if json.Unmarshal(envelope.Error, &detail) != nil {
		return nil
	}
	code := 0.0
	if json.Unmarshal(detail.Code, &code) != nil {
		var text string
		if json.Unmarshal(detail.Code, &text) != nil {
			return nil
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil
		}
		code = parsed
	}
	if code < 400 || code >= 600 {
		return nil
	}
	status := "undefined"
	if len(detail.Status) > 0 {
		if json.Unmarshal(detail.Status, &status) != nil {
			status = compactJSON(detail.Status)
		}
	}
	return fmt.Errorf("got status: %s. %s", status, compactJSON(chunk))
}

func (p *googleProvider) parseGeminiSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder) {
	var usage Usage
	var finishReason string
	sawToolCall := false
	// Track whether we're in a text or thinking block to emit proper deltas.
	inThinking := false

	decoder := newSSEDecoder(googleSSEReader{reader: r})

	for decoder.Next() {
		if err := ctx.Err(); err != nil {
			builder.fail(StopReasonAborted, err)
			return
		}
		data := decoder.Event().Data
		if data == "" {
			continue
		}

		var chunk geminiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			builder.fail(StopReasonError, fmt.Errorf("google: invalid SSE JSON: %w", err))
			return
		}

		if chunk.ResponseID != "" || chunk.ModelVersion != "" {
			builder.setResponseMetadata(chunk.ResponseID, chunk.ModelVersion, "", "", nil)
		}
		// Process candidates
		if len(chunk.Candidates) > 0 {
			candidate := chunk.Candidates[0]
			if candidate.FinishReason != "" {
				finishReason = candidate.FinishReason
				builder.setResponseMetadata("", "", finishReason, "", nil)
			}
			if candidate.Content != nil {
				for partIdx, part := range candidate.Content.Parts {
					// Text or thinking content
					text := ""
					if part.Text != nil {
						text = *part.Text
					}
					if text != "" || part.Thought != nil {
						isThinking := part.Thought != nil && *part.Thought

						// Transition between text and thinking
						if isThinking && !inThinking {
							inThinking = true
						} else if !isThinking && inThinking {
							inThinking = false
						}

						if isThinking {
							builder.thinkingDelta(text, false)
							if part.ThoughtSignature != "" {
								builder.thinkingSignature(part.ThoughtSignature)
							}
						} else {
							builder.textDelta(text)
							if part.ThoughtSignature != "" {
								builder.textSignature(part.ThoughtSignature)
							}
						}
					}

					// Function call
					if part.FunctionCall != nil {
						sawToolCall = true
						fc := part.FunctionCall
						// Generate unique ID if not provided
						toolID := fc.ID
						if toolID == "" {
							toolID = fmt.Sprintf("%s_%d", fc.Name, toolCallCounter.Add(1))
						}

						argsJSON, _ := json.Marshal(fc.Args)

						builder.toolCallDelta(streamToolCallDelta{
							index: partIdx, id: toolID, name: fc.Name, argumentsDelta: string(argsJSON), thoughtSignature: part.ThoughtSignature,
						})
						builder.endToolCall(partIdx)
					}
				}
			}
		}

		// Usage metadata
		if chunk.UsageMetadata != nil {
			um := chunk.UsageMetadata
			usage = Usage{
				Input:       um.PromptTokenCount - um.CachedContentTokenCount,
				Output:      um.CandidatesTokenCount + um.ThoughtsTokenCount,
				Reasoning:   new(um.ThoughtsTokenCount),
				CacheRead:   um.CachedContentTokenCount,
				TotalTokens: um.TotalTokenCount,
			}
			builder.calculateCost(&usage)
			builder.setUsage(&usage)
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
	stopReason, errorMessage := mapGoogleFinishReason(finishReason)
	if stopReason == StopReasonStop && sawToolCall {
		stopReason = StopReasonToolUse
	}
	if stopReason == StopReasonError {
		builder.fail(stopReason, errors.New(errorMessage))
		return
	}
	builder.done(stopReason, &usage, errorMessage)
}

func mapGoogleFinishReason(reason string) (StopReason, string) {
	switch reason {
	case "STOP":
		return StopReasonStop, ""
	case "MAX_TOKENS":
		return StopReasonLength, ""
	case "":
		return StopReasonError, "Google stream ended without a finish reason"
	default:
		return StopReasonError, "Provider stopped with: " + reason
	}
}

package ai

// Ports packages/ai/src/api/google-generative-ai.ts.
// Ports packages/ai/src/api/google-shared.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Generated tool-call IDs use one module-level counter per API, as google-generative-ai.ts:57 and google-vertex.ts:66 do.
var (
	googleGenerativeToolCallCounter atomic.Int64
	googleVertexToolCallCounter     atomic.Int64
)

// googleToolCallID keeps a provided ID unless it is empty or already used in this message; otherwise it generates name_<epoch ms>_<counter> (google-generative-ai.ts:195-200, google-vertex.ts:203-208).
func googleToolCallID(api API, name, provided string, content []AssistantContentBlock) string {
	needsNewID := provided == ""
	for _, block := range content {
		if call, ok := block.(ToolCall); ok && call.ID == provided {
			needsNewID = true
			break
		}
	}
	if !needsNewID {
		return provided
	}
	counter := &googleGenerativeToolCallCounter
	if api == APIGoogleVertex {
		counter = &googleVertexToolCallCounter
	}
	return name + "_" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "_" + strconv.FormatInt(counter.Add(1), 10)
}

// GoogleThinkingOptions is the raw Google API thinking object. A level takes precedence over a budget; a non-nil zero budget remains explicit.
type GoogleThinkingOptions struct {
	Enabled      bool                `json:"enabled"`
	BudgetTokens *int                `json:"budgetTokens,omitempty"`
	Level        GoogleThinkingLevel `json:"level,omitempty"`
}

// GoogleConfig configures the Google Gemini provider.
type GoogleConfig struct {
	api         API
	accessToken func(context.Context, ProviderEnv) (string, error)
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
	// ThinkingLevelMap carries the selected model's logical-to-provider thinking mapping. Nil uses the catalog mapping.
	ThinkingLevelMap ThinkingLevelMap
	// ModelMetadata is the selected model, which an OnProviderStreamEvent observer receives. Nil hands the observer the configured identity only.
	ModelMetadata *Model
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
	if cfg.api == "" {
		cfg.api = APIGoogleGenerativeAI
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

// parameters exposes the native Google SDK request before its configuration is lowered into REST fields.
func (request geminiRequest) parameters(model string) map[string]any {
	config := map[string]any{}
	if generation := request.GenerationConfig; generation != nil {
		if generation.Temperature != nil {
			config["temperature"] = generation.Temperature
		}
		if generation.MaxOutputTokens != nil {
			config["maxOutputTokens"] = generation.MaxOutputTokens
		}
		if generation.ThinkingConfig != nil {
			config["thinkingConfig"] = generation.ThinkingConfig
		}
	}
	if request.SystemInstruction != nil {
		config["systemInstruction"] = *request.SystemInstruction.Parts[0].Text
	}
	if len(request.Tools) > 0 {
		config["tools"] = request.Tools
	}
	if request.ToolConfig != nil {
		config["toolConfig"] = request.ToolConfig
	}
	return map[string]any{"model": model, "contents": request.Contents, "config": config}
}

// marshalGeminiParameters lowers native model/contents/config after onPayload, including a replacement model and generation configuration.
func marshalGeminiParameters(payload any) (string, []byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	var params struct {
		Model    string                     `json:"model"`
		Contents json.RawMessage            `json:"contents"`
		Config   map[string]json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(encoded, &params); err != nil {
		return "", nil, err
	}
	if params.Model == "" {
		return "", nil, errors.New("model is required")
	}
	body := map[string]any{"contents": params.Contents}
	var text string
	if json.Unmarshal(params.Contents, &text) == nil {
		body["contents"] = []geminiContent{{Role: "user", Parts: []geminiPart{{Text: &text}}}}
	}
	generation := map[string]json.RawMessage{}
	// These generation fields remain under generationConfig in the Google REST request.
	for _, name := range []string{"temperature", "topP", "topK", "candidateCount", "maxOutputTokens", "stopSequences", "responseLogprobs", "logprobs", "presencePenalty", "frequencyPenalty", "seed", "responseMimeType", "responseSchema", "responseJsonSchema", "responseModalities", "mediaResolution", "speechConfig", "thinkingConfig", "audioTranscriptionConfig", "imageConfig", "enableEnhancedCivicAnswers"} {
		if value := params.Config[name]; len(value) > 0 && string(value) != "null" {
			generation[name] = value
		}
	}
	if params.Config != nil {
		body["generationConfig"] = generation
	}
	for _, name := range []string{"tools", "toolConfig", "safetySettings", "cachedContent", "serviceTier", "labels", "modelArmorConfig"} {
		if value := params.Config[name]; len(value) > 0 && string(value) != "null" {
			body[name] = value
		}
	}
	if value := params.Config["systemInstruction"]; len(value) > 0 && string(value) != "null" {
		var instruction string
		if json.Unmarshal(value, &instruction) == nil {
			body["systemInstruction"] = geminiContent{Role: "user", Parts: []geminiPart{{Text: &instruction}}}
		} else {
			body["systemInstruction"] = value
		}
	}
	encoded, err = json.Marshal(body)
	return params.Model, encoded, err
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
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
	ID   string          `json:"id,omitempty"`

	// idName is the text `${part.functionCall.name}` produces for a decoded response, valid when decoded is set.
	idName  string
	decoded bool
}

// UnmarshalJSON records how JavaScript would stringify a missing or null name in a generated tool-call ID: "undefined" and "null" (google-generative-ai.ts:198-200). Name keeps `part.functionCall.name || ""`.
func (call *geminiFunctionCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name json.RawMessage `json:"name"`
		Args json.RawMessage `json:"args"`
		ID   string          `json:"id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*call = geminiFunctionCall{Args: raw.Args, ID: raw.ID, decoded: true, idName: "undefined"}
	if raw.Name == nil {
		return nil
	}
	var name string
	if string(raw.Name) == "null" {
		call.idName = "null"
		return nil
	}
	if err := json.Unmarshal(raw.Name, &name); err == nil {
		call.Name, call.idName = name, name
		return nil
	}
	call.idName = string(raw.Name)
	return nil
}

// generatedIDName is the name segment of a generated tool-call ID.
func (call *geminiFunctionCall) generatedIDName() string {
	if call.decoded {
		return call.idName
	}
	return call.Name
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
	// raw is the chunk's JSON as received, for OnProviderStreamEvent.
	raw           json.RawMessage
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ResponseID    string               `json:"responseId,omitempty"`
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
	gemini3ProRe   = lazyregexp.New(`(?i)gemini-3(?:\.\d+)?-pro`)
	gemini3FlashRe = lazyregexp.New(`(?i)gemini-3(?:\.\d+)?-flash`)
	geminiMajorRe  = lazyregexp.New(`(?i)^gemini(?:-live)?-([0-9]+)`)
	gemma4Re       = lazyregexp.New(`(?i)gemma-?4`)
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

var geminiMajorVersionRE = lazyregexp.New(`^gemini(?:-live)?-(\d+)`)

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

// geminiConvertMessages converts the conversation after the caller has collapsed system messages and removed the leading one, so any system message is skipped.
func geminiConvertMessages(messages []Message, providerID, modelID string, supportsImages bool) []geminiContent {
	var contents []geminiContent
	for index := 0; index < len(messages); index++ {
		switch message := messages[index].(type) {
		case UserMessage:
			var parts []geminiPart
			switch content := message.Content.(type) {
			case UserText:
				parts = append(parts, geminiPart{Text: new(sanitizeSurrogates(string(content)))})
			case UserContentBlocks:
				for _, block := range content {
					switch block := block.(type) {
					case TextContent:
						parts = append(parts, geminiPart{Text: new(sanitizeSurrogates(block.Text))})
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
					if trimJSWhitespace(block.Text) != "" || signature != "" {
						parts = append(parts, geminiPart{Text: new(sanitizeSurrogates(block.Text)), ThoughtSignature: signature})
					}
				case ThinkingContent:
					if sameProviderAndModel {
						signature := ""
						if isValidThoughtSignature(block.ThinkingSignature) {
							signature = block.ThinkingSignature
						}
						if trimJSWhitespace(block.Thinking) == "" && signature == "" {
							continue
						}
						thought := true
						parts = append(parts, geminiPart{Text: new(sanitizeSurrogates(block.Thinking)), Thought: &thought, ThoughtSignature: signature})
					} else if trimJSWhitespace(block.Thinking) != "" {
						parts = append(parts, geminiPart{Text: new(sanitizeSurrogates(block.Thinking))})
					}
				case ToolCall:
					// Pi: args = block.arguments ?? {}. validateProviderRequest rejected arguments with no JSON form before the request was converted.
					call := &geminiFunctionCall{Name: block.Name}
					call.Args, _ = block.ArgumentsJSON()
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
				text = sanitizeSurrogates(text)
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

func geminiConvertTools(tools []ToolSchema, useParameters, supportsStrictMode bool) ([]geminiToolDecl, bool, error) {
	if len(tools) == 0 {
		return nil, false, nil
	}
	decls := make([]geminiFuncDecl, len(tools))
	usesStrictMode := false
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, false, err
		}
		parameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, false, err
		}
		usesStrictMode = usesStrictMode || strict != nil && *strict
		decls[i] = geminiFuncDecl{Name: tool.Name, Description: tool.Description}
		if useParameters {
			decls[i].Parameters = sanitizeForOpenAPI(parameters).(map[string]any)
		} else {
			decls[i].ParametersJSONSchema = parameters
		}
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}, usesStrictMode, nil
}

// sanitizeForOpenAPI strips meta declarations from schema objects, preserving arrays and references as Pi's legacy Google helper does.
func sanitizeForOpenAPI(schema any) any {
	object, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	result := make(map[string]any, len(object))
	for key, value := range object {
		switch key {
		case "$schema", "$id", "$anchor", "$dynamicAnchor", "$vocabulary", "$comment", "$defs", "definitions":
			continue
		}
		result[key] = sanitizeForOpenAPI(value)
	}
	return result
}

func supportsGoogleStrictToolSampling(modelID string) bool {
	match := geminiMajorRe.FindStringSubmatch(strings.ToLower(modelID))
	if len(match) < 2 {
		return false
	}
	major, err := strconv.Atoi(match[1])
	return err == nil && major >= 3
}

// Thinking configuration follows the selected model's map before selecting Google's level or token-budget wire format.

func buildGeminiThinkingConfig(model *Model, level ThinkingLevel, isReasoning bool, customBudgets *ThinkingBudgets) (*geminiThinkingConfig, error) {
	if !isReasoning {
		return nil, nil
	}
	if model == nil {
		model = &Model{}
	}
	usesLevel := isGemini3Pro(model.ID) || isGemini3Flash(model.ID) || isGemma4(model.ID) || model.ID == "gemini-flash-latest" || model.ID == "gemini-flash-lite-latest"
	clamped := ClampThinkingLevel(model, level)
	if level == "" || level == ThinkingOff || clamped == ThinkingOff {
		if !usesLevel {
			return &geminiThinkingConfig{ThinkingBudget: new(0)}, nil
		}
		fallback := ClampThinkingLevel(model, ThinkingOff)
		if fallback == ThinkingOff {
			return &geminiThinkingConfig{ThinkingBudget: new(0)}, nil
		}
		resolved, err := resolveGoogleThinkingLevel(model, fallback)
		if err != nil {
			return nil, err
		}
		return &geminiThinkingConfig{ThinkingLevel: strings.ToUpper(string(resolved))}, nil
	}
	resolved, err := resolveGoogleThinkingLevel(model, clamped)
	if err != nil {
		return nil, err
	}
	config := &geminiThinkingConfig{IncludeThoughts: new(true)}
	if usesLevel {
		config.ThinkingLevel = strings.ToUpper(string(resolved))
	} else {
		budget := geminiThinkingBudget(resolved, model.ID)
		if customBudgets != nil {
			var custom int
			switch resolved {
			case ThinkingMinimal:
				custom = customBudgets.Minimal
			case ThinkingLow:
				custom = customBudgets.Low
			case ThinkingMedium:
				custom = customBudgets.Medium
			case ThinkingHigh:
				custom = customBudgets.High
			}
			if custom != 0 {
				budget = custom
			}
		}
		config.ThinkingBudget = &budget
	}
	return config, nil
}

func resolveGoogleThinkingLevel(model *Model, level ThinkingLevel) (ThinkingLevel, error) {
	mapped, present := model.ThinkingLevelMap[level]
	resolved := level
	mapping := "undefined"
	if present {
		mapping = "null"
	}
	if mapped != nil {
		mapping = *mapped
		resolved = ThinkingLevel(strings.ToLower(*mapped))
	}
	switch resolved {
	case ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh:
		return resolved, nil
	default:
		return "", fmt.Errorf("Unsupported Google thinking level mapping for %s/%s: %s -> %s", model.ProviderMeta.ProviderID, model.ID, level, mapping)
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

// Stream preserves omitted native thinking, applies explicit logical/raw thinking, and exposes model/contents/config to OnPayload before REST lowering. A replacement payload also owns the dispatched model and configuration.
func (p *googleProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	// upstream: packages/ai/src/api/google-generative-ai.ts:stream
	if opts.Fetch != nil && opts.Fetch != http.DefaultClient {
		adapter := "Google Generative AI"
		if p.cfg.api == APIGoogleVertex {
			adapter = "Google Vertex"
		}
		return nil, fmt.Errorf("Custom fetch is not supported by the %s adapter", adapter)
	}
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("google: invalid transcript: %w", err)
	}
	model := &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh}}
	if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	} else if generated, ok := LookupModel(p.cfg.Model); ok {
		model = generated.ToModel()
	}
	if p.cfg.ThinkingLevelMap != nil {
		model.ThinkingLevelMap = p.cfg.ThinkingLevelMap
	}
	resolved := prepareProviderToolFlow(CollapseSystemMessages(transcript))
	messages := resolved.Messages()
	contents := geminiConvertMessages(WithoutInitialSystemMessage(messages), p.cfg.ProviderID, p.cfg.Model, model.Capabilities.SupportsImages)

	req := geminiRequest{
		Contents: contents,
	}

	if systemPrompt := GetCurrentSystemPrompt(messages[:min(1, len(messages))]); systemPrompt != "" {
		req.SystemInstruction = &geminiContent{
			// The Google SDK's tContent wraps a string systemInstruction as user content.
			Role:  "user",
			Parts: []geminiPart{{Text: new(sanitizeSurrogates(systemPrompt))}},
		}
	}

	tools := GetCurrentTools(messages)
	if len(tools) > 0 {
		convertedTools, usesStrictMode, err := geminiConvertTools(tools, false, supportsGoogleStrictToolSampling(model.ID))
		if err != nil {
			return nil, fmt.Errorf("google: convert tools: %w", err)
		}
		req.Tools = convertedTools
		// Upstream google-shared.ts resolveGoogleFunctionCallingMode leaves the default absent and gives explicit none/any precedence over strict mode.
		choice, _ := opts.ToolChoice.(string)
		mode := ""
		switch {
		case choice == "none":
			mode = "NONE"
		case choice == "any":
			mode = "ANY"
		case usesStrictMode:
			mode = "VALIDATED"
		case choice != "":
			mode = "AUTO"
		}
		if mode != "" {
			req.ToolConfig = &geminiToolConfig{
				FunctionCallingConfig: &geminiFuncCallingConfig{Mode: mode},
			}
		}
	}

	genConfig := &geminiGenerationConfig{}
	if opts.TemperatureSet || opts.Temperature != 0 {
		genConfig.Temperature = new(opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		mt := opts.MaxTokens
		genConfig.MaxOutputTokens = &mt
	}

	var tc *geminiThinkingConfig
	var err error
	if raw := opts.GoogleThinking; raw != nil {
		if model.ProviderMeta.Reasoning || opts.IsReasoning {
			if raw.Enabled {
				tc = &geminiThinkingConfig{IncludeThoughts: new(true)}
				if raw.Level != "" {
					tc.ThinkingLevel = string(raw.Level)
				} else {
					tc.ThinkingBudget = raw.BudgetTokens
				}
			} else {
				tc, err = buildGeminiThinkingConfig(model, ThinkingOff, true, nil)
			}
		}
	} else if opts.Thinking != "" {
		tc, err = buildGeminiThinkingConfig(model, opts.Thinking, opts.IsReasoning, opts.ThinkingBudgets)
	}
	if err != nil {
		return nil, err
	}
	genConfig.ThinkingConfig = tc
	req.GenerationConfig = genConfig

	requestModel := p.cfg.Model
	var body []byte
	if opts.OnPayload != nil {
		payload := req.parameters(requestModel)
		next, payloadErr := opts.OnPayload(payload, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if payloadErr != nil {
			return nil, fmt.Errorf("google: onPayload: %w", payloadErr)
		}
		var value any = payload
		if next != nil {
			value = next
		}
		requestModel, body, err = marshalGeminiParameters(value)
	} else {
		body, err = json.Marshal(req)
	}
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
	urlBuf.WriteString(strings.TrimPrefix(requestModel, "models/"))
	urlBuf.WriteString(":streamGenerateContent?alt=sse")

	// The request owns a cancelable context so that closing the stream unblocks a body read.
	requestContext, abort := context.WithCancel(ctx)
	streaming := false
	defer func() {
		if !streaming {
			abort()
		}
	}()
	httpReq, err := http.NewRequestWithContext(requestContext, http.MethodPost, urlBuf.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", PiUserAgent())
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("x-goog-api-key", p.cfg.APIKey)
	}
	if p.cfg.accessToken != nil {
		token, err := p.cfg.accessToken(ctx, opts.Env)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range p.cfg.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}
	applyProviderHeaders(httpReq, opts.Headers)

	resp, err := providerHTTPClient(p.client, opts.Fetch).Do(httpReq) //nolint:bodyclose // body closed via defer in SSE goroutine below
	if err != nil {
		return nil, fmt.Errorf("google: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("google: HTTP %d: %s", resp.StatusCode, string(b))
	}

	builder := newObservedProviderBuilder(ctx, p.cfg.api, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	builder.setProviderEventObserver(opts, providerEventModel(p.cfg.ModelMetadata, p.cfg.api, p.cfg.ProviderID, p.cfg.Model))
	builder.abort = abort
	_, builder.managed = resp.Body.(*observedResponseBody)
	streaming = true
	go builder.runResponse(func() error { return p.streamResponse(ctx, resp, builder) })
	return builder.stream, nil
}

// streamResponse runs Pi's provider body after the response headers have arrived: the SDK promise chain, `start`, and the `for await` loop over the chunks (google-generative-ai.ts:100-268). An observed body runs as one executor turn so that the consumer of the event stream interleaves with it as it does with Pi's provider; any other body is read on this goroutine.
func (p *googleProvider) streamResponse(ctx context.Context, resp *http.Response, builder *assistantStreamBuilder) error {
	observed, ok := resp.Body.(*observedResponseBody)
	if !ok {
		defer func() { _ = resp.Body.Close() }()
		p.parseGeminiSSE(ctx, resp.Body, builder)
		return nil
	}
	return builder.responseTurn(func() error {
		p.runObservedResponse(ctx, observed, builder)
		return nil
	})
}

func (p *googleProvider) runObservedResponse(ctx context.Context, body *observedResponseBody, builder *assistantStreamBuilder) {
	source := newGoogleUndiciBody(ctx, builder.turn.executor, newObservedBodyReadiness(body), builder.abort)
	defer source.close()
	runGoogleTurn(ctx, builder, &googleFetchResponse{body: source.stream}, googleUndiciFetchRounds)
}

// runGoogleTurn is the body of Pi's provider IIFE from the moment fetch() is about to resolve until the terminal event. fetchRounds is how long fetch() takes to settle after the socket read that carried the headers, which is where this turn begins.
func runGoogleTurn(ctx context.Context, builder *assistantStreamBuilder, response *googleFetchResponse, fetchRounds int) {
	turn := builder.turn
	executor := turn.executor
	fail := func(err error) {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		builder.failUnfinished(reason, err)
	}
	fetched := newTslibPromise(executor)
	session := &googleSDKStream{}
	defer session.close()
	sdk := googleGenerateContentStream(executor, fetched, googleUndiciJSONRounds, session)
	afterReactions(executor, fetchRounds, func() { fetched.resolve(response) })
	// `const googleStream = await retryGoogleRequest(() => client.models.generateContentStream(params), options);` (google-generative-ai.ts:100)
	settled := awaitContinuation(turn, googleRetryGoogleRequest(sdk).settled)
	if settled.err != nil {
		fail(settled.err)
		return
	}
	// stream.push({ type: "start", partial: output }); (google-generative-ai.ts:102)
	builder.start()
	state := &googleStreamState{builder: builder}
	// `for await (const chunk of googleStream)` awaits each next() promise (google-generative-ai.ts:107).
	err := tslibForAwait(turn, settled.value.(*tslibAsyncIterator), func(value any) (bool, error) {
		chunk := value.(*geminiStreamChunk)
		if err := builder.observeProviderEvent(chunk.raw); err != nil { // google-generative-ai.ts:107
			return false, err
		}
		state.handle(chunk)
		// The loop body's synchronous segment ends here: consumers that resume before the next chunk see the state it left, not the state at each push.
		builder.publish()
		return false, nil
	})
	if err != nil {
		fail(err)
		return
	}
	state.finish(ctx)
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

// googleStreamState is the per-response state of Pi's `for await (const chunk of googleStream)` loop body (google-generative-ai.ts:103-235).
type googleStreamState struct {
	builder      *assistantStreamBuilder
	usage        Usage
	finishReason string
	sawToolCall  bool
	// inThinking tracks whether the open block is a text or thinking block, to emit proper deltas.
	inThinking bool
}

func (state *googleStreamState) handle(chunk *geminiStreamChunk) {
	builder := state.builder
	if chunk.ResponseID != "" {
		// `output.responseId ||= chunk.responseId` (google-generative-ai.ts:109). Pi's Google provider does not read modelVersion into responseModel.
		builder.setResponseMetadata(chunk.ResponseID, "", "", "", nil)
	}
	// Process candidates
	if len(chunk.Candidates) > 0 {
		candidate := chunk.Candidates[0]
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
					if isThinking && !state.inThinking {
						state.inThinking = true
					} else if !isThinking && state.inThinking {
						state.inThinking = false
					}

					// Pi appends the text and retains the signature before it pushes the delta (google-generative-ai.ts:150-176), so the block is opened first and the delta is the last mutation before its push.
					if isThinking {
						builder.thinkingStart(ThinkingContent{})
						if part.ThoughtSignature != "" {
							builder.thinkingSignature(part.ThoughtSignature)
						}
						builder.thinkingDelta(text, false)
					} else {
						builder.textStart("")
						if part.ThoughtSignature != "" {
							builder.textSignature(part.ThoughtSignature)
						}
						builder.textDelta(text)
					}
				}

				// Function call
				if part.FunctionCall != nil {
					state.sawToolCall = true
					fc := part.FunctionCall
					toolID := googleToolCallID(builder.partial.API, fc.generatedIDName(), fc.ID, builder.partial.Content)

					// Pi: arguments = args ?? {}, delta = JSON.stringify(arguments).
					var parsed ToolCall
					parsed.SetStreamingArguments(string(fc.Args))
					argsJSON, _ := parsed.ArgumentsJSON()

					// Pi builds the whole tool call, arguments included, before it pushes toolcall_start (google-generative-ai.ts:200-215).
					builder.toolCallDelta(streamToolCallDelta{
						index: partIdx, id: toolID, name: fc.Name, argumentsDelta: string(argsJSON), initialArguments: parsed.Arguments, thoughtSignature: part.ThoughtSignature,
					})
					builder.endToolCall(partIdx)
				}
			}
		}
		// The finish reason is applied after the parts, as `output.stopReason` is in Pi (google-generative-ai.ts:214-220): events pushed for this chunk still describe the message by reference.
		if candidate.FinishReason != "" {
			state.finishReason = candidate.FinishReason
			builder.setResponseMetadata("", "", state.finishReason, "", nil)
			stopReason, _ := mapGoogleFinishReason(state.finishReason)
			if stopReason == StopReasonStop && state.sawToolCall {
				stopReason = StopReasonToolUse
			}
			builder.partial.StopReason = stopReason
		}
	}

	// Usage metadata
	if chunk.UsageMetadata != nil {
		um := chunk.UsageMetadata
		state.usage = Usage{
			Input:       um.PromptTokenCount - um.CachedContentTokenCount,
			Output:      um.CandidatesTokenCount + um.ThoughtsTokenCount,
			Reasoning:   new(um.ThoughtsTokenCount),
			CacheRead:   um.CachedContentTokenCount,
			TotalTokens: um.TotalTokenCount,
		}
		builder.calculateCost(&state.usage)
		builder.setUsage(&state.usage)
	}
}

// finish ends the open blocks, applies the stop-reason checks that follow the loop (google-generative-ai.ts:237-264) and pushes the terminal event.
func (state *googleStreamState) finish(ctx context.Context) {
	builder := state.builder
	builder.finishBlocks()
	if ctx.Err() != nil {
		builder.failUnfinished(StopReasonAborted, errors.New("Request was aborted"))
		return
	}
	stopReason, errorMessage := mapGoogleFinishReason(state.finishReason)
	if stopReason == StopReasonStop && state.sawToolCall {
		stopReason = StopReasonToolUse
	}
	if stopReason == StopReasonError {
		builder.failUnfinished(stopReason, errors.New(errorMessage))
		return
	}
	builder.done(stopReason, &state.usage, errorMessage)
}

// parseGeminiSSE drives the loop from the SSE decoder when the response body is not observed by the continuation executor.
func (p *googleProvider) parseGeminiSSE(ctx context.Context, r io.Reader, builder *assistantStreamBuilder) {
	state := &googleStreamState{builder: builder}
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
		chunk.raw = json.RawMessage(data)
		if err := builder.observeProviderEvent(chunk.raw); err != nil {
			builder.fail(StopReasonError, err)
			return
		}
		state.handle(&chunk)
	}

	if err := decoder.Err(); err != nil && ctx.Err() == nil {
		builder.fail(StopReasonError, err)
		return
	}
	if err := ctx.Err(); err != nil {
		builder.fail(StopReasonAborted, err)
		return
	}
	stopReason, errorMessage := mapGoogleFinishReason(state.finishReason)
	if stopReason == StopReasonStop && state.sawToolCall {
		stopReason = StopReasonToolUse
	}
	if stopReason == StopReasonError {
		builder.fail(stopReason, errors.New(errorMessage))
		return
	}
	builder.done(stopReason, &state.usage, errorMessage)
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

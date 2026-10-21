package ai

// Ports packages/ai/src/api/google-generative-ai.ts.
// Ports packages/ai/src/api/google-shared.ts.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
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
func marshalGeminiParameters(payload any, api API) (string, []byte, error) {
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
	body := map[string]json.RawMessage{"contents": params.Contents}
	var text string
	if json.Unmarshal(params.Contents, &text) == nil {
		if body["contents"], err = json.Marshal([]geminiContent{{Role: "user", Parts: []geminiPart{{Text: &text}}}}); err != nil {
			return "", nil, err
		}
	}
	generation := map[string]json.RawMessage{}
	// These generation fields remain under generationConfig in the Google REST request.
	wire := geminiWireFor(api)
	for _, name := range wire.generationConfig {
		if value := params.Config[name]; len(value) > 0 && string(value) != "null" {
			generation[name] = value
		}
	}
	if params.Config != nil {
		if body["generationConfig"], err = json.Marshal(generation); err != nil {
			return "", nil, err
		}
	}
	for _, name := range []string{"tools", "toolConfig", "safetySettings", "cachedContent", "serviceTier", "labels", "modelArmorConfig"} {
		if value := params.Config[name]; len(value) > 0 && string(value) != "null" {
			body[name] = value
		}
	}
	if value := params.Config["systemInstruction"]; len(value) > 0 && string(value) != "null" {
		body["systemInstruction"] = value
		var instruction string
		if json.Unmarshal(value, &instruction) == nil {
			if body["systemInstruction"], err = json.Marshal(geminiContent{Role: "user", Parts: []geminiPart{{Text: &instruction}}}); err != nil {
				return "", nil, err
			}
		}
	}
	lowered, err := wire.lowerBody(body)
	if err != nil {
		return "", nil, err
	}
	encoded, err = marshalRequestBody(lowered)
	return params.Model, encoded, err
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// isThinkingPart reports whether a part is model thinking, which Gemini marks with thought: true.
// upstream: packages/ai/src/api/google-shared.ts:128 (isThinkingPart)
func isThinkingPart(part geminiPart) bool {
	return part.Thought != nil && *part.Thought
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

// geminiFuncResponse writes the members in the order convertMessages creates them: name, response, parts, id (google-shared.ts:312-317).
// The SDK passes a functionResponse through, so its image parts keep inlineData's mimeType before data.
type geminiFuncResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	Parts    []geminiPart   `json:"parts,omitempty"`
	ID       string         `json:"id,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

// geminiFunctionDecl is one declaration convertTools builds, a JavaScript object (Record<string, unknown>) whose members JSON.stringify
// writes in creation order: name, description, then the schema as parameters (useParameters) or as parametersJsonSchema
// (google-shared.ts:391-397). The SDK passes it through, so the schema keeps the tool's member order.
type geminiFunctionDecl []geminiDeclMember

// geminiDeclMember is one member of a declaration.
type geminiDeclMember struct {
	Key   string
	Value any
}

// MarshalJSON writes the members in order.
func (decl geminiFunctionDecl) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for i, member := range decl {
		if i > 0 {
			out = append(out, ',')
		}
		out = jsstring.AppendString(out, member.Key)
		out = append(out, ':')
		value, err := marshalRequestBody(member.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, value...)
	}
	return append(out, '}'), nil
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

// ResolvedGoogleThinkingLevel is a ThinkingLevel without "xhigh" and "max" (google-shared.ts ResolvedGoogleThinkingLevel).
type ResolvedGoogleThinkingLevel string

const (
	ResolvedGoogleThinkingLevelMinimal ResolvedGoogleThinkingLevel = "minimal"
	ResolvedGoogleThinkingLevelLow     ResolvedGoogleThinkingLevel = "low"
	ResolvedGoogleThinkingLevelMedium  ResolvedGoogleThinkingLevel = "medium"
	ResolvedGoogleThinkingLevelHigh    ResolvedGoogleThinkingLevel = "high"
)

// ToGoogleThinkingLevel maps a resolved level to Google's enum value (google-shared.ts:85 toGoogleThinkingLevel).
func ToGoogleThinkingLevel(level ResolvedGoogleThinkingLevel) GoogleThinkingLevel {
	switch level {
	case ResolvedGoogleThinkingLevelMinimal:
		return GoogleThinkingLevelMinimal
	case ResolvedGoogleThinkingLevelLow:
		return GoogleThinkingLevelLow
	case ResolvedGoogleThinkingLevelMedium:
		return GoogleThinkingLevelMedium
	case ResolvedGoogleThinkingLevelHigh:
		return GoogleThinkingLevelHigh
	}
	return ""
}

// GoogleSdkThinkingLevel is the @google/genai ThinkingLevel enum a thinkingConfig carries (google-shared.ts GoogleSdkThinkingLevel).
type GoogleSdkThinkingLevel string

// googleSdkThinkingLevels is GOOGLE_SDK_THINKING_LEVEL_MAP (google-shared.ts:36-42): each Google API level and the SDK enum value it selects.
var googleSdkThinkingLevels = map[GoogleThinkingLevel]GoogleSdkThinkingLevel{
	GoogleThinkingLevelUnspecified: "THINKING_LEVEL_UNSPECIFIED",
	GoogleThinkingLevelMinimal:     "MINIMAL",
	GoogleThinkingLevelLow:         "LOW",
	GoogleThinkingLevelMedium:      "MEDIUM",
	GoogleThinkingLevelHigh:        "HIGH",
}

// ToGoogleSdkThinkingLevel selects the SDK enum value for a Google API level (google-shared.ts:98 toGoogleSdkThinkingLevel).
func ToGoogleSdkThinkingLevel(level GoogleThinkingLevel) GoogleSdkThinkingLevel {
	return googleSdkThinkingLevels[level]
}

// ─── SSE response types ──────────────────────────────────────────────────────

type geminiStreamChunk struct {
	// raw is the chunk's JSON as received, for OnProviderStreamEvent.
	raw           json.RawMessage
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ResponseID    string               `json:"responseId,omitempty"`
	// read is what the JS read of a mistyped chunk yields beyond the rewritten members (decodeGeminiChunk).
	read geminiChunkRead
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
	gemini3ProRe   = lazyregexp.New(`gemini-3(?:\.\d+)?-pro`)
	gemini3FlashRe = lazyregexp.New(`gemini-3(?:\.\d+)?-flash`)
	// Gemini 4 and later, such as gemini-4-argon (Pi 1692bba6, #10890).
	gemini4PlusRe = lazyregexp.New(`gemini-(?:[4-9]|[1-9]\d)`)
	gemma4Re      = lazyregexp.New(`gemma-?4`)
)

func isGemini3Pro(modelID string) bool   { return gemini3ProRe.MatchString(modelID) }
func isGemini3Flash(modelID string) bool { return gemini3FlashRe.MatchString(modelID) }
func isGemma4(modelID string) bool       { return gemma4Re.MatchString(modelID) }

// UsesGoogleThinkingLevel reports whether the model takes Gemini's discrete thinkingLevel control instead of the token-based thinkingBudget control. The levels a model supports come from its thinking-level map; this only selects the Google wire format.
// Mirrors upstream usesGoogleThinkingLevel (google-shared.ts), which lowercases the id before every comparison.
func UsesGoogleThinkingLevel(model *Model) bool {
	id := jsstring.ToLower(model.ID)
	return isGemini3Pro(id) || isGemini3Flash(id) || gemini4PlusRe.MatchString(id) || isGemma4(id) || id == "gemini-flash-latest" || id == "gemini-flash-lite-latest"
}

// ─── Message conversion ──────────────────────────────────────────────────────
// Mirrors upstream google-shared.ts convertMessages.

// RetainThoughtSignature keeps the signature already on a streamed block unless the incoming part carries a non-empty one.
// Ports packages/ai/src/api/google-shared.ts retainThoughtSignature: it never moves signatures across parts, it only prevents an
// overwrite with an empty value within one block.
func RetainThoughtSignature(existing, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

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
func getGeminiMajorVersion(modelID string) (float64, bool) {
	m := geminiMajorVersionRE.FindStringSubmatch(jsstring.ToLower(modelID))
	if m == nil {
		return 0, false
	}
	// Number.parseInt: a version longer than a float64 integer stays a large float, and one past float64's range is +Inf (ParseFloat's range error), so both still compare as at least 3.
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
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

// normalizeToolCallId is google-shared.ts convertMessages' normalizeToolCallId: a model that requires ids gets them restricted to [A-Za-z0-9_-]
// (per UTF-16 unit, as the regex replaces them) and cut to 64 units; other models keep them.
func normalizeToolCallId(modelID, id string) string {
	if !requiresToolCallId(modelID) {
		return id
	}
	return normalizeAnthropicToolCallID(id)
}

// googleTranscriptMessages is the transcript a Gemini request is built from: system messages collapsed and the tool flow resolved.
func googleTranscriptMessages(transcript TranscriptContext) []Message {
	return prepareProviderToolFlow(TranscriptContext{messages: collapsedMessages(transcript)}).messages
}

// GeminiContent is one Google `Content` of a generateContent request.
type GeminiContent = geminiContent

// ConvertGoogleMessages converts a transcript to the Google `Content[]` of model's request: system messages are
// collapsed, the leading one is dropped (it is sent as systemInstruction) and the rest is converted for the model's
// provider, id and image support. Ports google-shared.ts convertMessages(model, context).
func ConvertGoogleMessages(model *Model, transcript TranscriptContext) []GeminiContent {
	conversation := withoutInitialSystemMessage(collapsedMessages(transcript))
	transformed := TransformMessages(conversation, model, func(id string, _ *Model, _ AssistantMessage) string { return normalizeToolCallId(model.ID, id) })
	return geminiConvertMessages(transformed, model.ProviderMeta.ProviderID, model.ID, slices.Contains(model.Input, ModelInputImage))
}

// geminiConvertMessages converts the conversation after the caller has collapsed system messages and removed the leading one, so any system message is skipped.
func geminiConvertMessages(messages []Message, providerID, modelID string, supportsImages bool) []geminiContent {
	// An empty conversation is `contents: []` in the request, not null.
	contents := []geminiContent{}
	// pig divergence (D115): Pi pushes a Gemini 2.x result's image turn before the next result, which splits the function-response turn of parallel calls; the image turns wait until the next turn that is not a function response and keep result order.
	var imageTurns []geminiContent
	flushImageTurns := func() {
		contents = append(contents, imageTurns...)
		imageTurns = nil
	}
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
				flushImageTurns()
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
					call.Args, _ = block.argumentsJSONDefaulting(false)
					if requiresToolCallId(modelID) {
						call.ID = block.ID
					}
					part := geminiPart{FunctionCall: call}
					if sameProviderAndModel && isValidThoughtSignature(block.ThoughtSignature) {
						part.ThoughtSignature = block.ThoughtSignature
					}
					parts = append(parts, part)
				}
			}
			if len(parts) > 0 {
				flushImageTurns()
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
				// Whitespace-only text counts as no text (google-shared.ts hasText: textResult.trim(), Pi 4dd2b149).
				if trimJSWhitespace(text) == "" {
					text = ""
					if len(imageParts) > 0 {
						text = "(see attached image)"
					}
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
					response.ID = result.ToolCallID
				}
				responsePart := geminiPart{FunctionResponse: response}
				last := len(contents) - 1
				if last >= 0 && contents[last].Role == "user" && len(contents[last].Parts) > 0 && contents[last].Parts[0].FunctionResponse != nil {
					contents[last].Parts = append(contents[last].Parts, responsePart)
				} else {
					contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{responsePart}})
				}
				if len(imageParts) > 0 && !supportsMultimodalFunctionResponse(modelID) {
					imageTurns = append(imageTurns, geminiContent{Role: "user", Parts: append([]geminiPart{{Text: new("Tool result image:")}}, imageParts...)})
				}
				index++
			}
			index--
		}
	}
	flushImageTurns()
	return contents
}

func geminiConvertTools(tools []ToolSchema, useParameters, supportsStrictMode bool) ([]geminiToolDecl, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	decls := make([]geminiFunctionDecl, len(tools))
	for i, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		toolParameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		schemaKey := "parametersJsonSchema"
		if useParameters {
			// sanitizeForOpenApi rebuilds the object from Object.entries, so the members it keeps keep their order.
			schemaKey = "parameters"
			toolParameters.Schema, _ = sanitizeForOpenAPI(toolParameters.Schema).(map[string]any)
		}
		decls[i] = geminiFunctionDecl{{"name", tool.Name}, {"description", tool.Description}, {schemaKey, toolParameters}}
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}, nil
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

// mapToolChoice ports google-shared.ts mapToolChoice: an unknown choice is AUTO.
func mapToolChoice(choice string) string {
	switch choice {
	case "none":
		return "NONE"
	case "any":
		return "ANY"
	default:
		return "AUTO"
	}
}

// resolveGoogleFunctionCallingMode ports google-shared.ts resolveGoogleFunctionCallingMode: an explicit none or any wins over strict mode,
// any strict tool selects VALIDATED, and with no tool choice the mode stays absent (""; undefined upstream).
func resolveGoogleFunctionCallingMode(tools []ToolSchema, toolChoice string, supportsStrictMode bool) (string, error) {
	useStrictMode := false
	for _, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return "", err
		}
		useStrictMode = useStrictMode || strict != nil && *strict
	}
	if toolChoice == "none" || toolChoice == "any" {
		return mapToolChoice(toolChoice), nil
	}
	if useStrictMode {
		return "VALIDATED", nil
	}
	if toolChoice != "" {
		return mapToolChoice(toolChoice), nil
	}
	return "", nil
}

func supportsGoogleStrictToolSampling(modelID string) bool {
	major, ok := getGeminiMajorVersion(modelID)
	return ok && major >= 3
}

// Thinking configuration follows the selected model's map before selecting Google's level or token-budget wire format.

func buildGeminiThinkingConfig(model *Model, level ModelThinkingLevel, isReasoning bool, customBudgets *ThinkingBudgets) (*geminiThinkingConfig, error) {
	if !isReasoning {
		return nil, nil
	}
	if model == nil {
		model = &Model{}
	}
	usesLevel := UsesGoogleThinkingLevel(model)
	clamped := ClampThinkingLevel(model, level)
	if level == "" || clamped == ThinkingOff {
		return getDisabledGoogleThinkingConfig(model)
	}
	resolved, err := resolveGoogleThinkingLevel(model, clamped)
	if err != nil {
		return nil, err
	}
	config := &geminiThinkingConfig{IncludeThoughts: new(true)}
	if usesLevel {
		config.ThinkingLevel = string(ToGoogleSdkThinkingLevel(ToGoogleThinkingLevel(resolved)))
	} else {
		budget := geminiThinkingBudget(ModelThinkingLevel(resolved), model.ID)
		if custom, ok := customBudgets.configured(ModelThinkingLevel(resolved)); ok {
			budget = custom
		}
		config.ThinkingBudget = &budget
	}
	return config, nil
}

// getDisabledGoogleThinkingConfig is the thinking config that switches thinking off: a zero token budget, or the lowest level
// the model's map still allows when the model takes the discrete level control (google-shared.ts:102 getDisabledGoogleThinkingConfig).
func getDisabledGoogleThinkingConfig(model *Model) (*geminiThinkingConfig, error) {
	if !UsesGoogleThinkingLevel(model) {
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
	return &geminiThinkingConfig{ThinkingLevel: string(ToGoogleSdkThinkingLevel(ToGoogleThinkingLevel(resolved)))}, nil
}

func resolveGoogleThinkingLevel(model *Model, level ModelThinkingLevel) (ResolvedGoogleThinkingLevel, error) {
	mapped, present := model.ThinkingLevelMap[level]
	resolved := level
	mapping := "undefined"
	if present {
		mapping = "null"
	}
	if mapped != nil {
		mapping = *mapped
		resolved = ModelThinkingLevel(jsstring.ToLower(*mapped))
	}
	switch ResolvedGoogleThinkingLevel(resolved) {
	case ResolvedGoogleThinkingLevelMinimal, ResolvedGoogleThinkingLevelLow, ResolvedGoogleThinkingLevelMedium, ResolvedGoogleThinkingLevelHigh:
		return ResolvedGoogleThinkingLevel(resolved), nil
	default:
		return "", fmt.Errorf("Unsupported Google thinking level mapping for %s/%s: %s -> %s", model.ProviderMeta.ProviderID, model.ID, level, mapping)
	}
}

func supportsMultimodalFunctionResponse(modelID string) bool {
	if major, ok := getGeminiMajorVersion(modelID); ok {
		return major >= 3
	}
	return true
}

func geminiThinkingBudget(level ModelThinkingLevel, modelID string) int {
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
	// pig divergence (D48): the agent loop asks for the tool-flow repair.
	if opts.RepairToolFlow {
		transcript = transcript.withRepairedToolFlow()
	}
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("google: invalid transcript: %w", err)
	}
	// A model outside the catalog still has the provider's API, as every Pi Model has an api: transformMessages' isSameModel compares it with the assistant message's.
	model := &Model{ID: p.cfg.Model, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}, ProviderMeta: ProviderMetadata{API: cmp.Or(p.cfg.api, APIGoogleGenerativeAI)}}
	if p.cfg.ModelMetadata != nil {
		selected := *p.cfg.ModelMetadata
		model = &selected
	} else if generated, ok := LookupModel(p.cfg.ProviderID + "/" + p.cfg.Model); ok {
		model = generated.ToModel()
	} else if generated, ok := LookupModel(p.cfg.Model); ok {
		model = generated.ToModel()
	}
	if p.cfg.ThinkingLevelMap != nil {
		model.ThinkingLevelMap = p.cfg.ThinkingLevelMap
	}
	messages := googleTranscriptMessages(transcript)
	conversionModel := *model
	conversionModel.ID = p.cfg.Model
	conversionModel.ProviderMeta.ProviderID = p.cfg.ProviderID
	// A model built without catalog metadata has no API of its own; it is the provider's, which decides whether an earlier assistant turn came from the same model (transform-messages.ts:46-49).
	conversionModel.ProviderMeta.API = cmp.Or(conversionModel.ProviderMeta.API, p.cfg.api, APIGoogleGenerativeAI)
	contents := ConvertGoogleMessages(&conversionModel, transcript)

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

	tools := borrowedCurrentTools(messages)
	if len(tools) > 0 {
		convertedTools, err := geminiConvertTools(tools, false, supportsGoogleStrictToolSampling(model.ID))
		if err != nil {
			return nil, fmt.Errorf("google: convert tools: %w", err)
		}
		req.Tools = convertedTools
		choice, _ := toolChoiceName(opts.ToolChoice)
		mode, err := resolveGoogleFunctionCallingMode(tools, choice, supportsGoogleStrictToolSampling(model.ID))
		if err != nil {
			return nil, fmt.Errorf("google: convert tools: %w", err)
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
				tc, err = getDisabledGoogleThinkingConfig(model)
			}
		}
	} else if opts.Thinking != "" {
		tc, err = buildGeminiThinkingConfig(model, ModelThinkingLevel(opts.Thinking), opts.IsReasoning, opts.ThinkingBudgets)
	}
	if err != nil {
		return nil, err
	}
	genConfig.ThinkingConfig = tc
	req.GenerationConfig = genConfig

	requestModel := p.cfg.Model
	var body *requestBody
	// The @google/genai SDK Pi uses (tContents) throws before sending when `contents` is undefined, null or empty, in the payload the onPayload hook returned when there is one.
	noContents := len(req.Contents) == 0
	if opts.OnPayload != nil {
		payload := req.parameters(requestModel)
		next, payloadErr := opts.OnPayload(payload, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID}})
		if payloadErr != nil {
			return nil, fmt.Errorf("google: onPayload: %w", payloadErr)
		}
		var value any = payload
		if next != nil {
			value = payloadReplacement(next)
		}
		body, err = marshalSpliced(value, requestStrings, func(value any) ([]byte, error) {
			model, encoded, err := marshalGeminiParameters(value, cmp.Or(p.cfg.api, APIGoogleGenerativeAI))
			if err != nil {
				return nil, err
			}
			requestModel = model
			var sent struct {
				Contents []json.RawMessage `json:"contents"`
			}
			noContents = json.Unmarshal(encoded, &sent) == nil && len(sent.Contents) == 0
			return encoded, nil
		})
	} else {
		body, err = marshalSpliced(req, requestStrings, func(value any) ([]byte, error) {
			wire, err := value.(geminiRequest).wireBody(cmp.Or(p.cfg.api, APIGoogleGenerativeAI))
			if err != nil {
				return nil, err
			}
			return marshalRequestBody(wire)
		})
	}
	if err != nil {
		return nil, fmt.Errorf("google: marshal request: %w", err)
	}
	if noContents {
		return nil, errors.New("contents are required")
	}
	checkRequestSegments(ctx, cmp.Or(p.cfg.api, APIGoogleGenerativeAI), body)

	endpoint, err := googleSDKURL(p.cfg.BaseURL, p.cfg.APIVersion, "models/"+strings.TrimPrefix(requestModel, "models/")+":streamGenerateContent?alt=sse")
	if err != nil {
		return nil, err
	}

	// The request owns a cancelable context so that closing the stream unblocks a body read.
	requestContext, abort := context.WithCancel(ctx)
	streaming := false
	defer func() {
		if !streaming {
			abort()
		}
	}()
	// `await retryGoogleRequest(() => client.models.generateContentStream(params), options)` (google-generative-ai.ts:49): each attempt is a fresh request, and a
	// status error from 400 to 599 is retried by its status alone, as the SDK's ApiError carries no headers.
	// @google/genai does not retry, so retryGoogleRequest is the only retry layer: the transport retry is off for each attempt, and the
	// request's retry budget (the per-request override, else retry.provider) moves to retryGoogleRequest.
	retryOptions := providerRetryStreamOptions(requestContext, opts)
	attemptContext := WithProviderMaxRetries(requestContext, 0)
	resp, err := RetryGoogleRequest(requestContext, func() (*http.Response, error) { //nolint:bodyclose // the attempt returns the open response and the SSE goroutine below closes its body
		httpReq, err := newFetchRequest(attemptContext, http.MethodPost, endpoint, body.reader(), false)
		if err != nil {
			return nil, err
		}
		body.prepare(httpReq)
		httpReq.Header.Set("Content-Type", "application/json")
		// undici's default accept header, which the SDK's fetch sends.
		httpReq.Header.Set("Accept", "*/*")
		applyIdentityHeaders(httpReq.Header, ClientIdentityTarget{Provider: p.cfg.ProviderID, BaseURL: p.cfg.BaseURL}, opts.Identity)
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

		resp, err := providerHTTPClient(p.client, opts.Fetch).Do(httpReq)
		if err != nil {
			// @google/genai's fetch rejection is the error itself ("fetch failed" for a transport failure); the Go client's "Post <url>:" decoration is not part of it.
			if requestError, ok := err.(*url.Error); ok { //nolint:errorlint // Only the direct client.Do decoration is stripped.
				return nil, requestError.Err
			}
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := readWebText(resp.Body)
			_ = resp.Body.Close()
			return nil, googleHTTPError(resp, b)
		}
		return resp, nil
	}, retryOptions)
	if err != nil {
		// retryProviderRequest throws createAbortError() once the signal is aborted (provider-retry.ts:68-72).
		if ctx.Err() != nil && errors.Is(err, errProviderRequestAborted) {
			return nil, &nodeTransportError{message: "Request aborted", cause: ctx.Err()}
		}
		return nil, err
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
		if err := state.handle(chunk); err != nil {
			return false, err
		}
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
	// finishSawToolCall is sawToolCall when the latest finish reason arrived: Pi turns a "stop" into "toolUse" only if the message holds a tool call at that
	// chunk (google-generative-ai.ts:214-220), and never again, so a tool call in a later chunk does not.
	finishSawToolCall bool
	// inThinking tracks whether the open block is a text or thinking block, to emit proper deltas.
	inThinking bool
}

func (state *googleStreamState) handle(chunk *geminiStreamChunk) error {
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
				// `part.text !== undefined` (google-generative-ai.ts:63): an empty text part still opens a block and pushes an empty delta.
				if part.Text != nil {
					isThinking := isThinkingPart(part)

					// Transition between text and thinking
					if isThinking && !state.inThinking {
						state.inThinking = true
					} else if !isThinking && state.inThinking {
						state.inThinking = false
					}

					// Pi appends the text and retains the signature before it pushes the delta (google-generative-ai.ts:150-176), so the block is opened first and the delta is the last mutation before its push.
					if isThinking {
						builder.thinkingStart(ThinkingContent{})
						// Only a retained change is written: writing back an unset signature would mark it as an explicit empty one, which Pi never produces here (google-shared.ts:141-144).
						if existing := builder.activeThinkingSignature(); RetainThoughtSignature(existing, part.ThoughtSignature) != existing {
							builder.thinkingSignature(RetainThoughtSignature(existing, part.ThoughtSignature))
						}
						builder.thinkingDelta(text, false)
					} else {
						builder.textStart("")
						if existing := builder.activeTextSignature(); RetainThoughtSignature(existing, part.ThoughtSignature) != existing {
							builder.textSignature(RetainThoughtSignature(existing, part.ThoughtSignature))
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
					parsed.defaultNullishArguments()
					argsJSON, _ := parsed.ArgumentsJSON()

					// Pi builds the whole tool call, arguments included, before it pushes toolcall_start (google-generative-ai.ts:200-215).
					builder.toolCallDelta(streamToolCallDelta{
						index: partIdx, id: toolID, name: fc.Name, argumentsDelta: string(argsJSON), initialArguments: parsed.Arguments, thoughtSignature: part.ThoughtSignature,
					})
					builder.endToolCall(partIdx)
				}
			}
		}
		if chunk.read.partsErr != nil {
			return chunk.read.partsErr
		}
		if text := chunk.read.finishReason; text != nil {
			// A finish reason that is not a string matches no member of mapStopReason's === switch, whatever its String() form.
			builder.setResponseMetadata("", "", *text, "", nil)
			return fmt.Errorf("Unhandled stop reason: %s", *text)
		}
		// The finish reason is applied after the parts, as `output.stopReason` is in Pi (google-generative-ai.ts:214-220): events pushed for this chunk still describe the message by reference.
		if candidate.FinishReason != "" {
			state.finishReason = candidate.FinishReason
			state.finishSawToolCall = state.sawToolCall
			builder.setResponseMetadata("", "", state.finishReason, "", nil)
			// mapStopReason throws inside the chunk loop (google-generative-ai.ts:226, google-vertex.ts:234): an unhandled reason fails the stream here, before this chunk's usage metadata and before any later chunk.
			stopReason, err := mapStopReason(state.finishReason)
			if err != nil {
				return err
			}
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
	return nil
}

// finish ends the open blocks, applies the stop-reason checks that follow the loop (google-generative-ai.ts:237-264) and pushes the terminal event.
func (state *googleStreamState) finish(ctx context.Context) {
	builder := state.builder
	builder.finishBlocks()
	if ctx.Err() != nil {
		builder.failUnfinished(StopReasonAborted, errors.New("Request was aborted"))
		return
	}
	stopReason, errorMessage := mapGoogleFinishReason(state.builder.partial.API, state.finishReason)
	if stopReason == StopReasonStop && state.finishSawToolCall {
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
		if err := decodeGeminiChunk(data, &chunk); err != nil {
			builder.fail(StopReasonError, googleChunkJSONError(data, err))
			return
		}
		chunk.raw = json.RawMessage(data)
		if err := builder.observeProviderEvent(chunk.raw); err != nil {
			builder.fail(StopReasonError, err)
			return
		}
		if err := state.handle(&chunk); err != nil {
			builder.fail(StopReasonError, err)
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
	stopReason, errorMessage := mapGoogleFinishReason(state.builder.partial.API, state.finishReason)
	if stopReason == StopReasonStop && state.finishSawToolCall {
		stopReason = StopReasonToolUse
	}
	if stopReason == StopReasonError {
		builder.fail(stopReason, errors.New(errorMessage))
		return
	}
	builder.done(stopReason, &state.usage, errorMessage)
}

// mapStopReasonString maps a Gemini finish reason to a stop reason; every reason other than STOP and MAX_TOKENS is an error
// (google-shared.ts:474 mapStopReasonString over a raw string).
func mapStopReasonString(reason string) StopReason {
	switch reason {
	case "STOP":
		return StopReasonStop
	case "MAX_TOKENS":
		return StopReasonLength
	default:
		return StopReasonError
	}
}

// mapStopReason is google-shared.ts:441 mapStopReason: the Gemini FinishReason enum members map to stop, length or error, and any other value reaches the
// exhaustive switch's default, which throws `Unhandled stop reason: <reason>`.
func mapStopReason(reason string) (StopReason, error) {
	switch reason {
	case "STOP", "MAX_TOKENS", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "SAFETY", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION", "IMAGE_OTHER", "RECITATION",
		"FINISH_REASON_UNSPECIFIED", "OTHER", "LANGUAGE", "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "NO_IMAGE":
		return mapStopReasonString(reason), nil
	default:
		return StopReasonError, fmt.Errorf("Unhandled stop reason: %s", reason)
	}
}

// mapGoogleFinishReason applies mapStopReason to the stream's raw finish reason and derives the error message the stream end throws
// (google-generative-ai.ts:276-283): a stream without a finish reason, an unhandled reason, or an error stop with its raw reason.
func mapGoogleFinishReason(api API, reason string) (StopReason, string) {
	if reason == "" {
		// google-vertex.ts:212 names the Vertex API in its own message.
		if api == APIGoogleVertex {
			return StopReasonError, "Google Vertex stream ended without a finish reason"
		}
		return StopReasonError, "Google stream ended without a finish reason"
	}
	stopReason, err := mapStopReason(reason)
	if err != nil {
		return StopReasonError, err.Error()
	}
	if stopReason == StopReasonError {
		return StopReasonError, "Provider stopped with: " + reason
	}
	return stopReason, ""
}

type googleErrorBody struct {
	Error googleErrorDetail `json:"error"`
}

type googleErrorDetail struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	Status  string `json:"status"`
}

// googleHTTPError reconstructs @google/genai's throwErrorIfNotOK: a JSON response is parsed and stringified again, any other response body becomes
// {"error":{"message":<text>,"code":<status>,"status":<status text>}}; a status from 400 to 599 is an ApiError carrying it.
func googleHTTPError(resp *http.Response, body []byte) error {
	var message string
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		if err := jsonparse.Validate(body); err != nil {
			// response.json() rejects with the SyntaxError itself.
			return err
		}
		canonical, err := jsonstringify.Canonicalize(body)
		if err != nil {
			return err
		}
		message = string(canonical)
	} else {
		text, err := jsstring.MarshalJSON(googleErrorBody{Error: googleErrorDetail{Message: jsstring.FromUTF8(body), Code: resp.StatusCode, Status: strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" ")}})
		if err != nil {
			return err
		}
		message = string(text)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 600 {
		return &providerError{status: new(resp.StatusCode), message: message}
	}
	return errors.New(message)
}

// googleStatusError is an error that carries an HTTP status but no response headers, the shape of @google/genai's ApiError.
type googleStatusError interface {
	error
	ProviderStatus() *int
}

// googleHeaderlessError gives a status-only error the `headers: undefined` that retryProviderRequest requires of a provider error.
type googleHeaderlessError struct{ googleStatusError }

func (googleHeaderlessError) ProviderHeaders() http.Header { return nil }
func (e googleHeaderlessError) Unwrap() error              { return e.googleStatusError }

// RetryGoogleRequest is google-shared.ts retryGoogleRequest: retryProviderRequest for the Google SDK, whose ApiError has a status but no
// headers. A status-only error is given `headers: undefined`, so it is retried by status, and the caller still receives the error it threw.
// Retries and the delay cap come from the stream options; the context is their AbortSignal.
func RetryGoogleRequest[T any](ctx context.Context, request func() (T, error), options ...StreamOptions) (T, error) {
	var opts StreamOptions
	if len(options) > 0 {
		opts = options[0]
	}
	value, err := RetryProviderRequest(ctx, func() (T, error) {
		value, err := request()
		if err != nil {
			if _, ok := errors.AsType[ProviderRequestError](err); !ok {
				if statusError, ok := errors.AsType[googleStatusError](err); ok {
					return value, googleHeaderlessError{statusError}
				}
			}
		}
		return value, err
	}, ProviderRetryOptions{MaxRetries: opts.MaxRetries, MaxRetryDelayMs: opts.MaxRetryDelayMs})
	if wrapped := (googleHeaderlessError{}); errors.As(err, &wrapped) {
		return value, wrapped.googleStatusError
	}
	return value, err
}

// googleChunkJSONError is the rejection of the SDK's Response.json() for a chunk: JSON.parse's SyntaxError for text that is not JSON.
func googleChunkJSONError(text string, err error) error {
	if syntax := jsonparse.Validate([]byte(text)); syntax != nil {
		return syntax
	}
	return fmt.Errorf("google: invalid SSE JSON: %w", err)
}

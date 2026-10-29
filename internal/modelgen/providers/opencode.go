package providers

// Ports packages/ai/catalog/providers/opencode.ts

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

// OpenAI-compatible endpoints reject the long prompt-cache retention parameter for these models.
var opencodeLongCacheRetentionUnsupportedModels = map[string]bool{
	"opencode:deepseek-v4-flash":   true,
	"opencode:deepseek-v4.1-flash": true,
	"opencode:deepseek-v4-pro":     true,
	"opencode:kimi-k2.5":           true,
	"opencode:kimi-k2.6":           true,
	"opencode:minimax-m2.7":        true,
	"opencode-go:kimi-k2.6":        true,
}

// OpenCode serves each model on one protocol-specific endpoint. Native protocols
// come before generic Chat Completions if models.dev ever lists several.
var opencodeProtocols = []string{"responses", "messages", "generate-content", "chat-completions"}

// opencodeServed is the API and base URL models.dev lists for a model, with optional compat.
type opencodeServed struct {
	api     string
	baseURL string
	compat  *Obj
}

var opencodeV1Suffix = regexp.MustCompile(`/v1/?$`)

// opencodeAPI returns the API and base URL models.dev lists for a model. Models served only on a
// protocol pi does not support fail strict generation and are skipped otherwise.
func opencodeAPI(ctx *ProviderContext, source *ModelsDevModel) (*opencodeServed, error) {
	var entry *ModelsDevAPI
	for _, protocol := range opencodeProtocols {
		for i := range source.APIs {
			if source.APIs[i].Protocol == protocol {
				entry = &source.APIs[i]
				break
			}
		}
		if entry != nil {
			break
		}
	}
	if entry == nil {
		protocols := make([]string, len(source.APIs))
		for i, candidate := range source.APIs {
			protocols[i] = candidate.Protocol
		}
		joined := strings.Join(protocols, ", ")
		if joined == "" {
			joined = "none"
		}
		message := fmt.Sprintf("models.dev lists no supported API for %s/%s (%s)", ctx.ProviderID, source.ID, joined)
		if ctx.Strict {
			return nil, fmt.Errorf("%s", message)
		}
		fmt.Fprintln(os.Stderr, "Skipping model: "+message)
		return nil, nil
	}
	switch entry.Protocol {
	case "responses":
		return &opencodeServed{"openai-responses", entry.BaseURL, O("sessionAffinityFormat", "openai-nosession")}, nil
	case "messages":
		// The Anthropic SDK appends /v1/messages itself.
		return &opencodeServed{api: "anthropic-messages", baseURL: opencodeV1Suffix.ReplaceAllString(entry.BaseURL, "")}, nil
	case "generate-content":
		return &opencodeServed{api: "google-generative-ai", baseURL: entry.BaseURL}, nil
	default:
		served := &opencodeServed{api: "openai-completions", baseURL: entry.BaseURL}
		if entry.AISDK == "@ai-sdk/alibaba" {
			served.compat = O("cacheControlFormat", "anthropic")
		}
		return served, nil
	}
}

var opencodeAnthropicSuffix = regexp.MustCompile(`/anthropic$`)

// opencodeModels returns OpenCode Zen and Go models, on the endpoints models.dev lists for them.
func opencodeModels(ctx *ProviderContext) ([]ChatModel, error) {
	provider := ctx.ProviderID
	return ModelsDevModels(ctx, provider, ModelsDevOptions{
		API: "openai-completions",
		// Replaced by the base URL of the API models.dev lists for each model.
		BaseURL:        "",
		SkipDeprecated: true,
		// Codex Spark is not usable through OpenCode.
		Include: func(source *ModelsDevModel) bool { return source.ID != "gpt-5.3-codex-spark" },
		Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
			served, err := opencodeAPI(ctx, source)
			if err != nil {
				return nil, err
			}
			if served == nil {
				return nil, nil
			}
			api, baseURL, compat := served.api, served.baseURL, served.compat
			id := model.Str("id")
			if provider == "opencode" && id == "grok-build-0.1" {
				compat = Spread(compat, O("supportsReasoningEffort", false))
			}
			if id == "kimi-k2.6" {
				// OpenCode Kimi K2.6 accepts Anthropic-style thinking objects
				// and rejects string thinking values or combined reasoning_effort.
				compat = Spread(compat, O("thinkingFormat", "deepseek", "supportsReasoningEffort", false))
			}
			// models.dev reports these as @ai-sdk/anthropic, but OpenCode Go serves
			// them through the OpenAI-compatible /openai/v1/chat/completions path.
			if provider == "opencode-go" && api == "anthropic-messages" && (id == "qwen3.5-plus" || id == "qwen3.6-plus") {
				api = "openai-completions"
				baseURL = opencodeAnthropicSuffix.ReplaceAllString(baseURL, "/openai/v1")
				// Qwen/DashScope uses enable_thinking at the top level.
				compat = Spread(compat, O("thinkingFormat", "qwen"))
			}
			if api == "openai-completions" {
				compat = Spread(compat, O("maxTokensField", "max_tokens"))
				if opencodeLongCacheRetentionUnsupportedModels[provider+":"+id] {
					compat.Set("supportsLongCacheRetention", false)
				}
			}
			var thinkingLevelMap *Obj
			if api == "google-generative-ai" {
				thinkingLevelMap = GoogleThinkingLevels(id, source.ReasoningOptions)
			} else if provider == "opencode-go" && id == "deepseek-v4.1-flash" {
				thinkingLevelMap = GetEffortThinkingLevelMap(source.ReasoningOptions)
			}
			result := Spread(model, O("api", api, "baseUrl", baseURL))
			if thinkingLevelMap != nil {
				result.Set("thinkingLevelMap", thinkingLevelMap)
			}
			if compat != nil {
				result.Set("compat", compat)
			}
			// OpenCode variants list Claude Sonnet 4/4.5 with 1M context, actual limit is 200K.
			if id == "claude-sonnet-4-5" || id == "claude-sonnet-4" {
				result.Set("contextWindow", 200000)
			}
			switch id {
			case "claude-opus-4-6", "claude-sonnet-4-6", "claude-opus-4.6", "claude-sonnet-4.6":
				result.Set("contextWindow", 1000000)
			}
			if id == "gpt-5.4" {
				result.Set("contextWindow", 272000)
				result.Set("maxTokens", 128000)
			}
			return result, nil
		},
	})
}

// opencodeCompletionsCompat is the OpenAI Completions compat shared by OpenCode Zen and Go.
func opencodeCompletionsCompat() *Obj {
	return O("supportsStore", false, "supportsDeveloperRole", false, "supportsStrictMode", true)
}

// finalizeOpencodeModel is the behavior shared by OpenCode Zen and Go.
func finalizeOpencodeModel(model ChatModel) {
	provider, id, api := model.Str("provider"), model.Str("id"), model.Str("api")
	if provider == "opencode-go" && id == "glm-5.2" {
		MergeThinking(model, O("off", nil, "minimal", nil, "low", nil, "medium", nil, "high", "high", "max", "max"))
	}
	if provider == "opencode-go" && id == "kimi-k2.6" {
		// OpenCode Go exposes Kimi K2.6 thinking as on/off, not distinct effort tiers.
		MergeThinking(model, O("minimal", nil, "low", nil, "medium", nil))
	}
	if provider == "opencode" && id == "grok-build-0.1" {
		// OpenCode Zen Grok Build reasons by default but rejects explicit reasoningEffort.
		MergeThinking(model, O("off", nil, "minimal", nil, "low", nil, "medium", nil))
	}
	if api == "anthropic-messages" {
		// OpenCode Zen forwards mid-conversation system messages but rejects
		// `tool_addition`/`tool_removal` blocks, so tool changes stay top-level.
		if provider == "opencode" && SupportsClaudeMidConvoSystemMessages(id) {
			MergeCompat(model, O("supportsMidConvoSystemMessages", true))
		}
		// OpenCode Qwen 3.8 Flash emits and accepts thinking blocks with empty signatures.
		if id == "qwen3.8-flash" {
			MergeCompat(model, O("allowEmptySignature", true))
		}
	}
	// The Kimi K3 family accepts tool-bearing system messages; OpenCode passes them through.
	if api == "openai-completions" && IsKimiK3FamilyID(id) {
		MergeCompat(model, O("supportsMidConvoSystemMessages", true, "supportsMidConvoToolAdditions", true))
	}
	// OpenCode passes mid-conversation developer messages and `additional_tools`
	// through to OpenAI unchanged; tool search is not verified through it.
	EnableOpenAIMidConvoSystemMessages(model, true)
}

// opencodeJev is OpenCode Zen's Jev, served through its TypeSafe-compatible System One endpoint.
// Neither its /inference/openai/v1/models listing nor models.dev carries metadata for it.
// https://opencode.ai/docs/zen
func opencodeJev(id, name string, input float64) *Obj {
	return O(
		"type", "classifier",
		"id", id,
		"name", name,
		"api", "typesafe-system-one",
		"provider", "opencode",
		"baseUrl", "https://opencode.ai/inference/systemone/v1",
		"input", Strings("text"),
		"cost", O("input", input, "output", 0, "cacheRead", 0, "cacheWrite", 0),
		"contextWindow", 32000,
	)
}

func opencode() ProviderDefinition {
	return ProviderDefinition{
		ID:     "opencode",
		Models: opencodeModels,
		Classifiers: func(*ProviderContext) ([]*Obj, error) {
			return []*Obj{opencodeJev("jev-1.13", "Jev 1.13", 0.042), opencodeJev("jev-1.13-free", "Jev 1.13 Free", 0)}, nil
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(opencodeCompletionsCompat())},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			finalizeOpencodeModel(model)
			EnableOpenAIGrammarTools(model)
		},
	}
}

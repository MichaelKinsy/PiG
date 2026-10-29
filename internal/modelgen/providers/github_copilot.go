package providers

// Ports packages/ai/catalog/providers/github-copilot.ts

import (
	"regexp"
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

const githubCopilotBaseURL = "https://api.individual.githubcopilot.com"

func githubCopilotStaticHeaders() *Obj {
	return O(
		"User-Agent", "GitHubCopilotChat/0.35.0",
		"Editor-Version", "vscode/1.107.0",
		"Editor-Plugin-Version", "copilot-chat/0.35.0",
		"Copilot-Integration-Id", "vscode-chat",
	)
}

// GitHub's "Models with extended capabilities" table lists these Copilot models as supporting
// the extended 1 million token context window.
var githubCopilotExtendedContextModelIDs = map[string]bool{
	"claude-fable-5":    true,
	"claude-opus-4.8":   true,
	"claude-opus-5":     true,
	"claude-opus-5.5":   true,
	"claude-sonnet-4.6": true,
	"claude-sonnet-5":   true,
	"gpt-5.3-codex":     true,
	"gpt-6-astra":       true,
	"gpt-6-luna":        true,
	"gpt-6-sol":         true,
}

var githubCopilotEagerToolInputStreamingUnsupportedModelIDs = map[string]bool{
	"claude-haiku-4.5":  true,
	"claude-sonnet-4":   true,
	"claude-sonnet-4.5": true,
}

// Checked manually against the authenticated GitHub Copilot /models endpoint on 2026-06-15.
// Keep this to narrow corrections over models.dev metadata instead of snapshotting Copilot's catalog.
func githubCopilotThinkingLevelOverride(modelID string) *Obj {
	switch modelID {
	case "claude-opus-4.8":
		return O("minimal", "low")
	case "claude-opus-5":
		return O("minimal", "low")
	case "claude-sonnet-4.6":
		return O("minimal", "low", "max", "max")
	}
	return nil
}

var (
	githubCopilotClaudeRegex = regexp.MustCompile(`^claude-(haiku|sonnet|opus|fable)-[45]([.-]|$)`)
	githubCopilotOpenAIRegex = regexp.MustCompile(`^(gpt-|grok-|oswe|mai-)`)
)

// githubCopilotAPI: Claude 4.x and 5.x use Messages; GPT, Grok, OSWE, and MAI-Code are only served on /responses.
func githubCopilotAPI(modelID string) string {
	if githubCopilotClaudeRegex.MatchString(modelID) {
		return "anthropic-messages"
	}
	if githubCopilotOpenAIRegex.MatchString(modelID) {
		return "openai-responses"
	}
	return "openai-completions"
}

// The authenticated Copilot catalog advertised these models on 2026-09-22,
// but models.dev did not include them yet.
func githubCopilotGPT6Luna() ChatModel {
	return O(
		"id", "gpt-6-luna",
		"name", "GPT-6 Luna",
		"api", "openai-responses",
		"provider", "github-copilot",
		"baseUrl", githubCopilotBaseURL,
		"reasoning", true,
		"input", Strings("text", "image"),
		"cost", OpenAIStandardCost("gpt-6-luna"),
		"contextWindow", 1000000,
		"maxTokens", 128000,
		"headers", githubCopilotStaticHeaders(),
	)
}

func githubCopilot() ProviderDefinition {
	return ProviderDefinition{
		ID: "github-copilot",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models, err := ModelsDevModels(ctx, "github-copilot", ModelsDevOptions{
				API:                  "openai-completions",
				BaseURL:              githubCopilotBaseURL,
				Headers:              githubCopilotStaticHeaders(),
				SkipDeprecated:       true,
				DefaultContextWindow: 128000,
				DefaultMaxTokens:     8192,
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					api := githubCopilotAPI(model.Str("id"))
					result := Spread(model).Set("api", api)
					if api == "openai-completions" {
						result.Set("compat", O(
							"supportsStore", false,
							"supportsDeveloperRole", false,
							"supportsReasoningEffort", false,
						))
					}
					if model.Str("id") == "claude-opus-5.5" {
						MergeThinking(result, Claude55ThinkingLevels(model.Str("id")))
					}
					return result, nil
				},
			})
			if err != nil {
				return nil, err
			}
			models = AddMissing(models, []ChatModel{githubCopilotGPT6Luna()})
			for i, model := range models {
				if githubCopilotExtendedContextModelIDs[model.Str("id")] {
					models[i] = Spread(model).Set("contextWindow", 1000000)
				}
			}
			return models, nil
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			id := model.Str("id")
			if strings.HasPrefix(id, "gpt-5") {
				MergeThinking(model, O("minimal", "low"))
			}
			if override := githubCopilotThinkingLevelOverride(id); override != nil {
				MergeThinking(model, override)
			}
			if model.Str("api") == "anthropic-messages" {
				// Copilot forwards mid-conversation system messages but rejects
				// `tool_addition`/`tool_removal` blocks, so tool changes stay top-level.
				if SupportsClaudeMidConvoSystemMessages(id) {
					MergeCompat(model, O("supportsMidConvoSystemMessages", true))
				}
				if githubCopilotEagerToolInputStreamingUnsupportedModelIDs[id] {
					MergeCompat(model, O("supportsEagerToolInputStreaming", false))
				}
			}
			// Copilot forwards Kimi K3's system text but silently drops its tool-bearing message.
			if model.Str("api") == "openai-completions" && id == "kimi-k3" {
				MergeCompat(model, O("supportsMidConvoSystemMessages", true))
			}
			// Copilot passes mid-conversation developer messages and `additional_tools`
			// through to OpenAI unchanged; tool search is not verified through it.
			EnableOpenAIMidConvoSystemMessages(model, true)
			EnableOpenAIGrammarTools(model)
		},
	}
}

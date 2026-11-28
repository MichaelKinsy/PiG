package providers

// Ports packages/ai/catalog/providers/github-copilot.ts

import (
	"regexp"
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts
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

// Copilot-served Responses models outside OpenAI's verified list that follow a developer message sent
// mid-conversation and keep the prompt cache with it; folded into the leading prompt, the flash models
// and gpt-5.3-codex ignored it (#12316, checked 2026-11-25).
var githubCopilotMidConvoSystemMessageModelIDs = map[string]bool{
	"gpt-5.3-codex":      true,
	"mai-code-1.1-flash": true,
	"mai-code-2":         true,
	"mai-code-2-flash":   true,
}

// Checked manually against the authenticated GitHub Copilot /models endpoint on 2026-06-15.
// Keep this to narrow corrections over models.dev metadata instead of snapshotting Copilot's catalog.
func githubCopilotThinkingLevelOverride(modelID string) *Obj {
	switch modelID {
	case "claude-opus-4.8":
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

// The authenticated Copilot catalog lists this decision model on /v1/systemone (checked 2026-11-02) without
// a context window or price, and GitHub's Copilot pricing page does not list it. These are Azure's values,
// which OpenRouter and Vercel AI Gateway also list.
func githubCopilotMicrosoftDecision1() *Obj {
	return O(
		"type", "classifier",
		"id", "microsoft-decision-1",
		"name", "Microsoft-Decision-1",
		"api", "typesafe-system-one",
		"provider", "github-copilot",
		"baseUrl", githubCopilotBaseURL,
		"input", Strings("text"),
		"cost", O("input", 0.042, "output", 0, "cacheRead", 0, "cacheWrite", 0),
		"contextWindow", 32768,
		"headers", githubCopilotStaticHeaders(),
	)
}

// The authenticated Copilot catalog lists this decision model on /v1/decisions with an 872K prompt limit and
// restricts it to the Pro, Pro+, Business, and Enterprise plans, among others (checked 2026-11-22), while
// Microsoft-Decision-1 is restricted to Business and Enterprise. It answers as gpt-6-luna and accepts images.
// GitHub's Copilot pricing page does not list it; these are OpenAI's Decisions API prices.
func githubCopilotGPT6LunaDecisions() *Obj {
	return O(
		"type", "classifier",
		"id", "gpt-6-luna-decisions",
		"name", "GPT-6 Luna Decisions",
		"api", "openai-decisions",
		"provider", "github-copilot",
		"baseUrl", githubCopilotBaseURL,
		"input", Strings("text", "image"),
		"cost", OpenAIDecisionsCost(),
		"contextWindow", 872000,
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
				Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
					api := githubCopilotAPI(model.Str("id"))
					result := Spread(model).Set("api", api)
					if api == "openai-completions" {
						result.Set("compat", O(
							"supportsStore", false,
							"supportsReasoningEffort", source.HasEffortOption(),
						))
					}
					if model.Str("id") == "claude-opus-5.5" {
						MergeThinking(result, Claude55ThinkingLevels())
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
		Classifiers: func(*ProviderContext) ([]*Obj, error) {
			return []*Obj{githubCopilotGPT6LunaDecisions(), githubCopilotMicrosoftDecision1()}, nil
		},
		Compat: ApiCompatDefaults{
			"anthropic-messages": FixedCompat(O("supportsStrictTools", true)),
			"openai-completions": FixedCompat(O("supportsStrictMode", true)),
			"openai-responses":   FixedCompat(O("supportsStrictMode", true)),
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			id := model.Str("id")
			if strings.HasPrefix(id, "gpt-5") {
				MergeThinking(model, O("minimal", "low"))
			}
			if override := githubCopilotThinkingLevelOverride(id); override != nil {
				MergeThinking(model, override)
			}
			if model.Str("api") == "anthropic-messages" {
				// Copilot forwards per-message `output_config` effort, mid-conversation system messages, and
				// `tool_addition`/`tool_removal` blocks with their betas (#12316, checked 2026-11-23 on
				// Opus, Sonnet, and Haiku 5.5).
				if SupportsClaudeMidConvoEffort(id) {
					EnableClaudeMidConvoEffort(model)
				}
				if SupportsClaudeMidConvoChanges(id) {
					MergeCompat(model, O("supportsMidConvoSystemMessages", true, "supportsMidConvoToolChanges", true))
				}
				// Copilot keeps Anthropic's cache lifetimes: a default entry was hit after 270 s and missed
				// after 330 s, a `ttl: "1h"` entry was hit after 420 s (#12316, checked 2026-11-23).
				model.Set("promptCache", O("short", 300, "long", 3600))
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
			if model.Str("api") == "openai-responses" && githubCopilotMidConvoSystemMessageModelIDs[id] {
				MergeCompat(model, O("supportsMidConvoSystemMessages", true))
			}
			EnableOpenAIGrammarTools(model)
			// Copilot applies `configuration_update` effort items and keeps the prompt cache across
			// effort changes, which a changed `reasoning.effort` drops (#12316, checked 2026-11-23).
			if model.Str("api") == "openai-responses" && OpenAIMidConvoEffortModelIDs[id] {
				MergeCompat(model, O("supportsMidConvoEffort", true))
			}
		},
	}
}

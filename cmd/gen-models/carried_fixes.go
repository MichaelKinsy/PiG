package main

import (
	"regexp"
	"strings"
)

// carriedFix is a Pi fix that landed after the pinned published catalog and that the 0.5.0 release carries forward. The
// generator reads the published package, which predates the fix, so each rule re-applies the fix's effect on the
// catalog rows. Every rule is a no-op on a source that already has the fix: when the pin moves past the commit, delete
// its row (and its rule) and the regenerated catalog does not change.
type carriedFix struct {
	// Sha is the Pi commit the rule carries.
	Sha string
	// Apply edits the catalog in place and returns the rows to keep.
	Apply func(rows []modelRow) []modelRow
}

var carriedFixes = []carriedFix{
	{Sha: "195120a9", Apply: opencodeInferenceEndpoints},
	{Sha: "44f6851b", Apply: openRouterAnthropicLatestMessages},
	{Sha: "2e0f175b", Apply: realtimeAndImageRowsAreNotChat},
}

// applyCarriedFixes applies every carried fix to the flattened catalog.
func applyCarriedFixes(catalog *modelCatalog) {
	for _, fix := range carriedFixes {
		catalog.Chat = fix.Apply(catalog.Chat)
		catalog.Image = fix.Apply(catalog.Image)
		catalog.Classifier = fix.Apply(catalog.Classifier)
	}
}

// realtimeAndImageChatRows are the chat rows Pi 2e0f175b (#11368) drops: models.dev types them as realtime or image models.
var realtimeAndImageChatRows = map[string]bool{
	"azure/gpt-realtime-2.1":                    true,
	"openai/gpt-realtime-2.1":                   true,
	"google/gemini-3.1-flash-live-preview":      true,
	"google/gemini-3.1-flash-lite-image":        true,
	"google-vertex/gemini-3.1-flash-lite-image": true,
}

// realtimeAndImageRowsAreNotChat is the row removal of Pi 2e0f175b (the catalog generator rewrite and the data it added are not carried):
// models.dev's realtime and image models (gpt-realtime-2.1, gemini-3.1-flash-live-preview, gemini-3.1-flash-lite-image) are no longer
// offered as chat models.
func realtimeAndImageRowsAreNotChat(rows []modelRow) []modelRow {
	kept := rows[:0:0]
	for _, row := range rows {
		if !realtimeAndImageChatRows[row.Provider+"/"+row.ID] {
			kept = append(kept, row)
		}
	}
	return kept
}

var reCompatMember = regexp.MustCompile(`,"compat":\{[^{}]*\}`)

// reOpenRouterAnthropicLatest is Pi's /^~anthropic\/claude-[a-z]+-latest$/ (generate-models.ts isAnthropicAdaptiveThinkingModel).
var reOpenRouterAnthropicLatest = regexp.MustCompile(`^~anthropic/claude-[a-z]+-latest$`)

// openRouterAnthropicLatestMessages is Pi 44f6851b (#9534, scripts/generate-models.ts and openrouter-catalog.ts): OpenRouter's
// ~anthropic/claude-*-latest aliases move from Chat Completions to the Anthropic Messages API with adaptive thinking, like the
// versioned Claude models, because Chat Completions dropped the thinking signature and OpenRouter then ignored Claude's earlier thinking.
// The row takes the compat of an openrouter anthropic/ row (forceAdaptiveThinking only) in place of the Chat Completions compat, and
// the base URL without /v1 because the Anthropic SDK appends /v1/messages.
func openRouterAnthropicLatestMessages(rows []modelRow) []modelRow {
	kept := rows[:0:0]
	for _, row := range rows {
		if row.Provider == "openrouter" && row.API == "openai-completions" && reOpenRouterAnthropicLatest.MatchString(row.ID) { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			row.API = "anthropic-messages"
			row.BaseURL = strings.TrimSuffix(row.BaseURL, "/v1")
			row.Compat = &ModelCompat{ForceAdaptiveThinking: new(true)}
			row.Record = strings.Replace(row.Record, `"api":"openai-completions"`, `"api":"anthropic-messages"`, 1)
			row.Record = strings.Replace(row.Record, `"baseUrl":"https://openrouter.ai/api/v1"`, `"baseUrl":"https://openrouter.ai/api"`, 1)
			row.Record = reCompatMember.ReplaceAllLiteralString(row.Record, `,"compat":{"forceAdaptiveThinking":true}`)
		}
		kept = append(kept, row)
	}
	return kept
}

// opencodeInferenceEndpoints is Pi 195120a9 (scripts/generate-models.ts): OpenCode Zen and OpenCode Go move from
// opencode.ai/zen to opencode.ai/inference (and /inference/go); opencode-go minimax-m2.7 is served through the Anthropic
// Messages API (it no longer gets the openai-completions override); opencode-go deepseek-v4-flash is no longer listed.
func opencodeInferenceEndpoints(rows []modelRow) []modelRow {
	const oldBase, newBase = "https://opencode.ai/zen", "https://opencode.ai/inference"
	kept := rows[:0:0]
	for _, row := range rows {
		if row.Provider != "opencode" && row.Provider != "opencode-go" { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			kept = append(kept, row)
			continue
		}
		if row.Provider == "opencode-go" && row.ID == "deepseek-v4-flash" { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			continue
		}
		if row.Provider == "opencode-go" && row.ID == "minimax-m2.7" && row.API == "openai-completions" { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			row.API = "anthropic-messages"
			row.Compat = nil
			row.Record = strings.Replace(row.Record, `"api":"openai-completions"`, `"api":"anthropic-messages"`, 1)
			row.Record = reCompatMember.ReplaceAllString(row.Record, "")
			// The Anthropic SDK appends /v1/messages, so the base URL has no /v1.
			row.BaseURL = strings.TrimSuffix(row.BaseURL, "/v1")
			row.Record = strings.Replace(row.Record, `/zen/go/v1"`, `/zen/go"`, 1)
		}
		row.BaseURL = strings.Replace(row.BaseURL, oldBase, newBase, 1)
		row.Record = strings.ReplaceAll(row.Record, oldBase, newBase)
		kept = append(kept, row)
	}
	return kept
}

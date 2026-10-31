package extension

import (
	"regexp"
	"strings"
)

// Pig divergence D217: the carried Pi fixes past the 1.4.0+b3a29f11 pin change catalog rows the pinned Pi that this oracle runs still has in the old form. Pi's
// model objects are read with those edits applied and nothing else, so every other member is still compared with Pi.

// Pi 2e0f175b (#11368) removes the realtime and image rows that models.dev does not type as chat models from the chat catalog.
var d217CarriedDroppedChatRefs = []string{
	"azure/gpt-realtime-2.1",
	"openai/gpt-realtime-2.1",
	"google/gemini-3.1-flash-live-preview",
	"google/gemini-3.1-flash-lite-image",
	"google-vertex/gemini-3.1-flash-lite-image",
}

var (
	d217OpenRouterAnthropicLatest = regexp.MustCompile(`^openrouter/~anthropic/claude-[a-z]+-latest$`)
	d217CompatMember              = regexp.MustCompile(`,"compat":\{[^{}]*\}`)
)

// d217CarriedPiJSON is Pi 44f6851b (#9534) over a model object's JSON text, which keeps Pi's member order: the OpenRouter ~anthropic/claude-*-latest aliases use
// the Anthropic Messages API on openrouter.ai/api with adaptive thinking in place of their Chat Completions compat.
func d217CarriedPiJSON(ref, raw string) string {
	if !d217OpenRouterAnthropicLatest.MatchString(ref) || !strings.Contains(raw, `"api":"openai-completions"`) {
		return raw
	}
	raw = strings.Replace(raw, `"api":"openai-completions"`, `"api":"anthropic-messages"`, 1)
	raw = strings.Replace(raw, `"baseUrl":"https://openrouter.ai/api/v1"`, `"baseUrl":"https://openrouter.ai/api"`, 1)
	return d217CompatMember.ReplaceAllLiteralString(raw, `,"compat":{"forceAdaptiveThinking":true}`)
}

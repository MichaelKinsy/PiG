package modelgen

// Ports packages/ai/catalog/families/google.ts: Google Gemini and Gemma models.

import (
	"regexp"
	"strings"
)

var gemma4 = regexp.MustCompile(`gemma-?4`)

// GoogleThinkingLevels returns Gemini effort levels from models.dev, with a Gemma 4 fallback; nil when neither applies.
func GoogleThinkingLevels(modelID string, reasoningOptions []ModelsDevReasoningOption) *Obj {
	if effortMap := GetEffortThinkingLevelMap(reasoningOptions); effortMap != nil {
		return effortMap
	}
	if gemma4.MatchString(strings.ToLower(modelID)) {
		return O("off", nil, "minimal", "MINIMAL", "low", nil, "medium", nil, "high", "HIGH")
	}
	return nil
}

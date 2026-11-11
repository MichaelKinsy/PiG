package modelgen

// Ports packages/ai/catalog/families/anthropic.ts: Anthropic Claude models, facts that hold on every host that serves them. Ids
// appear dashed (claude-opus-4-8), dotted (claude-opus-4.8), prefixed (anthropic/claude-opus-4.8), and dated
// (claude-opus-5-20260723).

import (
	"regexp"
	"strings"
)

var (
	claudePrefix           = regexp.MustCompile(`^~?anthropic/`)
	claudeDateSuffix       = regexp.MustCompile(`-\d{8}$`)
	claudeOpus5            = regexp.MustCompile(`^claude-opus-(?:5|5[.-]5)(?:-\d{8})?$`)
	claudeSonnetHaiku55    = regexp.MustCompile(`^claude-(?:sonnet|haiku)-5[.-]5(?:-\d{8})?$`)
	claudeFableMythos51    = regexp.MustCompile(`^claude-(?:fable|mythos)-5(?:[.-]1)(?:-\d{8})?$`)
	claudeSystemOpus       = regexp.MustCompile(`^claude-opus-(?:4[.-]8|5(?:[.-]5)?)(?:-\d{8})?$`)
	claudeSystemSonnet     = regexp.MustCompile(`^claude-(?:sonnet|haiku)-5[.-]5(?:-\d{8})?$`)
	claudeSystemFableMyth  = regexp.MustCompile(`^claude-(?:fable|mythos)-5(?:[.-]1)?(?:-\d{8})?$`)
	claudeLatestAliasRegex = regexp.MustCompile(`^~anthropic/claude-[a-z]+-latest$`)
)

func stripClaudePrefix(modelID string) string {
	return claudePrefix.ReplaceAllString(strings.ToLower(modelID), "")
}

// SupportsClaudeMidConvoEffort reports models that accept per-message `output_config` effort changes.
func SupportsClaudeMidConvoEffort(modelID string) bool {
	id := stripClaudePrefix(modelID)
	return claudeOpus5.MatchString(id) || claudeSonnetHaiku55.MatchString(id) || claudeFableMythos51.MatchString(id)
}

// Claude models that accept `thinking: {type: "disabled"}` alongside per-message effort. Opus 5.5, Sonnet 5.5, Fable 5.1, and
// Mythos 5.1 reject it.
// https://platform.claude.com/docs/en/build-with-claude/thinking#configuring-thinking
var disabledThinkingModels = map[string]bool{"claude-opus-5": true, "claude-haiku-5-5": true, "claude-haiku-5.5": true}

// SupportsClaudeMidConvoSystemMessages reports models that accept system messages and tool changes after the conversation starts.
func SupportsClaudeMidConvoSystemMessages(modelID string) bool {
	return claudeSystemOpus.MatchString(modelID) || claudeSystemSonnet.MatchString(modelID) || claudeSystemFableMyth.MatchString(modelID)
}

// EnableClaudeMidConvoEffort enables per-message effort changes. Models that cannot disable thinking with it lose "off".
func EnableClaudeMidConvoEffort(model ChatModel) {
	MergeCompat(model, O("supportsMidConvoEffort", true))
	if !disabledThinkingModels[claudeDateSuffix.ReplaceAllString(stripClaudePrefix(model.Str("id")), "")] {
		MergeThinking(model, O("off", nil))
	}
}

func idIncludesAny(modelID string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(modelID, fragment) {
			return true
		}
	}
	return false
}

var adaptiveThinking = []string{
	"opus-4-6", "opus-4.6", "opus-4-7", "opus-4.7", "opus-4-8", "opus-4.8", "opus-5", "opus.5",
	"sonnet-4-6", "sonnet-4.6", "sonnet-5", "sonnet.5", "haiku-5", "haiku.5", "fable-5", "mythos-5",
}

// isClaudeLatestAlias: OpenRouter's `~anthropic/claude-*-latest` aliases follow the newest model of each family, all of which use
// adaptive thinking.
func isClaudeLatestAlias(modelID string) bool { return claudeLatestAliasRegex.MatchString(modelID) }

var (
	temperatureUnsupported = []string{"opus-4-7", "opus-4.7", "opus-4-8", "opus-4.8", "opus-5", "opus.5", "sonnet-5-5", "sonnet-5.5", "haiku-5-5", "haiku-5.5"}
	maxEffortOnly          = []string{"opus-4-6", "opus-4.6", "sonnet-4-6", "sonnet-4.6"}
	xhighAndMaxEffort      = []string{"opus-4-7", "opus-4.7", "opus-4-8", "opus-4.8", "opus-5", "opus.5", "sonnet-5", "sonnet.5", "haiku-5", "haiku.5"}
)

// applyClaudeFamilyRules sets the adaptive thinking effort levels (per Anthropic adaptive thinking docs): "max" is available on all
// adaptive-thinking Claude models; "xhigh" only on Opus 4.7/4.8/5, Sonnet 5, Haiku 5.5, and Fable 5.
func applyClaudeFamilyRules(model ChatModel) {
	id := model.Str("id")
	if idIncludesAny(id, maxEffortOnly) {
		MergeThinking(model, O("max", "max"))
	}
	if idIncludesAny(id, xhighAndMaxEffort) {
		MergeThinking(model, O("xhigh", "xhigh", "max", "max"))
	}
	if strings.Contains(id, "fable-5") {
		MergeThinking(model, O("off", nil, "xhigh", "xhigh", "max", "max"))
	}
	if model.Str("api") != "anthropic-messages" {
		return
	}
	if isClaudeLatestAlias(id) || idIncludesAny(id, adaptiveThinking) {
		MergeCompat(model, O("forceAdaptiveThinking", true))
	}
	if idIncludesAny(strings.ToLower(id), temperatureUnsupported) {
		MergeCompat(model, O("supportsTemperature", false))
	}
}

// Claude55ThinkingLevels: models.dev may list Opus 5.5, Sonnet 5.5, and Haiku 5.5 before their effort metadata is complete.
// Haiku 5.5 accepts disabled thinking; Opus 5.5 and Sonnet 5.5 reject it.
func Claude55ThinkingLevels(modelID string) *Obj {
	levels := NewObj()
	if !strings.Contains(modelID, "haiku") {
		levels.Set("off", nil)
	}
	return levels.Set("minimal", nil).Set("low", "low").Set("medium", "medium").Set("high", "high").Set("xhigh", "xhigh").Set("max", "max")
}

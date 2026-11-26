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
	claudeVariantSuffix    = regexp.MustCompile(`[-:](?:batch|fast)$`)
	claudeDottedVersion    = regexp.MustCompile(`(\d)\.(\d)`)
	claudeOpus5            = regexp.MustCompile(`^claude-opus-(?:5|5[.-]5)(?:-\d{8})?$`)
	claudeSonnetHaiku55    = regexp.MustCompile(`^claude-(?:sonnet|haiku)-5[.-]5(?:-\d{8})?$`)
	claudeFableMythos51    = regexp.MustCompile(`^claude-(?:fable|mythos)-5[.-][12](?:-\d{8})?$`)
	claudeSystemOpus       = regexp.MustCompile(`^claude-opus-(?:4[.-]8|5(?:[.-]5)?)(?:-\d{8})?$`)
	claudeSystemSonnet     = regexp.MustCompile(`^claude-(?:sonnet|haiku)-5[.-]5(?:-\d{8})?$`)
	claudeSystemFableMyth  = regexp.MustCompile(`^claude-(?:fable|mythos)-5(?:[.-][12])?(?:-\d{8})?$`)
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

// SupportsClaudeMidConvoSystemMessages reports models that accept system messages and tool changes after the conversation starts.
func SupportsClaudeMidConvoSystemMessages(modelID string) bool {
	return claudeSystemOpus.MatchString(modelID) || claudeSystemSonnet.MatchString(modelID) || claudeSystemFableMyth.MatchString(modelID)
}

// EnableClaudeMidConvoEffort enables per-message effort changes.
func EnableClaudeMidConvoEffort(model ChatModel) {
	MergeCompat(model, O("supportsMidConvoEffort", true))
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
	temperatureUnsupported = []string{"opus-4-7", "opus-4.7", "opus-4-8", "opus-4.8", "opus-5", "opus.5", "sonnet-5", "sonnet.5", "haiku-5-5", "haiku-5.5"}
	maxEffortOnly          = []string{"opus-4-6", "opus-4.6", "sonnet-4-6", "sonnet-4.6"}
	xhighAndMaxEffort      = []string{"opus-4-7", "opus-4.7", "opus-4-8", "opus-4.8", "opus-5", "opus.5", "sonnet-5", "sonnet.5", "haiku-5", "haiku.5"}
)

// applyClaudeFamilyRules sets the adaptive thinking effort levels (per Anthropic adaptive thinking docs): "max" is available on all
// adaptive-thinking Claude models; "xhigh" only on Opus 4.7/4.8/5, Sonnet 5, Haiku 5.5, and Fable 5.
func applyClaudeFamilyRules(model ChatModel, ctx *ProviderContext) {
	id := model.Str("id")
	if idIncludesAny(id, maxEffortOnly) {
		MergeThinking(model, O("max", "max"))
	}
	if idIncludesAny(id, xhighAndMaxEffort) {
		MergeThinking(model, O("xhigh", "xhigh", "max", "max"))
	}
	if strings.Contains(id, "fable-5") {
		MergeThinking(model, O("xhigh", "xhigh", "max", "max"))
	}
	if cannotTurnThinkingOff(model, ctx) {
		MergeThinking(model, O("off", nil))
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

// cannotTurnThinkingOff reports whether Anthropic's own models.dev entry for this Claude model lists no thinking toggle.
func cannotTurnThinkingOff(model ChatModel, ctx *ProviderContext) bool {
	if ctx == nil {
		return false
	}
	canonicalID := ""
	if src := ctx.ModelsDevSource(model); src != nil {
		canonicalID = src.CanonicalID
	}
	if canonicalID == "" {
		canonicalID = "anthropic/" + anthropicModelID(model.Str("id"))
	}
	if !strings.HasPrefix(canonicalID, "anthropic/") {
		return false
	}
	anthropic := objectOf(ctx.Sources.ModelsDev.member("anthropic"))
	models := objectOf(anthropic.member("models"))
	entry := objectOf(models.member(strings.TrimPrefix(canonicalID, "anthropic/")))
	if entry == nil {
		return false
	}
	normalized := NormalizeModelsDevModel(entry)
	return normalized.Reasoning && !normalized.HasToggleOption()
}

func anthropicModelID(modelID string) string {
	id := claudeVariantSuffix.ReplaceAllString(stripClaudePrefix(modelID), "")
	return claudeDottedVersion.ReplaceAllString(id, "$1-$2")
}

// Claude55ThinkingLevels: models.dev may list Opus 5.5, Sonnet 5.5, and Haiku 5.5 before their effort metadata is complete.
func Claude55ThinkingLevels() *Obj {
	return NewObj().Set("minimal", nil).Set("low", "low").Set("medium", "medium").Set("high", "high").Set("xhigh", "xhigh").Set("max", "max")
}

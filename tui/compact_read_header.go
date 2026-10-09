package tui

import (
	"encoding/json"
	"math"
)

// CompactReadClassification is upstream read.ts CompactReadClassification:
// a read of a skill file, the agent's own docs, or a context file draws a
// short label while the card is collapsed.
type CompactReadClassification struct {
	Kind  string // "docs", "resource" or "skill"
	Label string
}

// compactReadClassifier classifies a read path. The coding agent installs
// it: it knows the documentation root and resolves paths as the read tool
// does.
var compactReadClassifier func(rawPath, cwd string) (CompactReadClassification, bool)

// SetCompactReadClassifier installs the classifier FormatCompactReadHeader
// uses. Nil turns compact read headers off.
func SetCompactReadClassifier(classify func(rawPath, cwd string) (CompactReadClassification, bool)) {
	compactReadClassifier = classify
}

// toolPathArg is upstream str(args?.file_path ?? args?.path): file_path
// unless it is null or absent, a string passing through, null or absent as
// "", and any other value invalid.
func toolPathArg(args map[string]any) (string, bool) {
	value := args["file_path"]
	if value == nil {
		value = args["path"]
	}
	return renderStr(value)
}

// readLineRange is upstream read.ts formatReadLineRange: `:start` or
// `:start-end` in the warning color from the JavaScript values of offset and
// limit, nothing when both are null or absent. A zero or NaN end line prints
// no end, as the template's falsy check does.
func readLineRange(args map[string]any) string {
	offset, limit := args["offset"], args["limit"]
	if offset == nil && limit == nil {
		return ""
	}
	start := any(1.0)
	if offset != nil {
		start = offset
	}
	text := ":" + jsTemplateString(start)
	if limit != nil {
		if end := jsToNumber(jsAdd(start, limit)) - 1; end != 0 && !math.IsNaN(end) {
			text += "-" + JSNumberString(end)
		}
	}
	return fg(ActiveTheme().Warning, text)
}

// FormatCompactReadHeader is upstream read.ts formatCompactReadCall for the
// collapsed read card, or "" when the path has no compact classification.
func FormatCompactReadHeader(raw json.RawMessage, cwd string) string {
	if compactReadClassifier == nil {
		return ""
	}
	args := decodeToolArgs(raw)
	rawPath, ok := toolPathArg(args)
	if !ok || rawPath == "" {
		return ""
	}
	classification, found := compactReadClassifier(rawPath, cwd)
	if !found {
		return ""
	}
	theme := ActiveTheme()
	expandHint := fg(theme.Dim, " ("+AppKeyText("app.tools.expand", "ctrl+o")+" to expand)")
	lineRange := readLineRange(args)
	if classification.Kind == "skill" {
		return fg(theme.CustomMessageLabel, "\x1b[1m[skill]\x1b[22m ") + fg(theme.CustomMessageText, classification.Label) + lineRange + expandHint
	}
	return toolTitleText("read "+classification.Kind) + " " + fg(theme.Accent, classification.Label) + lineRange + expandHint
}

package tui

import (
	"encoding/json"
	"math"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
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

// readLineRange is upstream read.ts styledReadLineRange: the range from
// FormatReadLineRange in the warning color, nothing when it is empty.
func readLineRange(args map[string]any) string {
	text := FormatReadLineRange(args["offset"], args["limit"])
	if text == "" {
		return ""
	}
	return fg(ActiveTheme().Warning, text)
}

// FormatReadLineRange is upstream render-utils.ts formatReadLineRange: `:start`
// or `:start-end` from finite numbers or numeric strings, nothing when both are
// null or absent or either is malformed. A zero end line prints no end, as the
// template's falsy check does.
func FormatReadLineRange(offset, limit any) string {
	if offset == nil && limit == nil {
		return ""
	}
	start := 1.0
	if offset != nil {
		var ok bool
		if start, ok = parseLineNumber(offset); !ok {
			return ""
		}
	}
	if limit == nil {
		return ":" + JSNumberString(start)
	}
	count, ok := parseLineNumber(limit)
	if !ok {
		return ""
	}
	text := ":" + JSNumberString(start)
	if end := start + count - 1; end != 0 && !math.IsNaN(end) {
		text += "-" + JSNumberString(end)
	}
	return text
}

// parseLineNumber is upstream render-utils.ts parseLineNumber: a finite number,
// or a non-blank string that converts to one.
func parseLineNumber(value any) (float64, bool) {
	var n float64
	switch x := value.(type) {
	case float64:
		n = x
	case string:
		// JavaScript trim also strips U+FEFF; U+0085 is NaN either way.
		if strings.TrimFunc(x, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }) == "" {
			return 0, false
		}
		n = jsnumber.Parse(x)
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
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

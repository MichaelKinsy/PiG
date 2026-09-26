package tui

import (
	"encoding/json"
	"fmt"
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

type readHeaderArgs struct {
	Path     *string `json:"path"`
	FilePath *string `json:"file_path"`
	Offset   *int    `json:"offset,omitempty"`
	Limit    *int    `json:"limit,omitempty"`
}

func (a readHeaderArgs) rawPath() string {
	switch {
	case a.FilePath != nil:
		return *a.FilePath
	case a.Path != nil:
		return *a.Path
	}
	return ""
}

// readLineRange is upstream read.ts formatReadLineRange.
func readLineRange(offset, limit *int) string {
	if offset == nil && limit == nil {
		return ""
	}
	start := 1
	if offset != nil {
		start = *offset
	}
	text := fmt.Sprintf(":%d", start)
	if limit != nil {
		text = fmt.Sprintf(":%d-%d", start, start+*limit-1)
	}
	return fg(ActiveTheme().Warning, text)
}

// FormatCompactReadHeader is upstream read.ts formatCompactReadCall for the
// collapsed read card, or "" when the path has no compact classification.
func FormatCompactReadHeader(raw json.RawMessage, cwd string) string {
	if compactReadClassifier == nil {
		return ""
	}
	var args readHeaderArgs
	_ = json.Unmarshal(raw, &args)
	rawPath := args.rawPath()
	if rawPath == "" {
		return ""
	}
	classification, ok := compactReadClassifier(rawPath, cwd)
	if !ok {
		return ""
	}
	theme := ActiveTheme()
	expandHint := fg(theme.Dim, " ("+AppKeyText("app.tools.expand", "ctrl+o")+" to expand)")
	lineRange := readLineRange(args.Offset, args.Limit)
	if classification.Kind == "skill" {
		return fg(theme.CustomMessageLabel, "\x1b[1m[skill]\x1b[22m ") + fg(theme.CustomMessageText, classification.Label) + lineRange + expandHint
	}
	return toolTitleText("read "+classification.Kind) + " " + fg(theme.Accent, classification.Label) + lineRange + expandHint
}

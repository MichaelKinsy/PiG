package codingagent

// tool_render_list.go: result-side presentation for grep, find, and ls.
//
// Ports formatGrepResult, formatFindResult, and formatLsResult from upstream
// packages/coding-agent/src/core/tools/renderers/{grep,find,ls}.ts: the
// trimmed output, the first 15 (grep) or 20 (find, ls) lines while collapsed
// with a "more lines" hint, then a "[Truncated: …]" warning built from the
// result details. The call headers live in tui/tool_execution.go.

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// listResultDetails is the union of the grep/find/ls details the renderers
// read, decoded from either the live structs or the upstream JSON shape.
type listResultDetails struct {
	Truncated          bool
	MaxBytes           int
	MatchLimitReached  int
	ResultLimitReached int
	EntryLimitReached  int
	LinesTruncated     bool
}

func listDetailsFrom(details any) listResultDetails {
	fromTruncation := func(tr *tools.TruncationResult) (bool, int) {
		if tr == nil {
			return false, 0
		}
		return tr.Truncated, tr.MaxBytes
	}
	var out listResultDetails
	switch d := details.(type) {
	case *tools.GrepDetails:
		if d != nil {
			out.Truncated, out.MaxBytes = fromTruncation(d.Truncation)
			out.MatchLimitReached, out.LinesTruncated = d.MatchLimitReached, d.LinesTruncated
		}
	case *tools.FindDetails:
		if d != nil {
			out.Truncated, out.MaxBytes = fromTruncation(d.Truncation)
			out.ResultLimitReached = d.ResultLimitReached
		}
	case *tools.LsDetails:
		if d != nil {
			out.Truncated, out.MaxBytes = fromTruncation(d.Truncation)
			out.EntryLimitReached = d.EntryLimitReached
		}
	case map[string]any:
		var wire struct {
			Truncation *struct {
				Truncated bool `json:"truncated"`
				MaxBytes  int  `json:"maxBytes"`
			} `json:"truncation"`
			MatchLimitReached  int  `json:"matchLimitReached"`
			ResultLimitReached int  `json:"resultLimitReached"`
			EntryLimitReached  int  `json:"entryLimitReached"`
			LinesTruncated     bool `json:"linesTruncated"`
		}
		if data, err := json.Marshal(d); err == nil && json.Unmarshal(data, &wire) == nil {
			if wire.Truncation != nil {
				out.Truncated, out.MaxBytes = wire.Truncation.Truncated, wire.Truncation.MaxBytes
			}
			out.MatchLimitReached, out.ResultLimitReached = wire.MatchLimitReached, wire.ResultLimitReached
			out.EntryLimitReached, out.LinesTruncated = wire.EntryLimitReached, wire.LinesTruncated
		}
	}
	return out
}

// listToolPreviewLines mirrors the collapsed line budgets of upstream
// formatGrepResult (15) and formatFindResult / formatLsResult (20).
func listToolPreviewLines(toolName string) int {
	if toolName == "grep" {
		return 15
	}
	return 20
}

// listToolWarnings mirrors each renderer's warning list.
func listToolWarnings(toolName string, d listResultDetails) []string {
	limit := func() string {
		maxBytes := d.MaxBytes
		if maxBytes == 0 {
			maxBytes = tools.DefaultMaxBytesUpstream
		}
		return tools.FormatSize(maxBytes) + " limit"
	}
	var warnings []string
	switch toolName {
	case "grep":
		if d.MatchLimitReached != 0 {
			warnings = append(warnings, strconv.Itoa(d.MatchLimitReached)+" matches limit")
		}
		if d.Truncated {
			warnings = append(warnings, limit())
		}
		if d.LinesTruncated {
			warnings = append(warnings, "some lines truncated")
		}
	case "find":
		if d.ResultLimitReached != 0 {
			warnings = append(warnings, strconv.Itoa(d.ResultLimitReached)+" results limit")
		}
		if d.Truncated {
			warnings = append(warnings, limit())
		}
	case "ls":
		if d.EntryLimitReached != 0 {
			warnings = append(warnings, strconv.Itoa(d.EntryLimitReached)+" entries limit")
		}
		if d.Truncated {
			warnings = append(warnings, limit())
		}
	}
	return warnings
}

// makeListBodyRenderer renders a grep, find, or ls result. Upstream builds
// one Text whose content starts with "\n"; the tool card draws that first
// blank row as its separator, so the returned lines omit it.
func makeListBodyRenderer(toolName, content string, details any) func(width int, expanded bool) []string {
	d := listDetailsFrom(details)
	return func(width int, expanded bool) []string {
		theme := tui.ActiveTheme()
		var text strings.Builder
		if output := jsTrim(shellTextOutput(content)); output != "" {
			lines := strings.Split(output, "\n")
			maxLines := len(lines)
			if !expanded {
				maxLines = min(maxLines, listToolPreviewLines(toolName))
			}
			styled := make([]string, maxLines)
			for i, line := range lines[:maxLines] {
				styled[i] = themeFg(theme.ToolOutput, line)
			}
			text.WriteString("\n" + strings.Join(styled, "\n"))
			if remaining := len(lines) - maxLines; remaining > 0 {
				text.WriteString(themeFg(theme.Muted, "\n... ("+strconv.Itoa(remaining)+" more lines,") +
					" " + expandKeyHint() + themeFg(theme.Muted, ")"))
			}
		}
		if warnings := listToolWarnings(toolName, d); len(warnings) > 0 {
			text.WriteString("\n" + themeFg(theme.Warning, "[Truncated: "+strings.Join(warnings, ", ")+"]"))
		}
		lines := tui.NewPaddedText(text.String(), 0, 0, nil).Render(width)
		if len(lines) > 0 {
			lines = lines[1:]
		}
		return lines
	}
}

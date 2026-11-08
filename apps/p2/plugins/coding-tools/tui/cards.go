// Package tui is the coding tools' TUI facet: their cards in the transcript, as pi draws them (cards.ts; bash with the shared shell card), copied from
// coding-agent core/tools/renderers/{read,write,edit,grep,find,ls}.ts. Removed (p2-tui-port.md section 11): local path resolution, `getReadmePath` docs
// classification and edit's local preview (`computeEditsDiff` reads the local fs): the edit card shows the result's `details.diff`. Paths display relative to
// the session cwd.
//
// pi: apps/p2/plugins/coding-tools/{cards,tui}.ts
package tui

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func pathArg(args tools.ToolArgs) (string, bool) {
	value := args.Lookup("file_path")
	if value == nil {
		value = args.Lookup("path")
	}
	return tools.Str(value)
}

func textOf(component pitui.Component) *pitui.Text {
	if text, ok := component.(*pitui.Text); ok {
		return text
	}
	return pitui.NewPaddedText("", 0, 0, nil)
}

func record(value any) *jsvalue.Object {
	object, _ := value.(*jsvalue.Object)
	return object
}

type truncation struct {
	truncatedBy                                 any
	firstLineExceedsLimit                       bool
	outputLines, totalLines, maxLines, maxBytes int
}

// truncationOf is Durable's truncation details (`ReadToolDetails`, grep/find/ls), read defensively.
func truncationOf(details any) *truncation {
	value := record(record(details).Lookup("truncation"))
	if value == nil || value.Lookup("truncated") != true {
		return nil
	}
	number := func(key string, fallback int) int {
		if n, ok := tools.Num(value.Lookup(key)); ok {
			return int(n)
		}
		return fallback
	}
	exceeds, _ := value.Lookup("firstLineExceedsLimit").(bool)
	return &truncation{
		truncatedBy: value.Lookup("truncatedBy"), firstLineExceedsLimit: exceeds, outputLines: number("outputLines", 0), totalLines: number("totalLines", 0),
		maxLines: number("maxLines", tools.DefaultMaxLines), maxBytes: number("maxBytes", tools.DefaultMaxBytes),
	}
}

func formatReadLineRange(args tools.ToolArgs, t *theme.Theme) string {
	offset, hasOffset := tools.Num(args.Lookup("offset"))
	limit, hasLimit := tools.Num(args.Lookup("limit"))
	if !hasOffset && !hasLimit {
		return ""
	}
	start := 1.0
	if hasOffset {
		start = offset
	}
	end := ""
	if hasLimit {
		end = "-" + jsvalueNumber(start+limit-1)
	}
	return t.Fg("warning", ":"+jsvalueNumber(start)+end)
}

func jsvalueNumber(n float64) string { return fmt.Sprint(n) }

func formatReadResult(result tools.ToolResultView, t *theme.Theme, context *tools.ToolRenderContext) string {
	if !context.Expanded && !context.IsError {
		return ""
	}
	rawPath, _ := pathArg(context.Args)
	output := tools.GetTextOutput(&result)
	lang := ""
	if !context.IsError && rawPath != "" {
		lang = theme.Language(rawPath)
	}
	var rendered []string
	if lang != "" {
		rendered = t.Highlight(tools.ReplaceTabs(output), lang)
	} else {
		rendered = strings.Split(output, "\n")
	}
	lines := tools.TrimTrailingEmptyLines(rendered)
	maxLines := len(lines)
	if !context.Expanded {
		maxLines = min(10, len(lines))
	}
	remaining := len(lines) - maxLines
	shown := make([]string, maxLines)
	for i, line := range lines[:maxLines] {
		if lang != "" {
			shown[i] = tools.ReplaceTabs(line)
		} else {
			shown[i] = t.Fg("toolOutput", tools.ReplaceTabs(line))
		}
	}
	text := "\n" + strings.Join(shown, "\n")
	if remaining > 0 {
		text += "\n" + tools.MoreLinesHint(context.UI, t, remaining, "")
	}
	if cut := truncationOf(result.Details); cut != nil {
		switch {
		case cut.firstLineExceedsLimit:
			text += "\n" + t.Fg("warning", "[First line exceeds "+tools.FormatSize(cut.maxBytes)+" limit]")
		case cut.truncatedBy == "lines":
			text += "\n" + t.Fg("warning", fmt.Sprintf("[Truncated: showing %d of %d lines (%d line limit)]", cut.outputLines, cut.totalLines, cut.maxLines))
		default:
			text += "\n" + t.Fg("warning", fmt.Sprintf("[Truncated: %d lines shown (%s limit)]", cut.outputLines, tools.FormatSize(cut.maxBytes)))
		}
	}
	return text
}

// ReadRenderers is read's card.
var ReadRenderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text := textOf(context.LastComponent)
		rawPath, valid := pathArg(args)
		text.SetText(t.Fg("toolTitle", t.Bold("read")) + " " + tools.RenderToolPath(rawPath, valid, t, context.Cwd, tools.RenderToolPathOptions{}) + formatReadLineRange(args, t))
		return text
	},
	RenderResult: func(result tools.ToolResultView, _ tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text := textOf(context.LastComponent)
		text.SetText(formatReadResult(result, t, context))
		return text
	},
}

// WriteRenderers is write's card.
var WriteRenderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		rawPath, valid := pathArg(args)
		fileContent, contentOK := tools.Str(args.Lookup("content"))
		text := t.Fg("toolTitle", t.Bold("write")) + " " + tools.RenderToolPath(rawPath, valid, t, context.Cwd, tools.RenderToolPathOptions{})
		if !contentOK {
			text += "\n\n" + t.Fg("error", "[invalid content arg - expected string]")
		} else if fileContent != "" {
			// Whole-file highlighting per update: p2 has no incremental highlight cache (the arguments arrive committed).
			lang := ""
			if rawPath != "" {
				lang = theme.Language(rawPath)
			}
			content := tools.ReplaceTabs(tools.NormalizeDisplayText(render.SafeText(fileContent)))
			var rendered []string
			if lang != "" {
				rendered = t.Highlight(content, lang)
			} else {
				rendered = strings.Split(content, "\n")
			}
			lines := tools.TrimTrailingEmptyLines(rendered)
			maxLines := len(lines)
			if !context.Expanded {
				maxLines = min(10, len(lines))
			}
			shown := make([]string, maxLines)
			for i, line := range lines[:maxLines] {
				if lang != "" {
					shown[i] = line
				} else {
					shown[i] = t.Fg("toolOutput", line)
				}
			}
			text += "\n\n" + strings.Join(shown, "\n")
			if remaining := len(lines) - maxLines; remaining > 0 {
				text += "\n" + tools.MoreLinesHint(context.UI, t, remaining, fmt.Sprintf("more lines, %d total", len(lines)))
			}
		}
		component := textOf(context.LastComponent)
		component.SetText(text)
		return component
	},
	RenderResult: func(result tools.ToolResultView, _ tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		output := ""
		if context.IsError {
			output = tools.GetTextOutput(&result)
		}
		component := textOf(context.LastComponent)
		if output != "" {
			component.SetText("\n" + t.Fg("error", output))
		} else {
			component.SetText("")
		}
		return component
	},
}

// EditRenderers is edit's card.
var EditRenderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text := textOf(context.LastComponent)
		rawPath, valid := pathArg(args)
		text.SetText(t.Fg("toolTitle", t.Bold("edit")) + " " + tools.RenderToolPath(rawPath, valid, t, context.Cwd, tools.RenderToolPathOptions{}))
		return text
	},
	RenderResult: func(result tools.ToolResultView, _ tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component, ok := context.LastComponent.(*pitui.Container)
		if !ok {
			component = pitui.NewContainer()
		}
		component.Clear()
		output := ""
		if context.IsError {
			output = t.Fg("error", tools.GetTextOutput(&result))
		} else if diff, ok := record(result.Details).Lookup("diff").(string); ok {
			output = components.RenderDiff(render.SafeText(diff), t)
		}
		if output != "" {
			component.Add(pitui.NewSpacer(1))
			component.Add(pitui.NewPaddedText(output, 0, 0, nil))
		}
		return component
	},
}

// listingRenderers is grep/find/ls: a titled call line and a capped listing.
func listingRenderers(name string, call func(args tools.ToolArgs, t *theme.Theme, cwd string) string, previewLines int, warnings func(details *jsvalue.Object) []string) *tools.ToolRenderers {
	return &tools.ToolRenderers{
		RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
			text := textOf(context.LastComponent)
			text.SetText(t.Fg("toolTitle", t.Bold(name)) + " " + call(args, t, context.Cwd))
			return text
		},
		RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
			output := strings.TrimSpace(tools.GetTextOutput(&result))
			text := ""
			if output != "" {
				lines := strings.Split(output, "\n")
				maxLines := len(lines)
				if !options.Expanded {
					maxLines = min(previewLines, len(lines))
				}
				shown := make([]string, maxLines)
				for i, line := range lines[:maxLines] {
					shown[i] = t.Fg("toolOutput", line)
				}
				text += "\n" + strings.Join(shown, "\n")
				if remaining := len(lines) - maxLines; remaining > 0 {
					text += "\n" + tools.MoreLinesHint(context.UI, t, remaining, "")
				}
			}
			details := record(result.Details)
			notes := warnings(details)
			if cut := truncationOf(details); cut != nil {
				notes = append(notes, tools.FormatSize(cut.maxBytes)+" limit")
			}
			if len(notes) > 0 {
				text += "\n" + t.Fg("warning", "[Truncated: "+strings.Join(notes, ", ")+"]")
			}
			component := textOf(context.LastComponent)
			component.SetText(text)
			return component
		},
	}
}

func pathIn(args tools.ToolArgs, t *theme.Theme, cwd string) string {
	rawPath, ok := tools.Str(args.Lookup("path"))
	if !ok {
		return t.Fg("toolOutput", " in "+tools.InvalidArgText(t))
	}
	if rawPath == "" {
		rawPath = "."
	}
	return t.Fg("toolOutput", " in "+render.SafeText(tools.ShortenPath(rawPath, cwd)))
}

func limitNote(details *jsvalue.Object, key, noun string) []string {
	if n, ok := tools.Num(details.Lookup(key)); ok && n != 0 {
		return []string{fmt.Sprintf("%v %s", n, noun)}
	}
	return nil
}

// GrepRenderers is grep's card.
var GrepRenderers = listingRenderers("grep", func(args tools.ToolArgs, t *theme.Theme, cwd string) string {
	pattern, patternOK := tools.Str(args.Lookup("pattern"))
	glob, _ := tools.Str(args.Lookup("glob"))
	limit, hasLimit := tools.Num(args.Lookup("limit"))
	out := tools.InvalidArgText(t)
	if patternOK {
		out = t.Fg("accent", "/"+render.SafeText(pattern)+"/")
	}
	out += pathIn(args, t, cwd)
	if glob != "" {
		out += t.Fg("toolOutput", " ("+render.SafeText(glob)+")")
	}
	if hasLimit {
		out += t.Fg("toolOutput", fmt.Sprintf(" limit %v", limit))
	}
	return out
}, 15, func(details *jsvalue.Object) []string {
	notes := limitNote(details, "matchLimitReached", "matches limit")
	if truncated, _ := details.Lookup("linesTruncated").(bool); truncated {
		notes = append(notes, "some lines truncated")
	}
	return notes
})

// FindRenderers is find's card.
var FindRenderers = listingRenderers("find", func(args tools.ToolArgs, t *theme.Theme, cwd string) string {
	pattern, patternOK := tools.Str(args.Lookup("pattern"))
	limit, hasLimit := tools.Num(args.Lookup("limit"))
	out := tools.InvalidArgText(t)
	if patternOK {
		out = t.Fg("accent", render.SafeText(pattern))
	}
	out += pathIn(args, t, cwd)
	if hasLimit {
		out += t.Fg("toolOutput", fmt.Sprintf(" (limit %v)", limit))
	}
	return out
}, 20, func(details *jsvalue.Object) []string {
	return limitNote(details, "resultLimitReached", "results limit")
})

// LsRenderers is ls's card.
var LsRenderers = listingRenderers("ls", func(args tools.ToolArgs, t *theme.Theme, cwd string) string {
	rawPath, ok := tools.Str(args.Lookup("path"))
	limit, hasLimit := tools.Num(args.Lookup("limit"))
	out := tools.InvalidArgText(t)
	if ok {
		if rawPath == "" {
			rawPath = "."
		}
		out = t.Fg("accent", render.SafeText(tools.ShortenPath(rawPath, cwd)))
	}
	if hasLimit {
		out += t.Fg("toolOutput", fmt.Sprintf(" (limit %v)", limit))
	}
	return out
}, 20, func(details *jsvalue.Object) []string {
	return limitNote(details, "entryLimitReached", "entries limit")
})

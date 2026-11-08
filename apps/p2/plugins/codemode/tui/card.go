package tui

// Code mode's card in the TUI, copied from coding-agent extensions/codemode/renderer.ts. The call shows the script; the result lists the nested tool calls
// (`context.Calls`: live `pi.live.nestedTools` slots while running, with depth and running output, then the settled `details.calls`, as nested.go reads
// them), followed by the script output without Durable's "Script completed|failed / Wall time / Output:" header.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components/visualtruncate"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const (
	codePreviewLines   = 10
	callPreviewCount   = 8
	outputPreviewLines = 5
	collapsedArgsChars = 80
)

var scriptHeader = regexp.MustCompile(`^Script (completed|failed)\nWall time [\d.]+ seconds\nOutput:\n\n?`)

func formatDuration(ms *float64) string {
	if ms == nil {
		return ""
	}
	if *ms < 1000 {
		return strconv.FormatFloat(math.Round(*ms), 'f', -1, 64) + "ms"
	}
	return fmt.Sprintf("%.1fs", *ms/1000)
}

func statusIcon(call transcript.NestedCall, t *theme.Theme) string {
	switch call.Status {
	case transcript.ToolPending, transcript.ToolRunning:
		return t.Fg("warning", "…")
	case transcript.ToolDone:
		return t.Fg("success", "✓")
	case transcript.ToolError:
		return t.Fg("error", "✗")
	}
	return t.Fg("muted", "⊘")
}

// argsText is one line of a call's arguments: its JSON (as pi), or the cut JSON text a settled list keeps.
func argsText(call transcript.NestedCall) string {
	text := call.ArgsText
	if call.Args != nil {
		text, _ = jsvalue.Stringify(call.Args, 0)
	}
	if text == "{}" {
		return ""
	}
	return render.LineText(text)
}

func formatCall(call transcript.NestedCall, t *theme.Theme, expanded bool) string {
	full := argsText(call)
	args := full
	if !expanded && jsstring.Length(full) > collapsedArgsChars {
		args = jsstring.Slice(full, 0, collapsedArgsChars-3) + "..."
	}
	indent := strings.Repeat("  ", call.Depth)
	duration := formatDuration(call.DurationMs)
	line := indent + statusIcon(call, t) + " " + t.Fg("toolTitle", render.SafeText(call.Name))
	if args != "" {
		line += " " + t.Fg("muted", args)
	}
	if duration != "" {
		line += " " + t.Fg("dim", duration)
	}
	if call.Error != "" && (expanded || call.Status == transcript.ToolError) {
		message := call.Error
		if !expanded {
			message, _, _ = strings.Cut(message, "\n")
		}
		line += "\n" + indent + "    " + t.Fg("error", strings.ReplaceAll(render.SafeText(message), "\n", "\n"+indent+"    "))
	}
	// A running call's newest output line, as the web card shows it.
	if call.Status == transcript.ToolRunning {
		lines := strings.Split(strings.TrimRight(call.Output, " \t\r\n"), "\n")
		if live := strings.TrimSpace(lines[len(lines)-1]); live != "" {
			line += "\n" + indent + "    " + t.Fg("dim", render.LineText(live))
		}
	}
	return line
}

func callLines(calls []transcript.NestedCall, t *theme.Theme, context *tools.ToolRenderContext) []string {
	shown := calls
	if !context.Expanded && len(calls) > callPreviewCount {
		shown = calls[len(calls)-callPreviewCount:]
	}
	lines := make([]string, 0, len(shown)+1)
	if len(shown) < len(calls) {
		lines = append(lines, tools.MoreLinesHint(context.UI, t, len(calls)-len(shown), "earlier calls"))
	}
	for _, call := range shown {
		lines = append(lines, formatCall(call, t, context.Expanded))
	}
	return lines
}

func container(last pitui.Component) *pitui.Container {
	component, ok := last.(*pitui.Container)
	if !ok {
		component = pitui.NewContainer()
	}
	component.Clear()
	return component
}

// Card is code mode's card: the script, its calls (live, then its result's list) and what it printed.
var Card = &tools.ToolRenderers{
	NestedCalls: ScriptCalls,
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		code, valid := tools.Str(args.Lookup("code"))
		title := t.Fg("toolTitle", t.Bold("codemode"))
		component := container(context.LastComponent)
		if !valid {
			component.Add(pitui.NewPaddedText(title+" "+t.Fg("error", "[invalid arg]"), 0, 0, nil))
			return component
		}
		component.Add(pitui.NewPaddedText(title, 0, 0, nil))
		if code != "" {
			highlighted := strings.Join(t.Highlight(tools.ReplaceTabs(strings.TrimRight(render.SafeText(code), " \t\r\n")), "javascript"), "\n")
			if context.Expanded {
				component.Add(pitui.NewPaddedText(highlighted, 0, 0, nil))
			} else {
				component.Add(visualtruncate.NewVisualLinePreview(visualtruncate.VisualLinePreviewOptions{
					Text: highlighted, MaxVisualLines: codePreviewLines, KeepStart: true,
					FormatHint: func(hidden int) string { return tools.MoreLinesHint(context.UI, t, hidden, "more lines") },
				}))
			}
		}
		return component
	},
	RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component := container(context.LastComponent)
		if len(context.Calls) > 0 {
			component.Add(pitui.NewSpacer(1))
			component.Add(pitui.NewPaddedText(strings.Join(callLines(context.Calls, t, context), "\n"), 0, 0, nil))
		}
		output := ""
		if !options.IsPartial {
			output = strings.TrimSpace(scriptHeader.ReplaceAllString(tools.GetTextOutput(&result), ""))
		}
		if output != "" {
			color := "toolOutput"
			if context.IsError {
				color = "error"
			}
			lines := strings.Split(tools.ReplaceTabs(output), "\n")
			for i, line := range lines {
				lines[i] = t.Fg(color, line)
			}
			styled := strings.Join(lines, "\n")
			component.Add(pitui.NewSpacer(1))
			if options.Expanded {
				component.Add(pitui.NewPaddedText(styled, 0, 0, nil))
			} else {
				// Limit wrapped lines, not logical ones: script output is often one long JSON line.
				component.Add(visualtruncate.NewVisualLinePreview(visualtruncate.VisualLinePreviewOptions{
					Text: styled, MaxVisualLines: outputPreviewLines, KeepStart: true,
					FormatHint: func(hidden int) string { return tools.MoreLinesHint(context.UI, t, hidden, "more lines") },
				}))
				if details, ok := result.Details.(*jsvalue.Object); ok {
					if path, ok := details.Lookup("fullOutputPath").(string); ok {
						component.Add(pitui.NewPaddedText(t.Fg("muted", "Full output: "+render.LineText(path)), 0, 0, nil))
					}
				}
			}
		}
		return component
	},
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-codemode", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, tuiruntime.TuiToolRenderers, p2runtime.Entry("codemode", Card))
		return nil
	})
})

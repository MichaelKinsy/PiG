// Package tui is the quiet-tools plugin's TUI facet. Quiet tool cards: every tool call is one dim line (`› read src/foo.ts ✓`) without its output. A failed call,
// an expanded transcript (Ctrl+O) or a clicked line shows the tool's full content, drawn by the renderers this resolver wraps, without the card's box.
//
// pi: apps/p2/plugins/quiet-tools/tui.ts
package tui

import (
	"fmt"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ToolCalls is the `/settings` row: quiet (one line per call) or the tools' own cards.
var ToolCalls = prefs.TuiSettingField{
	Plugin: "quiet-tools", Name: "toolCalls", Label: "Tool calls",
	Description: "quiet: one line per call (Ctrl+O or a click shows it); full: the tools' own cards",
	Values:      []prefs.PluginSettingValue{"quiet", "full"}, Fallback: "quiet",
}

const previewLines = 10

// QuietTools wraps every tool's renderers: one line while collapsed, the full content (without the box) when expanded or failed.
func QuietTools(name string, next func() *tools.ToolRenderers, settings prefs.TuiSettings) *tools.ToolRenderers {
	full := next()
	if prefs.PluginSetting(&settings, ToolCalls) == "full" {
		return full
	}
	isFull := func(context *tools.ToolRenderContext) bool { return context.Expanded || context.IsError }
	// indent is the wrapped content, indented as the card's box would: wrapped renderers see their own last component.
	indent := func(render func(inner *tools.ToolRenderContext) pitui.Component, context *tools.ToolRenderContext) pitui.Component {
		last := context.LastComponent
		if wrapped, ok := last.(*Indent); ok {
			last = wrapped.content
		}
		inner := *context
		inner.LastComponent = last
		content := render(&inner)
		// A renderer that draws its own frame is already laid out.
		if full != nil && full.RenderShell == "self" {
			return content
		}
		return NewIndent(content, context.OutputPad)
	}
	return &tools.ToolRenderers{
		// The card fixes its frame when created: no box, so a one-line call takes a blank line and the line.
		RenderShell: "self",
		RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
			if isFull(context) {
				return indent(func(inner *tools.ToolRenderContext) pitui.Component {
					if full != nil && full.RenderCall != nil {
						return full.RenderCall(args, t, inner)
					}
					return pitui.NewPaddedText(tools.FormatToolCallWithArgs(name, args, t, true), 0, 0, nil)
				}, context)
			}
			status := " …"
			if !context.IsPartial {
				status = " ✓" + duration(context.DurationMs)
			}
			text := t.Fg("muted", "›") + " " + t.Fg("muted", t.Bold(render.LineText(name))) + " " + t.Fg("dim", summary(name, args, context)+status)
			return &OneLine{text: text, pad: context.OutputPad}
		},
		RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
			if !isFull(context) {
				return pitui.NewContainer()
			}
			return indent(func(inner *tools.ToolRenderContext) pitui.Component {
				if full != nil && full.RenderResult != nil {
					return full.RenderResult(result, options, t, inner)
				}
				// The card's own fallback for tools without a result renderer: the output, cut unless expanded.
				lines := strings.Split(tools.GetTextOutput(&result), "\n")
				shown := lines
				if !options.Expanded && len(lines) > previewLines {
					shown = lines[:previewLines]
				}
				styled := make([]string, len(shown))
				for i, line := range shown {
					styled[i] = t.Fg("toolOutput", line)
				}
				text := strings.Join(styled, "\n")
				if more := len(lines) - len(shown); more > 0 {
					text += "\n" + tools.MoreLinesHint(inner.UI, t, more, "")
				}
				return pitui.NewPaddedText(text, 0, 0, nil)
			}, context)
		},
	}
}

// Indent pads the full content left and right, like the card's box without its background.
type Indent struct {
	*pitui.Box
	content pitui.Component
}

// NewIndent is the Indent constructor.
func NewIndent(content pitui.Component, pad int) *Indent {
	box := pitui.NewBox()
	box.PaddingX, box.PaddingY = pad, 0
	box.AddChild(content)
	return &Indent{Box: box, content: content}
}

// OneLine is one line cut to the width, indented like the answer's text.
type OneLine struct {
	text string
	pad  int
}

// Render renders the line.
func (o *OneLine) Render(width int) []string {
	return []string{strings.Repeat(" ", o.pad) + widthx.TruncateToWidth(o.text, max(1, width-o.pad), "…", false)}
}

// Invalidate has nothing to drop.
func (*OneLine) Invalidate() {}

// summary is the argument that tells calls of a tool apart: a path, a command, a pattern, or code mode's calls.
func summary(name string, args tools.ToolArgs, context *tools.ToolRenderContext) string {
	path := func(value any) string { return render.LineText(tools.ShortenPath(value, context.Cwd)) }
	switch name {
	case "read":
		offset := ""
		if n, ok := tools.Num(args.Lookup("offset")); ok {
			offset = fmt.Sprintf(":%v", n)
		}
		value := args.Lookup("path")
		if value == nil {
			value = args.Lookup("file_path")
		}
		return path(value) + offset
	case "write", "edit":
		value := args.Lookup("path")
		if value == nil {
			value = args.Lookup("file_path")
		}
		return path(value)
	case "ls":
		if shown := path(args.Lookup("path")); shown != "" {
			return shown
		}
		return "."
	case "grep", "find":
		pattern, _ := args.Lookup("pattern").(string)
		out := render.LineText(pattern)
		if args.Lookup("path") != nil {
			out += " in " + path(args.Lookup("path"))
		}
		return out
	case "bash", "powershell":
		return firstLine(args.Lookup("command"))
	case "codemode":
		calls := context.Calls
		if len(calls) == 0 {
			return firstLine(args.Lookup("code"))
		}
		var names []string
		seen := map[string]bool{}
		for _, call := range calls {
			if shown := render.LineText(call.Name); !seen[shown] {
				seen[shown] = true
				names = append(names, shown)
			}
		}
		plural := "s"
		if len(calls) == 1 {
			plural = ""
		}
		return fmt.Sprintf("%d call%s: %s", len(calls), plural, strings.Join(names, ", "))
	}
	for _, key := range args.Keys {
		if value, ok := args.Values[key].(string); ok && strings.TrimSpace(value) != "" {
			return firstLine(value)
		}
	}
	return ""
}

// firstLine is the first non-empty line of a string argument, marked when more follow.
func firstLine(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	out := ""
	if len(lines) > 0 {
		out = strings.TrimSpace(render.LineText(lines[0]))
	}
	if len(lines) > 1 {
		out += " …"
	}
	return out
}

// duration is a settled call's duration, when it took long enough to matter.
func duration(ms *float64) string {
	if ms == nil || *ms < 1000 {
		return ""
	}
	return fmt.Sprintf(" %.1fs", *ms/1000)
}

// Facet is the plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-quiet-tools", func(self host.StoreSelf) error {
		// Key 10 runs before other resolvers, so it wraps whatever they and the registry would draw.
		p2runtime.ContributeKeyed(self, tuiruntime.TuiToolRendererResolvers, p2runtime.Entry("10:quiet-tools", tools.ToolRendererResolver(QuietTools)))
		p2runtime.ContributeKeyed(self, tuiruntime.TuiSettingFields, p2runtime.Entry("quiet-tools.toolCalls", ToolCalls))
		return nil
	})
})

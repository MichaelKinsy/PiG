// Package tui is the terminal tool's card in the TUI: the tmux command line, then the output it returned (the last lines;
// expanded, all of it). The TUI has no terminal panel: the owner watches agent terminals in the web.
//
// Ports apps/p2/plugins/terminals/tui.ts.
package tui

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// PreviewLines: collapsed cards show the last lines of the output; expanded ones all.
const PreviewLines = 8

// Renderers draws the terminal tool's calls.
var Renderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		command, _ := tools.Str(args.Lookup("command"))
		words := []string{command}
		if list, ok := args.Lookup("args").([]any); ok {
			for _, each := range list {
				word, _ := tools.Str(each)
				words = append(words, word)
			}
		}
		line := strings.Join(words, " ")
		if line == "" {
			line = "..."
		}
		text.SetText(t.Fg("toolTitle", t.Bold("terminal")) + " " + t.Fg("accent", render.LineText(line)))
		return text
	},
	RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		color := "toolOutput"
		if context.IsError {
			color = "error"
		}
		all := strings.Split(tools.GetTextOutput(&result), "\n")
		for index, line := range all {
			all[index] = t.Fg(color, render.LineText(line))
		}
		shown := all
		if !options.Expanded && len(all) > PreviewLines {
			shown = all[len(all)-PreviewLines:]
		}
		var lines []string
		if hidden := len(all) - len(shown); hidden > 0 {
			lines = append(lines, t.Fg("muted", fmt.Sprintf("... (%d earlier lines, %s to expand)", hidden, context.UI.KeyText("app.tools.expand"))))
		}
		lines = append(lines, shown...)
		text.SetText("\n" + strings.Join(lines, "\n"))
		return text
	},
}

// Facet is the plugin's TUI facet.
var Facet = runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return runtime.Contribute(facet, "p2.tui-terminals", func(self host.StoreSelf) error {
		runtime.ContributeKeyed(self, tuiruntime.TuiToolRenderers, runtime.Entry("terminal", Renderers))
		return nil
	})
})

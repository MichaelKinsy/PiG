// Package tui is the history tool cards in the TUI (docs/history-tool.md section 5), after the web's: a search names its query and hit count, a read its mode and
// count, a handoff its text; expanded, the hit and entry lines.
//
// Ports apps/p2/plugins/history/tui.ts. The tool names are history.HistorySearch/HistoryRead/HistoryHandoff (not imported: the plugin imports this package).
package tui

import (
	"fmt"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const (
	historySearch  = "history_search"
	historyRead    = "history_read"
	historyHandoff = "history_handoff"
)

type row struct {
	entry     float64
	role      string
	author    string
	hasAuthor bool
	text      string
}

func record(value any) *jsvalue.Object {
	object, _ := value.(*jsvalue.Object)
	return object
}

// rows are the hit or entry rows of a card's details.
func rows(value any, field string) []row {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []row
	for _, each := range list {
		object := record(each)
		entry, ok := object.Lookup("entry").(float64)
		if !ok {
			continue
		}
		r := row{entry: entry, role: jsString(object.Lookup("role")), text: jsString(object.Lookup(field))}
		if author, ok := object.Lookup("author").(string); ok {
			r.author, r.hasAuthor = author, true
		}
		out = append(out, r)
	}
	return out
}

// jsString is String(value ?? ""): undefined and null are "".
func jsString(value any) string {
	if value == nil {
		return ""
	}
	return jsvalue.String(value)
}

func textOf(context *tools.ToolRenderContext) *pitui.Text {
	if text, ok := context.LastComponent.(*pitui.Text); ok {
		return text
	}
	return pitui.NewPaddedText("", 0, 0, nil)
}

func listRenderers(name, field, list string) *tools.ToolRenderers {
	return &tools.ToolRenderers{
		RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
			component := textOf(context)
			var what string
			if name == historySearch {
				query, _ := tools.Str(args.Lookup("query"))
				what = render.LineText(query)
			} else {
				mode, _ := tools.Str(args.Lookup("mode"))
				what = mode
				if from, ok := args.Lookup("from").(float64); ok {
					what += fmt.Sprintf(" from #%s", jsvalue.String(from))
				}
			}
			component.SetText(t.Fg("toolTitle", t.Bold(name)) + " " + t.Fg("accent", what))
			return component
		},
		RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
			component := textOf(context)
			if context.IsError {
				component.SetText("\n" + t.Fg("error", tools.GetTextOutput(&result)))
				return component
			}
			details := record(result.Details)
			found := rows(details.Lookup(list), field)
			noun := map[string][2]string{"hits": {"hit", "hits"}, "entries": {"entry", "entries"}}[list]
			word := noun[1]
			if len(found) == 1 {
				word = noun[0]
			}
			marks := []string{fmt.Sprintf("%d %s", len(found), word)}
			if details.Lookup("more") == true {
				marks = append(marks, "more")
			}
			if details.Lookup("truncated") == true {
				marks = append(marks, "truncated")
			}
			var lines []string
			if options.Expanded {
				for _, r := range found {
					who := r.role
					if r.hasAuthor {
						who = r.author
					}
					lines = append(lines, t.Fg("dim", "#"+jsvalue.String(r.entry))+" "+t.Fg("muted", who)+" "+render.LineText(r.text))
				}
			}
			out := "\n" + t.Fg("muted", strings.Join(marks, " · "))
			if len(lines) > 0 {
				out += "\n" + strings.Join(lines, "\n")
			}
			component.SetText(out)
			return component
		},
	}
}

var handoffRenderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component := textOf(context)
		body, _ := tools.Str(args.Lookup("text"))
		first := ""
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" {
				first = line
				break
			}
		}
		component.SetText(t.Fg("toolTitle", t.Bold(historyHandoff)) + " " + render.LineText(first))
		return component
	},
	RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component := textOf(context)
		if context.IsError {
			component.SetText("\n" + t.Fg("error", tools.GetTextOutput(&result)))
			return component
		}
		handoff, _ := tools.Str(record(result.Details).Lookup("text"))
		note := t.Fg("muted", "New context started; earlier history stays searchable.")
		var lines []string
		if options.Expanded {
			for _, line := range strings.Split(handoff, "\n") {
				lines = append(lines, render.LineText(line))
			}
		}
		component.SetText("\n" + strings.Join(append(lines, note), "\n"))
		return component
	},
}

// Facet is the plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-history", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiToolRenderers,
			p2runtime.Entry(historySearch, listRenderers(historySearch, "snippet", "hits")),
			p2runtime.Entry(historyRead, listRenderers(historyRead, "preview", "entries")),
			p2runtime.Entry(historyHandoff, handoffRenderers))
		return nil
	})
})

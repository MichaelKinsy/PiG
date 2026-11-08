package runtime

// Rendering contributions (p2-tui-port.md section 7.2). Ordered registries use numeric key prefixes (`<order>:<plugin>`), applied in key order.
//
// pi: apps/p2/src/runtime/tui-renderers.ts

import (
	"slices"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
)

// TuiToolRenderers are tool renderers by tool name, from the plugins that own the tools; a tool without any gets pi's generic card.
var TuiToolRenderers = p2runtime.NewKeyedRegistry[*tools.ToolRenderers]("p2.tui-tool-renderers")

// TuiToolRendererResolvers are `ToolRendererResolver`s with `next()`, run in key order before TuiToolRenderers.
var TuiToolRendererResolvers = p2runtime.NewKeyedRegistry[tools.ToolRendererResolver]("p2.tui-tool-renderer-resolvers")

// TuiEntryRenderers are components for committed entries by entry kind. A registered kind replaces the transcript's own display of the entry
// (people's messages, notices); a renderer returning nil hides it. Entry data is server data: sanitize text you show.
var TuiEntryRenderers = p2runtime.NewKeyedRegistry[*components.EntryRenderer]("p2.tui-entry-renderers")

// TuiMarkdownTransformers are pi's `MarkdownTransformer`s: they rewrite Markdown source before rendering; errors keep the current text.
var TuiMarkdownTransformers = p2runtime.NewKeyedRegistry[render.MarkdownTransformer]("p2.tui-markdown-transformers")

// Ordered is the values of an ordered registry table, in key order (`a.localeCompare(b, "en", { numeric: true })`).
func Ordered[V any](table *p2runtime.KeyedTable[V]) []V {
	keys := table.Keys()
	slices.SortStableFunc(keys, jsvalue.LocaleCompareNumeric)
	values := make([]V, 0, len(keys))
	for _, key := range keys {
		value, _ := table.Get(key)
		values = append(values, value)
	}
	return values
}

package tools

// How a tool call is drawn: plugins register their tools' renderers (`TuiToolRenderers`, by tool name) and resolvers (`TuiToolRendererResolvers`,
// wrapping all of them); a tool without any gets pi's generic card.
//
// pi: apps/p2/src/tui/tools/index.ts

import (
	"slices"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
)

// NamedResolver is a resolver with its registry key.
type NamedResolver struct {
	Key      string
	Resolver ToolRendererResolver
}

// ResolveToolRenderers is resolveToolRenderers. Section 7.2: ordered resolvers (by key), then the per-name registry; nil means the generic card. The
// registry's `nestedCalls` is the tool's own data, not drawing: it stays whatever resolvers draw the card with.
func ResolveToolRenderers(name string, resolvers []NamedResolver, registered map[string]*ToolRenderers, settings prefs.TuiSettings) *ToolRenderers {
	ordered := slices.Clone(resolvers)
	slices.SortStableFunc(ordered, func(a, b NamedResolver) int { return jsvalue.LocaleCompareNumeric(a.Key, b.Key) })
	own := registered[name]
	var at func(index int) *ToolRenderers
	at = func(index int) *ToolRenderers {
		if index >= len(ordered) {
			return own
		}
		var drawn *ToolRenderers
		failed := func() (failed bool) {
			defer func() {
				if recover() != nil {
					failed = true
				}
			}()
			drawn = ordered[index].Resolver(name, func() *ToolRenderers { return at(index + 1) }, settings)
			return false
		}()
		if failed {
			return at(index + 1)
		}
		return drawn
	}
	drawn := at(0)
	if own == nil || own.NestedCalls == nil || drawn == own {
		return drawn
	}
	merged := ToolRenderers{}
	if drawn != nil {
		merged = *drawn
	}
	merged.NestedCalls = own.NestedCalls
	return &merged
}

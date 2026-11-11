package views

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports apps/p2/plugins/views/layout.ts: arrangementError (the part the agent's `arrange` action needs; the rest of the
// file is the browser's layout tree, vendored under web/upstream and run by Pi's views-layout test).

const nodeShape = "a node is a view ID, { tabs }, { row } or { column }"

// ArrangementError is why an agent's arrangement (the `view` tool's `arrange`) is no layout of the views known (the
// transcript included), or "" when it is one. A node is a view's ID, `{ tabs: [IDs], active? }`, or `{ row | column:
// [nodes], sizes? }`; each view at most once.
func ArrangementError(value any, known map[string]bool) string {
	seen := map[string]bool{}
	var visit func(node any, depth int) string
	visit = func(node any, depth int) string {
		if depth > 8 {
			return "nested too deep"
		}
		if id, ok := node.(string); ok {
			switch {
			case !known[id]:
				return fmt.Sprintf("no view %q", id)
			case seen[id]:
				return fmt.Sprintf("%q appears twice", id)
			}
			seen[id] = true
			return ""
		}
		record, ok := node.(*delta.JsonObject)
		if !ok {
			return nodeShape
		}
		if tabs, ok := record.Value("tabs").([]any); ok {
			if len(tabs) == 0 {
				return "empty tabs"
			}
			for _, id := range tabs {
				text, isText := id.(string)
				if !isText {
					return "tabs hold view IDs"
				}
				if err := visit(text, depth+1); err != "" {
					return err
				}
			}
			if active := record.Value("active"); record.Has("active") && !slices.Contains(tabs, active) {
				return "active is not one of its tabs"
			}
			return ""
		}
		children, _ := record.Value("row").([]any)
		if _, isRow := record.Value("row").([]any); !isRow {
			children, _ = record.Value("column").([]any)
		}
		if len(children) == 0 {
			return nodeShape + " with children"
		}
		if record.Has("sizes") {
			sizes, isList := record.Value("sizes").([]any)
			valid := isList && len(sizes) == len(children)
			for _, size := range sizes {
				number, isNumber := size.(float64)
				valid = valid && isNumber && number > 0
			}
			if !valid {
				return "sizes: one positive number per child"
			}
		}
		for _, child := range children {
			if err := visit(child, depth+1); err != "" {
				return err
			}
		}
		return ""
	}
	return visit(value, 0)
}

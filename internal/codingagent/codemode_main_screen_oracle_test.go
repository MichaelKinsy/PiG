package codingagent

import (
	"fmt"
	"strings"
)

// funcComponent is a tui.Component drawn by a function.
type funcComponent struct{ render func(width int) []string }

func (c funcComponent) Render(width int) []string { return c.render(width) }
func (c funcComponent) Invalidate()               {}

func optionalErrorText(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

// codemodeSurrogateCutProbes are calls whose arguments the collapsed card cuts at 77 UTF-16 units, at every parity of the cut
// against the emoji (a pair split, a pair kept), at widths that fit and exactly fill the padded row.
func codemodeSurrogateCutProbes() ([]toolCardProbe, []string) {
	var probes []toolCardProbe
	var names []string
	for _, width := range []int{120, 100, 80, 40} {
		for _, prefix := range []int{0, 1, 2, 3, 4, 40} {
			for _, expanded := range []bool{false, true} {
				args := strings.Repeat("a", prefix) + strings.Repeat("😀", 41)
				calls := []any{map[string]any{"name": "read", "args": args, "status": "ok", "durationMs": 12}}
				probes = append(probes, toolCardProbe{
					Tool: "codemode", Args: map[string]any{"code": "return 1"}, Expanded: expanded, Width: width, CWD: "/tmp",
					Result: &toolCardResult{Content: cardText("Script completed\nWall time 0.1 seconds\nOutput:\nok"), Details: map[string]any{"calls": calls}},
				})
				names = append(names, fmt.Sprintf("prefix=%d width=%d expanded=%v", prefix, width, expanded))
			}
		}
	}
	return probes, names
}

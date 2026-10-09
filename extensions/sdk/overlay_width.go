package sdk

import (
	"math"
	"regexp"
	"strconv"
)

// overlayPercent matches upstream parseSizeValue's percentage form (tui.ts:232).
var overlayPercent = regexp.MustCompile(`^(\d+(?:\.\d+)?)%$`)

// remoteRenderWidth maps the terminal width to the width a focused component renders at, from the encoded ui.custom options. Pi renders an overlay component at the width TUI.resolveOverlayLayout resolves (tui.ts:1212-1233), min(80, available) by default, and an inline component at the terminal width. The host composites an overlay with the same layout unless it opens the legacy titled modal, which has no overlayOptions and sets title, widthFraction or heightFraction; that modal and an inline component render at the terminal width.
func remoteRenderWidth(args map[string]any) func(int) int {
	terminal := func(width int) int { return width }
	if overlay, _ := args["overlay"].(bool); !overlay {
		return terminal
	}
	layout, hasLayout := args["overlayOptions"].(map[string]any)
	title, _ := args["title"].(string)
	widthFraction, _ := args["widthFraction"].(float64)
	heightFraction, _ := args["heightFraction"].(float64)
	if !hasLayout && (title != "" || widthFraction > 0 || heightFraction > 0) {
		return terminal
	}
	return func(width int) int { return resolveOverlayWidth(layout, width) }
}

// resolveOverlayWidth is the width rule of upstream TUI.resolveOverlayLayout (tui.ts:1212-1233).
func resolveOverlayWidth(layout map[string]any, termWidth int) int {
	edge := func(name string) float64 { return 0 }
	switch margin := layout["margin"].(type) {
	case float64:
		edge = func(string) float64 { return margin }
	case map[string]any:
		edge = func(name string) float64 { value, _ := margin[name].(float64); return value }
	}
	availWidth := max(1, float64(termWidth)-max(0, edge("left"))-max(0, edge("right")))
	width, ok := parseOverlaySize(layout["width"], float64(termWidth))
	if !ok {
		width = min(80, availWidth)
	}
	if minWidth, ok := layout["minWidth"].(float64); ok {
		width = max(width, minWidth)
	}
	return int(math.Trunc(max(1, min(width, availWidth))))
}

// parseOverlaySize is upstream parseSizeValue (tui.ts:228-237): a number is cells, "N%" is a floored share of reference, anything else is unset.
func parseOverlaySize(value any, reference float64) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case string:
		match := overlayPercent.FindStringSubmatch(value)
		if match == nil {
			return 0, false
		}
		percent, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return 0, false
		}
		return math.Floor(reference * percent / 100), true
	}
	return 0, false
}

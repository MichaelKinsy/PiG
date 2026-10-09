// Ports packages/coding-agent/src/modes/interactive/components/settings-selector.ts (fullscreen-wheel-scroll-lines row).
package codingagent

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// wheelScrollLinesLabel is String(config.fullscreenWheelScrollLines): "auto" or the line count.
func wheelScrollLinesLabel(lines WheelScrollLines) string {
	if lines.Auto {
		return "auto"
	}
	return strconv.FormatFloat(lines.Lines, 'f', -1, 64)
}

// wheelScrollLinesValues lists "auto" first, then 1, 2, 3, 5, 10 and the current count in ascending order without duplicates (settings-selector.ts:739-745).
func wheelScrollLinesValues(current WheelScrollLines) []string {
	counts := []float64{1, 2, 3, 5, 10}
	if !current.Auto && !slices.Contains(counts, current.Lines) {
		counts = append(counts, current.Lines)
	}
	slices.Sort(counts)
	values := []string{"auto"}
	for _, count := range counts {
		values = append(values, strconv.FormatFloat(count, 'f', -1, 64))
	}
	return values
}

// parseWheelScrollLines is `newValue === "auto" ? "auto" : parseInt(newValue, 10)` (settings-selector.ts:974).
func parseWheelScrollLines(value string) WheelScrollLines {
	if value == "auto" {
		return WheelScrollLines{Auto: true}
	}
	trimmed := strings.TrimSpace(value)
	end := 0
	if trimmed != "" && (trimmed[0] == '+' || trimmed[0] == '-') {
		end++
	}
	for end < len(trimmed) && trimmed[end] >= '0' && trimmed[end] <= '9' {
		end++
	}
	lines, err := strconv.ParseFloat(trimmed[:end], 64)
	if err != nil {
		lines = math.NaN()
	}
	return WheelScrollLines{Lines: lines}
}

// tuiWheelScrollLines converts a stored setting to the renderer's option.
func tuiWheelScrollLines(lines WheelScrollLines) tui.WheelScrollLines {
	return tui.WheelScrollLines{Auto: lines.Auto, Lines: lines.Lines}
}

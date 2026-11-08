// Package render is what copied coding-agent components need instead of its globals (`theme`, `keyHint`, `process.platform`): p2-tui-port.md
// section 5.4. P0d's TuiContext is a superset of RenderContext.
//
// pi: apps/p2/src/tui/render-context.ts
package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// RenderContext is the interface of render-context.ts RenderContext.
type RenderContext interface {
	// Theme is the swappable theme.
	Theme() *theme.ThemeRef
	// KeyText is the formatted keys of a binding id ("ctrl+o"), "" when unbound.
	KeyText(id string) string
	// RequestRender is for components with timers or asynchronous state.
	RequestRender()
}

// SafeText is safeText: server/provider text must not carry terminal control sequences (cursor moves, OSC clipboard, ...).
func SafeText(text string) string {
	stripped := widthx.StripTerminalSequences(text)
	var b strings.Builder
	b.Grow(len(stripped))
	for _, r := range stripped {
		if r <= 0x08 || (r >= 0x0b && r <= 0x1f) || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LineText is lineText: one line of server text.
func LineText(text string) string {
	safe := SafeText(text)
	var b strings.Builder
	b.Grow(len(safe))
	inRun := false
	for _, r := range safe {
		if r == '\r' || r == '\n' || r == '\t' {
			if !inRun {
				b.WriteByte(' ')
				inRun = true
			}
			continue
		}
		inRun = false
		b.WriteRune(r)
	}
	return b.String()
}

// FormatKeyText is a copy of coding-agent keybinding-hints `formatKeyText`, without the local platform.
func FormatKeyText(key string, capitalize bool) string {
	alternatives := strings.Split(key, "/")
	for i, alternative := range alternatives {
		parts := strings.Split(alternative, "+")
		if capitalize {
			for j, part := range parts {
				if part != "" {
					parts[j] = strings.ToUpper(part[:1]) + part[1:]
				}
			}
		}
		alternatives[i] = strings.Join(parts, "+")
	}
	return strings.Join(alternatives, "/")
}

// KeyHint is keyHint: the keys of a binding id with a description; plugin ids are not in pi-tui's `Keybinding` union.
func KeyHint(ctx RenderContext, id, description string) string {
	current := ctx.Theme().Current()
	return current.Fg("dim", ctx.KeyText(id)) + current.Fg("muted", " "+description)
}

// FormatTokens is the compact token notation copied from coding-agent's footer.
func FormatTokens(count int) string {
	switch {
	case count < 1000:
		return fmt.Sprint(count)
	case count < 10000:
		return toFixed1(float64(count)/1000) + "k"
	case count < 1000000:
		return fmt.Sprintf("%dk", jsRound(float64(count)/1000))
	}
	return toFixed1(float64(count)/1000000) + "M"
}

// jsRound is Math.round: halves round toward +Infinity.
func jsRound(x float64) int { return int(x + 0.5) }

// toFixed1 is (x).toFixed(1) for the non-negative values FormatTokens passes: the decimal nearest x, the larger one on an exact tie (strconv rounds ties to even).
func toFixed1(x float64) string {
	exact := strconv.FormatFloat(x, 'f', 60, 64)
	point := strings.IndexByte(exact, '.')
	tail := exact[point+2:]
	if strings.HasPrefix(tail, "5") && strings.Trim(tail[1:], "0") == "" {
		// An exact tie: round the first decimal up.
		next := strconv.FormatFloat(x+0.05, 'f', 1, 64)
		return next
	}
	return strconv.FormatFloat(x, 'f', 1, 64)
}

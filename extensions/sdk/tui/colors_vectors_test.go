package tui

import (
	"fmt"
	"math"
	"testing"
)

func decodeColor(t *testing.T, v any) Color {
	m := v.(map[string]any)
	switch m["kind"] {
	case "indexed":
		return IndexedColor{Index: int(m["index"].(float64))}
	case "rgb":
		return RgbColorValue{R: m["r"].(float64), G: m["g"].(float64), B: m["b"].(float64)}
	case "oklch":
		return OklchColorValue{L: m["l"].(float64), C: m["c"].(float64), H: m["h"].(float64)}
	}
	t.Fatalf("color %v", v)
	return nil
}

func colorJSON(c Color) map[string]any {
	switch c := c.(type) {
	case IndexedColor:
		return map[string]any{"kind": "indexed", "index": float64(c.Index)}
	case RgbColorValue:
		return map[string]any{"kind": "rgb", "r": c.R, "g": c.G, "b": c.B}
	case OklchColorValue:
		return map[string]any{"kind": "oklch", "l": c.L, "c": c.C, "h": c.H}
	}
	return nil
}

// near is exact for integers and strings and 1e-9 for the transcendental channels: V8 and Go round sin, cos, atan2 and cbrt differently
// in the last bits, and every byte a color sequence prints is rounded long before that.
func near(got, want any) bool {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		return ok && (g == w || math.Abs(g-w) <= 1e-9*math.Max(1, math.Abs(w)) || (math.IsNaN(g) && math.IsNaN(w)))
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return false
		}
		// A neutral color has no hue (chroma or saturation is rounding noise), and atan2 of that noise differs between engines.
		neutral := false
		for _, k := range []string{"c", "s"} {
			if c, ok := w[k].(float64); ok && c < 1e-9 {
				neutral = true
			}
		}
		for k, wv := range w {
			if k == "h" && neutral {
				continue
			}
			if !near(g[k], wv) {
				return false
			}
		}
		return true
	}
	return got == want
}

func TestColorVectorsMatchPi(t *testing.T) {
	var v struct{ Ops [][3]any }
	loadVectors(t, "tui-colors.json", &v)
	if len(v.Ops) < 1000 {
		t.Fatalf("only %d color vectors", len(v.Ops))
	}
	fails := 0
	for _, op := range v.Ops {
		name, args, want := op[0].(string), op[1].([]any), op[2].(map[string]any)
		for i, a := range args {
			if a == nil && name != "mixColors" && name != "styleTextWithAnsi" {
				args[i] = math.NaN() // JSON.stringify writes NaN and Infinity as null; both fail requireFinite with the same message
			}
		}
		var got any
		var err error
		mode := func(i int) TerminalColorMode { return TerminalColorMode(args[i].(string)) }
		switch name {
		case "parseColor":
			var c Color
			if f, ok := args[0].(float64); ok {
				c, err = ParseColor(f)
			} else if args[0] == "NaN" {
				c, err = ParseColor(math.NaN())
			} else {
				c, err = ParseColor(args[0].(string))
			}
			if err == nil {
				got = colorJSON(c)
			}
		case "colorToRgb":
			r := ColorToRgb(decodeColor(t, args[0]))
			got = map[string]any{"r": r.R, "g": r.G, "b": r.B}
		case "colorToHex":
			got = ColorToHex(decodeColor(t, args[0]))
		case "colorToOklch":
			o := ColorToOklch(decodeColor(t, args[0]))
			got = map[string]any{"l": o.L, "c": o.C, "h": o.H}
		case "colorToOkhsl":
			o := ColorToOkhsl(decodeColor(t, args[0]))
			got = map[string]any{"h": o.H, "s": o.S, "l": o.L}
		case "foregroundAnsi":
			got = ForegroundAnsi(decodeColor(t, args[0]), mode(1))
		case "backgroundAnsi":
			got = BackgroundAnsi(decodeColor(t, args[0]), mode(1))
		case "rgbColor":
			var c RgbColorValue
			c, err = NewRgbColor(args[0].(float64), args[1].(float64), args[2].(float64))
			got = colorJSON(c)
		case "oklchColor":
			var c OklchColorValue
			c, err = NewOklchColor(args[0].(float64), args[1].(float64), args[2].(float64))
			got = colorJSON(c)
		case "okhslColor":
			var c RgbColorValue
			c, err = NewOkhslColor(args[0].(float64), args[1].(float64), args[2].(float64))
			got = colorJSON(c)
		case "indexedColor":
			var c IndexedColor
			f := args[0].(float64)
			if f != math.Trunc(f) {
				err = fmt.Errorf("ANSI color index must be an integer from 0 to 255: %v", f)
			} else {
				c, err = NewIndexedColor(int(f))
			}
			got = colorJSON(c)
		case "mixColors":
			space := ColorMixSpaceOklch
			if s, ok := args[3].(string); ok {
				space = ColorMixSpace(s)
			}
			var c Color
			c, err = MixColors(decodeColor(t, args[0]), decodeColor(t, args[1]), args[2].(float64), space)
			if err == nil {
				got = colorJSON(c)
			}
		case "styleText":
			s := args[1].(map[string]any)
			style := TextStyle{TextAttributes: TextAttributes{Bold: s["bold"] == true, Dim: s["dim"] == true, Italic: s["italic"] == true, Underline: s["underline"] == true, Inverse: s["inverse"] == true, Strikethrough: s["strikethrough"] == true}}
			if fg, ok := s["fg"]; ok {
				style.Fg = decodeColor(t, fg)
			}
			if bg, ok := s["bg"]; ok {
				style.Bg = decodeColor(t, bg)
			}
			got = StyleText(args[0].(string), style, mode(2))
		case "styleTextWithAnsi":
			s := args[3].(map[string]any)
			a := TextAttributes{Bold: s["bold"] == true, Dim: s["dim"] == true, Italic: s["italic"] == true, Underline: s["underline"] == true, Inverse: s["inverse"] == true, Strikethrough: s["strikethrough"] == true}
			fg, _ := args[1].(string)
			bg, _ := args[2].(string)
			got = StyleTextWithAnsi(args[0].(string), fg, bg, a)
		default:
			t.Fatalf("unknown op %s", name)
		}
		if msg, isErr := want["error"]; isErr {
			if err == nil || err.Error() != msg {
				if fails++; fails < 20 {
					t.Errorf("%s%v: error %v, Pi %q", name, args, err, msg)
				}
			}
			continue
		}
		if err != nil || !near(got, want["ok"]) {
			if fails++; fails < 20 {
				t.Errorf("%s%v = %v (err %v), Pi %v", name, args, got, err, want["ok"])
			}
		}
	}
}

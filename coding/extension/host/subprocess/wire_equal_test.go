package subprocess

import (
	"bytes"
	"encoding/json"
	"testing"
)

// equalWireValue is true exactly when two values encode to the same JSON, for the shapes a state push compares, and allocates nothing for them.
func TestEqualWireValueMatchesTheJSONEncoding(t *testing.T) {
	palette := func(accent string, extra any) map[string]any {
		return map[string]any{
			"name": "dark", "modifiers": true, "mode": "truecolor",
			"foregrounds": map[string]string{"accent": accent, "text": "\x1b[39m"},
			"colors":      map[string]any{"accent": map[string]any{"kind": "indexed", "index": 5}, "dim": map[string]any{"kind": "oklch", "l": 0.5, "c": 0.0016, "h": 286.3}},
			"extra":       extra,
		}
	}
	cases := []struct {
		name string
		a, b any
	}{
		{"nil and nil", nil, nil},
		{"nil and map", nil, map[string]any{}},
		{"equal palettes", palette("a", []any{"x", 1.5}), palette("a", []any{"x", 1.5})},
		{"nested string differs", palette("a", nil), palette("b", nil)},
		{"slice element differs", palette("a", []any{"x", 1.5}), palette("a", []any{"x", 2.5})},
		{"slice length differs", palette("a", []any{"x"}), palette("a", []any{"x", "y"})},
		{"missing key", map[string]any{"a": 1, "b": 2}, map[string]any{"a": 1, "c": 2}},
		{"int and float of one value", map[string]any{"n": 1}, map[string]any{"n": 1.0}},
		{"string and bool", "true", true},
		{"string differs", map[string]any{"name": "dark"}, map[string]any{"name": "light"}},
		{"bool differs", map[string]any{"modifiers": true}, map[string]any{"modifiers": false}},
		{"int differs", map[string]any{"index": 5}, map[string]any{"index": 6}},
		{"float differs", map[string]any{"l": 0.5}, map[string]any{"l": 0.6}},
		{"map[string]string differs", map[string]string{"a": "1"}, map[string]string{"a": "2"}},
		{"map[string]string length differs", map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}},
		{"pointers to equal structs", &TerminalCapabilitiesPayload{Images: "kitty", TrueColor: true}, &TerminalCapabilitiesPayload{Images: "kitty", TrueColor: true}},
		{"pointers to different structs", &TerminalCapabilitiesPayload{Images: "kitty"}, &TerminalCapabilitiesPayload{Images: "iterm2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ja, _ := json.Marshal(tc.a)
			jb, _ := json.Marshal(tc.b)
			// int and float64 of one value encode alike; the comparison is stricter, which only costs a resend.
			if got, same := equalWireValue(tc.a, tc.b), bytes.Equal(ja, jb); got && !same {
				t.Fatalf("equalWireValue(%s, %s) = true", ja, jb)
			} else if !got && same && tc.name != "int and float of one value" {
				t.Fatalf("equalWireValue(%s, %s) = false for identical encodings", ja, jb)
			}
		})
	}
	a, b := palette("a", []any{"x"}), palette("a", []any{"x"})
	if allocs := testing.AllocsPerRun(100, func() { _ = equalWireValue(a, b) }); allocs != 0 {
		t.Fatalf("a palette comparison allocated %v times", allocs)
	}
}

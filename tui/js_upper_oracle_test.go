package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// jsUpperFirstUnit is s.charAt(0).toUpperCase() + s.slice(1) (keybinding hints, alt-screen search labels). Every code point, as a one-character string
// and followed by a tail, against Node: this covers the SpecialCasing table in js_upper.go and the simple mappings of Go's unicode tables.
func TestJSUpperFirstUnitMatchesNode(t *testing.T) {
	var inputs []string
	for cp := 0; cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		s := string(rune(cp))
		inputs = append(inputs, s)
		if cp < 0x3000 || cp >= 0x1E000 && cp < 0x1F000 {
			inputs = append(inputs, s+"ab")
		}
	}
	var want []string
	pioracle.Run(t, `emit(input.map((s) => s.charAt(0).toUpperCase() + s.slice(1)));`, inputs, &want)
	if len(want) != len(inputs) {
		t.Fatalf("Node returned %d results for %d inputs", len(want), len(inputs))
	}
	bad := 0
	for i, s := range inputs {
		if got := jsUpperFirstUnit(s); got != want[i] {
			if bad++; bad <= 8 {
				t.Errorf("%+q: got %+q, want %+q", s, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d inputs differ", bad, len(inputs))
	}
}

package ai

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/ai/src/utils/sanitize-unicode.ts sanitizeSurrogates against pinned Pi over random UTF-16 code-unit sequences made of paired and unpaired surrogates at the range edges, BMP neighbours and ASCII. Each sequence reaches Go in the canonical WTF-8 form (a pair is one four-byte scalar) and in the split form (every surrogate three bytes).
func TestSanitizeSurrogatesMatchesPi(t *testing.T) {
	units := []uint16{'a', 'b', 0xd7ff, 0xd800, 0xd83d, 0xdbff, 0xdc00, 0xde48, 0xdfff, 0xe000, 0xfffd, 0x1f}
	rng := rand.New(rand.NewSource(20261010))
	var inputs [][]uint16
	for range 4000 {
		seq := make([]uint16, rng.Intn(10))
		for i := range seq {
			seq[i] = units[rng.Intn(len(units))]
		}
		inputs = append(inputs, seq)
	}
	var want [][]uint16
	pioracle.Run(t, `
const mod = await load("pi-ai/utils/sanitize-unicode.js");
emit(input.map((units) => { const out = mod.sanitizeSurrogates(String.fromCharCode(...units)); return Array.from({ length: out.length }, (_, i) => out.charCodeAt(i)); }));`, inputs, &want)
	split := func(seq []uint16) string {
		var b []byte
		for _, u := range seq {
			if u >= 0xd800 && u <= 0xdfff {
				b = append(b, 0xed, byte(0x80|(u>>6)&0x3f), byte(0x80|u&0x3f))
			} else {
				b = append(b, []byte(string(rune(u)))...)
			}
		}
		return string(b)
	}
	failures := 0
	for i, seq := range inputs {
		for name, in := range map[string]string{"canonical": jsstring.FromUTF16(seq), "split": split(seq)} {
			if got := jsstring.ToUTF16(sanitizeSurrogates(in)); !slices.Equal(got, want[i]) {
				if failures++; failures <= 8 {
					t.Errorf("%s %x: sanitizeSurrogates = %x, Pi %x", name, seq, got, want[i])
				}
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d inputs differ from Pi", failures)
	}
}

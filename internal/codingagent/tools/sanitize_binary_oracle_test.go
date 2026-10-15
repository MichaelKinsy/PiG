package tools

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/utils/shell.ts sanitizeBinaryOutput, against pinned Pi over every control character, the C1 range, the U+FFF9..U+FFFB annotation characters and their neighbours, astral characters, and random mixtures of them.
func TestSanitizeBinaryOutputMatchesPi(t *testing.T) {
	var inputs []string
	for r := range rune(0x200) {
		inputs = append(inputs, "a"+string(r)+"b")
	}
	for r := rune(0xFFF0); r <= 0xFFFF; r++ {
		inputs = append(inputs, "a"+string(r)+"b")
	}
	pieces := []string{"a", "é", "日本", "🙂", "\n", "\r\n", "\t", "\x00", "\x07", "\x08", "\x0b", "\x0c", "\x0e", "\x1b[31m", "\x1f", "\x7f", "\u0080", "\u009b", "\u009f", "\ufff8", "\ufff9", "\ufffa", "\ufffb", "\ufffc", "\ufffd", "\u2028", "\ufeff"}
	rng := rand.New(rand.NewSource(20261010))
	for range 2000 {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		inputs = append(inputs, b.String())
	}
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/utils/shell.js");
emit(input.map((s) => mod.sanitizeBinaryOutput(s)));`, inputs, &want)
	failures := 0
	for i, s := range inputs {
		if got := SanitizeBinaryOutput(s); got != want[i] {
			if failures++; failures <= 8 {
				t.Errorf("SanitizeBinaryOutput(%q) = %q, Pi %q", s, got, want[i])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d inputs differ from Pi", failures)
	}
}

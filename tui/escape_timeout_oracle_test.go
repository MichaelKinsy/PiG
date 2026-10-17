package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// terminal.ts resolveEscapeTimeoutMs reads PI_TUI_ESC_TIMEOUT with Number(): JS whitespace (U+FEFF, NBSP, the line separators, not U+0085) is trimmed, and the
// 0x/0o/0b forms, exponents and out-of-range literals follow StringToNumber. The pinned Pi's function and ResolveEscapeTimeoutMs get the same environments.
func TestResolveEscapeTimeoutMatchesPi(t *testing.T) {
	values := []string{"", " ", "0", "1", "50", " 50 ", "\ufeff50", "50\u00a0", "\u202850\u2029", "\u008550", "50\u0085", "50.5", "1e2", "1E2", "1e400", "-5", "-0", "+7", "0x10", "0X10", "0o17", "0b101",
		"0x", "0x1ffffffffffffffff", "0xg", "1_0", "Infinity", "-Infinity", "+Infinity", "infinity", "NaN", "inf", "1e", ".5", "5.", "..5", "0x1p3", "1d", "١٢", "50abc", "5 0", "\u180e50", "\u200b50", "\u30005\u3000"}
	type env struct {
		Value string `json:"value"`
		SSH   bool   `json:"ssh"`
	}
	var probes []env
	for _, v := range values {
		probes = append(probes, env{v, false}, env{v, true})
	}
	var want []float64
	pioracle.Run(t, `
const m = await load("pi-tui/terminal.js");
emit(input.map((p) => m.resolveEscapeTimeoutMs(Object.assign({ PI_TUI_ESC_TIMEOUT: p.value }, p.ssh ? { SSH_CONNECTION: "1" } : {}))));
`, probes, &want)
	bad := 0
	for i, p := range probes {
		got := ResolveEscapeTimeoutMs(func(name string) string {
			switch name {
			case "PI_TUI_ESC_TIMEOUT":
				return p.Value
			case "SSH_CONNECTION":
				if p.SSH {
					return "1"
				}
			}
			return ""
		})
		if got != want[i] {
			if bad++; bad <= 10 {
				t.Errorf("PI_TUI_ESC_TIMEOUT=%q ssh=%v: PiG %v, Pi %v", p.Value, p.SSH, got, want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("%d of %d environments differ", bad, len(probes))
	}
}

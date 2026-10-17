package tui

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// terminal.ts ProcessTerminal.columns/rows are `process.stdout.columns || Number(process.env.COLUMNS) || 80` (LINES, 24) when stdout has no size. Number() trims JS whitespace and reads 0x/0o/0b, exponents and fractions, and
// 0 and NaN fall through to the default. A negative, fractional or infinite value is returned by Pi as it is; PiG's width is a positive int, so those are excluded here (handoffs/owner-signoff.md, "COLUMNS and LINES that
// are not positive integers"), and PiG falls back to the default for the non-positive ones.
func TestProcessTerminalSizeFromEnvMatchesPi(t *testing.T) {
	values := []string{"", " ", "0", "-0", "1", "80", "120", " 120 ", "\ufeff120", "120\u00a0", "\u2028120\u2029", "\u0085120", "120\u0085", "1e2", "1E2", "0x50", "0X50", "0o120", "0b1111000", "0x", "0xg", "1_0", "+120", "12 0", "abc", "120px", "NaN", "00120", "1e3", "0x1ffffffffffffffff",
		"-5", "120.5", "0.5", "Infinity", "-Infinity", "1e400"}
	type env struct {
		Columns string `json:"columns"`
		Lines   string `json:"lines"`
	}
	var probes []env
	for _, v := range values {
		probes = append(probes, env{v, ""}, env{"", v})
	}
	type size struct {
		Columns float64 `json:"columns"`
		Rows    float64 `json:"rows"`
	}
	var want []size
	pioracle.Run(t, `
const { ProcessTerminal } = await load("pi-tui/terminal.js");
emit(input.map((p) => {
	const env = {};
	if (p.columns !== "") process.env.COLUMNS = p.columns; else delete process.env.COLUMNS;
	if (p.lines !== "") process.env.LINES = p.lines; else delete process.env.LINES;
	const terminal = new ProcessTerminal();
	const clamp = (n) => Number.isFinite(n) ? n : (n > 0 ? 1e300 : -1e300);
	return { columns: clamp(terminal.columns), rows: clamp(terminal.rows) };
}));
`, probes, &want)
	bad := 0
	for i, p := range probes {
		t.Setenv("COLUMNS", p.Columns)
		t.Setenv("LINES", p.Lines)
		terminal := NewProcessTerminal(nil, nil)
		for _, c := range []struct {
			name string
			got  int
			want float64
		}{{"COLUMNS", terminal.Columns(), want[i].Columns}, {"LINES", terminal.Rows(), want[i].Rows}} {
			if c.want <= 0 || c.want != math.Trunc(c.want) || c.want > math.MaxInt32 {
				continue
			}
			if float64(c.got) != c.want {
				if bad++; bad <= 8 {
					t.Errorf("%s=%q: PiG %d, Pi %v", c.name, map[string]string{"COLUMNS": p.Columns, "LINES": p.Lines}[c.name], c.got, c.want)
				}
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d sizes differ", bad)
	}
}

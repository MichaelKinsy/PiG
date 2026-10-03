package export

import "testing"

// Pi 0.99.2 export-html/index.ts:42-107 (parseColor, getLuminance, adjustBrightness, deriveExportColors). The expected
// values are the output of the upstream functions themselves, extracted verbatim and run under node, for hex and
// rgb() inputs, dark and light bases, unclamped huge channels and an unparseable base.
func TestDeriveExportColors(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		want       exportColors
	}{
		{"black", "#000000", exportColors{"rgb(0, 0, 0)", "rgb(0, 0, 0)", "rgb(20, 15, 0)"}},
		{"dark gray", "#343541", exportColors{"rgb(36, 37, 46)", "rgb(44, 45, 55)", "rgb(72, 68, 65)"}},
		{"dark side of the 0.5 luminance threshold", "#808080", exportColors{"rgb(90, 90, 90)", "rgb(109, 109, 109)", "rgb(148, 143, 128)"}},
		{"light side of the 0.5 luminance threshold", "#c0c0c0", exportColors{"rgb(184, 184, 184)", "#c0c0c0", "rgb(202, 197, 172)"}},
		{"just under the 0.5 luminance threshold", "#bbbbbb", exportColors{"rgb(131, 131, 131)", "rgb(159, 159, 159)", "rgb(207, 202, 187)"}},
		{"white", "#ffffff", exportColors{"rgb(245, 245, 245)", "#ffffff", "rgb(255, 255, 235)"}},
		{"light gray", "#f5f5f5", exportColors{"rgb(235, 235, 235)", "#f5f5f5", "rgb(255, 250, 225)"}},
		{"rgb() light", "rgb(250, 250, 250)", exportColors{"rgb(240, 240, 240)", "rgb(250, 250, 250)", "rgb(255, 255, 230)"}},
		{"rgb() with spaces", "rgb( 10 , 20,30 )", exportColors{"rgb(7, 14, 21)", "rgb(9, 17, 26)", "rgb(30, 35, 30)"}},
		{"unparseable", "var(--x)", exportColors{"rgb(24, 24, 30)", "rgb(30, 30, 36)", "rgb(60, 55, 40)"}},
		{"an unclamped huge channel is written as JavaScript writes the number", "rgb(0, 0, 1000000000000000000000)", exportColors{"rgb(0, 0, 255)", "rgb(0, 0, 1000000000000000000000)", "rgb(10, 5, 1e+21)"}},
		{"a channel past double precision", "rgb(0,0,99999999999999999999999)", exportColors{"rgb(0, 0, 255)", "rgb(0,0,99999999999999999999999)", "rgb(10, 5, 1e+23)"}},
		{"short hex is unparseable", "#fff", exportColors{"rgb(24, 24, 30)", "rgb(30, 30, 36)", "rgb(60, 55, 40)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveExportColors(tc.base); got != tc.want {
				t.Fatalf("deriveExportColors(%q) = %+v, want %+v", tc.base, got, tc.want)
			}
		})
	}
}

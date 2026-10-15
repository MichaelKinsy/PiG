package jsstring_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Number.prototype.toFixed over exact ties (k/2^n), decimal-looking costs, tiny and huge magnitudes, negatives that round to zero, NaN, infinities and
// every digit count the callers use, against Node's result.
func TestToFixedAgainstNodeCorpus(t *testing.T) {
	type probe struct {
		X      float64 `json:"x"`
		Digits int     `json:"digits"`
		Text   string  `json:"text"`
	}
	rng := rand.New(rand.NewSource(31))
	special := []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1), 1e21, -1e21, 1e20, 0.5, 1.5, 2.5, -2.5, 0.125, 0.375, 0.625, 0.875, 1.005, 4.35, 8.345, 1e-7, 5e-324, math.MaxFloat64}
	var probes []probe
	for range 12000 {
		var x float64
		switch rng.Intn(6) {
		case 0:
			x = special[rng.Intn(len(special))]
		case 1:
			x = float64(rng.Intn(1<<20)) / float64(int(1)<<rng.Intn(14))
		case 2:
			x = float64(rng.Intn(2000000)) / 1000
		case 3:
			x = rng.Float64() * math.Pow(10, float64(rng.Intn(30)-12))
		case 4:
			x = -rng.Float64() * math.Pow(10, float64(rng.Intn(8)-5))
		default:
			x = math.Float64frombits(rng.Uint64())
		}
		probes = append(probes, probe{X: x, Digits: rng.Intn(8)})
	}
	for i := range probes {
		switch {
		case math.IsNaN(probes[i].X):
			probes[i].Text = "NaN"
		case math.IsInf(probes[i].X, 1):
			probes[i].Text = "Infinity"
		case math.IsInf(probes[i].X, -1):
			probes[i].Text = "-Infinity"
		}
	}
	type wire struct {
		Special string  `json:"special,omitempty"`
		X       float64 `json:"x"`
		Digits  int     `json:"digits"`
	}
	var in []wire
	for _, p := range probes {
		w := wire{X: p.X, Digits: p.Digits}
		if p.Text != "" {
			w.X, w.Special = 0, p.Text
		}
		in = append(in, w)
	}
	var want []string
	pioracle.Run(t, `emit(input.map((p) => { const x = p.special === "NaN" ? NaN : p.special === "Infinity" ? Infinity : p.special === "-Infinity" ? -Infinity : p.x; return x.toFixed(p.digits); }));`, in, &want)
	bad := 0
	for i, p := range probes {
		if got := jsstring.ToFixed(p.X, p.Digits); got != want[i] {
			if bad++; bad <= 10 {
				t.Errorf("ToFixed(%v, %d) = %q, Node %q", p.X, p.Digits, got, want[i])
			}
		}
	}
}

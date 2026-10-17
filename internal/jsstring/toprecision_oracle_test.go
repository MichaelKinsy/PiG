package jsstring_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Number.prototype.toPrecision over ties, powers of ten and their neighbours, the exponent-form boundaries (1e-7, 1e21), subnormals, negatives, zero and every precision from 1 to 100, against Node's result.
func TestToPrecisionAgainstNodeCorpus(t *testing.T) {
	type wire struct {
		X         float64 `json:"x"`
		Precision int     `json:"precision"`
	}
	rng := rand.New(rand.NewSource(47))
	special := []float64{0, math.Copysign(0, -1), 1e-7, 1e-6, 9.999999e-7, 1e21, 1e20, 9.5, 99.5, 0.5, 1.5, 2.5, 0.125, 123456, 1e-5, 5e-324, math.MaxFloat64, math.SmallestNonzeroFloat64, 0.1, 0.3, 4.35}
	var probes []wire
	for range 30000 {
		var x float64
		switch rng.Intn(7) {
		case 0:
			x = special[rng.Intn(len(special))]
		case 1:
			x = float64(rng.Intn(1<<20)) / float64(int(1)<<rng.Intn(14))
		case 2:
			x = math.Pow(10, float64(rng.Intn(60)-30)) * float64(1+rng.Intn(9))
		case 3:
			x = math.Nextafter(math.Pow(10, float64(rng.Intn(60)-30)), math.Inf(2*rng.Intn(2)-1))
		case 4:
			x = -rng.Float64() * math.Pow(10, float64(rng.Intn(40)-20))
		case 5:
			x = rng.Float64() * math.Pow(10, float64(rng.Intn(40)-20))
		default:
			x = math.Float64frombits(rng.Uint64())
			if math.IsNaN(x) || math.IsInf(x, 0) {
				x = 1
			}
		}
		precision := 1 + rng.Intn(21)
		if rng.Intn(10) == 0 {
			precision = 1 + rng.Intn(100)
		}
		probes = append(probes, wire{X: x, Precision: precision})
	}
	var want []string
	pioracle.Run(t, `emit(input.map((p) => p.x.toPrecision(p.precision)));`, probes, &want)
	bad := 0
	for i, p := range probes {
		if got := jsstring.ToPrecision(p.X, p.Precision); got != want[i] {
			if bad++; bad <= 10 {
				t.Errorf("ToPrecision(%v, %d) = %q, Node %q", p.X, p.Precision, got, want[i])
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}

// A value just below a power of ten keeps its own exponent when the precision holds its digits (Node 24 results): math.Log10 rounds 1e-11, which is a little under 10^-11, up to -11.
func TestToPrecisionKeepsTheExponentOfAValueJustBelowAPowerOfTen(t *testing.T) {
	for _, c := range []struct {
		x         float64
		precision int
		want      string
	}{
		{1e-11, 16, "9.999999999999999e-12"},
		{1e-6, 17, "9.9999999999999995e-7"},
		{9.999999999999998e+09, 16, "9999999999.999998"},
		{9.999999999999999e-06, 16, "0.000009999999999999999"},
		{1e-14, 18, "9.99999999999999999e-15"},
		{9.999999999999997e+22, 16, "9.999999999999997e+22"},
		{1e-11, 15, "1.00000000000000e-11"},
		{0.5, 1, "0.5"},
		{9.5, 1, "1e+1"},
		{99.5, 2, "1.0e+2"},
	} {
		if got := jsstring.ToPrecision(c.x, c.precision); got != c.want {
			t.Errorf("ToPrecision(%v, %d) = %q, want %q", c.x, c.precision, got, c.want)
		}
	}
}

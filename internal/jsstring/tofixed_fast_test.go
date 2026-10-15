package jsstring

import (
	"math"
	"math/rand"
	"testing"
)

// The strconv path of ToFixed returns what the exact big.Rat path returns for every value it accepts: random doubles of all magnitudes, decimal-looking values, exact ties (k/2^n) and their neighbours,
// negatives that round to zero, at every digit count up to 25.
func TestToFixedFastPathEqualsExactPath(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	check := func(x float64, digits int) {
		t.Helper()
		if got, want := ToFixed(x, digits), toFixedExact(x, digits); got != want {
			t.Fatalf("ToFixed(%v, %d) = %q, exact %q", x, digits, got, want)
		}
	}
	for range 60000 {
		digits := rng.Intn(26)
		var x float64
		switch rng.Intn(7) {
		case 0:
			x = math.Float64frombits(rng.Uint64())
			if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 1e21 {
				continue
			}
		case 1:
			x = float64(rng.Int63n(1_000_000)) / 1000
		case 2:
			x = float64(rng.Int63n(1<<20)) / float64(int64(1)<<rng.Intn(24)) // exact ties at small digit counts
		case 3:
			tie := float64(2*rng.Int63n(1<<18)+1) / float64(int64(1)<<(1+rng.Intn(20)))
			x = math.Nextafter(tie, math.Inf(1-2*rng.Intn(2)))
		case 4:
			x = -rng.Float64() * math.Pow(10, float64(-rng.Intn(8)))
		case 5:
			x = rng.NormFloat64() * math.Pow(10, float64(rng.Intn(20)))
		default:
			x = float64(rng.Int63n(1000)) / 8
		}
		check(x, digits)
		check(-x, digits)
	}
	for _, x := range []float64{0, math.Copysign(0, -1), 0.5, 1.5, 2.5, 0.125, 1.005, 1e-7, 5e-324, 1e20, 0.045, -0.04, -0.05, 123456789.5, 4503599627370495.5} {
		for digits := 0; digits <= 25; digits++ {
			check(x, digits)
		}
	}
}

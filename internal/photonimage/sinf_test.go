package photonimage

import (
	"math"
	"testing"
)

// TestSinfConstants checks the multiples of pi/2 against the bit patterns in libm's sinf.rs:15-21.
func TestSinfConstants(t *testing.T) {
	for _, c := range []struct {
		got  float64
		bits uint64
	}{{s1Pio2, 0x3FF921FB54442D18}, {s2Pio2, 0x400921FB54442D18}, {s3Pio2, 0x4012D97C7F3321D2}, {s4Pio2, 0x401921FB54442D18}} {
		if math.Float64bits(c.got) != c.bits {
			t.Errorf("%x, want %x", math.Float64bits(c.got), c.bits)
		}
	}
}

// TestSinfStaysWithinOneStepOfTheCorrectlyRoundedSine: the port is a sine (never more than one float32 step from the sine rounded to
// float32), and it is not always the correctly rounded one, which is why lanczos3 needs it (TestResizeLanczos3MatchesPhoton and
// TestResizeLanczos3MatchesPhotonOnStripsAndNearOneRatios compare the resulting pixels with Photon's).
func TestSinfStaysWithinOneStepOfTheCorrectlyRoundedSine(t *testing.T) {
	differ := 0
	for i := range 3_000_000 {
		x := float32(i) * (3 * math.Pi / 3_000_000)
		got, want := sinf(x), float32(math.Sin(float64(x)))
		if got == want {
			continue
		}
		differ++
		if step := math.Abs(float64(got)-float64(want)) / math.Abs(float64(math.Nextafter32(want, 2)-want)); step > 1.5 {
			t.Fatalf("sinf(%v) = %v, the sine rounded to float32 is %v", x, got, want)
		}
	}
	t.Logf("%d of 3000000 arguments differ from the correctly rounded sine", differ)
}

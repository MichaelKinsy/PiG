package sdk

import "testing"

// TestJSRoundMatchesMathRound pins jsRound to Node's Math.round where flooring value+0.5 differs.
func TestJSRoundMatchesMathRound(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{0.49999999999999994, 0},
		{254.5, 255},
		{-2.5, -2},
		{4503599627370497, 4503599627370497},
		{-4503599627370497, -4503599627370497},
	} {
		if got := jsRound(c.in); got != c.want {
			t.Errorf("jsRound(%v) = %v, Math.round gives %v", c.in, got, c.want)
		}
	}
}

package cli

import "testing"

// list-models.ts:14-24 formatTokenCount: whole thousands and millions print without a fraction, others use toFixed(1), which rounds
// an exactly representable tie up (1.25 -> "1.3"; Go's %.1f gives "1.2").
func TestFormatTokenCountUsesJavaScriptToFixed(t *testing.T) {
	for _, tc := range []struct {
		count int
		want  string
	}{
		{0, "0"}, {999, "999"}, {1000, "1K"}, {1250, "1.3K"}, {1750, "1.8K"}, {131_072, "131.1K"}, {200_000, "200K"},
		{1_000_000, "1M"}, {1_250_000, "1.3M"}, {1_048_576, "1.0M"}, {2_500_000, "2.5M"},
	} {
		if got := formatTokenCount(tc.count); got != tc.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tc.count, got, tc.want)
		}
	}
}

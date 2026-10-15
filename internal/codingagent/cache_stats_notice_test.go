package codingagent

import "testing"

// interactive-mode.ts:4196-4210 addCacheMissNotice: the re-billed cost is toFixed(2), which rounds an exactly representable tie up
// (0.125 -> "0.13"; Go's %.2f gives "0.12"), is omitted under $0.01, and the notice is skipped under both thresholds.
func TestCacheMissNoticeFormatsCostWithJavaScriptToFixed(t *testing.T) {
	for _, tc := range []struct {
		name string
		miss *cacheMiss
		want string
	}{
		{"tie rounds up", &cacheMiss{missedTokens: 50_000, missedCost: 0.125}, "Cache miss: 50k tokens re-billed (~$0.13)"},
		{"second tie", &cacheMiss{missedTokens: 50_000, missedCost: 0.375}, "Cache miss: 50k tokens re-billed (~$0.38)"},
		{"under a cent has no cost", &cacheMiss{missedTokens: 50_000, missedCost: 0.009}, "Cache miss: 50k tokens re-billed"},
		{"model switch", &cacheMiss{missedTokens: 25_000, missedCost: 0.5, modelChanged: true}, "Cache miss after model switch: 25k tokens re-billed (~$0.50)"},
		{"idle past the ttl", &cacheMiss{missedTokens: 25_000, missedCost: 0.5, idleMillis: 7*60_000 + 30_000}, "Cache miss after 8m idle: 25k tokens re-billed (~$0.50)"},
		{"expensive but few tokens", &cacheMiss{missedTokens: 5_000, missedCost: 0.1}, "Cache miss: 5.0k tokens re-billed (~$0.10)"},
		{"below both thresholds", &cacheMiss{missedTokens: 19_999, missedCost: 0.0999}, ""},
	} {
		if got := formatCacheMissNotice(tc.miss); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

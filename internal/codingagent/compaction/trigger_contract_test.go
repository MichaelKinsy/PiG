package compaction

import "testing"

// The compaction page tells a reader that Pig compacts once the context in use
// passes the window minus the reserve, and that it does nothing when compaction
// is off. Those are the facts someone tuning reserveTokens acts on, so bind
// them rather than leaving them as prose. Like upstream shouldCompact, the
// rule has no special case for a zero window.
func TestShouldCompactMatchesTheDocumentedRule(t *testing.T) {
	const window, reserve = 100_000, 16_384
	on := CompactionSettings{Enabled: true, ReserveTokens: reserve}

	cases := []struct {
		name     string
		tokens   int
		window   int
		settings CompactionSettings
		want     bool
	}{
		{"just below the threshold", window - reserve, window, on, false},
		{"one token past it", window - reserve + 1, window, on, true},
		{"far past it", window, window, on, true},
		{"empty context", 0, window, on, false},
		{"compaction disabled", window, window, CompactionSettings{Enabled: false, ReserveTokens: reserve}, false},
		{"zero context window", 10, 0, CompactionSettings{Enabled: true, ReserveTokens: 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldCompact(tc.tokens, tc.window, tc.settings); got != tc.want {
				t.Errorf("ShouldCompact(%d, %d) = %v, want %v", tc.tokens, tc.window, got, tc.want)
			}
		})
	}
}

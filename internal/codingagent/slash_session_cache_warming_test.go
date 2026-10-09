package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// The /session "Cache Warming" block, byte for byte, as upstream
// handleSessionCommand renders it between Tokens and Cost.
// Pi: packages/coding-agent/src/core/cache-warmer.ts:101 (CacheWarmingDecision.expectedSavings); packages/coding-agent/src/core/cache-warmer.ts:93 (CacheWarmingDecision.phase); packages/coding-agent/src/core/cache-warmer.ts:95 (CacheWarmingDecision.warmCost); packages/coding-agent/src/core/cache-warmer.ts:97 (CacheWarmingDecision.missCost); packages/coding-agent/src/core/cache-warmer.ts:99 (CacheWarmingDecision.continuationProbability).
func TestSessionHandlerRendersCacheWarmingSection(t *testing.T) {
	// theme.bold is SGR 1/22; theme.fg("dim", label) is the theme's dim colour, not SGR 2 (interactive-mode.ts:6709-6760).
	const bold, reset = "\x1b[1m", "\x1b[22m"
	header := "\n" + bold + "Cache Warming" + reset + "\n"
	line := func(label, value string) string { return tui.ActiveTheme().Fg("dim", label) + " " + value + "\n" }
	decision := &CacheWarmingDecision{
		Phase: "streaming", WarmCost: 0.050025, MissCost: 0.575, ContinuationProbability: 1,
		ExpectedSavings: 0.524975, EconomicsAvailable: true, Action: CacheWarmingActionWarm,
	}
	for _, tc := range []struct {
		name   string
		mode   string
		status func() *CacheWarmingStatus
		want   string
	}{
		{
			name: "no warmer", status: nil,
			want: header + line("Mode:", "streaming") + line("Status:", "Inactive (cache warming unavailable)"),
		},
		{
			name: "waiting", mode: `{"cacheWarming":"idle"}`,
			status: func() *CacheWarmingStatus {
				return &CacheWarmingStatus{State: "inactive", Reason: "waiting for first request"}
			},
			want: header + line("Mode:", "idle") + line("Status:", "Inactive (waiting for first request)"),
		},
		{
			name: "refreshing",
			status: func() *CacheWarmingStatus {
				return &CacheWarmingStatus{State: "refreshing", NextWarmAt: 1, Decision: decision}
			},
			want: header + line("Mode:", "streaming") +
				line("Status:", "Warming cache (100% continuation probability while agent is running, expected savings $0.525 >= $0.050 -> warm)") +
				line("Cache miss penalty:", "$0.575") + line("Refresh cost:", "$0.050"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			if tc.mode != "" {
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(tc.mode), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			sc, out, _, _ := newTestSlashContext()
			sc.SettingsManager = NewSettingsManager(t.TempDir(), agentDir)
			sc.CacheWarmingStatus = tc.status
			sess := NewSession("cache-warming", "/tmp")
			sc.CurrentSession = func() *Session { return sess }
			if err := sessionHandler(sc); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			start := strings.Index(got, header)
			if start < 0 {
				t.Fatalf("no Cache Warming section:\n%q", got)
			}
			if section := got[start:]; !strings.HasPrefix(section, tc.want) || strings.TrimSuffix(section[len(tc.want):], "\n") != "" {
				t.Fatalf("section = %q, want %q", section, tc.want)
			}
			if !strings.Contains(got, line("Total:", "0")+header) {
				t.Fatalf("section does not follow the Tokens block:\n%q", got)
			}
		})
	}
}

package codingagent

import (
	"slices"
	"testing"
)

// settings-manager.ts CacheWarmingMode = "off" | "streaming" | "idle" (CACHE_WARMING_MODES): every member of the union is a mode the settings accept and the warmer
// acts on. "off" stops a warm in either phase, "streaming" stops it once the run has settled, "idle" keeps it going; a value outside the union reads as "streaming".
// mutation-checked: dropping "idle" from CacheWarmingModes, or making modeStopReason ignore "streaming", fails it.
func TestCacheWarmingModeUnionAllKindsAreHandled(t *testing.T) {
	if want := []CacheWarmingMode{"off", "streaming", "idle"}; !slices.Equal(CacheWarmingModes, want) {
		t.Fatalf("CacheWarmingModes = %v, want Pi's CACHE_WARMING_MODES %v", CacheWarmingModes, want)
	}
	stopReasons := map[CacheWarmingMode]map[string]string{
		"off":       {"streaming": "cache warming disabled", "idle": "cache warming disabled"},
		"streaming": {"streaming": "", "idle": "agent run settled"},
		"idle":      {"streaming": "", "idle": ""},
	}
	for _, mode := range CacheWarmingModes {
		manager := NewInMemorySettingsManager(Settings{})
		if err := manager.SetCacheWarmingMode(mode); err != nil {
			t.Fatal(err)
		}
		if got := manager.GetCacheWarmingMode(); got != mode {
			t.Errorf("GetCacheWarmingMode after Set(%q) = %q", mode, got)
		}
		warmer := &CacheWarmer{getMode: func() CacheWarmingMode { return mode }}
		for phase, want := range stopReasons[mode] {
			if got := warmer.modeStopReason(&cacheWarmingRun{phase: phase}); got != want {
				t.Errorf("mode %q in phase %q stops with %q, want %q", mode, phase, got, want)
			}
		}
	}
	unknown := NewInMemorySettingsManager(Settings{CacheWarming: "sometimes"})
	if got := unknown.GetCacheWarmingMode(); got != "streaming" {
		t.Errorf("a mode outside the union reads as %q, want streaming", got)
	}
}

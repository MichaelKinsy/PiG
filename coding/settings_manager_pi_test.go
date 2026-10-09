package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pi: packages/coding-agent/src/core/settings-manager.ts

// SettingsManager.inMemory (settings-manager.ts:403-410) with the compaction overrides of test/settings-manager-compaction.test.ts:
// each field resolves independently, the exact provider/id key wins, and missing fields fall back to the built-in defaults.
func TestInMemorySettingsManagerResolvesCompactionOverridesLikePi(t *testing.T) {
	model := &ai.Model{ID: "family/model", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}}
	plain := NewInMemorySettingsManager(Settings{})
	got, err := plain.GetCompactionSettings(model)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.ReserveTokens != 16384 || got.KeepRecentTokens != 20000 {
		t.Fatalf("defaults = %+v, want enabled 16384 20000", got)
	}

	manager := NewInMemorySettingsManager(Settings{Compaction: &icodingagent.CompactionSettingsJSON{
		ReserveTokens:    ptr(8192.0),
		KeepRecentTokens: ptr(10000.0),
		ModelOverrides:   map[string]icodingagent.CompactionModelOverride{"provider/family/model": {ReserveTokens: ptr(400000.0)}},
	}})
	if got, _ := manager.GetCompactionReserveTokens(model); got != 400000 {
		t.Errorf("reserve for the overridden model = %d, want 400000", got)
	}
	if got, _ := manager.GetCompactionKeepRecentTokens(model); got != 10000 {
		t.Errorf("keep for the overridden model = %d, want 10000", got)
	}
	if got, _ := manager.GetCompactionReserveTokens(); got != 8192 {
		t.Errorf("reserve without a model = %d, want 8192", got)
	}
}

func ptr[T any](v T) *T { return &v }

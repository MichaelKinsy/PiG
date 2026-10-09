package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// index.ts exports DEFAULT_COMPACTION_SETTINGS, findCutPoint, findTurnStartIndex and getLastAssistantUsage. The public Go names read the same settings defaults the SettingsManager resolves (compaction.ts:126, settings-manager.ts getCompactionSettings) and operate on a real Session's entries.
// Pi: packages/coding-agent/src/core/settings-manager.ts:964 (SettingsManager.getCompactionSettings).
func TestCompactionHelpersArePublicAndAgreeWithSettingsDefaults(t *testing.T) {
	if DefaultCompactionSettings != (CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}) {
		t.Fatalf("DefaultCompactionSettings = %+v", DefaultCompactionSettings)
	}
	resolved, err := codingagent.NewInMemorySettingsManager(codingagent.Settings{}).GetCompactionSettings()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Enabled != DefaultCompactionSettings.Enabled || resolved.ReserveTokens != DefaultCompactionSettings.ReserveTokens || resolved.KeepRecentTokens != DefaultCompactionSettings.KeepRecentTokens {
		t.Fatalf("settings default %+v differs from DefaultCompactionSettings %+v", resolved, DefaultCompactionSettings)
	}
	sess := newBashTestSession(t, `{}`)
	entries := sess.Entries()
	if usage := GetLastAssistantUsage(entries); usage != nil {
		t.Fatalf("GetLastAssistantUsage of an empty session = %+v", usage)
	}
	if cut := FindCutPoint(entries, 0, len(entries), DefaultCompactionSettings.KeepRecentTokens); cut.IsSplitTurn || cut.FirstKeptEntryIndex != 0 {
		t.Fatalf("FindCutPoint of an empty session = %+v", cut)
	}
	if got := FindTurnStartIndex(entries, 0, 0); got != -1 {
		t.Fatalf("FindTurnStartIndex of an empty session = %d, want -1", got)
	}
}

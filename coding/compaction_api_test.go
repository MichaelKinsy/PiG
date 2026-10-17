package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
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

// compaction.ts findCutPoint, findTurnStartIndex and getLastAssistantUsage over a session's entries: the cut keeps about keepRecentTokens of recent context starting at a user turn, the turn start is the
// closest earlier user entry, and the last usage is the last assistant message that reports one.
// Pi: packages/coding-agent/src/core/compaction/compaction.ts findCutPoint, findTurnStartIndex, getLastAssistantUsage.
func TestCompactionHelpersWorkOnASessionsEntries(t *testing.T) {
	sess := newBashTestSession(t, `{}`)
	base := len(sess.Entries())      // the session already holds its setup entries
	long := strings.Repeat("x", 400) // about 100 tokens
	appendUser(t, sess, long)
	appendAsst(t, sess, long)
	appendUser(t, sess, long)
	withUsage := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: long}},
		StopReason: ai.StopReasonStop, Timestamp: 3, Usage: &ai.Usage{Input: 7, Output: 3, TotalTokens: 10}}}
	if _, err := sess.inner.AppendMessage(withUsage); err != nil {
		t.Fatal(err)
	}
	latest := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "latest"}},
		StopReason: ai.StopReasonStop, Timestamp: 4, Usage: &ai.Usage{Input: 15, Output: 5, TotalTokens: 20}}}
	if _, err := sess.inner.AppendMessage(latest); err != nil {
		t.Fatal(err)
	}
	entries := sess.Entries()
	if len(entries) != base+5 {
		t.Fatalf("entries = %d, want %d", len(entries), base+5)
	}
	messages := entries[base:]

	usage := GetLastAssistantUsage(entries)
	if usage == nil || usage.TotalTokens != 20 || usage.Input != 15 {
		t.Fatalf("GetLastAssistantUsage = %+v, want the usage of the last assistant message that reports one", usage)
	}
	if earlier := GetLastAssistantUsage(messages[:4]); earlier == nil || earlier.TotalTokens != 10 {
		t.Errorf("GetLastAssistantUsage before the last report = %+v, want the earlier one", earlier)
	}
	if got := GetLastAssistantUsage(messages[:2]); got != nil {
		t.Errorf("GetLastAssistantUsage with no reported usage = %+v", got)
	}

	if got := FindTurnStartIndex(entries, base+3, 0); got != base+2 {
		t.Errorf("FindTurnStartIndex(assistant of turn 2) = %d, want %d", got, base+2)
	}
	if got := FindTurnStartIndex(entries, base+1, 0); got != base {
		t.Errorf("FindTurnStartIndex(assistant of turn 1) = %d, want %d", got, base)
	}
	if got := FindTurnStartIndex(entries, base+1, base+1); got != -1 {
		t.Errorf("FindTurnStartIndex before its start bound = %d, want -1", got)
	}

	if cut := FindCutPoint(entries, 0, len(entries), 150); cut.FirstKeptEntryIndex != base+2 || cut.IsSplitTurn || cut.TurnStartIndex != -1 {
		t.Errorf("FindCutPoint(150) = %+v, want the last turn kept from its user entry %d", cut, base+2)
	}
	if cut := FindCutPoint(entries, 0, len(entries), 1_000_000); cut.FirstKeptEntryIndex != 0 || cut.IsSplitTurn {
		t.Errorf("FindCutPoint(1000000) = %+v, want everything kept", cut)
	}
	if cut := FindCutPoint(entries, base+1, len(entries), 1_000_000); cut.FirstKeptEntryIndex != base+1 {
		t.Errorf("FindCutPoint from base+1 = %+v, want the cut to start no earlier than startIndex", cut)
	}
	if cut := FindCutPoint(entries, base, base+2, 150); cut.FirstKeptEntryIndex != base {
		t.Errorf("FindCutPoint ending before the last turn = %+v, want it to ignore entries from endIndex", cut)
	}
}

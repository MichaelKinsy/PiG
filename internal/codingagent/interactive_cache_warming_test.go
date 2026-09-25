package codingagent

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Changing the row reconciles the running warmer through the Session, as
// upstream onCacheWarmingModeChange calls session.setCacheWarmingMode.
func TestOnSettingAppliedReconcilesCacheWarmingMode(t *testing.T) {
	handle := &recordingCompactHandle{}
	mode := &InteractiveMode{opts: InteractiveOptions{SessionHandle: handle}}
	mode.buildSlashContext(t.Context()).OnSettingApplied("cache-warming-mode", "off")
	if len(handle.cacheWarmingModes) != 1 || handle.cacheWarmingModes[0] != "off" {
		t.Fatalf("SetCacheWarmingMode calls = %v, want [off]", handle.cacheWarmingModes)
	}
}

func cacheWarmingTestMode(t *testing.T, session *Session, notices bool) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	if err := sm.SetShowCacheMissNotices(notices); err != nil {
		t.Fatal(err)
	}
	mode := &InteractiveMode{
		opts:          InteractiveOptions{SettingsManager: sm, SessionHandle: &recordingCompactHandle{inner: session}},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 100, 30),
		statusLine: NewStatusLine(&ai.Model{ID: "active", Capabilities: ai.ModelCapabilities{
			CacheReadCostPer1M: 999,
		}}, "", nil),
	}
	mode.statusLine.SetUsageTotalsSource(session.FooterUsageTotals)
	return mode
}

// A live cache-warming refresh adds exactly its stored cost to the footer
// totals without re-pricing it with the active model, and, with cache notices
// on, adds a dim transcript line.
func TestEntryAppendedRendersCacheWarmingUsage(t *testing.T) {
	session := NewSession("warm", t.TempDir())
	entry, err := session.AppendUsage("cache_warm", "anthropic", "claude-opus-4-6", ai.Usage{
		CacheRead: 100_000, Output: 1, Cost: ai.UsageCost{Total: 0.050025},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Each iteration creates an ephemeral TUI whose requested render runs on a
	// timer. Pin the process-wide capability cache before those renders overlap,
	// and drain each render before restoring it after the test.
	savedCaps := tuiCapabilitiesForTest()
	t.Cleanup(savedCaps.restore)
	tuiSetCapsForTest(false)
	synctest.Test(t, func(t *testing.T) {
		for _, notices := range []bool{true, false} {
			mode := cacheWarmingTestMode(t, session, notices)
			mode.handleEntryAppended(raw)
			chat := strings.Join(mode.chatContainer.Render(100), "\n")
			if shown := strings.Contains(chat, "Cache warmed: $0.050025"); shown != notices {
				t.Fatalf("notices=%t: transcript = %q", notices, chat)
			}
			footer := strings.Join(mode.statusLine.Render(100), "\n")
			if !strings.Contains(stripANSI(footer), "$0.050") {
				t.Fatalf("footer = %q, want stored cost $0.050025 rendered as $0.050", stripANSI(footer))
			}
			totals := session.FooterUsageTotals()
			if totals.cacheRead != 100_000 || totals.output != 1 || totals.cost != entry.Usage.Cost.Total {
				t.Fatalf("footer totals = %+v, want cacheRead 100000 output 1 stored cost %v", totals, entry.Usage.Cost.Total)
			}
			synctest.Wait()
		}
	})
}

// Rebuilding the transcript from the Session shows persisted refreshes where
// they happened, as upstream renderSessionEntries does.
func TestRebuildRendersPersistedCacheWarmingUsage(t *testing.T) {
	session := NewSession("warm-rebuild", t.TempDir())
	if _, err := session.AppendUsage("cache_warm", "anthropic", "claude-opus-4-6", ai.Usage{Cost: ai.UsageCost{Total: 0.01}}, "extension override"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendUsage("custom_operation", "anthropic", "claude-opus-4-6", ai.Usage{Cost: ai.UsageCost{Total: 0.02}}, ""); err != nil {
		t.Fatal(err)
	}
	mode := cacheWarmingTestMode(t, session, true)
	mode.rebuildChatFromSession()
	chat := strings.Join(mode.chatContainer.Render(100), "\n")
	if !strings.Contains(chat, "Cache warmed (extension override): $0.010") || strings.Count(chat, "Cache warmed") != 1 {
		t.Fatalf("rebuilt transcript = %q", chat)
	}
}

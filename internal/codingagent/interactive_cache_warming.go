package codingagent

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/tui"
)

// addCacheWarmingUsage renders one persisted cache-warming refresh when cache
// notices are on. Mirrors upstream interactive-mode.ts addCacheWarmingUsage.
func (m *InteractiveMode) addCacheWarmingUsage(entry UsageEntry) {
	if !m.showCacheMissNotices() {
		return
	}
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewPaddedText(fg(tui.ActiveTheme().Dim, FormatCacheWarmingUsage(entry)), 1, 0, nil))
}

// decodeUsageEntry decodes a "usage" session entry.
func decodeUsageEntry(raw json.RawMessage) (UsageEntry, bool) {
	var entry UsageEntry
	if json.Unmarshal(raw, &entry) != nil || entry.Type != "usage" {
		return UsageEntry{}, false
	}
	return entry, true
}

// handleEntryAppended applies a Session entry appended outside the agent
// loop: session accounting supplies footer totals, and a cache-warming refresh
// gets its transcript notice. Mirrors the entry_appended case of upstream
// interactive-mode.ts handleEvent.
func (m *InteractiveMode) handleEntryAppended(raw json.RawMessage) {
	entry, ok := decodeUsageEntry(raw)
	if !ok {
		return
	}
	m.statusLine.Invalidate()
	if entry.Kind == "cache_warm" {
		m.addCacheWarmingUsage(entry)
	}
	m.tuiInst.RequestRender()
}

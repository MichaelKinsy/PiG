package codingagent

import (
	"encoding/json"
	"slices"

	"github.com/MichaelKinsy/PiG/tui"
)

// addCacheWarmingUsage renders one persisted cache-warming refresh when cache
// notices are on. Mirrors upstream interactive-mode.ts addCacheWarmingUsage.
func (m *InteractiveMode) addCacheWarmingUsage(entry UsageEntry) {
	if !m.showCacheMissNotices() {
		return
	}
	// Back-to-back warms collapse into one line with the cumulative cost.
	if previous := m.cacheWarmingNotice; previous != nil && previous.note == entry.Note {
		if _, last := m.chatContainer.LastTwoChildren(); last == tui.Component(previous.component) {
			previous.total += entry.Usage.Cost.Total
			previous.component.Invalidate()
			return
		}
	}
	notice := &cacheWarmingNotice{total: entry.Usage.Cost.Total, note: entry.Note}
	notice.component = tui.NewThemedText(func() string {
		return tui.ActiveTheme().Fg("dim", FormatCacheWarmingUsage(notice.total, notice.note))
	}, 1, 0)
	m.cacheWarmingNotice = notice
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(notice.component)
}

// cacheWarmingNotice is the last rendered cache-warming line and its running
// total; see addCacheWarmingUsage.
type cacheWarmingNotice struct {
	component *tui.ThemedText
	total     float64
	note      string
}

// decodeUsageEntry decodes a "usage" session entry.
func decodeUsageEntry(raw json.RawMessage) (UsageEntry, bool) {
	var entry UsageEntry
	if json.Unmarshal(raw, &entry) != nil || entry.Type != "usage" {
		return UsageEntry{}, false
	}
	return entry, true
}

// handleEntryAppended applies a Session entry appended outside the agent loop (entry_appended of upstream interactive-mode.ts handleEvent): an entry the
// last boundary compaction already rendered is skipped; a custom entry draws through its registered renderer, a displayed custom message
// draws as a message, a compaction entry rebuilds the transcript around its summary, and a cache-warming refresh gets its notice.
// Session accounting supplies the footer totals.
func (m *InteractiveMode) handleEntryAppended(raw json.RawMessage) {
	var head struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return
	}
	if _, rendered := m.entriesRenderedByBoundaryCompaction[head.ID]; rendered {
		delete(m.entriesRenderedByBoundaryCompaction, head.ID)
		return
	}
	switch head.Type {
	case "context_edit":
		// Pi's footer asks session.getContextUsage() on every render (footer.ts:143), so an edit that drops a message from the projection (a
		// length-truncated reply, a recovered attempt) changes the percentage at once; PiG's footer holds a stored estimate to refresh.
		m.refreshFooterContextUsage()
		m.tuiInst.RequestRender()
	case "custom":
		var entry CustomEntry
		if json.Unmarshal(raw, &entry) == nil {
			m.addCustomEntryToChat(entry)
			m.tuiInst.RequestRender()
		}
	case "custom_message":
		var entry CustomMessageEntry
		if json.Unmarshal(raw, &entry) == nil && entry.Display {
			m.appendCustomMessage(entry)
			m.tuiInst.RequestRender()
		}
	case "compaction":
		var entry CompactionEntry
		if json.Unmarshal(raw, &entry) == nil {
			m.renderBoundaryCompaction(entry)
		}
	default:
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
}

// renderBoundaryCompaction redraws the transcript around a compaction entry an extension's boundary handler appended: the entries the compaction
// kept render before its summary, the entries after it render below, and those are skipped when their own entry_appended events arrive
// (interactive-mode.ts entry_appended, compaction branch). A compaction that is not the first context entry changes nothing.
func (m *InteractiveMode) renderBoundaryCompaction(entry CompactionEntry) {
	session := m.currentSession()
	if session == nil {
		return
	}
	entries := session.BuildContextEntries()
	if len(entries) == 0 || entries[0].Base().ID != entry.ID {
		return
	}
	m.disposeArminComponents()
	m.chatContainer.Clear()
	m.tuiInst.ForceFullRender()
	branch := session.GetBranch()
	compactionIndex := slices.IndexFunc(branch, func(candidate SessionEntry) bool { return candidate.Base().ID == entry.ID })
	entriesAfterCompaction := map[string]struct{}{}
	for _, candidate := range branch[compactionIndex+1:] {
		entriesAfterCompaction[candidate.Base().ID] = struct{}{}
	}
	isAfter := func(candidate SessionEntry) bool {
		_, after := entriesAfterCompaction[candidate.Base().ID]
		return after
	}
	// The list renders in one pass: the retained entries before the compaction, its summary, then the retained entries after it.
	retained := entries[1:]
	ordered := slices.Concat(
		slices.DeleteFunc(slices.Clone(retained), isAfter),
		[]SessionEntry{entries[0]},
		slices.DeleteFunc(slices.Clone(retained), func(candidate SessionEntry) bool { return !isAfter(candidate) }),
	)
	m.renderSessionEntryList(ordered, false)
	if m.entriesRenderedByBoundaryCompaction == nil {
		m.entriesRenderedByBoundaryCompaction = map[string]struct{}{}
	}
	for id := range entriesAfterCompaction {
		m.entriesRenderedByBoundaryCompaction[id] = struct{}{}
	}
	m.statusLine.Invalidate()
	m.tuiInst.RequestRender()
}

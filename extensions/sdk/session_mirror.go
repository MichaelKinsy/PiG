package sdk

import (
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
)

// sessionMirror holds a local copy of the session log, kept in sync by
// incremental appends from the host's state_update notify. This is the
// mechanism that makes GetBranch() and GetEntries() local reads instead of
// IPC calls that fetch and parse the entire session-the difference between
// 2 KB and 77 MB per event for a large session.
//
// Replication is opt-in per extension and starts on the first read: an
// extension that never inspects the session never receives it, which matters
// because every loaded extension would otherwise hold its own full copy of the
// log resident.
//
// The host pushes only new entries since this extension's last cursor
// (EntriesAppended) and the total count (EntryCount). If the local length
// disagrees with EntryCount minus the append size, the session changed
// (switched or forked) and the mirror resets to the appended slice.
type sessionMirror struct {
	// subMu serializes the one-time subscribe so concurrent first readers make
	// a single host call. It is held across that call, so nothing on the
	// connection's reader goroutine may wait on it: subscribed is atomic
	// precisely so applySessionUpdate can test it while a subscribe is in
	// flight instead of deadlocking against the response it is waiting for.
	subMu sync.Mutex
	// subErr is the outcome of the one subscription attempt; a failed attempt is not retried, so every read reports it.
	subErr     error
	subscribed atomic.Bool

	mu        sync.RWMutex
	sessionID string
	entries   []json.RawMessage // full local log, append-only within a session
	leafID    string
	index     map[string]entryMeta // id → {position, parentId}

	// Branch cache, invalidated on append or leaf change.
	// revision increments on every change to entries or leaf. Both caches key
	// on it, so staleness is impossible to express: a cache built at revision N
	// is only served while the mirror is still at N. Keying on the leaf alone
	// cannot do that, because entries can change under an unchanged leaf.
	revision uint64

	// resetGen increments whenever entries and index are replaced rather than
	// extended (session switch, resync, seed, a repeated entry id). The branch
	// caches below can be extended in place only while it is unchanged.
	resetGen uint64

	// branchCache is the root→leaf path as of revision branchCacheFor. After a
	// miss the cache is extended with the entries appended since when the new
	// leaf descends from the old one (the shape of every ordinary append) and
	// rebuilt otherwise (leaf move, fork, rewind). branchTail is the id of the
	// last path entry, "" when the cache holds the whole log because the mirror
	// has no leaf; branchGen is the resetGen it was built under; branchScanned
	// is len(entries) when it was built, so a walk from the new leaf reaches the
	// cached path exactly when it stops at branchTail before any entry older
	// than branchScanned; branchBuild counts full rebuilds.
	branchCache    []json.RawMessage
	branchCacheFor uint64
	branchTail     string
	branchGen      uint64
	branchScanned  int
	branchBuild    uint64

	// Decoded form of the same branch. GetBranch is called per event by
	// extensions that track the conversation, and decoding a long branch costs
	// hundreds of milliseconds and six figures of allocations each time. The
	// raw cache above does not help that: it hands back the same bytes to be
	// parsed again. Invalidated together with branchCache.
	// branchDecodedRaw is how many entries of branchCache have been decoded and
	// branchDecodedBuild the branchBuild they belong to, so an extended cache
	// decodes only its new tail.
	branchDecoded      []*BranchEntry
	branchDecodedFor   uint64
	branchDecodedRaw   int
	branchDecodedBuild uint64
}

type entryMeta struct {
	pos      int
	parentID string
}

// applySessionUpdate processes the session portion of a state_update notify.
// It returns true if the log changed (so callers can invalidate derived state).
func (m *sessionMirror) applySessionUpdate(session json.RawMessage) bool {
	if len(session) == 0 {
		return false
	}
	var s struct {
		SessionID       string            `json:"sessionId"`
		LeafID          string            `json:"leafId"`
		EntriesAppended []json.RawMessage `json:"entriesAppended"`
		EntryCount      int               `json:"entryCount"`
	}
	if err := json.Unmarshal(session, &s); err != nil {
		return false
	}

	subscribed := m.subscribed.Load()

	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	if s.SessionID != "" && s.SessionID != m.sessionID {
		m.sessionID = s.SessionID
		m.entries, m.index = nil, nil
		m.branchCache, m.branchDecoded = nil, nil
		m.resetGen++
		m.leafID = ""
		changed = true
	}

	// Leaf tracking is cheap and always current; the log itself is only
	// applied once this extension has subscribed. Until then the host sends
	// entryCount 0, which is indistinguishable from a session switch.
	if s.LeafID != "" && s.LeafID != m.leafID {
		m.leafID = s.LeafID
		changed = true
	}
	// A push with no entries and a zero count carries no information about the
	// log: it is the shape sent to an extension that has not subscribed. Read
	// as a log state it is indistinguishable from "the session is empty", which
	// would discard a mirror a concurrent subscribe had just filled.
	if !subscribed || (s.EntryCount == 0 && len(s.EntriesAppended) == 0) {
		if changed {
			m.revision++
		}
		return changed
	}

	// Detect session switch: if entryCount minus appended doesn't match our
	// local length, the session changed under us. Reset.
	expectedBase := s.EntryCount - len(s.EntriesAppended)
	if expectedBase != len(m.entries) {
		// Full reset: host switched session or we're out of sync.
		m.entries = make([]json.RawMessage, 0, s.EntryCount)
		m.index = make(map[string]entryMeta, s.EntryCount)
		m.resetGen++
		changed = true
	}

	// Append new entries and index them.
	for _, raw := range s.EntriesAppended {
		pos := len(m.entries)
		m.entries = append(m.entries, raw)
		id, parentID := extractEntryIDs(raw)
		if id != "" {
			if m.index == nil {
				m.index = make(map[string]entryMeta, s.EntryCount)
			}
			if _, repeated := m.index[id]; repeated {
				m.resetGen++
			}
			m.index[id] = entryMeta{pos: pos, parentID: parentID}
		}
		changed = true
	}

	if changed {
		m.revision++
	}
	return changed
}

// getEntries returns a copy of the full local log.
func (m *sessionMirror) getEntries() []json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.entries) == 0 {
		return nil
	}
	out := make([]json.RawMessage, len(m.entries))
	copy(out, m.entries)
	return out
}

// getBranch returns a copy of the path from root to leaf. The path is cached
// and extended in place when entries are appended below the old leaf.
func (m *sessionMirror) getBranch() []json.RawMessage {
	m.mu.RLock()
	if m.branchCache != nil && m.branchCacheFor == m.revision {
		out := make([]json.RawMessage, len(m.branchCache))
		copy(out, m.branchCache)
		m.mu.RUnlock()
		return out
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	branch := m.branchLocked()
	out := make([]json.RawMessage, len(branch))
	copy(out, branch)
	return out
}

// branchLocked returns the cached path, bringing it up to date first. The
// slice is the cache itself: callers hold m.mu and copy before releasing it.
func (m *sessionMirror) branchLocked() []json.RawMessage {
	if m.branchCache != nil && m.branchCacheFor == m.revision {
		return m.branchCache
	}
	if !m.extendBranchLocked() {
		m.rebuildBranchLocked()
	}
	m.branchCacheFor = m.revision
	return m.branchCache
}

// extendBranchLocked brings the cached path up to the current leaf by walking
// parent links from the leaf back to the cached tail, visiting only entries
// appended since the cache was built. It reports false, leaving the cache
// untouched, when the leaf does not descend from the cached tail.
func (m *sessionMirror) extendBranchLocked() bool {
	if m.branchTail == "" || len(m.branchCache) == 0 || m.branchGen != m.resetGen || m.leafID == "" {
		return false
	}
	var fresh []json.RawMessage
	budget := len(m.entries) - m.branchScanned
	for current := m.leafID; current != m.branchTail; {
		meta, ok := m.index[current]
		if !ok || meta.pos < m.branchScanned || len(fresh) >= budget {
			return false
		}
		fresh = append(fresh, m.entries[meta.pos])
		current = meta.parentID
	}
	for _, f := range slices.Backward(fresh) {
		m.branchCache = append(m.branchCache, f)
	}
	if len(fresh) > 0 {
		m.branchTail = m.leafID
	}
	m.branchScanned = len(m.entries)
	return true
}

// rebuildBranchLocked walks parent links from the leaf to the root.
func (m *sessionMirror) rebuildBranchLocked() {
	m.branchBuild++
	m.branchGen = m.resetGen
	m.branchScanned = len(m.entries)
	m.branchTail = ""
	entries := m.entries
	if m.leafID == "" || len(m.index) == 0 {
		// No leaf or no index: return all entries (matches upstream behavior
		// when there's no branching).
		m.branchCache = make([]json.RawMessage, len(entries))
		copy(m.branchCache, entries)
		return
	}

	var path []json.RawMessage
	seen := make(map[string]bool, len(entries))
	current := m.leafID
	for current != "" && !seen[current] {
		seen[current] = true
		meta, ok := m.index[current]
		if !ok {
			break
		}
		path = append(path, entries[meta.pos])
		current = meta.parentID
	}

	// Reverse to get root→leaf order.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	if len(path) > 0 {
		m.branchTail = m.leafID
	}
	m.branchCache = path
}

// extractEntryIDs pulls id and parentId from a raw session entry without
// fully parsing it. These are the only fields needed for branch derivation.
func extractEntryIDs(raw json.RawMessage) (id, parentID string) {
	var partial struct {
		ID       string `json:"id"`
		ParentID string `json:"parentId"`
	}
	_ = json.Unmarshal(raw, &partial)
	return partial.ID, partial.ParentID
}

// seed installs the log returned by the host at subscribe time so the first
// read does not have to wait for a push. It is a no-op once the push stream
// has delivered anything, which keeps the two paths from fighting.
func (m *sessionMirror) seed(entries []json.RawMessage, count int, leafID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if leafID != "" {
		m.leafID = leafID
	}
	// A push may have delivered the log first, since responses and pushes
	// arrive on separate goroutines. That copy came from the stream the mirror
	// tracks its cursor against, so it wins.
	if len(m.entries) > 0 {
		return
	}
	m.entries = make([]json.RawMessage, 0, count)
	m.index = make(map[string]entryMeta, count)
	m.resetGen++
	for _, raw := range entries {
		pos := len(m.entries)
		m.entries = append(m.entries, raw)
		if id, parentID := extractEntryIDs(raw); id != "" {
			if _, repeated := m.index[id]; repeated {
				m.resetGen++
			}
			m.index[id] = entryMeta{pos: pos, parentID: parentID}
		}
	}
	m.revision++
}

// getBranchEntries returns the branch decoded once per branch revision.
//
// Extensions that follow the conversation call this on every event. Decoding is
// proportional to the whole branch, so on a long session an uncached call costs
// hundreds of milliseconds and six figures of allocations, repeated per event
// and per extension. The result is shared, so callers receive a copy of the
// slice; the entries themselves are treated as read-only.
func (m *sessionMirror) getBranchEntries() []*BranchEntry {
	// Fast path: a hit must not pay for a copy of the raw branch it will not
	// read, which is most of the cost once the decode itself is cached.
	m.mu.RLock()
	if m.branchDecoded != nil && m.branchDecodedFor == m.revision {
		out := append([]*BranchEntry(nil), m.branchDecoded...)
		m.mu.RUnlock()
		return out
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.branchDecoded == nil || m.branchDecodedFor != m.revision {
		raw := m.branchLocked()
		decoded, from := m.branchDecoded, m.branchDecodedRaw
		if m.branchDecodedBuild != m.branchBuild || from > len(raw) {
			decoded, from = make([]*BranchEntry, 0, len(raw)), 0
		}
		for _, r := range raw[from:] {
			var entry BranchEntry
			if err := json.Unmarshal(r, &entry); err == nil {
				decoded = append(decoded, &entry)
			}
		}
		m.branchDecoded = decoded
		m.branchDecodedRaw = len(raw)
		m.branchDecodedBuild = m.branchBuild
		m.branchDecodedFor = m.revision
	}
	return append([]*BranchEntry(nil), m.branchDecoded...)
}

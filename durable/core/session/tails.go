package session

import (
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// The newest entry and newest head marker of each recently used conversation, kept current from the Session's commits
// (session/conversation-tails.ts).

// latestCapacity is the number of conversations whose newest entry ID is kept: IDs only, so many.
const latestCapacity = 4096

// markerCapacity is the number of conversations whose newest head marker is kept: whole entries (a compaction summary is
// one), so fewer.
const markerCapacity = 256

// tailNode is one kept conversation in the recency list of an lruMap: its newest entry ID, or its newest head marker.
type tailNode struct {
	key        ID
	id         ID
	marker     *jv.Object
	prev, next *tailNode
}

// lruMap is a map bounded by a count, the least recently used key leaving first.
type lruMap struct {
	nodes    map[ID]*tailNode
	oldest   *tailNode
	newest   *tailNode
	capacity int
}

func (m *lruMap) unlink(n *tailNode) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		m.oldest = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		m.newest = n.prev
	}
	n.prev, n.next = nil, nil
}

func (m *lruMap) append(n *tailNode) {
	n.prev = m.newest
	if m.newest != nil {
		m.newest.next = n
	} else {
		m.oldest = n
	}
	m.newest = n
}

// use is the node for key, made the most recently used, or nil.
func (m *lruMap) use(key ID) *tailNode {
	n := m.nodes[key]
	if n != nil && m.newest != n {
		m.unlink(n)
		m.append(n)
	}
	return n
}

// put sets key as the most recently used, dropping the least recently used beyond the capacity, and returns its node to fill.
func (m *lruMap) put(key ID) *tailNode {
	if n := m.use(key); n != nil {
		return n
	}
	if m.nodes == nil {
		m.nodes = map[ID]*tailNode{}
	}
	n := &tailNode{key: key}
	m.nodes[key] = n
	m.append(n)
	if len(m.nodes) > m.capacity {
		drop := m.oldest
		m.unlink(drop)
		delete(m.nodes, drop.key)
	}
	return n
}

// conversationTails is the newest entry ID and newest head marker per conversation, for a Session that owns its Storage:
// every entry is written by one of its commits, so apply keeps both current, and a value read from Storage stays valid until
// the next entry of that conversation. The newest entry visible from a conversation is its own newest, or for a fork without
// entries of its own the parent's entry at the fork point: IDs grow, so a conversation's own new entry is always the newest.
type conversationTails struct {
	// latest is the newest entry ID per conversation, 0 for none.
	latest lruMap
	// markers is the newest head marker per conversation, nil for none.
	markers lruMap
}

func (t *conversationTails) init() {
	t.latest.capacity = latestCapacity
	t.markers.capacity = markerCapacity
}

// apply keeps the tails in step with the writes of a stored commit.
func (t *conversationTails) apply(changes []Change) {
	for i := range changes {
		c := &changes[i]
		if c.Kind != ChangeTable {
			continue
		}
		switch c.Table {
		case WriteConversation:
			// A new root conversation sees no entries; a fork sees its parent's up to the fork point.
			if c.Record.Has("parent") {
				continue
			}
			id := mustInt(c.Record, "id")
			t.latest.put(id).id = 0
			t.markers.put(id).marker = nil
		case WriteEntry:
			conversation := mustInt(c.Record, "conversationId")
			t.latest.put(conversation).id = mustInt(c.Record, "id")
			if c.Record.Has("head") {
				t.markers.put(conversation).marker = c.Record
			}
		}
	}
}

// LookupLatestEntryID is the newest entry ID visible from a conversation as the Session's commits and earlier reads know it,
// 0 for none; known is false when a Storage read is needed (always for a read-only Session). The conversation becomes the most
// recently used.
func (s *Session) LookupLatestEntryID(conversationID ID) (id ID, known bool) {
	if s.ReadOnly {
		return 0, false
	}
	if n := s.tails.latest.use(conversationID); n != nil {
		return n.id, true
	}
	return 0, false
}

// NoteLatestEntryID keeps the newest entry ID a Storage read found. It is called on the Session line after the conversation's
// appends are published, so no unpublished commit can change it.
func (s *Session) NoteLatestEntryID(conversationID, id ID) {
	if !s.ReadOnly {
		s.tails.latest.put(conversationID).id = id
	}
}

// KnownLatestEntryID is the newest entry ID of a conversation as known from the Session's commits right now, without reading
// and without changing the recency: known is false when it is not (and always for a read-only Session); 0 is no entry.
func (s *Session) KnownLatestEntryID(conversationID ID) (id ID, known bool) {
	if s.ReadOnly {
		return 0, false
	}
	if n := s.tails.latest.nodes[conversationID]; n != nil {
		return n.id, true
	}
	return 0, false
}

// LookupHeadMarker is the newest visible head marker of a conversation at or before atOrBefore (0: any) as the Session knows it,
// nil for none; known is false when a Storage read is needed. The newest marker answers every cutoff at or after it, and no
// marker answers every cutoff.
func (s *Session) LookupHeadMarker(conversationID, atOrBefore ID) (marker *jv.Object, known bool) {
	if s.ReadOnly {
		return nil, false
	}
	if n := s.tails.markers.use(conversationID); n != nil && (n.marker == nil || atOrBefore == 0 || atOrBefore >= mustInt(n.marker, "id")) {
		return n.marker, true
	}
	return nil, false
}

// NoteHeadMarker keeps the head marker a Storage read found for atOrBefore, only when no newer entry can change it: no cutoff, or
// one at or after the newest entry known.
func (s *Session) NoteHeadMarker(conversationID, atOrBefore ID, marker *jv.Object) {
	if s.ReadOnly {
		return
	}
	if n := s.tails.latest.nodes[conversationID]; atOrBefore == 0 || (n != nil && n.id <= atOrBefore) {
		s.tails.markers.put(conversationID).marker = marker
	}
}

// ColdEntryAwaits is hopcost.ColdEntryAwaits of this Session.
func (s *Session) ColdEntryAwaits() int { return hopcost.ColdEntryAwaits(s.ReadOnly) }

// ColdMarkerAwaits is hopcost.ColdMarkerAwaits of this Session.
func (s *Session) ColdMarkerAwaits() int { return hopcost.ColdMarkerAwaits(s.ReadOnly) }

// TailsSize is how many conversations have their newest entry kept (session.ts documentCacheSizes.tails).
func (s *Session) TailsSize() int { return len(s.tails.latest.nodes) }

// LatestEntryID is the newest entry ID visible from a conversation, 0 for none (session.ts latestEntryId); ticks is the awaits
// the read costs its caller.
func (s *Session) LatestEntryID(conversationID ID) (id ID, ticks int, err error) {
	// What an unpublished commit appended or created is not in the tails yet.
	if err := s.touchEntries(conversationID); err != nil {
		return 0, 0, err
	}
	if id, known := s.LookupLatestEntryID(conversationID); known {
		return id, hopcost.LatestEntryID(s.hop, true, s.ReadOnly), nil
	}
	page, err := s.store.ScanEntries(EntryQuery{ConversationID: conversationID}, 1, 0)
	if err != nil {
		return 0, 0, err
	}
	if len(page.Items) != 0 {
		id = mustInt(page.Items[0], "id")
	}
	s.NoteLatestEntryID(conversationID, id)
	return id, hopcost.LatestEntryID(s.hop, false, s.ReadOnly), nil
}

// LatestHeadMarker is the newest visible head marker of a conversation at or before atOrBefore (0: any), nil for none, known
// from the Session's own commits as LatestEntryID is (session.ts latestHeadMarker); ticks is the awaits the read costs its caller.
func (s *Session) LatestHeadMarker(conversationID, atOrBefore ID) (marker *jv.Object, ticks int, err error) {
	if err := s.touchEntries(conversationID); err != nil {
		return nil, 0, err
	}
	if marker, known := s.LookupHeadMarker(conversationID, atOrBefore); known {
		return marker, hopcost.LatestHeadMarker(s.hop, true, s.ReadOnly), nil
	}
	marker, err = s.store.LatestHeadMarker(conversationID, atOrBefore)
	if err != nil {
		return nil, 0, err
	}
	s.NoteHeadMarker(conversationID, atOrBefore, marker)
	return marker, hopcost.LatestHeadMarker(s.hop, false, s.ReadOnly), nil
}

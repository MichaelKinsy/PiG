// The newest entry and newest head marker of each recently used conversation, kept current from the Session's commits.
//
// Ports packages/durable/src/session/conversation-tails.ts
//
// Go mapping decisions:
//   - `EntryId | null | undefined` of `peek` is an explicit result: the ID and a TailState (TailUnknown for
//     `undefined`, TailNone for `null`, TailKnown when the newest entry ID is returned).
//   - `read` callbacks are blocking calls that return the value or the error that failed the read. Upstream's
//     microtask interleavings around `await read()` are the interleavings of the goroutines that call Apply while a
//     read is out; no lock is held across `read`.
//   - Kept markers are detached copies: Object.freeze has no Go counterpart, so Apply keeps its own copy of the written
//     record and Marker returns a copy of the kept one, as a Storage read does. A caller that changes the record it wrote
//     or the marker it got changes neither the kept marker nor another caller's.
package session

import (
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
)

// HeadMarker is an entry that carries a Head: the marker of a context range.
type HeadMarker = durable.EntryRecord

const (
	// latestCapacity is the conversations whose newest entry ID is kept: IDs only, so many.
	latestCapacity = 4096
	// markerCapacity is the conversations whose newest head marker is kept: whole entries (a compaction summary is
	// one), so fewer.
	markerCapacity = 256
)

// TailState says what ConversationTails.Peek knows of a conversation's newest entry.
type TailState int

const (
	// TailUnknown is a conversation whose newest entry is not kept (upstream `undefined`).
	TailUnknown TailState = iota
	// TailNone is a conversation known to have no entry (upstream `null`).
	TailNone
	// TailKnown is a conversation whose newest entry ID is kept.
	TailKnown
)

// tail is one conversation's newest entry ID; present is false for a conversation known to have none.
type tail struct {
	id      durable.EntryId
	present bool
}

// ConversationTails is the newest entry ID and newest head marker per conversation, for the Session that owns its
// Storage: every entry is written by one of its commits, so Apply keeps both current, and a value read from Storage
// stays valid until the next entry of that conversation. Read only on the Session line after touching the
// conversation's appends, so no commit that appends to it is unpublished and a value read from Storage can be kept.
// The newest entry visible from a conversation is its own newest, or for a fork without entries of its own the
// parent's entry at the fork point: IDs grow, so a conversation's own new entry is always the newest. Each map is
// bounded by a count, the least recently used conversation leaving first.
type ConversationTails struct {
	mu sync.Mutex
	// latest is the newest entry ID per conversation, none for none.
	latest *lru[durable.ConversationId, tail]
	// markers is the newest head marker per conversation, nil for none.
	markers *lru[durable.ConversationId, *HeadMarker]
}

// NewConversationTails returns tails that know no conversation.
func NewConversationTails() *ConversationTails {
	return &ConversationTails{
		latest:  newLru[durable.ConversationId, tail](latestCapacity),
		markers: newLru[durable.ConversationId, *HeadMarker](markerCapacity),
	}
}

// Latest is the newest entry ID visible from conversationId, nil for none, read with read when not known.
func (tails *ConversationTails) Latest(conversationId durable.ConversationId, read func() (*durable.EntryId, error)) (*durable.EntryId, error) {
	tails.mu.Lock()
	known, ok := tails.latest.get(conversationId)
	tails.mu.Unlock()
	if ok {
		if !known.present {
			return nil, nil
		}
		id := known.id
		return &id, nil
	}
	latest, err := read()
	if err != nil {
		return nil, err
	}
	kept := tail{}
	if latest != nil {
		kept = tail{id: *latest, present: true}
	}
	tails.mu.Lock()
	tails.latest.set(conversationId, kept)
	tails.mu.Unlock()
	return latest, nil
}

// Marker is the newest visible head marker at or before atOrBefore (any when nil), nil for none, read with read when
// not known. The newest marker answers every cutoff at or after it, and no marker answers every cutoff.
func (tails *ConversationTails) Marker(conversationId durable.ConversationId, atOrBefore *durable.EntryId, read func() (*HeadMarker, error)) (*HeadMarker, error) {
	tails.mu.Lock()
	marker, known := tails.markers.get(conversationId)
	if known && marker == nil {
		tails.mu.Unlock()
		return nil, nil
	}
	if known && (atOrBefore == nil || *atOrBefore >= marker.Id) {
		kept := detach.Entry(*marker)
		tails.mu.Unlock()
		return &kept, nil
	}
	latest, latestKnown := tails.latest.peek(conversationId)
	tails.mu.Unlock()
	found, err := read()
	if err != nil {
		return nil, err
	}
	// Only a read that no newer entry can change: no cutoff, or one at or after the newest entry.
	if atOrBefore == nil || (latestKnown && (!latest.present || latest.id <= *atOrBefore)) {
		tails.mu.Lock()
		tails.markers.set(conversationId, found)
		tails.mu.Unlock()
	}
	return found, nil
}

// Peek is the newest entry ID of conversationId if known, without reading: TailNone for none, TailUnknown when
// unknown. The ID is meaningful only with TailKnown.
func (tails *ConversationTails) Peek(conversationId durable.ConversationId) (durable.EntryId, TailState) {
	tails.mu.Lock()
	defer tails.mu.Unlock()
	known, ok := tails.latest.peek(conversationId)
	switch {
	case !ok:
		return 0, TailUnknown
	case !known.present:
		return 0, TailNone
	}
	return known.id, TailKnown
}

// Apply is called at a commit's publication: apply a durable batch.
func (tails *ConversationTails) Apply(writes []durable.StorageWrite) {
	tails.mu.Lock()
	defer tails.mu.Unlock()
	for _, write := range writes {
		switch write := write.(type) {
		case durable.ConversationWrite:
			// A new root conversation sees no entries; a fork sees its parent's up to the fork point.
			if write.Value.Parent != nil {
				continue
			}
			tails.latest.set(write.Value.Id, tail{})
			tails.markers.set(write.Value.Id, nil)
		case durable.EntryWrite:
			entry := write.Value
			tails.latest.set(entry.ConversationId, tail{id: entry.Id, present: true})
			if entry.Head != nil {
				kept := detach.Entry(entry)
				tails.markers.set(entry.ConversationId, &kept)
			}
		}
	}
}

// Size is how many conversations have their newest entry kept, for tests that bound memory.
func (tails *ConversationTails) Size() int {
	tails.mu.Lock()
	defer tails.mu.Unlock()
	return tails.latest.size()
}

// Markers is how many conversations have their newest head marker kept, for tests that bound memory.
func (tails *ConversationTails) Markers() int {
	tails.mu.Lock()
	defer tails.mu.Unlock()
	return tails.markers.size()
}

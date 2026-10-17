// SPDX-License-Identifier: MIT

package history

import (
	"math"
	"slices"
)

// unbounded is the upper bound of a scan that has none.
const unbounded = int64(math.MaxInt64)

// unloadedFrom is Chain.From of a conversation none of whose entries are loaded.
const unloadedFrom = int64(math.MaxInt64)

// ConvRecord is a conversation's immutable identity: its fork parent and its task owner (spec section 2).
type ConvRecord struct {
	ID int64
	// HasParent is set for a fork: entries of ParentConv with an id of at most ParentAt are inherited.
	HasParent  bool
	ParentConv int64
	ParentAt   int64
	// HasOwner is set when a task created the conversation.
	HasOwner  bool
	OwnerConv int64
	OwnerTask int64
}

// ParseConversation reads a stored conversation record.
func ParseConversation(rec []byte) (ConvRecord, error) {
	var out ConvRecord
	s := newScanner(rec)
	it, err := s.object()
	if err != nil {
		return out, err
	}
	for it.next() {
		switch {
		case it.keyIs("id"):
			out.ID, _ = intValue(it.value())
		case it.keyIs("parent"):
			out.HasParent = false
			out.ParentConv, out.ParentAt = 0, 0
			if v := it.value(); valueKind(v) == '{' {
				sub := newScanner(v)
				pit, err := sub.object()
				if err != nil {
					return out, err
				}
				var okC, okA bool
				for pit.next() {
					switch {
					case pit.keyIs("conversationId"):
						out.ParentConv, okC = intValue(pit.value())
					case pit.keyIs("at"):
						out.ParentAt, okA = intValue(pit.value())
					}
				}
				if pit.err != nil {
					return out, pit.err
				}
				out.HasParent = okC && okA
			}
		case it.keyIs("owner"):
			out.HasOwner = false
			out.OwnerConv, out.OwnerTask = 0, 0
			if v := it.value(); valueKind(v) == '{' {
				sub := newScanner(v)
				oit, err := sub.object()
				if err != nil {
					return out, err
				}
				var okC, okT bool
				for oit.next() {
					switch {
					case oit.keyIs("conversationId"):
						out.OwnerConv, okC = intValue(oit.value())
					case oit.keyIs("taskId"):
						out.OwnerTask, okT = intValue(oit.value())
					}
				}
				if oit.err != nil {
					return out, oit.err
				}
				out.HasOwner = okC && okT
			}
		}
	}
	if it.err != nil {
		return out, it.err
	}
	if !s.atEnd() {
		return out, ErrSyntax
	}
	return out, nil
}

// AppendConversation appends the stored form of a conversation record: {id, parent?, owner?} in that key order
// (`#stageConversation`, src/session/transaction.ts).
func AppendConversation(dst []byte, c ConvRecord) []byte {
	dst = append(dst, `{"id":`...)
	dst = appendInt(dst, c.ID)
	if c.HasParent {
		dst = append(dst, `,"parent":{"conversationId":`...)
		dst = appendInt(dst, c.ParentConv)
		dst = append(dst, `,"at":`...)
		dst = appendInt(dst, c.ParentAt)
		dst = append(dst, '}')
	}
	if c.HasOwner {
		dst = append(dst, `,"owner":{"conversationId":`...)
		dst = appendInt(dst, c.OwnerConv)
		dst = append(dst, `,"taskId":`...)
		dst = appendInt(dst, c.OwnerTask)
		dst = append(dst, '}')
	}
	return append(dst, '}')
}

// hint is a cold-open answer for one segment: the newest entry at or below an upper bound and the newest head
// marker at or below it (NeedBounds).
type hint struct {
	upper      int64
	hasMax     bool
	maxID      int64
	hasMarker  bool
	markerID   int64
	markerHead int64
}

type convState struct {
	rec   ConvRecord
	known bool
	chain *Chain
	hints []hint
	ctx   *derived
}

// Store holds the loaded transcript of one Session: every conversation's record and entry chain. Everything in it is
// a cache of the SQLite rows (ADR-0001 D9).
type Store struct {
	trimAtHead  bool
	kinds       [][]byte
	kindStrings []string // kinds as strings, same codes
	convs       map[int64]*convState
	// chainList numbers the chains so a message reference needs no pointer.
	chainList []*Chain
	chainNo   map[int64]int32
	// loader reads evicted record text back (evict.go); resident is the record text held; loadErr the first failed read.
	loader   Loader
	resident int
	loadErr  error
	// extended and rebuilt count how many derivations extended a cached context and how many scanned a range.
	extended, rebuilt int
	// lineages numbers the settled message lists derivations start, so views can tell a shared settled prefix (View.Settled).
	lineages uint64
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{convs: map[int64]*convState{}, chainNo: map[int64]int32{}}
}

func (st *Store) state(conv int64) *convState {
	cs := st.convs[conv]
	if cs == nil {
		cs = &convState{}
		st.convs[conv] = cs
	}
	return cs
}

func (st *Store) chainOf(conv int64, create bool) *Chain {
	cs := st.state(conv)
	if cs.chain == nil && create {
		cs.chain = newChain(conv)
		cs.chain.st = st
		cs.chain.From, cs.chain.To = unloadedFrom, 0
		st.chainNo[conv] = int32(len(st.chainList))
		st.chainList = append(st.chainList, cs.chain)
	}
	return cs.chain
}

// Conversation returns a loaded conversation's record.
func (st *Store) Conversation(conv int64) (ConvRecord, bool) {
	cs := st.convs[conv]
	if cs == nil || !cs.known {
		return ConvRecord{}, false
	}
	return cs.rec, true
}

// AddConversation registers a conversation record: a row read from the store, or one the Session just created.
// A conversation created in this process has no stored entries, so its chain starts complete.
func (st *Store) AddConversation(rec ConvRecord, created bool) {
	cs := st.state(rec.ID)
	cs.rec, cs.known = rec, true
	if created {
		ch := st.chainOf(rec.ID, true)
		ch.From, ch.To = 0, unbounded
	}
}

// Drop forgets a conversation's loaded state; its next use cold-loads it.
func (st *Store) Drop(conv int64) {
	cs := st.convs[conv]
	if cs == nil {
		return
	}
	if cs.chain != nil {
		cs.chain.reset(unloadedFrom, 0)
	}
	cs.hints = cs.hints[:0]
	cs.ctx = nil
}

// Release forgets conversation conv's derived context, and frees the entries of every chain it read that no other
// derived context reads: its own and its fork ancestors'. The Session calls it when Pi's scheduler drops the
// conversation's kept context range after settings.contextRetentionMs of idleness (scheduler.ts #settleIdle), so an idle
// conversation holds no transcript; its next use loads what it needs again.
func (st *Store) Release(conv int64) {
	cs := st.convs[conv]
	if cs == nil {
		return
	}
	cs.ctx = nil
	cs.hints = cs.hints[:0]
	read := make([]bool, len(st.chainList))
	for _, other := range st.convs {
		if other.ctx != nil {
			for _, r := range other.ctx.reads {
				read[r.ch] = true
			}
		}
	}
	for cur := conv; ; {
		c := st.convs[cur]
		if c == nil {
			break
		}
		if c.chain != nil && c.chain.From != unloadedFrom && !read[st.chainNo[cur]] {
			c.chain.reset(unloadedFrom, 0)
		}
		if !c.rec.HasParent {
			break
		}
		cur = c.rec.ParentConv
	}
}

// BeginLoad starts loading a range of a conversation: the following AddRow calls supply every entry of the
// conversation with an id in [from, to] (to unbounded: through the newest), ascending. Whatever the chain held is
// discarded, unless it holds the entries through from-1 only through a bound (not through the newest): the rows then extend
// it.
func (st *Store) BeginLoad(conv, from, to int64) {
	ch := st.chainOf(conv, true)
	if ch.From != unloadedFrom && ch.To != unbounded && from == ch.To+1 && to > ch.To {
		// The entries after the range the chain holds: they extend it, and what is derived from it stays valid.
		ch.To = to
		return
	}
	ch.reset(from, to)
	st.state(conv).ctx = nil
}

// Reserve makes room for rows entries in the chain being loaded, so loading them does not grow the index piecemeal. The
// record text needs no reservation: the arena grows by fixed-size chunks and nothing is copied. The allocation is exact
// plus an eighth (room): a loader reserves page by page, and an exact reservation per page copied the whole index once a
// page, leaving a quadratic amount of garbage that TinyGo's collector pinned (13 MiB of a 33,335-entry transcript).
func (st *Store) Reserve(conv int64, rows, _ int) {
	ch := st.chainOf(conv, true)
	ch.ents = room(ch.ents, rows)
	ch.msgs = room(ch.msgs, rows)
	ch.spans = room(ch.spans, rows/2)
}

// AddRow adds one stored entry row to the conversation's chain being loaded.
func (st *Store) AddRow(conv, id, seq int64, rec []byte) error {
	return st.chainOf(conv, true).appendRecord(st, id, seq, rec)
}

// Append adds an entry the Session just committed. A conversation that is not loaded stays so. A chain that holds a bounded range
// (a cutoff, or a fork point) keeps it, and its next use for a later entry extends it with the entries above it.
func (st *Store) Append(conv, id, seq int64, rec []byte) error {
	// A bounds answer for a cutoff at or above the new entry no longer names the newest entry or head marker.
	cs := st.state(conv)
	cs.hints = slices.DeleteFunc(cs.hints, func(h hint) bool { return h.upper >= id })
	ch := st.chainOf(conv, true)
	if ch.From == unloadedFrom {
		st.state(conv).ctx = nil
		return nil
	}
	if ch.To != unbounded {
		// The chain holds a bounded range, and the entry lies above it. The views derived from the chain stay valid (a
		// request in flight holds one across its hops), and a later load of the entries above the range extends the chain.
		return nil
	}
	heads := len(ch.heads)
	if err := ch.appendRecord(st, id, seq, rec); err != nil {
		return err
	}
	if st.trimAtHead && len(ch.heads) > heads {
		if err := st.trimToNewestHead(cs, ch); err != nil {
			return err
		}
	}
	return nil
}

// trimToNewestHead drops what a chain holds below the head of its newest head marker. A head marker ends the context before
// its head: a derivation reads only the marker's range, and the edits and messages of everything older (a compaction entry
// carries an edit for each entry it replaces) would otherwise stay indexed for the life of the conversation. An as-of read below
// the marker loads its own range again.
func (st *Store) trimToNewestHead(cs *convState, ch *Chain) error {
	if ch.From == unloadedFrom || len(ch.heads) == 0 {
		return nil
	}
	if err := ch.trimBelow(ch.heads[len(ch.heads)-1].head); err != nil {
		return err
	}
	cs.ctx = nil
	return nil
}

// EndLoad is called when a loader has added the rows a Need asked for. With SetTrimAtHead it drops what a loaded chain holds
// below its newest head marker's head, as appending the marker would have.
func (st *Store) EndLoad(conv int64) error {
	if !st.trimAtHead {
		return nil
	}
	cs := st.state(conv)
	if cs.chain == nil {
		return nil
	}
	return st.trimToNewestHead(cs, cs.chain)
}

// SetTrimAtHead makes the store drop the entries below a head marker's head from a chain when the marker is appended: the store
// then holds the range a derivation from the newest marker reads, which the context window bounds, not the whole conversation.
// An as-of read below the marker loads its range again. Without it a store keeps everything it was fed.
func (st *Store) SetTrimAtHead() { st.trimAtHead = true }

// SupplyBounds answers a NeedBounds read.
func (st *Store) SupplyBounds(conv, upper int64, hasMax bool, maxID int64, hasMarker bool, markerID, markerHead int64) {
	cs := st.state(conv)
	for i := range cs.hints {
		if cs.hints[i].upper == upper {
			cs.hints = append(cs.hints[:i], cs.hints[i+1:]...)
			break
		}
	}
	if len(cs.hints) >= 4 {
		cs.hints = cs.hints[1:]
	}
	cs.hints = append(cs.hints, hint{upper, hasMax, maxID, hasMarker, markerID, markerHead})
}

// NeedKind names a read the host must run before a derivation can proceed.
type NeedKind uint8

// The reads a derivation can ask for.
const (
	// NeedConversation asks for the conversation row with id Conv.
	NeedConversation NeedKind = iota + 1
	// NeedBounds asks, for conversation Conv and upper bound Max, for the newest entry id at or below Max and
	// the newest head marker's id and head at or below Max (SQLBounds).
	NeedBounds
	// NeedEntries asks for every entry row of conversation Conv with Min <= id <= Max, ascending (SQLEntries),
	// answered by BeginLoad(Conv, Min) and AddRow.
	NeedEntries
)

// Need is one read the host runs and answers before the derivation continues.
type Need struct {
	Kind     NeedKind
	Conv     int64
	Min, Max int64
}

// segment is one conversation's contribution to a fork chain: its entries up to upper.
type segment struct {
	conv  int64
	upper int64
}

// segBounds computes the newest entry and newest head marker of one segment, locally when the chain allows it and
// from a cold-open answer otherwise. A loaded chain holds every entry from Chain.From on, so a local hit is the
// newest; a hint fills only what the loaded suffix cannot answer.
func (st *Store) segBounds(sg segment) (h hint, need *Need) {
	cs := st.state(sg.conv)
	ch := cs.chain
	h.upper = sg.upper
	i, hi := -1, -1
	// A chain loaded only through To answers a cutoff at or below To: entries above it are not loaded.
	if ch != nil && ch.From != unloadedFrom && ch.To >= sg.upper {
		if sg.upper == unbounded {
			i = len(ch.ents) - 1
		} else {
			i = ch.search(sg.upper+1) - 1
		}
		hi = ch.latestHead(sg.upper)
		if i >= 0 {
			h.hasMax, h.maxID = true, ch.ents[i].id
		}
		if hi >= 0 {
			h.hasMarker, h.markerID, h.markerHead = true, ch.ents[ch.heads[hi].ent].id, ch.heads[hi].head
		}
		if ch.From == 0 || (i >= 0 && hi >= 0) {
			return h, nil
		}
	}
	for _, c := range cs.hints {
		if c.upper == sg.upper {
			if i < 0 {
				h.hasMax, h.maxID = c.hasMax, c.maxID
			}
			if hi < 0 {
				h.hasMarker, h.markerID, h.markerHead = c.hasMarker, c.markerID, c.markerHead
			}
			return h, nil
		}
	}
	return h, &Need{Kind: NeedBounds, Conv: sg.conv, Max: sg.upper}
}

// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// histConv is a conversation record as history.Store takes it.
func histConv(c *jv.Object) history.ConvRecord {
	out := history.ConvRecord{ID: int64(numOf(c, "id"))}
	if p := objOf(c, "parent"); p != nil {
		out.HasParent, out.ParentConv, out.ParentAt = true, int64(numOf(p, "conversationId")), int64(numOf(p, "at"))
	}
	if o := objOf(c, "owner"); o != nil && has(o, "taskId") {
		out.HasOwner, out.OwnerConv, out.OwnerTask = true, int64(numOf(o, "conversationId")), int64(numOf(o, "taskId"))
	}
	return out
}

// storedEntries is the history store's Loader: the stored entries of a conversation, read from storage through the host's
// synchronous read (history.Evict releases record text only once storage has every record it holds).
func (h *handler) storedEntries(conv, lo, hi int64, fn func(id, seq int64, rec []byte) bool) error {
	m := h.mir
	if !m.storedEntries(session.ID(conv), session.ID(lo), session.ID(hi), func(rows [][]abi.Value) bool {
		for _, r := range rows {
			if !fn(int64(r[0].Number()), int64(r[1].Number()), r[2].Bytes) {
				return false
			}
		}
		return true
	}) {
		return m.lazy.err
	}
	return nil
}

// historyBytes is the record text the transcript store holds in memory and the size of its index (mem_stats slots arena
// and index).
func (h *handler) historyBytes() (arena, index uint64) {
	if h.hist == nil {
		return 0, 0
	}
	return uint64(h.hist.ResidentBytes()), uint64(h.hist.IndexBytes())
}

// contextView derives a conversation's context (history.Store.Context, spec §2.1) at an entry (0: the tail). The
// transcript store loads what the derivation asks for from the mirror.
func (h *handler) contextView(conv, at int64) (*history.View, error) {
	if h.hist == nil {
		h.hist = history.NewStore()
		h.hist.SetTrimAtHead()
		if h.mir != nil && h.mir.lazy.read != nil {
			h.hist.SetLoader(h.storedEntries)
		}
	}
	m := h.mir
	for range 1 << 16 {
		v, need, err := h.hist.Context(conv, at)
		if err != nil || need == nil {
			return v, err
		}
		switch need.Kind {
		case history.NeedConversation:
			c := m.convs.get(need.Conv)
			if c == nil {
				return nil, session.NewStorageRequestError("Unknown conversation: " + itoa(need.Conv))
			}
			h.hist.AddConversation(histConv(c), false)
		case history.NeedBounds:
			b, ok := m.entryBounds(need.Conv, history.SQLBound(need.Max))
			if !ok {
				return nil, m.lazy.err
			}
			h.hist.SupplyBounds(need.Conv, need.Max, b.hasMax, b.maxID, b.hasMarker, b.markerID, b.markerHead)
		case history.NeedEntries:
			max := need.Max
			if h.newestAtOrBelow(need.Conv, max) {
				// A read as of the conversation's newest entry is the tip read: the chain it loads takes the entries the Session
				// commits next (Store.Append drops them on a bounded chain), so the next turn does not read them back.
				max = history.Unbounded
			}
			if err := h.loadEntries(need.Conv, need.Min, max); err != nil {
				return nil, err
			}
			if err := h.hist.EndLoad(need.Conv); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("core: unknown history need")
		}
	}
	return nil, errors.New("core: the context derivation did not converge")
}

// newestAtOrBelow reports whether conversation conv has no entry above id: id is its newest entry or higher.
func (h *handler) newestAtOrBelow(conv, id int64) bool {
	if id == history.Unbounded {
		return false
	}
	b, ok := h.mir.entryBounds(session.ID(conv), 0)
	return ok && (!b.hasMax || int64(b.maxID) <= id)
}

// contextBounds finds the tail and head marker entry of a conversation's context (history.Store.Bounds): what a host needs
// to derive the context itself. found is false when the conversation has no visible entry at the cutoff.
func (h *handler) contextBounds(conv, at int64) (tail, head int64, found bool, err error) {
	if h.hist == nil {
		h.hist = history.NewStore()
		if h.mir != nil && h.mir.lazy.read != nil {
			h.hist.SetLoader(h.storedEntries)
		}
	}
	m := h.mir
	for range 1 << 16 {
		t, hasHead, headID, ok, need, err := h.hist.Bounds(conv, at)
		if err != nil {
			return 0, 0, false, err
		}
		if need == nil {
			if hasHead {
				head = headID
			}
			return t, head, ok, nil
		}
		switch need.Kind {
		case history.NeedConversation:
			c := m.convs.get(need.Conv)
			if c == nil {
				return 0, 0, false, session.NewStorageRequestError("Unknown conversation: " + itoa(need.Conv))
			}
			h.hist.AddConversation(histConv(c), false)
		case history.NeedBounds:
			b, ok := m.entryBounds(need.Conv, history.SQLBound(need.Max))
			if !ok {
				return 0, 0, false, m.lazy.err
			}
			h.hist.SupplyBounds(need.Conv, need.Max, b.hasMax, b.maxID, b.hasMarker, b.markerID, b.markerHead)
		default:
			return 0, 0, false, errors.New("core: unknown history need")
		}
	}
	return 0, 0, false, errors.New("core: the context bounds did not converge")
}

// loadEntries answers a NeedEntries: conversation conv's entries with ids from lo up to the Need's max, the stored ones
// a page at a time (mirror_lazy.go), then the ones the mirror holds, which all follow them.
func (h *handler) loadEntries(conv, lo, max int64) error {
	m := h.mir
	h.hist.BeginLoad(conv, lo, max)
	hi := history.SQLBound(max)
	if top := min(hi, m.storedBelow()-1); top >= lo {
		if rows, ok := m.lazyRead(store.SQLEntryCount, abi.Int(conv), abi.Int(lo), abi.Int(top)); ok && len(rows) == 1 {
			// The load reserves its index once: reserving page by page copied it once a page.
			h.hist.Reserve(conv, int(rows[0][0].Number()), 0)
		}
		var addErr error
		if !m.storedEntries(conv, lo, top, func(rows [][]abi.Value) bool {
			size := 0
			for _, r := range rows {
				size += len(r[2].Bytes)
			}
			h.hist.Reserve(conv, len(rows), size)
			for _, r := range rows {
				if addErr = h.hist.AddRow(conv, int64(r[0].Number()), session.Seq(r[1].Number()), r[2].Bytes); addErr != nil {
					return false
				}
			}
			// Every row of the chain so far is in storage: release its older text now, so loading a long transcript does
			// not hold all of it (the next StepStart would, after the whole load).
			h.hist.EvictLoaded(conv)
			return true
		}) {
			if addErr != nil {
				return addErr
			}
			return m.lazy.err
		}
	}
	held, _ := m.entries.held(conv)
	held = held[firstAbove(held, lo-1):firstAbove(held, hi)]
	size := 0
	for _, id := range held {
		raw, _ := m.entries.raw(id)
		size += len(raw)
	}
	h.hist.Reserve(conv, len(held), size)
	for _, id := range held {
		i, _ := m.entries.at(id)
		if err := h.hist.AddRow(conv, id, m.entries.seqs[i], m.entries.raws[i]); err != nil {
			return err
		}
	}
	return nil
}

// observeCommit keeps the transcript store in step with a commit the mirror applied.
//
//go:noinline
func (h *handler) observeCommit(writes []session.Write, seq session.Seq) error {
	if h.hist == nil {
		return nil
	}
	for _, w := range writes {
		switch w.Kind {
		case session.WriteConversation:
			h.hist.AddConversation(histConv(w.Record), true)
		case session.WriteEntry:
			if err := h.hist.Append(int64(numOf(w.Record, "conversationId")), int64(numOf(w.Record, "id")), seq, jv.Append(nil, w.Record)); err != nil {
				return err
			}
		}
	}
	return nil
}

// contextAnswer is the api answer of a context read (ABI section 8.1, op context): the context is not built in the core. A
// context with an entry is named, `{"$context":{conversationId, head, tail}}`, and the host that serves the read derives the
// ContextView from storage; a conversation without one is the empty view.
type contextAnswer struct {
	conv, head, tail int64
	found            bool
}

func (a contextAnswer) json() []byte {
	if !a.found {
		return []byte(`{"entries":[],"contributions":[],"messages":[]}`)
	}
	out := append([]byte(`{"$context":{"conversationId":`), itoa(a.conv)...)
	out = append(out, `,"head":`...)
	out = append(out, itoa(a.head)...)
	out = append(out, `,"tail":`...)
	out = append(out, itoa(a.tail)...)
	return append(out, `}}`...)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// rawJSON is an api answer already encoded.
type rawJSON []byte

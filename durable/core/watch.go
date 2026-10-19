// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// docWatch is one open document watch (api watch.open, kind doc): Session.watchDoc's observer of one committed
// incarnation. version is the definition version of the value the observer holds.
type docWatch struct {
	doc     session.ID
	version int
	// unpin releases the pin that keeps the watched incarnation cached while the watch is open (session.ts #attachDocument).
	unpin func()
}

// watchOpen answers watch.open for a document target: {value} with the committed value, or {value: null} when the
// document is absent and no watch opened. Later commits of the incarnation become frames (publishWatches).
func (h *handler) watchOpen(req *jv.Object) (any, error) {
	target := objOf(req, "target")
	answer := jv.NewObject()
	switch strOf(target, "kind") {
	case "doc":
	case "view":
		return h.viewOpen(req, target)
	case "graph":
		return h.graphWatchOpen(req)
	default:
		return nil, ErrNotImplemented
	}
	op := jv.NewObject()
	op.Set("address", objOf(target, "address"))
	if d := objOf(target, "def"); d != nil {
		op.Set("def", d)
	}
	def, args, err := docDef(op)
	if err != nil {
		return nil, err
	}
	migrating := hostMigration(def, target)
	value, err := h.sess.Snapshot(def, args)
	if err != nil {
		return nil, migrating.need(err)
	}
	if value == nil {
		answer.Set("value", nil)
		return answer, nil
	}
	rec, err := h.mir.FindDocument(session.DocumentAddress(def, args), session.Current)
	if err != nil {
		return nil, err
	}
	if h.watches == nil {
		h.watches = map[uint32]*docWatch{}
	}
	h.watches[uint32(numOf(req, "watchId"))] = &docWatch{doc: session.ID(numOf(rec, "id")), version: def.Version, unpin: h.sess.PinDocument(def, args)}
	answer.Set("value", value)
	return answer, nil
}

func (h *handler) watchClose(req *jv.Object) (any, error) {
	h.dropWatch(uint32(numOf(req, "watchId")))
	delete(h.views, uint32(numOf(req, "watchId")))
	h.graphWatchClose(uint32(numOf(req, "watchId")))
	return nil, nil
}

// publishWatches emits the frames of one commit: for each document change of a watched incarnation, a published notice
// {watchId, frame: {value, ops}} in watch order (session.ts #attachDocument, observedOperations).
//
// Not inlined: it does work only for an open watch, and an inlined copy would be compiled by V8 with its hot caller, publish.
//
//go:noinline
func (h *handler) publishWatches(pub *session.Publication) {
	h.publishViews(pub)
	h.advanceTaskGraph(pub)
	if len(h.watches) == 0 {
		return
	}
	ids := make([]uint32, 0, len(h.watches))
	for id := range h.watches {
		ids = append(ids, id)
	}
	sortU32(ids)
	for _, c := range pub.Changes {
		if c.Kind != session.ChangeDocument {
			continue
		}
		docID := session.ID(numOf(c.Record, "id"))
		for _, id := range ids {
			w := h.watches[id]
			if w == nil || w.doc != docID {
				continue
			}
			var value any
			var ops []any
			switch {
			case c.Value == nil:
				ops = []any{[]any{"r", nil}}
				h.dropWatch(id)
			case c.Version == w.version:
				value, ops = c.Value, delta.OpsValue(c.Ops)
			default:
				w.version = c.Version
				value, ops = c.Value, []any{[]any{"r", c.Value}}
			}
			if len(ops) == 0 {
				continue
			}
			out := append(make([]byte, 0, 96), `{"watchId":`...)
			out = jv.Append(out, float64(id))
			out = append(out, `,"frame":{"value":`...)
			out = jv.Append(out, value)
			out = append(out, `,"ops":`...)
			out = jv.Append(out, ops)
			h.b.Notice(abi.NoticePublished, append(out, '}', '}'))
		}
	}
}

func sortU32(s []uint32) { slices.Sort(s) }

// dropWatch forgets a document watch and releases its pin.
func (h *handler) dropWatch(id uint32) {
	if w := h.watches[id]; w != nil && w.unpin != nil {
		w.unpin()
	}
	delete(h.watches, id)
}

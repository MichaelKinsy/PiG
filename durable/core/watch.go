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

// docWatch is one open document watch (api watch.open, kind doc): Session.watchDoc's observer of one document address,
// from the incarnation found at acquisition (session.ts #observeDocument). version is the definition version of the value
// the observer holds.
type docWatch struct {
	// address is the address followed, as an address ID; def and args pin the incarnation at it.
	address string
	def     *session.DocDef
	args    session.DocArgs
	// task is the owner task of a task document's address, 0 for another scope: its terminal commit ends the watch.
	task session.ID
	// doc is the incarnation followed; followed is false while the address is absent.
	doc      session.ID
	followed bool
	version  int
	// unpin releases the pin that keeps the followed incarnation cached while the watch is open.
	unpin func()
}

// watchOpen answers watch.open for a document target: {value} with the committed value, or {value: null} when the
// document is absent and no watch opened. Later commits at the address become frames (publishWatches).
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
	watch := &docWatch{address: session.DocAddress(def, args), def: def, args: args, doc: session.ID(numOf(rec, "id")), followed: true, version: def.Version, unpin: h.sess.PinDocument(def, args)}
	if def.Scope == session.ScopeTask {
		watch.task = args.Owner
	}
	h.watches[uint32(numOf(req, "watchId"))] = watch
	answer.Set("value", value)
	return answer, nil
}

func (h *handler) watchClose(req *jv.Object) (any, error) {
	h.dropWatch(uint32(numOf(req, "watchId")))
	delete(h.views, uint32(numOf(req, "watchId")))
	h.graphWatchClose(uint32(numOf(req, "watchId")))
	return nil, nil
}

// publishWatches emits the frames of one commit: for each document change at a watched address, a published notice
// {watchId, frame} in watch order (session.ts #observeDocument). The frame is {value, ops} for a change, {value, ops: [["r",
// value]]} for a new incarnation at the address or a value of another definition version, {retired: true} for a
// retirement (the driver gives the last value it holds, with no ops), and {end: true} once the commit made the owner task
// of a task document terminal, after its retirement.
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
	h.publishDocWatches(pub)
}

// publishDocWatches is the work of publishWatches for the document watches that are open. It is a function of its own so that the
// hot publishWatches, which every commit runs, stays small (the function-size gate of the Cloudflare host: V8 compiles a hot
// function whole).
//
//go:noinline
func (h *handler) publishDocWatches(pub *session.Publication) {
	ids := make([]uint32, 0, len(h.watches))
	for id := range h.watches {
		ids = append(ids, id)
	}
	sortU32(ids)
	var ended []uint32
	for _, c := range pub.Changes {
		if c.Kind == session.ChangeTable && c.Table == session.WriteTask {
			if task, terminal := session.TerminalTask(c.Record); terminal {
				for _, id := range ids {
					if w := h.watches[id]; w != nil && w.task == task {
						ended = append(ended, id)
					}
				}
			}
			continue
		}
		if c.Kind != session.ChangeDocument {
			continue
		}
		docID := session.ID(numOf(c.Record, "id"))
		address := ""
		for _, id := range ids {
			w := h.watches[id]
			if w == nil {
				continue
			}
			current := w.followed && w.doc == docID
			if !current {
				// The incarnation followed is at its address by definition; only another record needs it compared.
				if address == "" {
					address = session.RecordAddressID(c.Record)
				}
				if address != w.address {
					continue
				}
			}
			var value any
			var ops []any
			retired := false
			switch {
			case c.Value == nil:
				if !current {
					continue
				}
				retired = true
				w.followed = false
				w.release()
			case !current || c.Version != w.version:
				// A new incarnation at the address, or a value of another definition version, whose shape the observer's
				// value does not have: shown whole.
				if !current {
					w.release()
					w.doc, w.followed = docID, true
					w.unpin = h.sess.PinDocument(w.def, w.args)
				}
				w.version = c.Version
				value, ops = c.Value, []any{[]any{"r", c.Value}}
			default:
				// A migration-only base changes nothing for an observer of the new version.
				if len(c.Ops) == 0 {
					continue
				}
				value, ops = c.Value, delta.OpsValue(c.Ops)
			}
			out := append(make([]byte, 0, 96), `{"watchId":`...)
			out = jv.Append(out, float64(id))
			if retired {
				out = append(out, `,"frame":{"retired":true}}`...)
			} else {
				out = append(out, `,"frame":{"value":`...)
				out = jv.Append(out, value)
				out = append(out, `,"ops":`...)
				out = jv.Append(out, ops)
				out = append(out, '}', '}')
			}
			h.b.Notice(abi.NoticePublished, out)
		}
	}
	for _, id := range ended {
		if h.watches[id] == nil {
			continue
		}
		out := append(make([]byte, 0, 64), `{"watchId":`...)
		out = jv.Append(out, float64(id))
		h.b.Notice(abi.NoticePublished, append(out, `,"frame":{"end":true}}`...))
		h.dropWatch(id)
	}
}

// release unpins the incarnation followed; it is idempotent.
func (w *docWatch) release() {
	if w.unpin != nil {
		w.unpin()
		w.unpin = nil
	}
}

func sortU32(s []uint32) { slices.Sort(s) }

// dropWatch forgets a document watch and releases its pin.
func (h *handler) dropWatch(id uint32) {
	if w := h.watches[id]; w != nil {
		w.release()
	}
	delete(h.watches, id)
}

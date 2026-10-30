// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/docs"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// viewWatch is one open conversation view (api watch.open, kind view): harness/view.ts ConversationViews' mount of one
// conversation with one observer. The view is the ConversationView {conversation, entries, before?, docs}: the conversation
// record, a window over the transcript (its fork-aware history in entry ID order), the ID of the entry just before the window
// when there is one, and the mounted built-in documents. The watch does not hold it: a frame is the ops of one commit, and the host
// applies them to the view it holds. The watch keeps only what choosing those ops reads: the IDs of its window's entries.
type viewWatch struct {
	conv         session.ID
	conversation *jv.Object // the conversation record the mount was built with, which later observers of the mount are given
	min, max     int
	ids          []float64 // the IDs of the window's entries, oldest first
	docs         map[string]viewDoc
}

type viewDoc struct {
	id      session.ID
	version int
}

// defaultWindowMin is view.ts DEFAULT_WINDOW_MIN.
const defaultWindowMin = 50

// windowBounds is a ConversationWindow with its defaults applied (view.ts WindowBounds).
type windowBounds struct {
	from     float64
	hasFrom  bool
	min, max int
}

// resolveWindow is view.ts resolveWindow: the window of a watch.open target with its defaults; bounds that are not integers, or a
// max below min, fail as the RangeError of view.ts does.
func resolveWindow(target *jv.Object) (windowBounds, error) {
	window, _ := target.Get("window")
	object, _ := window.(*jv.Object)
	bound := func(key string) (float64, bool, error) {
		if object == nil || !object.Has(key) {
			return 0, false, nil
		}
		value, _ := object.Get(key)
		number, ok := value.(float64)
		if !ok || number != math.Trunc(number) || math.Abs(number) > 1<<53-1 {
			return 0, false, reject("RangeError", "Window "+key+" must be an integer, got "+jv.Stringify(value))
		}
		return number, true, nil
	}
	minimum, hasMin, err := bound("min")
	if err != nil {
		return windowBounds{}, err
	}
	if !hasMin {
		minimum = defaultWindowMin
	}
	if minimum < 1 {
		return windowBounds{}, reject("RangeError", "Window min must be an integer >= 1, got "+jv.Stringify(minimum))
	}
	maximum, hasMax, err := bound("max")
	if err != nil {
		return windowBounds{}, err
	}
	if !hasMax {
		maximum = 2 * minimum
	}
	if maximum < minimum {
		return windowBounds{}, reject("RangeError", "Window max must be an integer >= min ("+jv.Stringify(minimum)+"), got "+jv.Stringify(maximum))
	}
	from, hasFrom, err := bound("from")
	if err != nil {
		return windowBounds{}, err
	}
	return windowBounds{from: from, hasFrom: hasFrom, min: int(minimum), max: int(maximum)}, nil
}

func (h *handler) mountedDefs() []*session.DocDef {
	return []*session.DocDef{docs.AgentDef, turn.LiveDef, inbox.Def, docs.ProviderDef(h.uuid), usageDef}
}

func isMounted(kind string) bool {
	switch kind {
	case "pi.agent", "pi.live", "pi.inbox", "pi.provider", "pi.usage":
		return true
	}
	return false
}

// viewOpen builds the view of a conversation and registers its watch.
//
//go:noinline
func (h *handler) viewOpen(req *jv.Object, target *jv.Object) (any, error) {
	bounds, err := resolveWindow(target)
	if err != nil {
		return nil, err
	}
	conv := session.ID(numOf(target, "conversationId"))
	mount := h.mountOf(conv)
	var record *jv.Object
	var held map[string]viewDoc
	if mount != nil {
		// An observer of a mount another holds only loads its window (view.ts attach: `this.#mounts.get(id) ?? build`).
		record, held = mount.conversation, mount.docs
	} else {
		h.read(hopcost.ReadRow)
		var err error
		record, err = h.mir.Conversation(conv)
		if err != nil {
			return nil, err
		}
		if record == nil {
			return nil, errors.New("Conversation " + itoa(int64(conv)) + " does not exist")
		}
	}
	docObject := jv.NewObject()
	mounted := map[string]viewDoc{}
	for _, def := range h.mountedDefs() {
		at, isHeld := held[def.Kind]
		if mount != nil && !isHeld {
			continue
		}
		args := session.DocArgs{Owner: conv}
		value, err := h.sess.Snapshot(def, args)
		if err != nil {
			return nil, err
		}
		if mount == nil {
			h.chargeViewDoc()
		}
		if value == nil {
			continue
		}
		if mount == nil {
			rec, err := h.mir.FindDocument(session.DocumentAddress(def, args), session.Current)
			if err != nil {
				return nil, err
			}
			at = viewDoc{id: session.ID(numOf(rec, "id")), version: def.Version}
		}
		docObject.Set(def.Kind, value)
		mounted[def.Kind] = at
	}
	if mount == nil {
		h.job.change += hopcost.ViewBuildReturn
	}
	records, before, err := h.loadWindow(conv, bounds)
	if err != nil {
		return nil, err
	}
	entries := make([]any, len(records))
	ids := make([]float64, len(records), max(len(records), bounds.max))
	for i, e := range records {
		entries[i] = e
		ids[i] = numOf(e, "id")
	}
	value := jv.NewObject()
	value.Set("conversation", record)
	value.Set("entries", entries)
	if before != 0 {
		value.Set("before", float64(before))
	}
	value.Set("docs", docObject)
	if h.views == nil {
		h.views = map[uint32]*viewWatch{}
	}
	h.views[uint32(numOf(req, "watchId"))] = &viewWatch{conv: conv, conversation: record, min: bounds.min, max: bounds.max, ids: ids, docs: mounted}
	answer := jv.NewObject()
	answer.Set("value", value)
	return answer, nil
}

// mountOf is the mount of a conversation's view: the watch of an observer that holds it, or nil when no observer does (the mount is
// dropped with its last observer, view.ts attach). Every observer of the conversation holds the same documents.
func (h *handler) mountOf(conv session.ID) *viewWatch {
	var found *viewWatch
	var at uint32
	for id, w := range h.views {
		if w.conv == conv && (found == nil || id < at) {
			found, at = w, id
		}
	}
	return found
}

// loadWindow is view.ts loadWindow: a new observer's window, read with bounded scans: the entries from bounds.from when at most
// bounds.max are at or after it, otherwise the newest bounds.min, and the entry before them (0 when none).
func (h *handler) loadWindow(conv session.ID, bounds windowBounds) ([]*jv.Object, session.ID, error) {
	if bounds.hasFrom {
		since, err := h.scanUpTo(session.EntryQuery{ConversationID: conv, MinEntryID: session.ID(bounds.from), Ascending: true}, bounds.max+1)
		if err != nil {
			return nil, 0, err
		}
		if len(since) <= bounds.max {
			h.read(hopcost.ReadScanEntries)
			// A `from` below 1 has no entry before it; the query's 0 would not bound the scan.
			var before session.ID
			if bounds.from > 1 {
				page, err := h.mir.ScanEntries(session.EntryQuery{ConversationID: conv, MaxEntryID: session.ID(bounds.from - 1)}, 1, 0)
				if err != nil {
					return nil, 0, err
				}
				if len(page.Items) > 0 {
					before = session.ID(numOf(page.Items[0], "id"))
				}
			}
			h.job.change += hopcost.ViewWindowReturn
			return since, before, nil
		}
	}
	newest, err := h.scanUpTo(session.EntryQuery{ConversationID: conv}, bounds.min+1)
	if err != nil {
		return nil, 0, err
	}
	var before session.ID
	if len(newest) > bounds.min {
		before = session.ID(numOf(newest[bounds.min], "id"))
		newest = newest[:bounds.min]
	}
	slices.Reverse(newest)
	h.job.change += hopcost.ViewWindowReturn
	return newest, before, nil
}

// scanUpTo is view.ts scanUpTo: up to limit entries of query, following cursors past short pages.
func (h *handler) scanUpTo(query session.EntryQuery, limit int) ([]*jv.Object, error) {
	entries := make([]*jv.Object, 0, min(limit, viewScanPage))
	var after session.ID
	for {
		h.read(hopcost.ReadScanEntries)
		page, err := h.mir.ScanEntries(query, min(viewScanPage, limit-len(entries)), after)
		if err != nil {
			return nil, err
		}
		entries = append(entries, page.Items...)
		after = page.After
		if after == 0 || len(entries) >= limit {
			h.job.change += hopcost.ViewScanReturn
			return entries, nil
		}
	}
}

// viewScanPage is view.ts SCAN_PAGE_SIZE.
const viewScanPage = 256

// chargeViewDoc charges the ticks of `await session.conversationDocumentOnLine(token, id)` (session.ts :121-133) beyond its Storage
// reads: the Session's loadDocument charges findDocument and document() itself, through the adapter, for a document it has not
// cached; a cached one answers in the two returns alone.
func (h *handler) chargeViewDoc() { h.job.change += hopcost.ViewDocReturn }

// prefixed is view.ts prefixed(): a document op relocated under ["docs", kind].
func prefixed(op delta.Op, kind string) delta.Op {
	base := delta.Path{delta.Key("docs"), delta.Key(kind)}
	if op.Verb == delta.Replace {
		return delta.Op{Verb: delta.Set, Path: base, Value: op.Value}
	}
	op.Path = append(append(delta.Path{}, base...), op.Path...)
	return op
}

// advance is view.ts advance(): the ops one commit makes to the view. The commit's documents are applied, then its entries
// appended to the window, which past its max drops its oldest down to its min in one splice.
func (w *viewWatch) advance(pub *session.Publication) []delta.Op {
	var docOps []delta.Op
	var appended []any
	for _, c := range pub.Changes {
		switch c.Kind {
		case session.ChangeTable:
			// Entry writes are published in ID order.
			if c.Table == session.WriteEntry && numOf(c.Record, "conversationId") == float64(w.conv) {
				appended = append(appended, c.Record)
			}
		case session.ChangeDocument:
			if c.ConversationID != w.conv {
				continue
			}
			kind := strOf(c.Record, "kind")
			if !isMounted(kind) || c.Record.Has("key") {
				continue
			}
			id := session.ID(numOf(c.Record, "id"))
			mounted, isMountedDoc := w.docs[kind]
			path := delta.Path{delta.Key("docs"), delta.Key(kind)}
			switch {
			case c.Value == nil:
				if !isMountedDoc || mounted.id != id {
					continue
				}
				delete(w.docs, kind)
				docOps = append(docOps, delta.Op{Verb: delta.Delete, Path: path})
			case isMountedDoc && mounted.id == id && mounted.version == c.Version:
				for _, op := range c.Ops {
					docOps = append(docOps, prefixed(op, kind))
				}
			default:
				w.docs[kind] = viewDoc{id: id, version: c.Version}
				docOps = append(docOps, delta.Op{Verb: delta.Set, Path: path, Value: c.Value})
			}
		}
	}
	ops := docOps
	if len(appended) == 0 {
		return ops
	}
	ops = append(ops, delta.Op{Verb: delta.Splice, Path: delta.Path{delta.Key("entries")}, Index: len(w.ids), Items: appended})
	for _, record := range appended {
		w.ids = append(w.ids, numOf(record.(*jv.Object), "id"))
	}
	if len(w.ids) > w.max {
		drop := len(w.ids) - w.min
		before := w.ids[drop-1]
		w.ids = append(w.ids[:0], w.ids[drop:]...)
		ops = append(ops,
			delta.Op{Verb: delta.Splice, Path: delta.Path{delta.Key("entries")}, Index: 0, Remove: drop},
			delta.Op{Verb: delta.Set, Path: delta.Path{delta.Key("before")}, Value: before})
	}
	return ops
}

// publishViews emits one frame per open view for a commit that changed it: {watchId, frame: {ops}}. A view frame carries the ops
// alone; the host applies them to the value it holds, because the whole value would be rebuilt and serialized for every commit.
//
// Not inlined: it does work only for an open view, and an inlined copy would be compiled by V8 with its hot caller, publishWatches.
//
//go:noinline
func (h *handler) publishViews(pub *session.Publication) {
	if len(h.views) == 0 {
		return
	}
	ids := make([]uint32, 0, len(h.views))
	for id := range h.views {
		ids = append(ids, id)
	}
	sortU32(ids)
	for _, id := range ids {
		ops := h.views[id].advance(pub)
		if len(ops) == 0 {
			continue
		}
		frame := jv.NewObject()
		frame.Set("ops", delta.OpsValue(ops))
		out := append(make([]byte, 0, 96), `{"watchId":`...)
		out = jv.Append(out, float64(id))
		out = append(out, `,"frame":`...)
		out = jv.Append(out, frame)
		h.b.Notice(abi.NoticePublished, append(out, '}'))
	}
}

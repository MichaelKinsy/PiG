// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"
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
// conversation with one observer. The view is the ConversationView {conversation, entries, docs}: the conversation record, the
// active entries (the head marker, then the visible range's entries without a head) and the mounted built-in documents. The
// watch does not hold it: a frame is the ops of one commit, and the host applies them to the view it holds. The watch keeps
// only what choosing those ops reads, a slim record of each active entry.
type viewWatch struct {
	conv    session.ID
	entries []viewEntry
	docs    map[string]viewDoc
	changes bool // frames also carry the commit's entry, task and submission records of the conversation
}

// viewEntry is what the next head marker reads of an active entry: its id and whether it carries a head.
type viewEntry struct {
	id      float64
	hasHead bool
}

func viewEntryOf(e *jv.Object) viewEntry {
	return viewEntry{id: numOf(e, "id"), hasHead: e.Has("head")}
}

type viewDoc struct {
	id      session.ID
	version int
}

// mountedDefs are the built-in documents a view mounts, in view.ts MOUNTED order.
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
	conv := session.ID(numOf(target, "conversationId"))
	record, err := h.mir.Conversation(conv)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, errors.New("Conversation " + itoa(int64(conv)) + " does not exist")
	}
	entries := []any{}
	slim := make([]viewEntry, 0, 16)
	view, err := h.contextView(int64(conv), 0)
	if err != nil {
		return nil, err
	}
	h.chargeViewBuild(view.NumEntries())
	for i := range view.NumEntries() {
		e, err := jv.Parse(view.EntryRecord(i))
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
		slim = append(slim, viewEntryOf(e.(*jv.Object)))
	}
	docObject := jv.NewObject()
	mounted := map[string]viewDoc{}
	for _, def := range h.mountedDefs() {
		args := session.DocArgs{Owner: conv}
		value, err := h.sess.Snapshot(def, args)
		if err != nil {
			return nil, err
		}
		h.chargeViewDoc()
		if value == nil {
			continue
		}
		rec, err := h.mir.FindDocument(session.DocumentAddress(def, args), session.Current)
		if err != nil {
			return nil, err
		}
		docObject.Set(def.Kind, value)
		mounted[def.Kind] = viewDoc{id: session.ID(numOf(rec, "id")), version: def.Version}
	}
	h.job.change += hopcost.ViewBuildReturn
	value := jv.NewObject()
	value.Set("conversation", record)
	value.Set("entries", entries)
	value.Set("docs", docObject)
	if h.views == nil {
		h.views = map[uint32]*viewWatch{}
	}
	changes, _ := boolOf(target, "changes")
	h.views[uint32(numOf(req, "watchId"))] = &viewWatch{conv: conv, entries: slim, docs: mounted, changes: changes}
	answer := jv.NewObject()
	answer.Set("value", value)
	if want, _ := boolOf(target, "completing"); want {
		if err := h.setCompleting(answer, conv); err != nil {
			return nil, err
		}
	}
	return answer, nil
}

// viewAttach answers api op view.attach {conversationId, completing?}: an observer joining a view another observer holds. Pi's attach
// finds the mount on the Session line and only creates the observer (view.ts :140-150), which for watchEvents reads the generations in
// `completing` (events.ts :148-150); the answer carries their ids.
func (h *handler) viewAttach(req *jv.Object) (any, error) {
	answer := jv.NewObject()
	if want, _ := boolOf(req, "completing"); want {
		if err := h.setCompleting(answer, session.ID(numOf(req, "conversationId"))); err != nil {
			return nil, err
		}
	}
	// Pi's attach awaits `create` (view.ts :146), one tick of the job's body that this host spends in the facade's longer chain
	// after the answer (an observer of a mount the core just built is exact without it, hopcost.ViewBuildReturn): the job takes
	// the one tick off hopcost.ReadOnLine's floor for a job that awaits nothing.
	h.job.direct = true
	h.job.change++
	return answer, nil
}

// chargeViewBuild charges the Storage reads of ConversationViews #build before its documents (view.ts :156-161): the conversation,
// captureContextBounds (context.ts :23-40: a limit-1 scan, and for a conversation with entries the head marker) and activeEntries
// (a scanRange page per SCAN_PAGE_SIZE entries), with the ticks of the async returns between them.
func (h *handler) chargeViewBuild(active int) {
	h.read(hopcost.ReadRow)
	h.read(hopcost.ReadScanEntries)
	if active == 0 {
		// captureContextBounds returns undefined and activeEntries returns [] without a read.
		h.job.change += hopcost.ViewEmptyBounds
		return
	}
	h.read(hopcost.ReadHeadMarker)
	h.job.change += hopcost.ViewBoundsReturn
	for range max(1, (active+viewScanPage-1)/viewScanPage) {
		h.read(hopcost.ReadScanEntries)
	}
	h.job.change += hopcost.ViewActiveReturn
}

// viewScanPage is context.ts SCAN_PAGE_SIZE.
const viewScanPage = 256

// chargeViewDoc charges the ticks of `await session.conversationDocumentOnLine(token, id)` (session.ts :121-133) beyond its Storage
// reads: the Session's loadDocument charges findDocument and document() itself, through the adapter, for a document it has not
// cached; a cached one answers in the two returns alone.
func (h *handler) chargeViewDoc() { h.job.change += hopcost.ViewDocReturn }

// setCompleting sets answer.completing to the scanAll of watchEvents' create callback (events.ts :148-150), a read on the same line
// job as the view: the IDs of the conversation's generation tasks in `completing`, in ID order.
func (h *handler) setCompleting(answer *jv.Object, conv session.ID) error {
	ids := []any{}
	var after session.ID
	for {
		h.read(hopcost.ReadRow)
		page, err := h.mir.ScanTasks(session.TaskQuery{ConversationID: conv, Kind: "pi.generation", Status: "completing"}, viewTaskPage, after)
		if err != nil {
			return err
		}
		for _, t := range page.Items {
			ids = append(ids, numOf(t, "id"))
		}
		if page.After == 0 {
			answer.Set("completing", ids)
			return nil
		}
		after = page.After
	}
}

// viewTaskPage is the page size of that scan.
const viewTaskPage = 100

// prefixed is view.ts prefixed(): a document op relocated under ["docs", kind].
func prefixed(op delta.Op, kind string) delta.Op {
	base := delta.Path{delta.Key("docs"), delta.Key(kind)}
	if op.Verb == delta.Replace {
		return delta.Op{Verb: delta.Set, Path: base, Value: op.Value}
	}
	op.Path = append(append(delta.Path{}, base...), op.Path...)
	return op
}

// advance is view.ts advance(): the ops one commit makes to the view.
func (w *viewWatch) advance(pub *session.Publication) (ops []delta.Op, changes *jv.Object) {
	var docOps, entryOps []delta.Op
	var entryRecords, taskRecords, submissionRecords, nestedResults []any
	for _, c := range pub.Changes {
		switch c.Kind {
		case session.ChangeTable:
			if numOf(c.Record, "conversationId") != float64(w.conv) {
				continue
			}
			switch c.Table {
			case session.WriteEntry:
				entryRecords = append(entryRecords, c.Record)
				value := any(c.Record)
				head, hasHead := c.Record.Get("head")
				if !hasHead || head == nil {
					entryOps = append(entryOps, delta.Op{Verb: delta.Splice, Path: delta.Path{delta.Key("entries")}, Index: len(w.entries), Items: []any{value}})
					w.entries = append(w.entries, viewEntryOf(c.Record))
					continue
				}
				target, _ := head.(float64)
				kept := len(w.entries)
				for i, e := range w.entries {
					if !e.hasHead && e.id >= target {
						kept = i
						break
					}
				}
				entryOps = append(entryOps, delta.Op{Verb: delta.Splice, Path: delta.Path{delta.Key("entries")}, Index: 0, Remove: kept, Items: []any{value}})
				w.entries = slices.Replace(w.entries, 0, kept, viewEntryOf(c.Record))
			case session.WriteTask:
				taskRecords = append(taskRecords, c.Record)
			case session.WriteSubmission:
				submissionRecords = append(submissionRecords, c.Record)
			}
		case session.ChangeDocument:
			if c.ConversationID != w.conv {
				continue
			}
			kind := strOf(c.Record, "kind")
			// A nested call's result, read now: its caller's documents may retire in the next commit (events.ts nestedResults).
			if kind == "pi.tool.nested-call" && c.Value != nil && c.Record.Has("key") {
				nestedResults = append(nestedResults, jobj("key", strOf(c.Record, "key"), "value", c.Value))
				continue
			}
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
	ops = append(ops, docOps...)
	ops = append(ops, entryOps...)
	if w.changes {
		changes = jv.NewObject()
		changes.Set("entries", orEmpty(entryRecords))
		changes.Set("tasks", orEmpty(taskRecords))
		changes.Set("submissions", orEmpty(submissionRecords))
		if len(nestedResults) > 0 {
			changes.Set("nestedResults", nestedResults)
		}
	}
	return ops, changes
}

func orEmpty(list []any) []any {
	if list == nil {
		return []any{}
	}
	return list
}

// publishViews emits one frame per open view for a commit that changed it (or, with changes, that touched its conversation's
// tables): {watchId, frame: {ops, changes?}}. A view frame carries the ops alone; the host applies them to the value it
// holds, because the whole value would be rebuilt and serialized for every commit.
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
		w := h.views[id]
		ops, changes := w.advance(pub)
		touched := len(ops) > 0 || (changes != nil && (changes.Len() > 0 && !emptyChanges(changes)))
		if !touched {
			continue
		}
		frame := jv.NewObject()
		frame.Set("ops", delta.OpsValue(ops))
		if changes != nil {
			frame.Set("changes", changes)
		}
		out := append(make([]byte, 0, 96), `{"watchId":`...)
		out = jv.Append(out, float64(id))
		out = append(out, `,"frame":`...)
		out = jv.Append(out, frame)
		h.b.Notice(abi.NoticePublished, append(out, '}'))
	}
}

func emptyChanges(c *jv.Object) bool {
	for _, k := range c.Keys() {
		v, _ := c.Get(k)
		if l, _ := v.([]any); len(l) != 0 {
			return false
		}
	}
	return true
}

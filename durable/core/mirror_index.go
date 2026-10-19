// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"cmp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The mirror's record tables are ID-sorted columns, not Go maps. TinyGo allocates map buckets without a pointer layout and
// scans them conservatively, so the record IDs and commit sequences a map holds read as addresses: they keep unrelated
// heap objects alive and make every collection walk to the head of each object they hit. A column of IDs or sequences
// is pointer-free and never scanned; a column of records holds only real pointers. Lookups are binary searches over
// the ID column; records are minted in ascending ID order, so an insert is almost always an append.

// convTable holds the conversation records, and the ascending IDs of the conversations each task and each conversation
// owns, so the scan of one owner's conversations reads only those.
type convTable struct {
	ids       []session.ID
	recs      []*jv.Object
	byTask    map[session.ID][]session.ID
	byConvOwn map[session.ID][]session.ID
}

func (t *convTable) get(id session.ID) *jv.Object {
	if i, ok := slices.BinarySearch(t.ids, id); ok {
		return t.recs[i]
	}
	return nil
}

func (t *convTable) has(id session.ID) bool {
	_, ok := slices.BinarySearch(t.ids, id)
	return ok
}

// ownerOf is the task and conversation that own a conversation record; 0 for none.
func ownerOf(rec *jv.Object) (task, conv session.ID) {
	if o := sub(rec, "owner"); o != nil {
		return num(o, "taskId"), num(o, "conversationId")
	}
	return 0, 0
}

func (t *convTable) put(id session.ID, rec *jv.Object) {
	i, ok := slices.BinarySearch(t.ids, id)
	if ok {
		oldTask, oldConv := ownerOf(t.recs[i])
		newTask, newConv := ownerOf(rec)
		if oldTask != newTask {
			t.unindex(t.byTask, oldTask, id)
			t.index(&t.byTask, newTask, id)
		}
		if oldConv != newConv {
			t.unindex(t.byConvOwn, oldConv, id)
			t.index(&t.byConvOwn, newConv, id)
		}
		t.recs[i] = rec
		return
	}
	t.ids = slices.Insert(t.ids, i, id)
	t.recs = slices.Insert(t.recs, i, rec)
	task, conv := ownerOf(rec)
	t.index(&t.byTask, task, id)
	t.index(&t.byConvOwn, conv, id)
}

func (t *convTable) index(m *map[session.ID][]session.ID, owner, id session.ID) {
	if owner == 0 {
		return
	}
	if *m == nil {
		*m = map[session.ID][]session.ID{}
	}
	(*m)[owner] = insertSorted((*m)[owner], id)
}

func (t *convTable) unindex(m map[session.ID][]session.ID, owner, id session.ID) {
	if at, ok := slices.BinarySearch(m[owner], id); ok && owner != 0 {
		m[owner] = slices.Delete(m[owner], at, at+1)
	}
}

// entryTable holds the stored record text and commit sequence of every entry the mirror holds (all of them, or with a
// host read the ones this handle wrote: mirror_lazy.go); a record is parsed when a read asks for it.
type entryTable struct {
	ids  []session.ID
	seqs []session.Seq
	raws [][]byte
	// byConv is each conversation's entry IDs and head-marker entry IDs, ascending, sorted by conversation.
	byConv []convEntries
	// bytes is the record text the table holds.
	bytes int
}

type convEntries struct {
	conv         session.ID
	ids, headIDs []session.ID
}

func (t *entryTable) at(id session.ID) (int, bool) { return slices.BinarySearch(t.ids, id) }

func (t *entryTable) has(id session.ID) bool {
	_, ok := t.at(id)
	return ok
}

// raw is the stored record text of entry id.
func (t *entryTable) raw(id session.ID) ([]byte, bool) {
	if i, ok := t.at(id); ok {
		return t.raws[i], true
	}
	return nil, false
}

// conv is the entry index of conversation c; nil when it has no entries. create adds an empty one.
func (t *entryTable) conv(c session.ID, create bool) *convEntries {
	i, ok := slices.BinarySearchFunc(t.byConv, c, func(e convEntries, c session.ID) int { return cmpID(e.conv, c) })
	if ok {
		return &t.byConv[i]
	}
	if !create {
		return nil
	}
	t.byConv = slices.Insert(t.byConv, i, convEntries{conv: c})
	return &t.byConv[i]
}

// held are the IDs of conversation c's entries the mirror holds and of its head markers, ascending.
func (t *entryTable) held(c session.ID) (ids, heads []session.ID) {
	if e := t.conv(c, false); e != nil {
		return e.ids, e.headIDs
	}
	return nil, nil
}

func (t *entryTable) put(id, conv session.ID, seq session.Seq, head bool, raw []byte) {
	i, ok := t.at(id)
	if ok {
		t.seqs[i], t.raws[i] = seq, raw
	} else {
		t.ids = slices.Insert(t.ids, i, id)
		t.seqs = slices.Insert(t.seqs, i, seq)
		t.raws = slices.Insert(t.raws, i, raw)
	}
	t.bytes += len(raw)
	e := t.conv(conv, true)
	e.ids = insertSorted(e.ids, id)
	if head {
		e.headIDs = insertSorted(e.headIDs, id)
	}
}

// taskTable holds the tasks: each one's scan columns, its parsed record while it is live or after a read parsed it, and
// a terminal task's stored text until then. terminal counts the terminal heads, so the step that asks whether a turn is
// over (releaseApplied) costs the same with 20,000 tasks held as with one.
type taskTable struct {
	ids      []session.ID
	heads    []taskHead
	recs     []*jv.Object
	raws     [][]byte
	terminal int
}

func (t *taskTable) at(id session.ID) (int, bool) { return slices.BinarySearch(t.ids, id) }

func (t *taskTable) has(id session.ID) bool {
	_, ok := t.at(id)
	return ok
}

// slot is the position of task id, inserting an empty row when the table has none.
func (t *taskTable) slot(id session.ID) int {
	i, ok := t.at(id)
	if !ok {
		t.ids = slices.Insert(t.ids, i, id)
		t.heads = slices.Insert(t.heads, i, taskHead{})
		t.recs = slices.Insert(t.recs, i, nil)
		t.raws = slices.Insert(t.raws, i, nil)
	}
	return i
}

// setHead sets the head of the task at i, keeping terminal.
func (t *taskTable) setHead(i int, head taskHead) {
	if t.heads[i].status == "terminal" {
		t.terminal--
	}
	if head.status == "terminal" {
		t.terminal++
	}
	t.heads[i] = head
}

// keepLive drops the terminal tasks.
func (t *taskTable) keepLive() {
	var k taskTable
	for i, h := range t.heads {
		if h.status != "terminal" {
			k.ids = append(k.ids, t.ids[i])
			k.heads = append(k.heads, h)
			k.recs = append(k.recs, t.recs[i])
			k.raws = append(k.raws, t.raws[i])
		}
	}
	*t = k
}

// subTable holds the submissions as taskTable holds the tasks, with each one's requestId; settled counts the settled heads.
type subTable struct {
	ids   []session.ID
	heads []subHead
	recs  []*jv.Object
	raws  [][]byte
	// byRequest is the submission of each (conversation, requestId), sorted by both.
	byRequest []requestKey
	settled   int
}

type requestKey struct {
	conv    session.ID
	request string
	id      session.ID
}

func (t *subTable) at(id session.ID) (int, bool) { return slices.BinarySearch(t.ids, id) }

func (t *subTable) has(id session.ID) bool {
	_, ok := t.at(id)
	return ok
}

func (t *subTable) slot(id session.ID) int {
	i, ok := t.at(id)
	if !ok {
		t.ids = slices.Insert(t.ids, i, id)
		t.heads = slices.Insert(t.heads, i, subHead{})
		t.recs = slices.Insert(t.recs, i, nil)
		t.raws = slices.Insert(t.raws, i, nil)
	}
	return i
}

// setHead sets the head of the submission at i, keeping settled.
func (t *subTable) setHead(i int, head subHead) {
	if settledStatus(t.heads[i].status) {
		t.settled--
	}
	if settledStatus(head.status) {
		t.settled++
	}
	t.heads[i] = head
}

// keepLive drops the settled submissions and their request IDs.
func (t *subTable) keepLive() {
	var k subTable
	for i, h := range t.heads {
		if !settledStatus(h.status) {
			k.ids = append(k.ids, t.ids[i])
			k.heads = append(k.heads, h)
			k.recs = append(k.recs, t.recs[i])
			k.raws = append(k.raws, t.raws[i])
		}
	}
	for _, r := range t.byRequest {
		if k.has(r.id) {
			k.byRequest = append(k.byRequest, r)
		}
	}
	*t = k
}

func cmpRequest(k requestKey, conv session.ID, request string) int {
	if c := cmpID(k.conv, conv); c != 0 {
		return c
	}
	return strings.Compare(k.request, request)
}

// byRequestOf is the submission conversation conv holds under request.
func (t *subTable) byRequestOf(conv session.ID, request string) (session.ID, bool) {
	i, ok := slices.BinarySearchFunc(t.byRequest, requestKey{conv: conv, request: request}, func(k, want requestKey) int {
		return cmpRequest(k, want.conv, want.request)
	})
	if !ok {
		return 0, false
	}
	return t.byRequest[i].id, true
}

func (t *subTable) setRequest(conv session.ID, request string, id session.ID) {
	want := requestKey{conv: conv, request: request, id: id}
	i, ok := slices.BinarySearchFunc(t.byRequest, want, func(k, want requestKey) int { return cmpRequest(k, want.conv, want.request) })
	if ok {
		t.byRequest[i].id = id
		return
	}
	t.byRequest = slices.Insert(t.byRequest, i, want)
}

// docTable holds the document incarnations.
type docTable struct {
	ids  []session.ID
	docs []*mirrorDoc
}

func (t *docTable) get(id session.ID) *mirrorDoc {
	if i, ok := slices.BinarySearch(t.ids, id); ok {
		return t.docs[i]
	}
	return nil
}

func (t *docTable) put(id session.ID, d *mirrorDoc) {
	i, ok := slices.BinarySearch(t.ids, id)
	if ok {
		t.docs[i] = d
		return
	}
	t.ids = slices.Insert(t.ids, i, id)
	t.docs = slices.Insert(t.docs, i, d)
}

func cmpID(a, b session.ID) int { return cmp.Compare(a, b) }

// recordType is the table that holds record id (Pi's record_ids), or "" when none does. An id the mirror lacks may
// still be a settled record in storage (mirror_lazy.go).
func (m *mirror) recordType(id session.ID) string {
	switch {
	case m.entries.has(id):
		return "entry"
	case m.tasks.has(id):
		return "task"
	case m.subs.has(id):
		return "submission"
	case m.convs.has(id):
		return "conversation"
	case m.docs.get(id) != nil:
		return "document"
	}
	return m.storedType(id)
}

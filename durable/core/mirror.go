// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"bytes"
	"errors"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

type mirrorRevision struct {
	seq     session.Seq
	version int
	base    bool
	value   *jv.Object
	ops     []delta.Op
}

type mirrorDoc struct {
	record    *jv.Object
	revisions []mirrorRevision
}

type addrIndex struct {
	ids     []session.ID
	current session.ID
}

// mirror is an in-memory session.Store holding every committed record, as pi-durable's MemoryStorage does: the
// adapter answers every Session read from it, so no read returns session.NeedRead.
type mirror struct {
	convs   convTable
	entries entryTable
	tasks   taskTable
	subs    subTable
	docs    docTable
	// docAddr and docByScope index the document incarnations by address and by scope; both are keyed by strings.
	docAddr    map[string]*addrIndex
	docByScope map[string][]session.ID
	nextID     session.ID
	nextSeq    session.Seq
	lazy       lazyState
}

// taskHead holds what a task scan filters on, so a scan parses only the records it returns.
type taskHead struct {
	conv         session.ID
	kind, status string
	abort, bg    bool
}

// subHead holds what a submission scan filters on.
type subHead struct {
	conv   session.ID
	status string
}

func flagOf(o *jv.Object, k string) bool {
	v, _ := o.Get(k)
	b, _ := v.(bool)
	return b
}

func headOfTask(o *jv.Object) taskHead {
	return taskHead{conv: num(o, "conversationId"), kind: str(o, "kind"), status: str(sub(o, "state"), "status"), abort: flagOf(o, "abortRequested"), bg: flagOf(o, "background")}
}

func headOfSubmission(o *jv.Object) subHead {
	return subHead{conv: num(o, "conversationId"), status: str(o, "status")}
}

// parseRaw parses a stored record; a record that does not parse is absent.
func parseRaw(raw []byte) *jv.Object {
	v, err := jv.Parse(raw)
	if err != nil {
		return nil
	}
	o, _ := v.(*jv.Object)
	return o
}

// entry parses the stored record of entry id; nil when the mirror has none.
func (m *mirror) entry(id session.ID) *jv.Object {
	raw, _, ok := m.entryRaw(id)
	if !ok {
		return nil
	}
	return parseRaw(raw)
}

// task is the record of task id: the live one, or a terminal one parsed on its first read.
func (m *mirror) task(id session.ID) *jv.Object {
	i, ok := m.tasks.at(id)
	if !ok {
		if i, ok = m.loadTask(id); !ok {
			return nil
		}
	}
	if t := m.tasks.recs[i]; t != nil {
		return t
	}
	if m.tasks.raws[i] == nil {
		return nil
	}
	t := parseRaw(m.tasks.raws[i])
	m.tasks.recs[i] = t
	return t
}

// submission is the record of submission id: a live one, or a settled one parsed on its first read.
func (m *mirror) submission(id session.ID) *jv.Object {
	i, ok := m.subs.at(id)
	if !ok {
		if i, ok = m.loadSubmission(id); !ok {
			return nil
		}
	}
	if s := m.subs.recs[i]; s != nil {
		return s
	}
	if m.subs.raws[i] == nil {
		return nil
	}
	s := parseRaw(m.subs.raws[i])
	m.subs.recs[i] = s
	return s
}

// putTask records task id as stored: a terminal task keeps its text until a read parses it.
func (m *mirror) putTask(id session.ID, head taskHead, rec *jv.Object, raw []byte) int {
	i := m.tasks.slot(id)
	m.tasks.setHead(i, head)
	if head.status == "terminal" {
		if raw == nil {
			raw = jv.Append(nil, rec)
		}
		m.tasks.raws[i], m.tasks.recs[i] = raw, nil
	} else {
		if rec == nil {
			rec = parseRaw(raw)
		}
		m.tasks.recs[i], m.tasks.raws[i] = rec, nil
	}
	return i
}

// settledStatus reports a settled submission status: one a later commit does not change.
func settledStatus(status string) bool { return status == "done" || status == "unanswered" }

// putSubmission records submission id as stored: a settled submission keeps its text until a read parses it.
func (m *mirror) putSubmission(id session.ID, head subHead, rec *jv.Object, raw []byte) int {
	i := m.subs.slot(id)
	m.subs.setHead(i, head)
	if settledStatus(head.status) {
		if raw == nil {
			raw = jv.Append(nil, rec)
		}
		m.subs.raws[i], m.subs.recs[i] = raw, nil
	} else {
		if rec == nil {
			rec = parseRaw(raw)
		}
		m.subs.recs[i], m.subs.raws[i] = rec, nil
	}
	return i
}

// newMirror returns an empty mirror: the next ID is 2 (1 is the root conversation) and the next sequence 1.
func newMirror() *mirror {
	return &mirror{docAddr: map[string]*addrIndex{}, docByScope: map[string][]session.ID{}, nextID: 2, nextSeq: 1}
}

// NextID is the next unminted ID the store has seen.
func (m *mirror) NextID() session.ID { return m.nextID }

// NextSeq is the sequence the next commit takes.
func (m *mirror) NextSeq() session.Seq { return m.nextSeq }

func insertSorted(ids []session.ID, id session.ID) []session.ID {
	at, found := slices.BinarySearch(ids, id)
	if found {
		return ids
	}
	return slices.Insert(ids, at, id)
}

// firstAbove is the index of the first ID above v (sort.Search over sorted IDs).
func firstAbove(ids []session.ID, v session.ID) int {
	at, found := slices.BinarySearch(ids, v)
	if found {
		at++
	}
	return at
}

func num(o *jv.Object, k string) session.ID {
	v, _ := o.Get(k)
	f, _ := v.(float64)
	return session.ID(f)
}

func str(o *jv.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func sub(o *jv.Object, k string) *jv.Object {
	v, _ := o.Get(k)
	r, _ := v.(*jv.Object)
	return r
}

func f(id session.ID) float64 { return float64(id) }

// appendScopeKey appends the JSON text of a scope's key: ["conversation",id], ["task",id] or ["session"].
func appendScopeKey(dst []byte, scope *jv.Object) []byte {
	switch str(scope, "kind") {
	case "conversation":
		dst = append(dst, `["conversation",`...)
		return append(jv.AppendNumber(dst, f(num(scope, "conversationId"))), ']')
	case "task":
		dst = append(dst, `["task",`...)
		return append(jv.AppendNumber(dst, f(num(scope, "taskId"))), ']')
	}
	return append(dst, `["session"]`...)
}

func scopeKey(scope *jv.Object) string {
	var buf [48]byte
	return string(appendScopeKey(buf[:0], scope))
}

// recordAddrKey is JSON.stringify([kind, scopeKey, ["singleton"] or ["family", key]]), built in place: it runs for every
// document a commit touches, and the arrays it used to stringify were most of its allocations.
func recordAddrKey(rec *jv.Object) string {
	var scope [48]byte
	var buf [128]byte
	b := append(buf[:0], '[')
	b = jv.AppendString(b, str(rec, "kind"))
	b = append(b, ',')
	b = jv.AppendString(b, string(appendScopeKey(scope[:0], sub(rec, "scope"))))
	if k, ok := rec.Get("key"); ok {
		b = append(b, `,["family",`...)
		b = jv.Append(b, k)
		b = append(b, `]]`...)
	} else {
		b = append(b, `,["singleton"]]`...)
	}
	return string(b)
}

func addrKey(a session.Address) string {
	rec := jv.NewObject()
	rec.Set("kind", a.Kind)
	if a.Family {
		rec.Set("key", a.Key)
	}
	scope := jv.NewObject()
	scope.Set("kind", string(a.Scope.Kind))
	switch a.Scope.Kind {
	case session.ScopeConversation:
		scope.Set("conversationId", f(a.Scope.Owner))
	case session.ScopeTask:
		scope.Set("taskId", f(a.Scope.Owner))
	}
	rec.Set("scope", scope)
	return recordAddrKey(rec)
}

func aliveAt(rec *jv.Object, at session.Point) bool {
	createdAt, retired := num(rec, "createdAt"), rec.Has("retiredAt")
	if at.Current {
		return !retired
	}
	return createdAt <= at.Seq && (!retired || at.Seq < num(rec, "retiredAt"))
}

func currentOnly(rec *jv.Object) bool {
	return str(sub(rec, "scope"), "kind") != "conversation" || str(rec, "history") == "latest"
}

// Conversation implements session.Store.
func (m *mirror) Conversation(id session.ID) (*jv.Object, error) { return m.convs.get(id), nil }

// ScanConversations implements session.Store. It walks the conversations of the owner the query names (all of them
// without one) from the page's start, and stops at limit+1 matches.
func (m *mirror) ScanConversations(q session.ConversationQuery, limit int, after session.ID) (session.Page, error) {
	ids := m.convs.ids
	switch {
	case q.OwnerTaskID != 0:
		ids = m.convs.byTask[q.OwnerTaskID]
	case q.OwnerConversationID != 0:
		ids = m.convs.byConvOwn[q.OwnerConversationID]
	}
	// Both owners may be named: the index served one, the other filters.
	match := func(c *jv.Object) bool {
		o := sub(c, "owner")
		return (q.OwnerTaskID == 0 || (o != nil && num(o, "taskId") == q.OwnerTaskID)) && (q.OwnerConversationID == 0 || (o != nil && num(o, "conversationId") == q.OwnerConversationID))
	}
	var out []*jv.Object
	more := false
	take := func(id session.ID) bool {
		c := m.convs.get(id)
		if c == nil || !match(c) {
			return true
		}
		if limit > 0 && len(out) == limit {
			more = true
			return false
		}
		out = append(out, c)
		return true
	}
	if q.Descending {
		start := len(ids)
		if after != 0 {
			start, _ = slices.BinarySearch(ids, after)
		}
		for i := start - 1; i >= 0 && take(ids[i]); i-- {
		}
	} else {
		for i := firstAbove(ids, after); i < len(ids) && take(ids[i]); i++ {
		}
	}
	if more {
		return session.Page{Items: out, After: num(out[len(out)-1], "id")}, nil
	}
	return session.Page{Items: out}, nil
}

func pageOf(items []*jv.Object, limit int, after session.ID, descending bool) session.Page {
	if descending {
		slices.Reverse(items)
	}
	if after != 0 {
		keep := items[:0:0]
		for _, it := range items {
			id := num(it, "id")
			if (!descending && id > after) || (descending && id < after) {
				keep = append(keep, it)
			}
		}
		items = keep
	}
	if limit > 0 && len(items) > limit {
		return session.Page{Items: items[:limit], After: num(items[limit-1], "id")}
	}
	return session.Page{Items: items}
}

// Entry implements session.Store.
func (m *mirror) Entry(id session.ID) (*jv.Object, session.Seq, error) {
	raw, seq, ok := m.entryRaw(id)
	if !ok {
		return nil, 0, nil
	}
	return parseRaw(raw), seq, nil
}

// visible walks the entries visible from a conversation, newest first: the
// conversation's own, then each ancestor's up to the fork point.
func (m *mirror) visible(conv, min, max session.ID, want int, fn func(session.ID) bool) {
	m.visibleRows(conv, min, max, 0, want, func(id session.ID, _ []byte) bool { return fn(id) })
}

// visibleRows is visible that also gives fn the stored record of an entry read from storage (entriesDescRows), reading
// pages of first records.
func (m *mirror) visibleRows(conv, min, max session.ID, first, want int, fn func(id session.ID, record []byte) bool) {
	cur, upper := conv, max // upper 0: unbounded
	for {
		if !m.entriesDescRows(cur, min, upper, first, want, fn) {
			return
		}
		p := sub(m.convs.get(cur), "parent")
		if p == nil {
			return
		}
		if pat := num(p, "at"); upper == 0 || pat < upper {
			upper = pat
		}
		if min != 0 && upper < min {
			return
		}
		cur = num(p, "conversationId")
	}
}

// EntryIn implements session.Store.
func (m *mirror) EntryIn(conv, id session.ID) (*jv.Object, session.Seq, error) {
	if !m.convs.has(conv) {
		return nil, 0, session.NewStorageRequestError("Unknown conversation: " + strconv.FormatInt(int64(conv), 10))
	}
	var found session.ID
	m.visible(conv, id, id, 1, func(e session.ID) bool { found = e; return false })
	if found == 0 {
		return nil, 0, nil
	}
	return m.Entry(found)
}

// LatestHeadMarker implements session.Store.
func (m *mirror) LatestHeadMarker(conv, atOrBefore session.ID) (*jv.Object, error) {
	if !m.convs.has(conv) {
		return nil, session.NewStorageRequestError("Unknown conversation: " + strconv.FormatInt(int64(conv), 10))
	}
	cur, upper := conv, atOrBefore
	for {
		b, ok := m.entryBounds(cur, upper)
		if !ok {
			return nil, m.lazy.err
		}
		if b.hasMarker {
			return m.entry(b.markerID), nil
		}
		p := sub(m.convs.get(cur), "parent")
		if p == nil {
			return nil, nil
		}
		if pat := num(p, "at"); upper == 0 || pat < upper {
			upper = pat
		}
		cur = num(p, "conversationId")
	}
}

// ScanEntries implements session.Store.
func (m *mirror) scanEntries(q session.EntryQuery, limit int, after session.ID, raw bool) ([]foundEntry, session.ID, error) {
	if !m.convs.has(q.ConversationID) {
		return nil, 0, session.NewStorageRequestError("Unknown conversation: " + strconv.FormatInt(int64(q.ConversationID), 10))
	}
	// A scan stops at the page: the entries it walks are the page and one more. The records of the stored ones come with the
	// ids, a page at a time, and are parsed as they arrive (their bytes are valid for the read only).
	var all []foundEntry
	add := func(e session.ID, record []byte) foundEntry {
		f := foundEntry{id: e}
		if record != nil {
			if raw {
				f.raw = bytes.Clone(record)
			} else {
				f.rec = parseRaw(record)
			}
		}
		return f
	}
	if q.Ascending {
		// An oldest-first page reads pages of records from the cursor and stops at the page's end and one more.
		m.visibleRowsAsc(q.ConversationID, max(q.MinEntryID, after+1), q.MaxEntryID, func(e session.ID, record []byte) bool {
			all = append(all, add(e, record))
			return limit <= 0 || len(all) <= limit
		})
	} else if after != 1 {
		// A newest-first page after a cursor starts below it: the entries above are not walked again.
		maxID := q.MaxEntryID
		if after != 0 && (maxID == 0 || after-1 < maxID) {
			maxID = after - 1
		}
		first := 0
		if m.lazy.read != nil {
			first = store.PageRows
			if limit > 0 {
				first = limit + 1
			}
		}
		m.visibleRows(q.ConversationID, q.MinEntryID, maxID, first, 0, func(e session.ID, record []byte) bool {
			all = append(all, add(e, record))
			return limit <= 0 || len(all) <= limit
		})
	}
	next := session.ID(0)
	if limit > 0 && len(all) > limit {
		all, next = all[:limit], all[limit-1].id
	}
	return all, next, nil
}

// foundEntry is an entry a scan walked: its id and its record, parsed or as the stored text (nil when the mirror holds it).
type foundEntry struct {
	id  session.ID
	rec *jv.Object
	raw []byte
}

// ScanEntries implements session.Store.
func (m *mirror) ScanEntries(q session.EntryQuery, limit int, after session.ID) (session.Page, error) {
	all, next, err := m.scanEntries(q, limit, after, false)
	if err != nil {
		return session.Page{}, err
	}
	items := make([]*jv.Object, len(all))
	for i, f := range all {
		items[i] = f.rec
		if items[i] == nil {
			items[i] = m.entry(f.id)
		}
	}
	return session.Page{Items: items, After: next}, nil
}

// scanEntriesRaw is ScanEntries with each record as its stored text, not parsed: the page an api answer carries splices the text
// (jv.Raw) where the parsed tree would be written out again. The text belongs to the caller.
func (m *mirror) scanEntriesRaw(q session.EntryQuery, limit int, after session.ID) ([]jv.Raw, session.ID, error) {
	all, next, err := m.scanEntries(q, limit, after, true)
	if err != nil {
		return nil, 0, err
	}
	items := make([]jv.Raw, len(all))
	for i, f := range all {
		items[i] = f.raw
		if items[i] == nil {
			if raw, _, ok := m.entryRaw(f.id); ok {
				items[i] = bytes.Clone(raw)
			} else {
				items[i] = jv.Raw("null")
			}
		}
	}
	return items, next, nil
}

// Task implements session.Store.
func (m *mirror) Task(id session.ID) (*jv.Object, error) { return m.task(id), nil }

// ScanTasks implements session.Store. It reads storage only a page at a time and stops at the first limit+1 matches, and
// parses only the records it returns, so a page costs what it returns, not the number of tasks.
func (m *mirror) ScanTasks(q session.TaskQuery, limit int, after session.ID) (session.Page, error) {
	match := func(h *taskHead) bool {
		return (q.Status == "" || h.status == q.Status) && (q.ConversationID == 0 || h.conv == q.ConversationID) && (q.Kind == "" || h.kind == q.Kind) &&
			(q.AbortRequested == nil || h.abort == *q.AbortRequested) && (q.Background == nil || h.bg == *q.Background)
	}
	var out []*jv.Object
	more := false
	// take offers a matching task; it reports whether the scan goes on.
	take := func(rec func() *jv.Object) bool {
		if limit > 0 && len(out) == limit {
			more = true
			return false
		}
		if r := rec(); r != nil {
			out = append(out, r)
		}
		return true
	}
	// The mirror holds the live tasks and the terminal ones this handle wrote; the rest are in storage when the scan can
	// return terminal tasks.
	stored := m.lazy.read != nil && (q.Status == "" || q.Status == "terminal")
	m.scanTaskRows(stored, q.Descending, after, func(id session.ID, held bool, row []abi.Value) bool {
		if held {
			if i, _ := m.tasks.at(id); match(&m.tasks.heads[i]) {
				return take(func() *jv.Object { return m.task(id) })
			}
			return true
		}
		h := taskHead{conv: int64(row[1].Number()), kind: unquoteColumn(row[2].Bytes), status: string(row[3].Bytes), abort: row[4].Number() != 0, bg: row[5].Number() != 0}
		if match(&h) {
			return take(func() *jv.Object { return parseRaw(row[6].Bytes) })
		}
		return true
	})
	if more {
		return session.Page{Items: out, After: num(out[len(out)-1], "id")}, nil
	}
	return session.Page{Items: out}, nil
}

// Submission implements session.Store.
func (m *mirror) Submission(id session.ID) (*jv.Object, error) { return m.submission(id), nil }

// ScanSubmissions implements session.Store.
func (m *mirror) ScanSubmissions(q session.SubmissionQuery, limit int, after session.ID) (session.Page, error) {
	if q.Status != "queued" && q.Status != "placed" {
		m.loadAllSubmissions()
	}
	var out []*jv.Object
	for i, id := range m.subs.ids {
		h := &m.subs.heads[i]
		if (q.Status != "" && h.status != q.Status) || (q.ConversationID != 0 && h.conv != q.ConversationID) {
			continue
		}
		out = append(out, m.submission(id))
	}
	return pageOf(out, limit, after, q.Descending), nil
}

// SubmissionByRequest implements session.Store.
func (m *mirror) SubmissionByRequest(conv session.ID, request string) (*jv.Object, error) {
	if id, ok := m.subs.byRequestOf(conv, request); ok {
		return m.submission(id), nil
	}
	if id, ok := m.loadSubmissionByRequest(conv, request); ok {
		return m.submission(id), nil
	}
	return nil, nil
}

// FindDocument implements session.Store.
func (m *mirror) FindDocument(a session.Address, at session.Point) (*jv.Object, error) {
	idx := m.docAddr[addrKey(a)]
	if idx == nil {
		return nil, nil
	}
	if at.Current {
		if idx.current == 0 {
			return nil, nil
		}
		return m.docs.get(idx.current).record, nil
	}
	for _, id := range idx.ids {
		if r := m.docs.get(id).record; aliveAt(r, at) {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mirror) materialize(id session.ID, at session.Point) (*session.StoredDoc, error) {
	d := m.docs.get(id)
	if d == nil {
		return nil, nil
	}
	if !at.Current && currentOnly(d.record) {
		return nil, session.NewStorageRequestError("Document " + strconv.FormatInt(int64(id), 10) + " does not retain historical content")
	}
	if !aliveAt(d.record, at) {
		return nil, nil
	}
	revs := d.revisions
	if !at.Current {
		revs = nil
		for _, r := range d.revisions {
			if r.seq <= at.Seq {
				revs = append(revs, r)
			}
		}
	}
	baseAt := len(revs) - 1
	for baseAt >= 0 && !revs[baseAt].base {
		baseAt--
	}
	if baseAt < 0 {
		return nil, errors.New("Document " + strconv.FormatInt(int64(id), 10) + " is missing a required base")
	}
	base := revs[baseAt]
	var value any = base.value
	var batch []delta.Op
	for _, r := range revs[baseAt+1:] {
		if r.base || r.version != base.version {
			return nil, errors.New("Document " + strconv.FormatInt(int64(id), 10) + " crosses a stored version boundary without a base")
		}
		batch = append(batch, r.ops...)
	}
	if len(batch) > 0 {
		var err error
		if value, err = delta.Apply(value, batch); err != nil {
			return nil, err
		}
	}
	return &session.StoredDoc{Record: d.record, Version: base.version, Value: value.(*jv.Object), DeltasSinceBase: len(revs) - baseAt - 1}, nil
}

// Document implements session.Store.
func (m *mirror) Document(id session.ID, at session.Point) (*session.StoredDoc, error) {
	return m.materialize(id, at)
}

// ScanDocuments implements session.Store.
func (m *mirror) ScanDocuments(scope session.Scope, at session.Point, kind string, limit int, after session.ID) (session.Page, error) {
	sc := jv.NewObject()
	sc.Set("kind", string(scope.Kind))
	switch scope.Kind {
	case session.ScopeConversation:
		sc.Set("conversationId", f(scope.Owner))
	case session.ScopeTask:
		sc.Set("taskId", f(scope.Owner))
	}
	ids := m.docByScope[scopeKey(sc)]
	var out []*jv.Object
	for _, id := range ids {
		if id <= after {
			continue
		}
		r := m.docs.get(id).record
		if (kind != "" && str(r, "kind") != kind) || !aliveAt(r, at) {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) > limit {
			break
		}
	}
	if limit > 0 && len(out) > limit {
		return session.Page{Items: out[:limit], After: num(out[limit-1], "id")}, nil
	}
	return session.Page{Items: out}, nil
}

type mirrorAction struct {
	create  *jv.Object
	content *session.Write
	retire  bool
}

// Commit applies a commit as session.Store does. staged is the store commit the same writes were staged in (stageWrite);
// the mirror keeps the record text its statement binds (store.Tx.RecordText) as an entry's, terminal task's or settled
// submission's stored text instead of writing the record out a second time. With staged nil, a text is written from the
// record.
//
//go:noinline
func (m *mirror) Commit(writes []session.Write, nextID session.ID, staged *store.Tx) (session.Seq, error) {
	seq := m.nextSeq
	writes = m.resolveCopies(writes)
	if err := m.checkGlobalIDs(writes); err != nil {
		return 0, err
	}
	actions, order, err := m.prepareDocActions(writes)
	if err != nil {
		return 0, err
	}
	if err := m.checkDocActions(actions, order); err != nil {
		return 0, err
	}
	m.nextID = max(m.nextID, nextID)
	row := 0 // the table write's index among staged's, which hold one per conversation, entry, task or submission write
	for _, w := range writes {
		if w.Kind > session.WriteSubmission {
			continue // documents: applyDocActions
		}
		var text []byte
		if staged != nil {
			text = staged.RecordText(row)
		}
		row++
		id := num(w.Record, "id")
		switch w.Kind {
		case session.WriteConversation:
			m.convs.put(id, w.Record)
		case session.WriteEntry:
			if text == nil {
				text = jv.Append(nil, w.Record)
			}
			m.entries.put(id, num(w.Record, "conversationId"), seq, w.Record.Has("head"), text)
		case session.WriteTask:
			m.putTask(id, headOfTask(w.Record), w.Record, text)
		case session.WriteSubmission:
			m.putSubmission(id, headOfSubmission(w.Record), w.Record, text)
			if r, ok := w.Record.Get("requestId"); ok {
				m.subs.setRequest(num(w.Record, "conversationId"), r.(string), id)
			}
		}
		if id >= m.nextID {
			m.nextID = id + 1
		}
	}
	m.applyDocActions(actions, order, seq)
	m.nextSeq = seq + 1
	return seq, nil
}

//go:noinline
func (m *mirror) resolveCopies(writes []session.Write) []session.Write {
	has := false
	for _, w := range writes {
		if w.Kind == session.WriteDocCopy {
			has = true
		}
	}
	if !has {
		return writes
	}
	out := make([]session.Write, len(writes))
	for i, w := range writes {
		out[i] = w
		if w.Kind != session.WriteDocCopy {
			continue
		}
		stored, err := m.materialize(w.Source.ID, w.Source.At)
		if err != nil || stored == nil {
			// Surface as a rejected commit through an invalid create; checkDocActions reports it.
			out[i] = session.Write{Kind: session.WriteDocCreate, Record: w.Record, Value: nil, Base: true}
			continue
		}
		out[i] = session.Write{Kind: session.WriteDocCreate, Record: w.Record, Version: stored.Version, Base: true, Value: stored.Value}
	}
	return out
}

// writeTable is the table a record write lands in ("" for a kind that writes no record). A switch, not a map literal: the literal
// builds a hash map (heap blocks for its buckets) on every call.
func writeTable(k session.WriteKind) string {
	switch k {
	case session.WriteConversation:
		return "conversation"
	case session.WriteEntry:
		return "entry"
	case session.WriteTask:
		return "task"
	case session.WriteSubmission:
		return "submission"
	case session.WriteDocCreate, session.WriteDocCopy:
		return "document"
	}
	return ""
}

//go:noinline
func (m *mirror) checkGlobalIDs(writes []session.Write) error {
	claimed := map[session.ID]string{}
	for _, w := range writes {
		if w.Kind == session.WriteDocChange || w.Kind == session.WriteDocRetire {
			continue
		}
		table := writeTable(w.Kind)
		id := num(w.Record, "id")
		existing, earlier := m.recordType(id), claimed[id]
		if table == "conversation" || table == "entry" || table == "document" {
			if existing != "" {
				return errors.New("ID " + strconv.FormatInt(int64(id), 10) + " already belongs to " + existing)
			}
			if earlier != "" {
				return errors.New("ID " + strconv.FormatInt(int64(id), 10) + " is written more than once")
			}
			if table == "entry" && id < m.storedBelow() {
				// The stored-entry bound (mirror_lazy.go releaseApplied) relies on each commit writing higher IDs than the
				// commits before it.
				return errors.New("core: entry ID " + strconv.FormatInt(int64(id), 10) + " is below the stored-entry bound")
			}
		} else {
			if existing != "" && existing != table {
				return errors.New("ID " + strconv.FormatInt(int64(id), 10) + " already belongs to " + existing)
			}
			if earlier != "" && earlier != table {
				return errors.New("ID " + strconv.FormatInt(int64(id), 10) + " is written as two record types")
			}
		}
		claimed[id] = table
	}
	return nil
}

//go:noinline
func (m *mirror) prepareDocActions(writes []session.Write) (map[session.ID]*mirrorAction, []session.ID, error) {
	// Most commits write no document: the map is made at the first document write, and a commit without one gets nil (lookups
	// and ranges over a nil map are empty, and `order` is empty too).
	var actions map[session.ID]*mirrorAction
	var order []session.ID
	for _, w := range writes {
		var id session.ID
		switch w.Kind {
		case session.WriteDocCreate:
			id = num(w.Record, "id")
		case session.WriteDocChange, session.WriteDocRetire:
			id = w.ID
		default:
			continue
		}
		a := actions[id]
		if a == nil {
			a = &mirrorAction{}
			if actions == nil {
				actions = map[session.ID]*mirrorAction{}
			}
			actions[id] = a
			order = append(order, id)
		}
		switch w.Kind {
		case session.WriteDocCreate:
			if a.create != nil || a.content != nil {
				return nil, nil, errors.New("Document " + strconv.FormatInt(int64(id), 10) + " has more than one content command")
			}
			cw := w
			a.create, a.content = w.Record, &cw
		case session.WriteDocChange:
			if a.content != nil {
				return nil, nil, errors.New("Document " + strconv.FormatInt(int64(id), 10) + " has more than one content command")
			}
			cw := w
			a.content = &cw
		case session.WriteDocRetire:
			if a.retire {
				return nil, nil, errors.New("Document " + strconv.FormatInt(int64(id), 10) + " is retired more than once")
			}
			a.retire = true
		}
	}
	return actions, order, nil
}

//go:noinline
func (m *mirror) checkDocActions(actions map[session.ID]*mirrorAction, order []session.ID) error {
	if len(order) == 0 {
		return nil
	}
	live := map[string]int{}
	for _, id := range order {
		a := actions[id]
		existing := m.docs.get(id)
		if a.create == nil && existing == nil {
			return errors.New("Unknown document: " + strconv.FormatInt(int64(id), 10))
		}
		if a.create != nil && existing != nil {
			return errors.New("Document " + strconv.FormatInt(int64(id), 10) + " already exists")
		}
		if existing != nil && existing.record.Has("retiredAt") {
			return errors.New("Document " + strconv.FormatInt(int64(id), 10) + " is retired")
		}
		if a.content != nil && a.create != nil && a.content.Value == nil {
			return errors.New("Fork source document cannot be read")
		}
		if a.content != nil && !a.content.Base {
			if existing == nil || len(existing.revisions) == 0 {
				return errors.New("Document " + strconv.FormatInt(int64(id), 10) + " delta has no base")
			}
			if prev := existing.revisions[len(existing.revisions)-1]; prev.version != a.content.Version {
				return errors.New("Document " + strconv.FormatInt(int64(id), 10) + " version transition requires a base")
			}
		}
		var key string
		if a.create != nil {
			key = recordAddrKey(a.create)
		} else {
			key = recordAddrKey(existing.record)
		}
		cur := m.docAddr[key]
		var currentID session.ID
		if cur != nil {
			currentID = cur.current
		}
		n, ok := live[key]
		if !ok && currentID != 0 {
			n = 1
		}
		if a.retire && currentID == id {
			n--
		}
		if a.create != nil && !a.retire {
			n++
		}
		live[key] = n
	}
	for _, n := range live {
		if n > 1 {
			return errors.New("Document address already has a current incarnation")
		}
	}
	return nil
}

// Out of line: TinyGo would inline this single-caller function into Commit, and V8 compiles a function that runs hot as one unit, at a cost that grows with its body (host/cf/test/functionsize.test.ts).
//
//go:noinline
func (m *mirror) applyDocActions(actions map[session.ID]*mirrorAction, order []session.ID, seq session.Seq) {
	for _, id := range order {
		a := actions[id]
		st := m.docs.get(id)
		if a.create != nil {
			rec := a.create.Shallow()
			rec.Set("createdAt", f(seq))
			if a.retire {
				rec.Set("retiredAt", f(seq))
			}
			st = &mirrorDoc{record: rec, revisions: []mirrorRevision{revision(a.content, seq)}}
			m.docs.put(id, st)
			key := recordAddrKey(rec)
			idx := m.docAddr[key]
			if idx == nil {
				idx = &addrIndex{}
				m.docAddr[key] = idx
			}
			idx.ids = insertSorted(idx.ids, id)
			sk := scopeKey(sub(rec, "scope"))
			m.docByScope[sk] = insertSorted(m.docByScope[sk], id)
			m.nextID = max(m.nextID, id+1)
		} else if a.content != nil {
			rev := revision(a.content, seq)
			if rev.base && currentOnly(st.record) {
				st.revisions = []mirrorRevision{rev}
			} else {
				st.revisions = append(st.revisions, rev)
			}
		}
		if a.retire && a.create == nil {
			r := st.record.Shallow()
			r.Set("retiredAt", f(seq))
			st.record = r
		}
		if a.retire && currentOnly(st.record) {
			st.revisions = nil
		}
		if a.create != nil || a.retire {
			idx := m.docAddr[recordAddrKey(st.record)]
			if a.retire && idx.current == id {
				idx.current = 0
			}
			if a.create != nil && !a.retire {
				idx.current = id
			}
		}
	}
}

func revision(w *session.Write, seq session.Seq) mirrorRevision {
	return mirrorRevision{seq: seq, version: w.Version, base: w.Base, value: w.Value, ops: w.Ops}
}

// load fills an empty mirror with a store's full contents (store.LoadAll) and its metadata counters.
func (m *mirror) load(d *store.Dump, nextID session.ID, nextSeq session.Seq) error {
	m.nextID, m.nextSeq = nextID, nextSeq
	for _, c := range d.Conversations {
		m.convs.put(num(c, "id"), c)
	}
	// Exact allocations: TinyGo's slices.Grow allocates a zeroed temporary of the added length as well.
	m.entries.ids = make([]session.ID, 0, len(d.Entries))
	m.entries.seqs = make([]session.Seq, 0, len(d.Entries))
	m.entries.raws = make([][]byte, 0, len(d.Entries))
	for i := range d.Entries {
		e := &d.Entries[i]
		m.entries.put(e.ID, e.Conversation, e.Seq, e.Head, e.Raw)
	}
	for i := range d.Tasks {
		t := &d.Tasks[i]
		m.putTask(t.ID, taskHead{conv: t.Conversation, kind: t.Kind, status: t.Status, abort: t.Abort, bg: t.Background}, nil, t.Raw)
	}
	for i := range d.Submissions {
		s := &d.Submissions[i]
		m.putSubmission(s.ID, subHead{conv: s.Conversation, status: s.Status}, nil, s.Raw)
		if s.HasRequest {
			m.subs.setRequest(s.Conversation, s.Request, s.ID)
		}
	}
	for _, rec := range d.Documents {
		id := num(rec, "id")
		st := &mirrorDoc{record: rec}
		for _, r := range d.Revisions[id] {
			rev := mirrorRevision{seq: r.Seq, version: r.Version, base: r.Base}
			if r.Base {
				obj, ok := r.Content.(*jv.Object)
				if !ok {
					return errors.New("Document " + strconv.FormatInt(id, 10) + " has a base that is not an object")
				}
				rev.value = obj
			} else {
				ops, err := delta.DecodeOps(r.Content)
				if err != nil {
					return err
				}
				rev.ops = ops
			}
			st.revisions = append(st.revisions, rev)
		}
		m.docs.put(id, st)
		key := recordAddrKey(rec)
		idx := m.docAddr[key]
		if idx == nil {
			idx = &addrIndex{}
			m.docAddr[key] = idx
		}
		idx.ids = insertSorted(idx.ids, id)
		if !rec.Has("retiredAt") {
			idx.current = id
		}
		sk := scopeKey(sub(rec, "scope"))
		m.docByScope[sk] = insertSorted(m.docByScope[sk], id)
	}
	return nil
}

// checkCopies is the copy validation of SqliteStorage's applyDocumentActions: a copy's source must not change in the
// same batch, must be readable at its point, and must match the copied record; each failure is StorageRejected.
//
//go:noinline
func (m *mirror) checkCopies(writes []session.Write) error {
	changed := map[session.ID]bool{}
	for _, w := range writes {
		switch w.Kind {
		case session.WriteDocChange, session.WriteDocRetire:
			changed[w.ID] = true
		case session.WriteDocCreate, session.WriteDocCopy:
			changed[num(w.Record, "id")] = true
		}
	}
	for _, w := range writes {
		if w.Kind != session.WriteDocCopy {
			continue
		}
		id := num(w.Record, "id")
		if changed[w.Source.ID] {
			return errors.New("Document copy " + strconv.FormatInt(id, 10) + " source is changed in the copy batch")
		}
		rejected := errors.New("Document copy " + strconv.FormatInt(id, 10) + " was rejected")
		stored, err := m.materialize(w.Source.ID, w.Source.At)
		if err != nil || stored == nil {
			return rejected
		}
		src := stored.Record
		if str(sub(src, "scope"), "kind") != "conversation" || str(sub(w.Record, "scope"), "kind") != "conversation" {
			return rejected
		}
		for _, k := range []string{"kind", "key", "history", "fork"} {
			a, ha := src.Get(k)
			b, hb := w.Record.Get(k)
			if ha != hb || !jv.Equal(a, b) {
				return rejected
			}
		}
	}
	return nil
}

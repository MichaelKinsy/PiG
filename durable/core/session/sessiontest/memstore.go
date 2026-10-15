// Package sessiontest provides an in-memory session.Store that keeps all
// committed state, as pi-durable's MemoryStorage does. It is the reference
// store of the Session layer's tests: every read is answered, none returns
// session.NeedRead.
package sessiontest

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

type docRevision struct {
	seq     session.Seq
	version int
	base    bool
	value   *jv.Object
	ops     []delta.Op
}

type docState struct {
	record    *jv.Object
	revisions []docRevision
}

type addrIndex struct {
	ids     []session.ID
	current session.ID
}

// MemStore is an in-memory session.Store.
type MemStore struct {
	recordTypes   map[session.ID]string
	conversations map[session.ID]*jv.Object
	convIDs       []session.ID
	entries       map[session.ID]*jv.Object
	entryIDs      map[session.ID][]session.ID
	headIDs       map[session.ID][]session.ID
	entrySeq      map[session.ID]session.Seq
	tasks         map[session.ID]*jv.Object
	taskIDs       []session.ID
	submissions   map[session.ID]*jv.Object
	subIDs        []session.ID
	subByRequest  map[session.ID]map[string]session.ID
	documents     map[session.ID]*docState
	docAddr       map[string]*addrIndex
	docByScope    map[string][]session.ID
	nextID        session.ID
	nextSeq       session.Seq
	// Commits records every committed write batch, for tests that compare
	// the write stream.
	Commits []Committed
}

// Committed is one applied commit.
type Committed struct {
	Seq    session.Seq
	Writes []session.Write
}

// New returns an empty store: the next ID is 2 (1 is the root conversation) and the next sequence 1.
func New() *MemStore {
	return &MemStore{
		recordTypes: map[session.ID]string{}, conversations: map[session.ID]*jv.Object{}, entries: map[session.ID]*jv.Object{},
		entryIDs: map[session.ID][]session.ID{}, headIDs: map[session.ID][]session.ID{}, entrySeq: map[session.ID]session.Seq{},
		tasks: map[session.ID]*jv.Object{}, submissions: map[session.ID]*jv.Object{}, subByRequest: map[session.ID]map[string]session.ID{},
		documents: map[session.ID]*docState{}, docAddr: map[string]*addrIndex{}, docByScope: map[string][]session.ID{},
		nextID: 2, nextSeq: 1,
	}
}

// NextID is the next unminted ID the store has seen.
func (m *MemStore) NextID() session.ID { return m.nextID }

// NextSeq is the sequence the next commit takes.
func (m *MemStore) NextSeq() session.Seq { return m.nextSeq }

func insertSorted(ids []session.ID, id session.ID) []session.ID {
	at := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	if at < len(ids) && ids[at] == id {
		return ids
	}
	return slices.Insert(ids, at, id)
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

func scopeKey(scope *jv.Object) string {
	switch str(scope, "kind") {
	case "conversation":
		return jv.Stringify([]any{"conversation", f(num(scope, "conversationId"))})
	case "task":
		return jv.Stringify([]any{"task", f(num(scope, "taskId"))})
	}
	return `["session"]`
}

func recordAddrKey(rec *jv.Object) string {
	var key any = []any{"singleton"}
	if k, ok := rec.Get("key"); ok {
		key = []any{"family", k}
	}
	return jv.Stringify([]any{str(rec, "kind"), scopeKey(sub(rec, "scope")), key})
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
func (m *MemStore) Conversation(id session.ID) (*jv.Object, error) { return m.conversations[id], nil }

// ScanConversations implements session.Store.
func (m *MemStore) ScanConversations(q session.ConversationQuery, limit int, after session.ID) (session.Page, error) {
	var out []*jv.Object
	for _, id := range m.convIDs {
		c := m.conversations[id]
		o := sub(c, "owner")
		if q.OwnerTaskID != 0 && (o == nil || num(o, "taskId") != q.OwnerTaskID) {
			continue
		}
		if q.OwnerConversationID != 0 && (o == nil || num(o, "conversationId") != q.OwnerConversationID) {
			continue
		}
		out = append(out, c)
	}
	return pageOf(out, limit, after, q.Descending), nil
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
func (m *MemStore) Entry(id session.ID) (*jv.Object, session.Seq, error) {
	e := m.entries[id]
	if e == nil {
		return nil, 0, nil
	}
	return e, m.entrySeq[id], nil
}

// visible walks the entries visible from a conversation, newest first: the
// conversation's own, then each ancestor's up to the fork point.
func (m *MemStore) visible(conv, min, max session.ID, fn func(*jv.Object) bool) {
	cur, upper := conv, max // upper 0: unbounded
	for {
		ids := m.entryIDs[cur]
		i := len(ids) - 1
		if upper != 0 {
			i = sort.Search(len(ids), func(k int) bool { return ids[k] > upper }) - 1
		}
		for ; i >= 0; i-- {
			if min != 0 && ids[i] < min {
				break
			}
			if !fn(m.entries[ids[i]]) {
				return
			}
		}
		p := sub(m.conversations[cur], "parent")
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
func (m *MemStore) EntryIn(conv, id session.ID) (*jv.Object, session.Seq, error) {
	if m.conversations[conv] == nil {
		return nil, 0, session.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", conv))
	}
	var found *jv.Object
	m.visible(conv, id, id, func(e *jv.Object) bool { found = e; return false })
	if found == nil {
		return nil, 0, nil
	}
	return found, m.entrySeq[id], nil
}

// LatestHeadMarker implements session.Store.
func (m *MemStore) LatestHeadMarker(conv, atOrBefore session.ID) (*jv.Object, error) {
	if m.conversations[conv] == nil {
		return nil, session.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", conv))
	}
	cur, upper := conv, atOrBefore
	for {
		ids := m.headIDs[cur]
		n := len(ids)
		if upper != 0 {
			n = sort.Search(len(ids), func(i int) bool { return ids[i] > upper })
		}
		if n > 0 {
			return m.entries[ids[n-1]], nil
		}
		p := sub(m.conversations[cur], "parent")
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
func (m *MemStore) ScanEntries(q session.EntryQuery, limit int, after session.ID) (session.Page, error) {
	if m.conversations[q.ConversationID] == nil {
		return session.Page{}, session.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", q.ConversationID))
	}
	var all []*jv.Object
	m.visible(q.ConversationID, q.MinEntryID, q.MaxEntryID, func(e *jv.Object) bool { all = append(all, e); return true })
	if q.Ascending {
		slices.Reverse(all)
	}
	if after != 0 {
		keep := all[:0:0]
		for _, e := range all {
			id := num(e, "id")
			if (q.Ascending && id > after) || (!q.Ascending && id < after) {
				keep = append(keep, e)
			}
		}
		all = keep
	}
	if limit > 0 && len(all) > limit {
		return session.Page{Items: all[:limit], After: num(all[limit-1], "id")}, nil
	}
	return session.Page{Items: all}, nil
}

// Task implements session.Store.
func (m *MemStore) Task(id session.ID) (*jv.Object, error) { return m.tasks[id], nil }

// ScanTasks implements session.Store.
func (m *MemStore) ScanTasks(q session.TaskQuery, limit int, after session.ID) (session.Page, error) {
	var out []*jv.Object
	for _, id := range m.taskIDs {
		t := m.tasks[id]
		status := str(sub(t, "state"), "status")
		if (q.Status != "" && status != q.Status) || (q.ConversationID != 0 && num(t, "conversationId") != q.ConversationID) ||
			(q.Kind != "" && str(t, "kind") != q.Kind) {
			continue
		}
		if q.AbortRequested != nil {
			if v, _ := t.Get("abortRequested"); v != *q.AbortRequested {
				continue
			}
		}
		if q.Background != nil {
			if v, _ := t.Get("background"); v != *q.Background {
				continue
			}
		}
		out = append(out, t)
	}
	return pageOf(out, limit, after, q.Descending), nil
}

// Submission implements session.Store.
func (m *MemStore) Submission(id session.ID) (*jv.Object, error) { return m.submissions[id], nil }

// ScanSubmissions implements session.Store.
func (m *MemStore) ScanSubmissions(q session.SubmissionQuery, limit int, after session.ID) (session.Page, error) {
	var out []*jv.Object
	for _, id := range m.subIDs {
		s := m.submissions[id]
		if (q.Status != "" && str(s, "status") != q.Status) || (q.ConversationID != 0 && num(s, "conversationId") != q.ConversationID) {
			continue
		}
		out = append(out, s)
	}
	return pageOf(out, limit, after, q.Descending), nil
}

// SubmissionByRequest implements session.Store.
func (m *MemStore) SubmissionByRequest(conv session.ID, request string) (*jv.Object, error) {
	if id, ok := m.subByRequest[conv][request]; ok {
		return m.submissions[id], nil
	}
	return nil, nil
}

// FindDocument implements session.Store.
func (m *MemStore) FindDocument(a session.Address, at session.Point) (*jv.Object, error) {
	idx := m.docAddr[addrKey(a)]
	if idx == nil {
		return nil, nil
	}
	if at.Current {
		if idx.current == 0 {
			return nil, nil
		}
		return m.documents[idx.current].record, nil
	}
	for _, id := range idx.ids {
		if r := m.documents[id].record; aliveAt(r, at) {
			return r, nil
		}
	}
	return nil, nil
}

func (m *MemStore) materialize(id session.ID, at session.Point) (*session.StoredDoc, error) {
	d := m.documents[id]
	if d == nil {
		return nil, nil
	}
	if !at.Current && currentOnly(d.record) {
		return nil, session.NewStorageRequestError(fmt.Sprintf("Document %d does not retain historical content", id))
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
		return nil, fmt.Errorf("Document %d is missing a required base", id)
	}
	base := revs[baseAt]
	var value any = base.value
	var batch []delta.Op
	for _, r := range revs[baseAt+1:] {
		if r.base || r.version != base.version {
			return nil, fmt.Errorf("Document %d crosses a stored version boundary without a base", id)
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
func (m *MemStore) Document(id session.ID, at session.Point) (*session.StoredDoc, error) {
	return m.materialize(id, at)
}

// ScanDocuments implements session.Store.
func (m *MemStore) ScanDocuments(scope session.Scope, at session.Point, kind string, limit int, after session.ID) (session.Page, error) {
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
		r := m.documents[id].record
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

type docAction struct {
	create  *jv.Object
	content *session.Write
	retire  bool
}

// Commit implements session.Store.
func (m *MemStore) Commit(writes []session.Write, nextID session.ID) (session.Seq, error) {
	seq := m.nextSeq
	submitted := writes
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
	for _, w := range writes {
		if w.Kind > session.WriteSubmission {
			continue // documents: applyDocActions
		}
		id := num(w.Record, "id")
		switch w.Kind {
		case session.WriteConversation:
			m.recordTypes[id] = "conversation"
			m.conversations[id] = w.Record
			m.convIDs = insertSorted(m.convIDs, id)
		case session.WriteEntry:
			m.recordTypes[id] = "entry"
			m.entries[id] = w.Record
			m.entrySeq[id] = seq
			conv := num(w.Record, "conversationId")
			m.entryIDs[conv] = insertSorted(m.entryIDs[conv], id)
			if w.Record.Has("head") {
				m.headIDs[conv] = insertSorted(m.headIDs[conv], id)
			}
		case session.WriteTask:
			m.recordTypes[id] = "task"
			if m.tasks[id] == nil {
				m.taskIDs = insertSorted(m.taskIDs, id)
			}
			m.tasks[id] = w.Record
		case session.WriteSubmission:
			m.recordTypes[id] = "submission"
			prev := m.submissions[id]
			if prev == nil {
				m.subIDs = insertSorted(m.subIDs, id)
			} else if r, ok := prev.Get("requestId"); ok {
				c := num(prev, "conversationId")
				if m.subByRequest[c][r.(string)] == id {
					delete(m.subByRequest[c], r.(string))
				}
			}
			m.submissions[id] = w.Record
			if r, ok := w.Record.Get("requestId"); ok {
				c := num(w.Record, "conversationId")
				if m.subByRequest[c] == nil {
					m.subByRequest[c] = map[string]session.ID{}
				}
				m.subByRequest[c][r.(string)] = id
			}
		}
		if id >= m.nextID {
			m.nextID = id + 1
		}
	}
	m.applyDocActions(actions, order, seq)
	m.nextSeq = seq + 1
	m.Commits = append(m.Commits, Committed{Seq: seq, Writes: submitted})
	return seq, nil
}

func (m *MemStore) resolveCopies(writes []session.Write) []session.Write {
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

func (m *MemStore) checkGlobalIDs(writes []session.Write) error {
	claimed := map[session.ID]string{}
	for _, w := range writes {
		if w.Kind == session.WriteDocChange || w.Kind == session.WriteDocRetire {
			continue
		}
		table := map[session.WriteKind]string{session.WriteConversation: "conversation", session.WriteEntry: "entry", session.WriteTask: "task", session.WriteSubmission: "submission", session.WriteDocCreate: "document", session.WriteDocCopy: "document"}[w.Kind]
		id := num(w.Record, "id")
		existing, earlier := m.recordTypes[id], claimed[id]
		if table == "conversation" || table == "entry" || table == "document" {
			if existing != "" {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" {
				return fmt.Errorf("ID %d is written more than once", id)
			}
		} else {
			if existing != "" && existing != table {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" && earlier != table {
				return fmt.Errorf("ID %d is written as two record types", id)
			}
		}
		claimed[id] = table
	}
	return nil
}

func (m *MemStore) prepareDocActions(writes []session.Write) (map[session.ID]*docAction, []session.ID, error) {
	actions := map[session.ID]*docAction{}
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
			a = &docAction{}
			actions[id] = a
			order = append(order, id)
		}
		switch w.Kind {
		case session.WriteDocCreate:
			if a.create != nil || a.content != nil {
				return nil, nil, fmt.Errorf("Document %d has more than one content command", id)
			}
			cw := w
			a.create, a.content = w.Record, &cw
		case session.WriteDocChange:
			if a.content != nil {
				return nil, nil, fmt.Errorf("Document %d has more than one content command", id)
			}
			cw := w
			a.content = &cw
		case session.WriteDocRetire:
			if a.retire {
				return nil, nil, fmt.Errorf("Document %d is retired more than once", id)
			}
			a.retire = true
		}
	}
	return actions, order, nil
}

func (m *MemStore) checkDocActions(actions map[session.ID]*docAction, order []session.ID) error {
	live := map[string]int{}
	for _, id := range order {
		a := actions[id]
		existing := m.documents[id]
		if a.create == nil && existing == nil {
			return fmt.Errorf("Unknown document: %d", id)
		}
		if a.create != nil && existing != nil {
			return fmt.Errorf("Document %d already exists", id)
		}
		if existing != nil && existing.record.Has("retiredAt") {
			return fmt.Errorf("Document %d is retired", id)
		}
		if a.content != nil && a.create != nil && a.content.Value == nil {
			return fmt.Errorf("Fork source document cannot be read")
		}
		if a.content != nil && !a.content.Base {
			if existing == nil || len(existing.revisions) == 0 {
				return fmt.Errorf("Document %d delta has no base", id)
			}
			if prev := existing.revisions[len(existing.revisions)-1]; prev.version != a.content.Version {
				return fmt.Errorf("Document %d version transition requires a base", id)
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

func (m *MemStore) applyDocActions(actions map[session.ID]*docAction, order []session.ID, seq session.Seq) {
	for _, id := range order {
		a := actions[id]
		st := m.documents[id]
		if a.create != nil {
			rec := a.create.Shallow()
			rec.Set("createdAt", f(seq))
			if a.retire {
				rec.Set("retiredAt", f(seq))
			}
			st = &docState{record: rec, revisions: []docRevision{revision(a.content, seq)}}
			m.recordTypes[id] = "document"
			m.documents[id] = st
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
				st.revisions = []docRevision{rev}
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

func revision(w *session.Write, seq session.Seq) docRevision {
	return docRevision{seq: seq, version: w.Version, base: w.Base, value: w.Value, ops: w.Ops}
}

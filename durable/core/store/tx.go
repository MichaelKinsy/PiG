// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
)

type tableKind uint8

const (
	tConversation tableKind = iota + 1
	tEntry
	tTask
	tSubmission
	tDocument
)

// tableNames is read-only.
var tableNames = [...]string{"", "conversation", "entry", "task", "submission", "document"}

// tableWrite is one conversation, entry, task or submission write, staged with every column but the commit sequence.
type tableWrite struct {
	table  tableKind
	id     int64
	params []abi.Value
	seqAt  int // entries: index of the commit_seq parameter, patched at Commit
	conv   *rec.Conversation
	task   *rec.Task
	sub    *rec.Submission
}

type contentKind uint8

const (
	contentNone contentKind = iota
	contentBase
	contentDelta
)

// docAction is everything one Tx does to one document incarnation (storage.ts DocumentAction).
type docAction struct {
	id       int64
	create   *rec.Document
	copyFrom int64 // source document ID of a copy, else 0
	copyAt   int64 // source point: <= 0 is "current"
	isCopy   bool
	content  contentKind
	version  int
	base     *jv.Object // contentBase
	baseJSON []byte
	ops      []byte // contentDelta: Chord Op[] JSON
	retire   bool
}

// Tx is one Pi commit under construction: the StorageWrite batch of spec §10, in call order.
type Tx struct {
	s      *Store
	writes []tableWrite
	acts   []*docAction
	byDoc  map[int64]*docAction
	err    error
}

// Begin starts a Pi commit.
func (s *Store) Begin() *Tx { return &Tx{s: s} }

func (tx *Tx) fail(err error) {
	if tx.err == nil {
		tx.err = err
	}
}

func idParam(v int64) abi.Value { return abi.Int(v) }

func textParam(b []byte) abi.Value { return abi.TextBytes(b) }

// Conversation stages a conversation insert (the "conversation" StorageWrite).
func (tx *Tx) Conversation(c *rec.Conversation) {
	params := make([]abi.Value, 4)
	params[0] = idParam(c.ID())
	params[1], params[2] = abi.Null(), abi.Null()
	if v, ok := c.OwnerConversation(); ok {
		params[1] = idParam(v)
	}
	if v, ok := c.OwnerTask(); ok {
		params[2] = idParam(v)
	}
	params[3] = textParam(c.Append(nil))
	tx.writes = append(tx.writes, tableWrite{table: tConversation, id: c.ID(), params: params, conv: c})
}

// ConversationRow stages a conversation insert from the bytes history.AppendConversation produced.
func (tx *Tx) ConversationRow(row history.ConversationRow) {
	params := []abi.Value{idParam(row.ID), abi.Null(), abi.Null(), textParam(row.Record)}
	if row.HasOwner {
		params[1], params[2] = idParam(row.OwnerConv), idParam(row.OwnerTask)
	}
	c, err := rec.ParseConversation(row.Record)
	if err != nil {
		tx.fail(err)
		return
	}
	tx.writes = append(tx.writes, tableWrite{table: tConversation, id: row.ID, params: params, conv: c})
}

// Entry writes one entry row; record is the bytes history.AppendEntryRecord produced. After Commit, the session appends
// the same record to the history.Store with the returned seq.
func (tx *Tx) Entry(id, conversationID, head int64, record []byte) {
	hv := abi.Null()
	if head != 0 {
		hv = idParam(head)
	}
	tx.writes = append(tx.writes, tableWrite{table: tEntry, id: id, seqAt: 3,
		params: []abi.Value{idParam(id), idParam(conversationID), hv, abi.Int(0), textParam(record)}})
}

// EntryRow stages an entry insert from history.NewEntryRow, which reads the id, conversation and head columns from the
// record the way SqliteStorage binds them (`head ?? null`).
func (tx *Tx) EntryRow(row history.EntryRow) {
	hv := abi.Null()
	if row.HasHead {
		hv = idParam(row.Head)
	}
	tx.writes = append(tx.writes, tableWrite{table: tEntry, id: row.ID, seqAt: 3,
		params: []abi.Value{idParam(row.ID), idParam(row.Conv), hv, abi.Int(0), textParam(row.Record)}})
}

// Task stages a task upsert (the "task" StorageWrite); the tasks row columns come from the record, and text is its JSON
// text (rec.Task.Append).
func (tx *Tx) Task(t *rec.Task, text []byte) {
	bg, abort := int64(0), int64(0)
	if t.Background() {
		bg = 1
	}
	if t.AbortRequested() {
		abort = 1
	}
	params := []abi.Value{
		idParam(t.ID()), idParam(t.ConversationID()), abi.TextBytes(tx.s.kindColumn(t.Kind())), abi.TextBytes(tx.s.text(t.Status().String())),
		abi.Int(abort), abi.Int(bg), textParam(text),
	}
	tx.writes = append(tx.writes, tableWrite{table: tTask, id: t.ID(), params: params, task: t})
}

// Submission stages a submission upsert (the "submission" StorageWrite); text is the record's JSON text
// (rec.Submission.Append).
func (tx *Tx) Submission(sub *rec.Submission, text []byte) {
	req := abi.Null()
	if r, ok := sub.RequestColumn(); ok {
		req = abi.Text(r)
	}
	params := []abi.Value{
		idParam(sub.ID()), idParam(sub.ConversationID()), req, abi.TextBytes(tx.s.text(sub.Status().String())), textParam(text),
	}
	tx.writes = append(tx.writes, tableWrite{table: tSubmission, id: sub.ID(), params: params, sub: sub})
}

// RecordText returns the record text the k-th conversation, entry, task or submission write staged in tx binds, in call
// order: an entry's, task's or submission's JSON text, nil for a conversation. The mirror keeps it as the row's stored text
// (mirror.Commit), so a commit needs no list of the texts of its own.
func (tx *Tx) RecordText(k int) []byte {
	w := &tx.writes[k]
	if w.table == tConversation {
		return nil
	}
	return w.params[len(w.params)-1].Bytes
}

func (tx *Tx) action(id int64) *docAction {
	if tx.byDoc == nil {
		tx.byDoc = map[int64]*docAction{}
	}
	a := tx.byDoc[id]
	if a == nil {
		a = &docAction{id: id}
		tx.byDoc[id] = a
		tx.acts = append(tx.acts, a)
	}
	return a
}

func (a *docAction) hasContentCommand() bool {
	return a.create != nil || a.content != contentNone || a.isCopy
}

// DocumentCreate writes a new incarnation with its base (the "document.create" StorageWrite); d is the DocumentCreate
// record, without createdAt and retiredAt.
func (tx *Tx) DocumentCreate(d *rec.Document, version int, base JSON) {
	a := tx.action(d.ID())
	if a.hasContentCommand() {
		tx.fail(errMoreThanOneContent(a.id))
		return
	}
	obj, _ := base.(*jv.Object)
	a.create, a.content, a.version, a.base = d, contentBase, version, obj
	a.baseJSON = jv.Append(nil, base)
}

// DocumentCopy copies a source at a point (a fork; the "document.copy" StorageWrite). at <= 0 selects "current".
func (tx *Tx) DocumentCopy(d *rec.Document, source int64, at int64) {
	a := tx.action(d.ID())
	if a.hasContentCommand() {
		tx.fail(errMoreThanOneContent(a.id))
		return
	}
	a.create, a.isCopy, a.copyFrom, a.copyAt = d, true, source, at
}

// DocumentBase stores a new complete base of an existing incarnation (the "document.change" StorageWrite, base).
func (tx *Tx) DocumentBase(id int64, version int, base JSON) {
	a := tx.action(id)
	if a.content != contentNone || a.isCopy {
		tx.fail(errMoreThanOneContent(id))
		return
	}
	obj, _ := base.(*jv.Object)
	a.content, a.version, a.base = contentBase, version, obj
	a.baseJSON = jv.Append(nil, base)
}

// DocumentDelta stores a delta of an existing incarnation; ops is Chord Op[] JSON (the "document.change" write, delta).
func (tx *Tx) DocumentDelta(id int64, version int, ops []byte) {
	a := tx.action(id)
	if a.content != contentNone || a.isCopy {
		tx.fail(errMoreThanOneContent(id))
		return
	}
	a.content, a.version, a.ops = contentDelta, version, ops
}

// DocumentRetire retires an incarnation (the "document.retire" StorageWrite).
func (tx *Tx) DocumentRetire(id int64) {
	a := tx.action(id)
	if a.retire {
		tx.fail(&plainError{"Document " + itoa(id) + " is retired more than once"})
		return
	}
	a.retire = true
}

// RequestError is Pi's StorageRequestError: a read the caller's request got wrong, which fails only that read.
type RequestError struct{ msg string }

func (e *RequestError) Error() string { return e.msg }

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

func errMoreThanOneContent(id int64) error {
	return &plainError{"Document " + itoa(id) + " has more than one content command"}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// stmts accumulates the statements of one commit.
type stmts struct{ list []abi.Stmt }

func (l *stmts) add(sql uint16, params ...abi.Value) {
	l.list = append(l.list, abi.Stmt{SQL: sql, Params: params})
}

// Commit validates tx as SqliteStorage.commit does (ID claims, document actions; a failure is Pi's StorageRejected and
// changes nothing), emits one abi.Commit with Pi's rows in Pi's order and the guarded metadata update, applies the
// commit to the store's in-memory state (next_id, next_seq, documents), and returns the sequence it consumed. It
// returns ErrPending, having emitted reads and no commit, when a document copy needs a source the store has not
// loaded; the caller returns it unchanged and runs the whole handler again after the rows.
//
//go:noinline
func (s *Store) Commit(b *abi.Builder, tx *Tx) (seq int64, err error) {
	if tx.err != nil {
		return 0, tx.err
	}
	if err := s.checkGlobalIDs(tx); err != nil {
		return 0, err
	}
	plan, err := s.planDocuments(b, tx)
	if err != nil {
		return 0, err
	}
	seq = s.nextSeq
	candidate := s.nextID
	for i := range tx.writes {
		if id := tx.writes[i].id; id+1 > candidate {
			candidate = id + 1
		}
	}
	for _, a := range tx.acts {
		if a.create != nil && a.id+1 > candidate {
			candidate = a.id + 1
		}
	}

	var out stmts
	for i := range tx.writes {
		w := &tx.writes[i]
		switch w.table {
		case tConversation:
			s.claim(&out, w.id, tConversation)
			out.add(stmtInsertConversation, w.params...)
		case tEntry:
			s.claim(&out, w.id, tEntry)
			w.params[w.seqAt] = abi.Int(seq)
			out.add(stmtInsertEntry, w.params...)
		case tTask:
			if s.claimed[w.id] != claimTask {
				s.claim(&out, w.id, tTask)
			}
			out.add(stmtUpsertTask, w.params...)
		case tSubmission:
			if s.claimed[w.id] != claimSubmission {
				s.claim(&out, w.id, tSubmission)
			}
			out.add(stmtUpsertSubmission, w.params...)
		}
	}
	for _, p := range plan {
		s.documentStatements(&out, p, seq)
	}
	out.add(stmtMetadata, abi.Text(itoa(candidate)), abi.Int(seq+1), abi.Int(seq))
	b.Commit(seq, out.list)

	s.nextSeq = seq + 1
	s.nextID = candidate
	for i := range tx.writes {
		w := &tx.writes[i]
		switch w.table {
		case tTask:
			if w.task.Terminal() {
				delete(s.claimed, w.id)
			} else {
				s.claimed[w.id] = claimTask
			}
		case tSubmission:
			switch w.sub.Status() {
			case rec.Done, rec.Unanswered:
				delete(s.claimed, w.id)
			default:
				s.claimed[w.id] = claimSubmission
			}
		case tConversation:
			s.conversat[w.id] = true
			s.markScopeLoaded(rec.ScopeConversation, w.id)
		}
	}
	for _, p := range plan {
		s.applyDocument(p, seq)
	}
	s.invalidate(tx)
	return seq, nil
}

func (s *Store) claim(out *stmts, id int64, t tableKind) {
	out.add(stmtClaimID, abi.Int(id), abi.TextBytes(s.text(tableNames[t])))
}

// kindColumn is the tasks.kind column of a task of kind, JSON.stringify(kind) (storage.ts encodeIndexedString). It is
// encoded once per kind, and the statements that bind it share it.
func (s *Store) kindColumn(kind string) []byte {
	if b, ok := s.kinds[kind]; ok {
		return b
	}
	if s.kinds == nil {
		s.kinds = map[string][]byte{}
	}
	b := jv.AppendString(nil, kind)
	s.kinds[kind] = b
	return b
}

// text is the bytes of a fixed column value (a status, a record type), made once per value; the statements that bind it
// share them. They are on the heap, where the collector does not scan text: a []byte literal would be in .data, whose
// words it scans for pointers.
func (s *Store) text(v string) []byte {
	if b, ok := s.texts[v]; ok {
		return b
	}
	if s.texts == nil {
		s.texts = map[string][]byte{}
	}
	b := []byte(v)
	s.texts[v] = b
	return b
}

// checkGlobalIDs is checkGlobalIds (storage.ts) over the store's own state. The record_ids table is not read: IDs come
// from MintID, so a conversation, entry or document ID can collide only inside one batch, and the table's primary key
// refuses any other collision. A task or submission write may repeat an ID of its own type across commits.
func (s *Store) checkGlobalIDs(tx *Tx) error {
	earlier := make(map[int64]tableKind, len(tx.writes)+len(tx.acts))
	check := func(id int64, t tableKind) error {
		prior, seen := earlier[id]
		known := tableKind(0)
		switch s.claimed[id] {
		case claimTask:
			known = tTask
		case claimSubmission:
			known = tSubmission
		}
		if t == tConversation || t == tEntry || t == tDocument {
			if _, ok := s.docs[id]; ok {
				known = tDocument
			} else if s.conversat[id] {
				known = tConversation
			}
			if known != 0 {
				return &plainError{"ID " + itoa(id) + " already belongs to " + tableNames[known]}
			}
			if seen {
				return &plainError{"ID " + itoa(id) + " is written more than once"}
			}
		} else {
			if known != 0 && known != t {
				return &plainError{"ID " + itoa(id) + " already belongs to " + tableNames[known]}
			}
			if seen && prior != t {
				return &plainError{"ID " + itoa(id) + " is written as two record types"}
			}
		}
		earlier[id] = t
		return nil
	}
	for i := range tx.writes {
		if err := check(tx.writes[i].id, tx.writes[i].table); err != nil {
			return err
		}
	}
	for _, a := range tx.acts {
		if a.create != nil {
			if err := check(a.id, tDocument); err != nil {
				return err
			}
		}
	}
	return nil
}

// docPlan is one document action after validation: what to write and the state it leaves.
type docPlan struct {
	act     *docAction
	existed *docState
	record  *rec.Document // the record as stored by this commit (stamped, or retired)
	value   *jv.Object    // the content's value when the store keeps one
	content contentKind
	version int
	base    []byte // encoded base value
	ops     []byte
	retire  bool
}

// Out of line: TinyGo would inline this single-caller function into Commit, and V8 compiles a function that runs hot as one unit, at a cost that grows with its body (host/cf/test/functionsize.test.ts).
//
//go:noinline
func (s *Store) planDocuments(b *abi.Builder, tx *Tx) ([]*docPlan, error) {
	if len(tx.acts) == 0 {
		return nil, nil
	}
	plans := make([]*docPlan, 0, len(tx.acts))
	live := map[string]int{}
	for _, a := range tx.acts {
		p := &docPlan{act: a, retire: a.retire}
		if a.isCopy {
			if _, also := tx.byDoc[a.copyFrom]; also {
				return nil, &RejectedError{"Document copy " + itoa(a.id) + " source is changed in the copy batch"}
			}
		}
		existing := s.docs[a.id]
		if a.create == nil && existing == nil {
			return nil, &plainError{"Unknown document: " + itoa(a.id)}
		}
		if a.create != nil && existing != nil {
			return nil, &plainError{"Document " + itoa(a.id) + " already exists"}
		}
		if existing != nil && existing.retired {
			return nil, &plainError{"Document " + itoa(a.id) + " is retired"}
		}
		p.existed = existing
		if a.content == contentDelta {
			if existing == nil && a.create == nil {
				return nil, &plainError{"Document " + itoa(a.id) + " delta has no base"}
			}
			if existing != nil && existing.version != a.version {
				return nil, &plainError{"Document " + itoa(a.id) + " version transition requires a base"}
			}
		}
		recd := a.create
		if recd == nil {
			recd = existing.rec
		}
		addr := recd.Address()
		key := addr.AddressKey()
		n, seen := live[key]
		if !seen {
			if _, ok := s.addrs[key]; ok {
				n = 1
			}
		}
		if a.retire && existing != nil {
			n--
		}
		if a.create != nil && !a.retire {
			n++
		}
		live[key] = n
		plans = append(plans, p)
	}
	for _, n := range live {
		if n > 1 {
			return nil, &plainError{"Document address already has a current incarnation"}
		}
	}
	// Resolve content. A copy materialises its source from the store's own state.
	for _, p := range plans {
		a := p.act
		switch {
		case a.isCopy:
			src, err := s.copySource(b, a)
			if err != nil {
				return nil, err
			}
			p.content, p.version, p.value = contentBase, src.version, src.value
			p.base = jv.Append(nil, src.value)
		case a.content == contentBase:
			p.content, p.version, p.value, p.base = contentBase, a.version, a.base, a.baseJSON
		case a.content == contentDelta:
			p.content, p.version, p.ops = contentDelta, a.version, a.ops
			if p.existed != nil && p.existed.value != nil {
				ops, err := decodeOps(a.ops)
				if err != nil {
					return nil, err
				}
				v, err := delta.Apply(p.existed.value, ops)
				if err != nil {
					return nil, err
				}
				obj, _ := v.(*jv.Object)
				p.value = obj
			}
		}
	}
	return plans, nil
}

func decodeOps(raw []byte) ([]delta.Op, error) {
	v, err := jv.Parse(raw)
	if err != nil {
		return nil, err
	}
	return delta.DecodeOps(v)
}

type copied struct {
	version int
	value   *jv.Object
}

// copySource is materializeDocument for a copy (storage.ts applyDocumentActions): the source must be readable at the
// point, conversation-scoped, and match the copied record; anything else is Pi's StorageRejected.
func (s *Store) copySource(b *abi.Builder, a *docAction) (copied, error) {
	rejected := func(cause string) error {
		return &RejectedError{"Document copy " + itoa(a.id) + " was rejected"} // cause: cause
	}
	src, loaded := s.docs[a.copyFrom]
	var ver int
	var val *jv.Object
	var srcRec *rec.Document
	switch {
	case loaded && src.value != nil && s.currentSuffices(src, a.copyAt):
		if !src.rec.AliveAt(a.copyAt <= 0, a.copyAt) {
			return copied{}, rejected("unreadable")
		}
		ver, val, srcRec = src.version, src.value, src.rec
	default:
		d, err := s.ensureAsOf(b, a.copyFrom, a.copyAt)
		if err != nil {
			if errors.Is(err, ErrPending) {
				return copied{}, err
			}
			return copied{}, rejected(err.Error())
		}
		if d == nil {
			return copied{}, rejected("Fork source document " + itoa(a.copyFrom) + " cannot be read")
		}
		ver, val, srcRec = d.Version, d.Value, d.Record
	}
	create := a.create
	sk, _ := srcRec.Scope()
	ck, _ := create.Scope()
	ckey, cHas := create.Key()
	skey, sHas := srcRec.Key()
	if sk != rec.ScopeConversation || ck != rec.ScopeConversation || srcRec.Kind() != create.Kind() ||
		cHas != sHas || ckey != skey || srcRec.History() != create.History() || srcRec.Fork() != create.Fork() {
		return copied{}, rejected("does not match the copied record")
	}
	return copied{version: ver, value: val}, nil
}

// currentSuffices reports whether the cached current value answers a read at the point: the point is "current" or the
// document has not changed since it.
func (s *Store) currentSuffices(d *docState, at int64) bool {
	if at <= 0 {
		return true
	}
	return d.lastSeq <= at
}

// documentStatements appends the statements applyDocumentActions issues for one action.
//
// Out of line: TinyGo would inline this single-caller function into Commit, and V8 compiles a function that runs hot as one unit, at a cost that grows with its body (host/cf/test/functionsize.test.ts).
//
//go:noinline
func (s *Store) documentStatements(out *stmts, p *docPlan, seq int64) {
	a := p.act
	var record *rec.Document
	if a.create != nil {
		record = a.create.Stamped(seq, a.retire)
		p.record = record
		addr := record.Address()
		s.claim(out, a.id, tDocument)
		retired := abi.Null()
		if a.retire {
			retired = abi.Int(seq)
		}
		kind, owner := record.Scope()
		out.add(stmtInsertDocument, abi.Int(a.id), abi.Text(addr.Kind), abi.Int(addr.Family), abi.Text(addr.KeyValue),
			abi.Text(kind.String()), abi.Int(owner), abi.Int(seq), retired, textParam(record.Append(nil)))
	} else {
		record = p.existed.rec
	}
	if p.content != contentNone {
		if p.content == contentBase && record.CurrentOnly() {
			out.add(stmtDeleteRevisions, abi.Int(a.id))
		}
		kind, body := "base", p.base
		if p.content == contentDelta {
			kind, body = "delta", p.ops
		}
		out.add(stmtInsertRevision, abi.Int(a.id), abi.Int(seq), abi.Text(kind), abi.Int(int64(p.version)), textParam(body))
	}
	if a.retire {
		if a.create == nil {
			record = record.Retired(seq)
			p.record = record
			out.add(stmtRetireDocument, abi.Int(seq), textParam(record.Append(nil)), abi.Int(a.id))
		}
		if record.CurrentOnly() {
			out.add(stmtDeleteRevisions, abi.Int(a.id))
		}
	}
	if p.record == nil {
		p.record = record
	}
}

// invalidate drops cached reads a commit may have changed: a task EnsureTask returned, a task found missing, and a
// document read at "current".
func (s *Store) invalidate(tx *Tx) {
	for i := range tx.writes {
		if tx.writes[i].table != tTask {
			continue
		}
		id := tx.writes[i].id
		delete(s.absent, id)
		s.taskLRU = slices.DeleteFunc(s.taskLRU, func(t *rec.Task) bool { return t.ID() == id })
	}
	s.docLRU = slices.DeleteFunc(s.docLRU, func(c *asOfDoc) bool { return c.at == 0 })
}

// ObserveNextID raises the in-memory ID counter to a caller's: the Session mints IDs itself, and the next commit
// persists max(stored next_id, the counter, max written id + 1).
func (s *Store) ObserveNextID(next int64) {
	if next > s.nextID {
		s.nextID = next
	}
}

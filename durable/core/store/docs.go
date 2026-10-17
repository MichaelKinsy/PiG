// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
)

// maxSafe is JavaScript's Number.MAX_SAFE_INTEGER, the "current" upper bound of a revision read.
const maxSafe = int64(1)<<53 - 1

// docState is what the store knows of one loaded incarnation: enough to validate every document write (storage.ts
// checkDocumentActions) and to serve a copy (applyDocumentActions). value is the materialised current content and is
// treated as immutable: a new revision replaces it, never edits it.
type docState struct {
	rec     *rec.Document
	version int   // version of the newest stored revision
	lastSeq int64 // sequence of the newest stored revision
	deltas  int   // deltas after the newest base
	value   *jv.Object
	retired bool
	size    int
}

type scopeKey struct {
	kind  rec.ScopeKind
	owner int64
}

// scopeState tracks the load of one scope's live documents.
type scopeState struct {
	loaded      bool
	requested   bool
	outstanding int // revision reads of this scope not yet answered
}

func (s *Store) markScopeLoaded(kind rec.ScopeKind, owner int64) {
	k := scopeKey{kind, owner}
	st := s.scopes[k]
	if st == nil {
		st = &scopeState{}
		s.scopes[k] = st
	}
	st.loaded, st.requested = true, true
}

// asOfDoc is the result of EnsureDocument at a point, kept in a small cache.
type asOfDoc struct {
	id, at   int64
	doc      *LiveDocument // nil: absent or not alive at the point
	err      error
	haveRec  bool
	haveRevs bool
	record   []byte
	revs     [][]abi.Value
}

const asOfCacheSize = 16

// Out of line: TinyGo would inline this single-caller function into Commit, and V8 compiles a function that runs hot as one unit, at a cost that grows with its body (host/cf/test/functionsize.test.ts).
//
//go:noinline
func (s *Store) applyDocument(p *docPlan, seq int64) {
	a := p.act
	if a.create != nil {
		ds := &docState{rec: p.record, version: p.version, lastSeq: seq, value: p.value, deltas: 0}
		ds.size = len(p.base)
		s.docs[a.id] = ds
		if a.retire {
			ds.retired, ds.value, ds.size = true, nil, 0
		} else {
			s.addrs[ds.rec.Address().AddressKey()] = a.id
		}
		return
	}
	ds := p.existed
	switch p.content {
	case contentBase:
		ds.version, ds.lastSeq, ds.value, ds.deltas, ds.size = p.version, seq, p.value, 0, len(p.base)
	case contentDelta:
		ds.version, ds.lastSeq = p.version, seq
		ds.deltas++
		ds.size += len(p.ops)
		if ds.value != nil {
			ds.value = p.value
		}
	}
	if a.retire {
		ds.rec, ds.retired, ds.value, ds.size = p.record, true, nil, 0
		delete(s.addrs, ds.rec.Address().AddressKey())
	}
}

// EnsureScope loads the live (current, not retired) documents of a scope: the Session, a conversation or a task. It
// returns ErrPending after emitting reads, and the documents, ordered by ID, once every revision has arrived. A scope
// the store created itself (a new conversation or task) is loaded and empty.
func (s *Store) EnsureScope(b *abi.Builder, kind rec.ScopeKind, owner int64) ([]*LiveDocument, error) {
	k := scopeKey{kind, owner}
	st := s.scopes[k]
	if st == nil {
		st = &scopeState{}
		s.scopes[k] = st
	}
	if st.loaded {
		return s.scopeDocuments(k), nil
	}
	if !st.requested {
		s.requestScope(b, k)
	}
	return nil, ErrPending
}

// ForgetScope drops the loaded documents of a scope that is no longer in use (an unloaded conversation).
func (s *Store) ForgetScope(kind rec.ScopeKind, owner int64) {
	k := scopeKey{kind, owner}
	for id, d := range s.docs {
		if dk, do := d.rec.Scope(); dk == kind && do == owner {
			if !d.retired {
				delete(s.addrs, d.rec.Address().AddressKey())
			}
			delete(s.docs, id)
		}
	}
	delete(s.scopes, k)
}

func (s *Store) requestScope(b *abi.Builder, k scopeKey) {
	s.scopes[k].requested = true
	id := b.Read(stmtDocumentsByScope, []abi.Value{abi.Text(k.kind.String()), abi.Int(k.owner)})
	s.pending = append(s.pending, pendingRead{id: id, kind: rkScopeDocs, scope: k})
}

// scopeDocuments returns the live documents of a loaded scope by ID.
func (s *Store) scopeDocuments(k scopeKey) []*LiveDocument {
	var ids []int64
	for id, d := range s.docs {
		if d.retired {
			continue
		}
		if dk, do := d.rec.Scope(); dk == k.kind && do == k.owner {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	out := make([]*LiveDocument, len(ids))
	for i, id := range ids {
		d := s.docs[id]
		out[i] = &LiveDocument{Record: d.rec, Version: d.version, Value: d.value, DeltasSinceBase: d.deltas}
	}
	return out
}

// onScopeDocs handles the document records of a scope: each becomes a docState and asks for its revisions.
func (s *Store) onScopeDocs(b *abi.Builder, k scopeKey, rows [][]abi.Value) error {
	st := s.scopes[k]
	for _, row := range rows {
		d, err := rec.ParseDocument(row[0].Bytes)
		if err != nil {
			return err
		}
		if _, dup := s.docs[d.ID()]; dup {
			continue
		}
		s.docs[d.ID()] = &docState{rec: d}
		s.addrs[d.Address().AddressKey()] = d.ID()
		st.outstanding++
		id := b.Read(stmtRevisions, []abi.Value{abi.Int(d.ID()), abi.Int(maxSafe), abi.Int(d.ID()), abi.Int(maxSafe)})
		s.pending = append(s.pending, pendingRead{id: id, kind: rkRevisions, scope: k, doc: d.ID()})
	}
	if st.outstanding == 0 {
		st.loaded = true
	}
	return nil
}

func (s *Store) onRevisions(k scopeKey, docID int64, rows [][]abi.Value) error {
	st := s.scopes[k]
	ver, val, deltas, last, err := materialize(docID, rows)
	if err != nil {
		return err
	}
	if d := s.docs[docID]; d != nil {
		d.version, d.value, d.deltas, d.lastSeq = ver, val, deltas, last
		d.size = estimateSize(rows)
	}
	st.outstanding--
	if st.outstanding == 0 {
		st.loaded = true
	}
	return nil
}

func estimateSize(rows [][]abi.Value) int {
	n := 0
	for _, r := range rows {
		n += len(r[3].Bytes)
	}
	return n
}

// materialize is materializeDocument's revision replay (storage.ts): the newest base at or below the bound, then every
// later revision, which must be a delta of the base's version. rows are (seq, kind, version, content) in seq order.
func materialize(docID int64, rows [][]abi.Value) (version int, value *jv.Object, deltas int, lastSeq int64, err error) {
	if len(rows) == 0 || string(rows[0][1].Bytes) != "base" {
		return 0, nil, 0, 0, &plainError{"Document " + itoa(docID) + " is missing a required base"}
	}
	baseVersion := int64(rows[0][2].Number())
	v, perr := jv.Parse(rows[0][3].Bytes)
	if perr != nil {
		return 0, nil, 0, 0, perr
	}
	obj, _ := v.(*jv.Object)
	var cur any = obj
	lastSeq = int64(rows[0][0].Number())
	for _, r := range rows[1:] {
		if string(r[1].Bytes) != "delta" || int64(r[2].Number()) != baseVersion {
			return 0, nil, 0, 0, &plainError{"Document " + itoa(docID) + " crosses a stored version boundary without a base"}
		}
		opsValue, perr := jv.Parse(r[3].Bytes)
		if perr != nil {
			return 0, nil, 0, 0, perr
		}
		ops, derr := delta.DecodeOps(opsValue)
		if derr != nil {
			return 0, nil, 0, 0, derr
		}
		if cur, derr = delta.Apply(cur, ops); derr != nil {
			return 0, nil, 0, 0, derr
		}
		lastSeq = int64(r[0].Number())
	}
	obj, _ = cur.(*jv.Object)
	return int(baseVersion), obj, len(rows) - 1, lastSeq, nil
}

// EnsureDocument returns incarnation id materialised at a point (at <= 0 is "current"), for a historical read or a
// document of a scope the store has not loaded. A nil document with a nil error means absent or not alive at the point.
// ErrPending means reads were emitted.
func (s *Store) EnsureDocument(b *abi.Builder, id int64, at int64) (*LiveDocument, error) {
	if d := s.docs[id]; d != nil && d.value != nil && s.currentSuffices(d, at) {
		if !d.rec.AliveAt(at <= 0, at) {
			return nil, nil
		}
		return &LiveDocument{Record: d.rec, Version: d.version, Value: d.value, DeltasSinceBase: d.deltas}, nil
	}
	return s.ensureAsOf(b, id, at)
}

func (s *Store) ensureAsOf(b *abi.Builder, id, at int64) (*LiveDocument, error) {
	if at < 0 {
		at = 0
	}
	for _, c := range s.docLRU {
		if c.id == id && c.at == at {
			if c.haveRec && c.haveRevs {
				return c.doc, c.err
			}
			return nil, ErrPending
		}
	}
	c := &asOfDoc{id: id, at: at}
	if len(s.docLRU) >= asOfCacheSize {
		s.docLRU = append(s.docLRU[:0], s.docLRU[1:]...)
	}
	s.docLRU = append(s.docLRU, c)
	upper := maxSafe
	if at > 0 {
		upper = at
	}
	rid := b.Read(stmtDocumentByID, []abi.Value{abi.Int(id)})
	s.pending = append(s.pending, pendingRead{id: rid, kind: rkAsOfRecord, asof: c})
	vid := b.Read(stmtRevisions, []abi.Value{abi.Int(id), abi.Int(upper), abi.Int(id), abi.Int(upper)})
	s.pending = append(s.pending, pendingRead{id: vid, kind: rkAsOfRevisions, asof: c})
	return nil, ErrPending
}

// finishAsOf runs materializeDocument once both reads of an asOfDoc have arrived.
func (c *asOfDoc) finish() {
	if len(c.record) == 0 {
		c.doc = nil
		return
	}
	d, err := rec.ParseDocument(c.record)
	if err != nil {
		c.err = err
		return
	}
	if c.at > 0 && d.CurrentOnly() {
		c.err = &RequestError{"Document " + itoa(c.id) + " does not retain historical content"}
		return
	}
	if !d.AliveAt(c.at <= 0, c.at) {
		return
	}
	ver, val, deltas, _, err := materialize(c.id, c.revs)
	if err != nil {
		c.err = err
		return
	}
	c.doc = &LiveDocument{Record: d, Version: ver, Value: val, DeltasSinceBase: deltas}
}

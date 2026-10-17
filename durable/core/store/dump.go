// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
)

// Revision is one document_revisions row.
type Revision struct {
	Seq     int64
	Base    bool
	Version int
	Content any // the base value, or the delta's Chord Op[] JSON value
}

// DumpEntry is one entries row. Raw is the stored record text, which the dump holds without parsing it: opening a long
// history costs a copy of its bytes, not a tree per record.
type DumpEntry struct {
	ID, Conversation, Seq int64
	Head                  bool
	Raw                   []byte
}

// DumpTask is one tasks row: the columns every scan filters on, and the stored record text.
type DumpTask struct {
	ID, Conversation  int64
	Kind, Status      string
	Abort, Background bool
	Raw               []byte
}

// DumpSubmission is one submissions row: Request is the requestId, when the row has one.
type DumpSubmission struct {
	ID, Conversation int64
	Request          string
	HasRequest       bool
	Status           string
	Raw              []byte
}

// Dump is every record of the store, in ID order: what LoadAll reads after cold open.
type Dump struct {
	Conversations []*jv.Object
	Entries       []DumpEntry
	Tasks         []DumpTask
	Submissions   []DumpSubmission
	Documents     []*jv.Object
	Revisions     map[int64][]Revision // by document ID, in seq order
}

const dumpTables = 6

// dumpPage is how many rows of the large tables (entries, tasks, submissions) one load read returns: the host answers a
// page at a time, so opening a long history holds a page of rows at once, not the whole table.
const (
	dumpPage     = 1024
	dumpPageText = "1024"
)

type dumpState struct {
	live            bool // entries, terminal tasks and settled submissions stay in storage (LoadAll)
	asked, answered int
	out             Dump
	err             error
}

// LoadAll reads every Pi table after Open and seeds the store's commit validation with it (record_ids claims of live
// tasks and submissions, every conversation, every document incarnation with its current value). With live set it reads
// no entries and only the live tasks and submissions: a core that reads the rest on demand (ABI section 4.2, pig.read). It
// returns ErrPending after emitting reads, and the dump once every table has arrived. A store Open initialised needs no
// reads.
func (s *Store) LoadAll(b *abi.Builder, live bool) (*Dump, error) {
	if s.dump == nil {
		s.dump = &dumpState{live: live, out: Dump{Revisions: map[int64][]Revision{}}}
		if s.fresh {
			s.dump.asked = dumpTables
			s.dump.answered = dumpTables
		} else {
			for i := range dumpTables {
				if live && uint16(i)+stmtDumpConversations == stmtDumpEntries {
					continue // entries: read when a step needs them
				}
				var params []abi.Value
				if pagedDump(i) {
					params = []abi.Value{abi.Int(0)}
				}
				id := b.Read(dumpStmt(i, live), params)
				s.pending = append(s.pending, pendingRead{id: id, kind: rkDump, doc: int64(i)})
				s.dump.asked++
			}
			return nil, ErrPending
		}
	}
	d := s.dump
	if d.err != nil {
		return nil, d.err
	}
	if d.answered < d.asked {
		return nil, ErrPending
	}
	return &d.out, nil
}

// unquote is the string a JSON.stringify(string) column holds.
func unquote(b []byte) string {
	if n := len(b); n >= 2 && b[0] == '"' && b[n-1] == '"' && !slices.Contains(b, '\\') {
		return string(b[1 : n-1])
	}
	if v, err := jv.Parse(b); err == nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return string(b)
}

// ReleaseDump drops the rows LoadAll gathered, once the caller has taken what it keeps from them.
func (s *Store) ReleaseDump() {
	if s.dump != nil {
		s.dump.out = Dump{}
	}
}

// dumpStmt is the statement that reads dump table i: the live tasks and submissions only when live is set.
func dumpStmt(i int, live bool) uint16 {
	switch stmt := uint16(i) + stmtDumpConversations; {
	case live && stmt == stmtDumpTasks:
		return stmtDumpLiveTasks
	case live && stmt == stmtDumpSubmissions:
		return stmtDumpLiveSubmissions
	default:
		return stmt
	}
}

// pagedDump reports whether dump table i is read a page at a time: its first column is the id the next page follows.
func pagedDump(i int) bool {
	switch uint16(i) + stmtDumpConversations {
	case stmtDumpEntries, stmtDumpTasks, stmtDumpSubmissions:
		return true
	}
	return false
}

func (s *Store) onDump(b *abi.Builder, p pendingRead, rows [][]abi.Value) error {
	d := s.dump
	if pagedDump(int(p.doc)) && len(rows) == dumpPage {
		id := b.Read(dumpStmt(int(p.doc), d.live), []abi.Value{abi.Int(int64(rows[len(rows)-1][0].Number()))})
		s.pending = append(s.pending, pendingRead{id: id, kind: rkDump, doc: p.doc})
	} else {
		d.answered++
	}
	parse := func(b []byte) *jv.Object {
		v, err := jv.Parse(b)
		if err != nil {
			if d.err == nil {
				d.err = err
			}
			return nil
		}
		o, _ := v.(*jv.Object)
		return o
	}
	switch uint16(p.doc) + stmtDumpConversations {
	case stmtDumpConversations:
		for _, r := range rows {
			if o := parse(r[0].Bytes); o != nil {
				d.out.Conversations = append(d.out.Conversations, o)
			}
		}
	case stmtDumpEntries:
		d.out.Entries = slices.Grow(d.out.Entries, len(rows))
		for _, r := range rows {
			d.out.Entries = append(d.out.Entries, DumpEntry{
				ID: int64(r[0].Number()), Conversation: int64(r[1].Number()), Head: r[2].Number() != 0, Seq: int64(r[3].Number()), Raw: r[4].Bytes,
			})
		}
	case stmtDumpTasks:
		d.out.Tasks = slices.Grow(d.out.Tasks, len(rows))
		for _, r := range rows {
			d.out.Tasks = append(d.out.Tasks, DumpTask{
				ID: int64(r[0].Number()), Conversation: int64(r[1].Number()), Kind: unquote(r[2].Bytes), Status: string(r[3].Bytes),
				Abort: r[4].Number() != 0, Background: r[5].Number() != 0, Raw: r[6].Bytes,
			})
		}
	case stmtDumpSubmissions:
		d.out.Submissions = slices.Grow(d.out.Submissions, len(rows))
		for _, r := range rows {
			s := DumpSubmission{ID: int64(r[0].Number()), Conversation: int64(r[1].Number()), Status: string(r[3].Bytes), Raw: r[4].Bytes}
			if r[2].Kind != abi.ValueNull {
				// The request_id column is JSON.stringify(requestId).
				s.Request, s.HasRequest = unquote(r[2].Bytes), true
			}
			d.out.Submissions = append(d.out.Submissions, s)
		}
	case stmtDumpDocuments:
		for _, r := range rows {
			if o := parse(r[0].Bytes); o != nil {
				d.out.Documents = append(d.out.Documents, o)
			}
		}
	case stmtDumpRevisions:
		for _, r := range rows {
			id := int64(r[0].Number())
			v, err := jv.Parse(r[4].Bytes)
			if err != nil {
				return err
			}
			d.out.Revisions[id] = append(d.out.Revisions[id], Revision{
				Seq: int64(r[1].Number()), Base: string(r[2].Bytes) == "base", Version: int(r[3].Number()), Content: v,
			})
		}
	}
	if d.err != nil {
		return d.err
	}
	if d.answered == d.asked {
		return s.seed(&d.out)
	}
	return nil
}

// seed registers the dump with commit validation: every conversation and document ID, the current value of every
// alive document, and the record_ids claims Open would hold for the live tasks and submissions.
func (s *Store) seed(d *Dump) error {
	for _, c := range d.Conversations {
		if id, ok := c.Get("id"); ok {
			s.conversat[int64(id.(float64))] = true
		}
	}
	for i := range d.Tasks {
		if t := &d.Tasks[i]; t.Status != "terminal" {
			s.claimed[t.ID] = claimTask
		}
	}
	for i := range d.Submissions {
		switch sub := &d.Submissions[i]; sub.Status {
		case "done", "unanswered":
		default:
			s.claimed[sub.ID] = claimSubmission
		}
	}
	for _, o := range d.Documents {
		dr, err := rec.ParseDocument(jv.Append(nil, o))
		if err != nil {
			return err
		}
		ds := &docState{rec: dr}
		if _, retired := dr.RetiredAt(); retired {
			ds.retired = true
		} else {
			revs := d.Revisions[dr.ID()]
			rows := make([][]abi.Value, 0, len(revs))
			start := 0
			for i, r := range revs {
				if r.Base {
					start = i
				}
			}
			for _, r := range revs[start:] {
				kind := "delta"
				if r.Base {
					kind = "base"
				}
				rows = append(rows, []abi.Value{abi.Int(r.Seq), abi.Text(kind), abi.Int(int64(r.Version)), abi.TextBytes(jv.Append(nil, r.Content))})
			}
			if len(rows) != 0 {
				ver, val, deltas, last, err := materialize(dr.ID(), rows)
				if err != nil {
					return err
				}
				ds.version, ds.value, ds.deltas, ds.lastSeq, ds.size = ver, val, deltas, last, estimateSize(rows)
			}
			s.addrs[dr.Address().AddressKey()] = dr.ID()
		}
		s.docs[dr.ID()] = ds
		kind, owner := dr.Scope()
		s.markScopeLoaded(kind, owner)
	}
	return nil
}

// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
)

type openState uint8

const (
	openNone openState = iota
	openRunning
	openDone
)

type readKind uint8

const (
	rkSchema readKind = iota + 1
	rkMetadata
	rkLiveTasks
	rkLiveSubs
	rkConversation // open: a conversation record of the live state
	rkScopeDocs
	rkRevisions
	rkAsOfRecord
	rkAsOfRevisions
	rkTask
	rkNeed
	rkSidecarHeader
	rkSidecarBody
	rkDump
)

// pendingRead is a read the store emitted and has not seen answered.
type pendingRead struct {
	id    uint32
	kind  readKind
	scope scopeKey
	doc   int64
	asof  *asOfDoc
	need  history.Need
	h     *history.Store
}

// errMissing is the failure of a store with no metadata row. Read-only.
var errMissing = errors.New("Durable SQLite metadata is missing")

// Open runs cold open: it emits the first reads into b and returns ErrPending, or returns the loaded Live once the rows
// it asked for have been delivered through Rows. A newer schema returns SchemaNewerError. Open reads the live state
// only: the metadata row, the non-terminal tasks and submissions, the conversations they live in and the live documents
// of the Session, those conversations and those tasks (ADR D5). Transcripts load per conversation through Load.
func (s *Store) Open(b *abi.Builder) (*Live, error) {
	switch s.open {
	case openNone:
		s.open = openRunning
		// applySqliteMigrations: the schema table exists before the version is read, so the schema is found by identifier,
		// never by a table name held as data, and an adapter that renames Pi's tables in the SQL text (a table prefix)
		// renames every name the store relies on. Pi seeds the version row before reading it; a missing row reads as version 0, so the seed heads the migration commit and every row of a new database is written in one transaction, as Pi's.
		// Only the empty schema table is created one transaction before Pi would; a store left with it and no row is migrated on the next open.
		b.Commit(-1, []abi.Stmt{{SQL: stmtSchemaTable}})
		id := b.Read(stmtSchemaVersion, nil)
		s.pending = append(s.pending, pendingRead{id: id, kind: rkSchema})
		return nil, ErrPending
	case openRunning:
		return nil, ErrPending
	}
	if s.openErr != nil {
		return nil, s.openErr
	}
	return &s.live, nil
}

// Rows delivers a rows event. It returns ErrPending when it emitted further reads, nil when every pending load is
// complete.
func (s *Store) Rows(b *abi.Builder, answers []abi.ReadRows) error {
	for i := range answers {
		at := -1
		for j := range s.pending {
			if s.pending[j].id == answers[i].ID {
				at = j
				break
			}
		}
		if at < 0 {
			return errors.New("store: rows for a read that was not asked")
		}
		p := s.pending[at]
		s.pending = slices.Delete(s.pending, at, at+1)
		if err := s.answer(b, p, answers[i].Rows); err != nil {
			s.pending = s.pending[:0]
			if s.open == openRunning {
				s.open, s.openErr = openDone, err
				return nil
			}
			return err
		}
	}
	if len(s.pending) != 0 {
		return ErrPending
	}
	if s.open == openRunning {
		s.finishOpen()
	}
	return nil
}

func (s *Store) answer(b *abi.Builder, p pendingRead, rows [][]abi.Value) error {
	switch p.kind {
	case rkSchema:
		return s.onSchema(b, rows)
	case rkMetadata:
		return s.onMetadata(rows)
	case rkLiveTasks:
		return s.onLiveTasks(b, rows)
	case rkLiveSubs:
		return s.onLiveSubmissions(b, rows)
	case rkConversation:
		return s.onOpenConversation(rows)
	case rkScopeDocs:
		return s.onScopeDocs(b, p.scope, rows)
	case rkRevisions:
		return s.onRevisions(p.scope, p.doc, rows)
	case rkAsOfRecord:
		p.asof.haveRec = true
		if len(rows) != 0 {
			p.asof.record = append([]byte(nil), rows[0][0].Bytes...)
		}
		if p.asof.haveRevs {
			p.asof.finish()
		}
		return nil
	case rkAsOfRevisions:
		p.asof.haveRevs = true
		p.asof.revs = rows
		if p.asof.haveRec {
			p.asof.finish()
		}
		return nil
	case rkTask:
		return s.onTask(p.doc, rows)
	case rkNeed:
		return s.onNeed(p.h, p.need, rows)
	case rkSidecarHeader, rkSidecarBody:
		return s.onSidecar(b, p, rows)
	case rkDump:
		return s.onDump(b, p, rows)
	}
	return errors.New("store: unknown pending read")
}

func (s *Store) onSchema(b *abi.Builder, rows [][]abi.Value) error {
	version := int64(0)
	if len(rows) != 0 {
		version = int64(rows[0][0].Number())
	}
	if version > SupportedSchemaVersion {
		return SchemaNewerError{Version: version}
	}
	if version == 0 || version < SupportedSchemaVersion {
		// Version 0 (or no row) is a new database or a migration that never committed: applySqliteMigrations seeds the
		// row, runs every migration after the stored version and records each version, in one commit.
		if version == 0 && !s.opts.EmptyNew {
			return errors.New("SQLite schema metadata is missing")
		}
		list := make([]abi.Stmt, 0, len(sqlSchemaV1)+len(sqlSchemaV2)+3)
		if version == 0 {
			list = append(list, abi.Stmt{SQL: stmtSchemaSeed})
			for i := range sqlSchemaV1 {
				list = append(list, abi.Stmt{SQL: stmtMigrationFirst + uint16(i)})
			}
			list = append(list, abi.Stmt{SQL: stmtSchemaVersionSet, Params: []abi.Value{abi.Int(1)}})
		}
		for i := range sqlSchemaV2 {
			list = append(list, abi.Stmt{SQL: stmtMigrationFirst + uint16(len(sqlSchemaV1)+i)})
		}
		list = append(list, abi.Stmt{SQL: stmtSchemaVersionSet, Params: []abi.Value{abi.Int(SupportedSchemaVersion)}})
		b.Commit(-1, list)
		if version != 0 {
			// A migrated store keeps its rows: the open goes on to read them.
			id := b.Read(stmtMetadataRead, nil)
			s.pending = append(s.pending, pendingRead{id: id, kind: rkMetadata})
			s.startLive(b)
			return nil
		}
		s.fresh = true
		s.live = Live{NextID: s.nextID, NextSeq: s.nextSeq}
		s.markScopeLoaded(rec.ScopeSession, 0)
		return nil
	}
	id := b.Read(stmtMetadataRead, nil)
	s.pending = append(s.pending, pendingRead{id: id, kind: rkMetadata})
	s.startLive(b)
	return nil
}

// onMetadata reads next_id and next_seq and starts the live-state reads.
func (s *Store) onMetadata(rows [][]abi.Value) error {
	if len(rows) == 0 {
		return errMissing
	}
	nextID, err := strconv.ParseInt(string(rows[0][0].Bytes), 10, 64)
	if err != nil {
		return err
	}
	s.nextID, s.nextSeq = nextID, int64(rows[0][1].Number())
	s.live.NextID, s.live.NextSeq = s.nextID, s.nextSeq
	return nil
}

// startLive asks for the live state; its reads do not depend on the metadata row, so they share a step with it.
func (s *Store) startLive(b *abi.Builder) {
	id := b.Read(stmtLiveTasks, nil)
	s.pending = append(s.pending, pendingRead{id: id, kind: rkLiveTasks})
	id = b.Read(stmtLiveSubmissions, nil)
	s.pending = append(s.pending, pendingRead{id: id, kind: rkLiveSubs})
	id = b.Read(stmtConversation, []abi.Value{abi.Int(1)})
	s.pending = append(s.pending, pendingRead{id: id, kind: rkConversation, doc: 1})
	s.conversat[1] = false
	s.markScopeRequested(b, scopeKey{rec.ScopeSession, 0})
	s.markScopeRequested(b, scopeKey{rec.ScopeConversation, 1})
}

func (s *Store) markScopeRequested(b *abi.Builder, k scopeKey) {
	if s.scopes[k] == nil {
		s.scopes[k] = &scopeState{}
	}
	if !s.scopes[k].requested {
		s.requestScope(b, k)
	}
}

func (s *Store) onLiveTasks(b *abi.Builder, rows [][]abi.Value) error {
	convs := map[int64]bool{}
	for _, r := range rows {
		t, err := rec.ParseTask(r[0].Bytes)
		if err != nil {
			return err
		}
		s.live.Tasks = append(s.live.Tasks, t)
		s.claimed[t.ID()] = claimTask
		convs[t.ConversationID()] = true
		s.markScopeRequested(b, scopeKey{rec.ScopeTask, t.ID()})
	}
	s.openConversations(b, convs)
	return nil
}

func (s *Store) onLiveSubmissions(b *abi.Builder, rows [][]abi.Value) error {
	convs := map[int64]bool{}
	for _, r := range rows {
		sub, err := rec.ParseSubmission(r[0].Bytes)
		if err != nil {
			return err
		}
		s.live.Submissions = append(s.live.Submissions, sub)
		s.claimed[sub.ID()] = claimSubmission
		convs[sub.ConversationID()] = true
	}
	s.openConversations(b, convs)
	return nil
}

// openConversations asks for the records of conversations the live state lives in, and their live documents.
func (s *Store) openConversations(b *abi.Builder, convs map[int64]bool) {
	ids := make([]int64, 0, len(convs))
	for id := range convs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, asked := s.conversat[id]; asked {
			continue
		}
		s.conversat[id] = false
		rid := b.Read(stmtConversation, []abi.Value{abi.Int(id)})
		s.pending = append(s.pending, pendingRead{id: rid, kind: rkConversation, doc: id})
		s.markScopeRequested(b, scopeKey{rec.ScopeConversation, id})
	}
}

func (s *Store) onOpenConversation(rows [][]abi.Value) error {
	if len(rows) == 0 {
		return nil // the root conversation of an empty store is created by the Session
	}
	c, err := rec.ParseConversation(rows[0][0].Bytes)
	if err != nil {
		return err
	}
	s.conversat[c.ID()] = true
	s.live.Conversations = append(s.live.Conversations, c)
	return nil
}

func (s *Store) finishOpen() {
	slices.SortFunc(s.live.Conversations, func(a, b *rec.Conversation) int { return int(a.ID() - b.ID()) })
	var ids []int64
	for id, d := range s.docs {
		if !d.retired {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		d := s.docs[id]
		s.live.Documents = append(s.live.Documents, &LiveDocument{Record: d.rec, Version: d.version, Value: d.value, DeltasSinceBase: d.deltas})
	}
	s.open = openDone
}

// Load emits the reads of a history.Need (the transcript range history asks for) and returns ErrPending; the rows
// return through Rows, which hands them to history.Store.AddRow and SupplyBounds.
func (s *Store) Load(b *abi.Builder, h *history.Store, need *history.Need) error {
	params := need.Params()
	vals := make([]abi.Value, len(params))
	for i, p := range params {
		vals[i] = abi.Int(p)
	}
	var sql uint16
	switch need.Kind {
	case history.NeedConversation:
		sql = stmtConversation
	case history.NeedBounds:
		sql = stmtBounds
	case history.NeedEntries:
		sql = stmtEntries
	default:
		return errors.New("store: unknown need")
	}
	id := b.Read(sql, vals)
	s.pending = append(s.pending, pendingRead{id: id, kind: rkNeed, need: *need, h: h})
	return ErrPending
}

func (s *Store) onNeed(h *history.Store, n history.Need, rows [][]abi.Value) error {
	switch n.Kind {
	case history.NeedConversation:
		if len(rows) == 0 {
			return &RequestError{"Unknown conversation: " + itoa(n.Conv)}
		}
		c, err := history.ParseConversation(rows[0][0].Bytes)
		if err != nil {
			return err
		}
		h.AddConversation(c, false)
		s.conversat[c.ID] = true
	case history.NeedBounds:
		var maxID, markerID, markerHead int64
		var hasMax, hasMarker bool
		if len(rows) != 0 {
			r := rows[0]
			if r[0].Kind != abi.ValueNull {
				hasMax, maxID = true, int64(r[0].Number())
			}
			if r[1].Kind != abi.ValueNull {
				hasMarker, markerID, markerHead = true, int64(r[1].Number()), int64(r[2].Number())
			}
		}
		h.SupplyBounds(n.Conv, n.Max, hasMax, maxID, hasMarker, markerID, markerHead)
	case history.NeedEntries:
		h.BeginLoad(n.Conv, n.Min, n.Max)
		for _, r := range rows {
			if err := h.AddRow(n.Conv, int64(r[0].Number()), int64(r[1].Number()), r[2].Bytes); err != nil {
				return err
			}
		}
	}
	return nil
}

const taskCacheSize = 64

// EnsureTask loads a committed task that is not live (a terminal task for getTask or outcomes). ErrPending when reads
// were emitted. A task the store has never seen and the database lacks returns (nil, nil).
func (s *Store) EnsureTask(b *abi.Builder, id int64) (*rec.Task, error) {
	for _, t := range s.taskLRU {
		if t == nil {
			continue
		}
		if t.ID() == id {
			return t, nil
		}
	}
	for _, pr := range s.pending {
		if pr.kind == rkTask && pr.doc == id {
			return nil, ErrPending
		}
	}
	if s.absent[id] {
		return nil, nil
	}
	rid := b.Read(stmtTaskByID, []abi.Value{abi.Int(id)})
	s.pending = append(s.pending, pendingRead{id: rid, kind: rkTask, doc: id})
	return nil, ErrPending
}

func (s *Store) onTask(id int64, rows [][]abi.Value) error {
	if len(rows) == 0 {
		if s.absent == nil {
			s.absent = map[int64]bool{}
		}
		s.absent[id] = true
		return nil
	}
	t, err := rec.ParseTask(rows[0][0].Bytes)
	if err != nil {
		return err
	}
	if len(s.taskLRU) >= taskCacheSize {
		s.taskLRU = append(s.taskLRU[:0], s.taskLRU[1:]...)
	}
	s.taskLRU = append(s.taskLRU, t)
	return nil
}

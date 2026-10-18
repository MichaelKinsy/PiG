// SPDX-License-Identifier: MIT

// Package store owns pi-durable's SQLite format inside the core: the SQL table, the rows every commit writes (CONTRACT
// section 3.1, row identity with pi-durable main), the single-writer guard, the in-memory metadata, cold open of the
// live state (ADR D5), loads of committed non-live records, and the pig_ sidecar (ADR D6). Transcripts (entries, forks,
// head markers, context derivation) live in package history; the store writes entry rows that history encodes and
// serves the reads history asks for (history.Need).
//
// The store never performs I/O. It emits statements into the session's abi.Builder: commits, and reads that the host
// answers with a rows event. A caller that needs state the store has not loaded calls the matching Ensure method; when
// it returns ErrPending the caller stops handling its event, the root core keeps the event, and re-delivers it after the
// rows arrive (see core.Session). No other package issues SQL.
//
// Rows written per commit are exactly pi-durable main's (src/storage/sqlite/storage.ts commit): the same statements in
// the same order, except that reads Pi makes (record_ids lookups, the metadata read, existence checks) are answered from
// the store's own state, and `INSERT OR IGNORE INTO record_ids` is left out for a task or submission the store has
// already claimed. The metadata update carries the single-writer guard `AND next_seq = ?` (ADR D13).
//
// Owner: dcore-store. Consumers: session (all writes and loads), turn (through session).
package store

import (
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
)

// ErrPending means the store emitted reads the caller's operation depends on. Read-only.
var ErrPending = errors.New("store: waiting for rows")

// SchemaNewerError is the refusal applySqliteMigrations gives a store whose durable_schema.version is newer than SupportedSchemaVersion.
type SchemaNewerError struct{ Version int64 }

func (e SchemaNewerError) Error() string {
	return "SQLite schema version " + strconv.FormatInt(e.Version, 10) + " is newer than supported version " + strconv.Itoa(SupportedSchemaVersion)
}

// SupportedSchemaVersion is the newest durable_schema.version this build writes and reads (migrations.ts
// CURRENT_SQLITE_SCHEMA_VERSION).
const SupportedSchemaVersion = 2

// RejectedError is pi-durable's StorageRejected: a commit the store refused before it changed anything.
type RejectedError struct{ Message string }

func (e *RejectedError) Error() string { return e.Message }

// SQL returns the sql_table: every statement the core issues, by statement ID. The texts of Pi's writes are copied
// from pi-durable's storage.ts so DO and node:sqlite plan them the same way; reads of record columns use
// CAST(record AS BLOB) (ABI section 4.3).
func SQL() []string { return sqlTable }

// Options are the open-time choices the open event carries.
type Options struct {
	Sidecar  Sidecar
	CoreID   string // layout identity the sidecar must carry; empty means the build's own
	Debug    bool   // rebuild from rows and compare on every cold open (RISKS R11)
	EmptyNew bool   // an empty database is initialised with Pi's schema (migrations.ts) instead of rejected
}

// Sidecar selects the cold-open path of ADR D6.
type Sidecar uint8

// Sidecar modes.
const (
	SidecarOff Sidecar = iota
	SidecarIndex
	SidecarSnapshot
)

// Live is the live state cold open loads (ADR D5) and hands to the session once: everything non-terminal.
type Live struct {
	NextID, NextSeq int64
	Conversations   []*rec.Conversation // the ones open needs: root and every conversation of a live task or submission
	Tasks           []*rec.Task         // status != terminal, ID order
	Submissions     []*rec.Submission   // queued or placed, ID order
	Documents       []*LiveDocument     // current, alive documents of the session, the loaded conversations and the live tasks
}

// LoadInto registers the loaded conversation records with the transcript store.
func (l *Live) LoadInto(h *history.Store) {
	for _, c := range l.Conversations {
		h.AddConversation(historyConv(c), false)
	}
}

// LiveDocument is one materialised document (base plus delta tail, spec §10 document()).
type LiveDocument struct {
	Record          *rec.Document
	Version         int
	Value           *jv.Object
	DeltasSinceBase int
}

// Store is one Session's view of its database.
type Store struct {
	opts            Options
	nextID, nextSeq int64
	pending         []pendingRead

	open      openState
	fresh     bool // the database had no schema at open
	live      Live
	openErr   error
	conversat map[int64]bool // conversations whose record the store loaded or created

	docs   map[int64]*docState
	addrs  map[string]int64 // address key -> current incarnation, for loaded scopes
	scopes map[scopeKey]*scopeState

	claimed  map[int64]claim // task and submission IDs already in record_ids
	taskLRU  []*rec.Task     // EnsureTask results, newest last
	absent   map[int64]bool  // task IDs EnsureTask found missing
	docLRU   []*asOfDoc      // EnsureDocument results at a point
	sidecars map[int64]*sideState
	dump     *dumpState        // LoadAll
	kinds    map[string][]byte // tasks.kind column bytes by kind (kindColumn)
	texts    map[string][]byte // fixed column values' bytes (text)
}

type claim uint8

const (
	claimTask claim = iota + 1
	claimSubmission
)

// New returns a store that has read nothing.
func New(opts Options) *Store {
	return &Store{
		opts:      opts,
		nextID:    2,
		nextSeq:   1,
		conversat: map[int64]bool{},
		docs:      map[int64]*docState{},
		addrs:     map[string]int64{},
		scopes:    map[scopeKey]*scopeState{},
		claimed:   map[int64]claim{},
		sidecars:  map[int64]*sideState{},
	}
}

// MintID allocates from the global ID namespace like SqliteStorage.mintId: an in-memory counter that the next commit
// persists as max(stored next_id, counter, max written id + 1).
func (s *Store) MintID() int64 { id := s.nextID; s.nextID++; return id }

// NextID is the ID the next MintID returns.
func (s *Store) NextID() int64 { return s.nextID }

// NextSeq is the next_seq the next Pi commit consumes.
func (s *Store) NextSeq() int64 { return s.nextSeq }

// Footprint reports resident bytes of live documents for mem_stats.
func (s *Store) Footprint() (docs uint64) {
	for _, d := range s.docs {
		docs += uint64(d.size)
	}
	return docs
}

// JSON is a value of package jv (nil, bool, float64, string, []any or *jv.Object; ECMAScript property order).
type JSON = any

// historyConv converts a conversation record to the transcript store's view of it.
func historyConv(c *rec.Conversation) history.ConvRecord {
	out := history.ConvRecord{ID: c.ID()}
	out.ParentConv, out.ParentAt, out.HasParent = c.Parent()
	if conv, ok := c.OwnerConversation(); ok {
		if task, ok := c.OwnerTask(); ok {
			out.HasOwner, out.OwnerConv, out.OwnerTask = true, conv, task
		}
	}
	return out
}

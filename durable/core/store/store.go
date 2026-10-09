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
// Owner: dcore-store. Consumers: session (all writes and loads), turn (through session).
package store

import (
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
)

// ErrPending means the store emitted reads the caller's operation depends on. Read-only.
var ErrPending = errors.New("store: waiting for rows")

// SchemaNewerError is the refusal applySqliteMigrations gives a store whose durable_schema.version is newer than 1.
type SchemaNewerError struct{ Version int64 }

func (e SchemaNewerError) Error() string {
	return "Durable SQLite schema version " + strconv.FormatInt(e.Version, 10) + " is newer than supported version 1"
}

// SQL returns the sql_table: every statement the core issues, by statement ID. The texts of Pi's writes are copied
// from pi-durable's storage.ts so DO and node:sqlite plan them the same way; reads of record columns use
// CAST(record AS BLOB) (ABI section 4.3).
func SQL() []string { return sqlTable }

// sqlTable is read-only. Statement IDs are positions; dcore-store appends Pi's statements here.
var sqlTable = []string{}

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
	Conversations   []rec.Conversation // the ones open needs: root and every owner of a live task
	Tasks           []rec.Task         // status != terminal, ID order
	Submissions     []rec.Submission   // queued or placed, ID order
	Documents       []LiveDocument     // current, alive documents of the loaded conversations
}

// LiveDocument is one materialised document (base plus delta tail, spec §10 document()).
type LiveDocument struct {
	Record          rec.Document
	Version         int
	Value           JSON
	DeltasSinceBase int
}

// Store is one Session's view of its database.
type Store struct {
	opts            Options
	nextID, nextSeq int64
}

// New returns a store that has read nothing.
func New(opts Options) *Store {
	return &Store{opts: opts}
}

// Open runs cold open: it emits the first reads into b and returns ErrPending, or returns the loaded Live once the rows
// it asked for have been delivered through Rows. A newer schema returns SchemaNewerError.
func (s *Store) Open(b *abi.Builder) (*Live, error) { panic("store.Store.Open: dcore-store") }

// Rows delivers a rows event. It returns ErrPending when it emitted further reads, nil when every pending load is
// complete.
func (s *Store) Rows(b *abi.Builder, answers []abi.ReadRows) error {
	panic("store.Store.Rows: dcore-store")
}

// Load emits the reads of a history.Need (the transcript range history asks for) and returns ErrPending; the rows
// return through Rows, which hands them to history.Store.AddRow and SupplyBounds.
func (s *Store) Load(b *abi.Builder, h *history.Store, need *history.Need) error {
	panic("store.Store.Load: dcore-store")
}

// EnsureTask and EnsureDocument load committed records that are not live (a terminal task for getTask or outcomes, a
// document at a historical point). ErrPending when reads were emitted.
func (s *Store) EnsureTask(b *abi.Builder, id int64) (*rec.Task, error) {
	panic("store.Store.EnsureTask: dcore-store")
}
func (s *Store) EnsureDocument(b *abi.Builder, id int64, at int64) (*LiveDocument, error) {
	panic("store.Store.EnsureDocument: dcore-store")
}

// MintID allocates from the global ID namespace like SqliteStorage.mintId: an in-memory counter that the next commit
// persists as max(stored next_id, counter, max written id + 1).
func (s *Store) MintID() int64 { id := s.nextID; s.nextID++; return id }

// NextSeq is the next_seq the next Pi commit consumes.
func (s *Store) NextSeq() int64 { return s.nextSeq }

// Tx is one Pi commit under construction: the StorageWrite batch of spec §10, in call order.
type Tx struct{}

// Begin starts a Pi commit.
func (s *Store) Begin() *Tx { return &Tx{} }

// The StorageWrite kinds (spec §10). Records are encoded by rec; the Tx keeps the bytes.
func (tx *Tx) Conversation(c *rec.Conversation) { panic("store.Tx.Conversation: dcore-store") }

// Entry writes one entry row; record is the bytes history.AppendEntryRecord produced. After Commit, the session appends
// the same record to the history.Store with the returned seq.
func (tx *Tx) Entry(id, conversationID, head int64, record []byte) {
	panic("store.Tx.Entry: dcore-store")
}
func (tx *Tx) Task(t *rec.Task)               { panic("store.Tx.Task: dcore-store") }
func (tx *Tx) Submission(sub *rec.Submission) { panic("store.Tx.Submission: dcore-store") }

// DocumentCreate writes a new incarnation with its base; DocumentCopy copies a source at a point (fork); DocumentBase
// and DocumentDelta change content (ops is Chord Op[] JSON); DocumentRetire retires.
func (tx *Tx) DocumentCreate(d *rec.Document, version int, base JSON) {
	panic("store.Tx.DocumentCreate: dcore-store")
}
func (tx *Tx) DocumentCopy(d *rec.Document, source int64, at int64) {
	panic("store.Tx.DocumentCopy: dcore-store")
}
func (tx *Tx) DocumentBase(id int64, version int, base JSON) {
	panic("store.Tx.DocumentBase: dcore-store")
}
func (tx *Tx) DocumentDelta(id int64, version int, ops []byte) {
	panic("store.Tx.DocumentDelta: dcore-store")
}
func (tx *Tx) DocumentRetire(id int64) { panic("store.Tx.DocumentRetire: dcore-store") }

// Commit validates tx as SqliteStorage.commit does (ID claims, document actions; a failure is Pi's StorageRejected and
// changes nothing), emits one abi.Commit with Pi's rows in Pi's order and the guarded metadata update, applies the
// commit to the store's in-memory state (transcripts, next_id, next_seq), and returns the sequence it consumed.
func (s *Store) Commit(b *abi.Builder, tx *Tx) (seq int64, err error) {
	panic("store.Store.Commit: dcore-store")
}

// Idle is called by the session at an idle boundary (a run settled, nothing live): the store writes due sidecar chunks
// as a sidecar-only commit (seq -1).
func (s *Store) Idle(b *abi.Builder) { panic("store.Store.Idle: dcore-store") }

// Footprint reports resident bytes of live documents for mem_stats.
func (s *Store) Footprint() (docs uint64) { return 0 }

// JSON is a value of package jv (nil, bool, float64, string, []any or *jv.Object; ECMAScript property order), the
// session lane's ordered JSON model at durable/core/jv.
type JSON = any

package store

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/next/store/schema.ts
//
// The store's own SQL schema (slice 6a §2): its metadata, the recorded table definitions, the documents engine's
// records and revisions, and the outbox; versioned by `ApplySqliteMigrations`. Declared tables are named
// `"<owner>:<table>"`, so no name here can meet one of them.
//
// Async contract: Pi's `applyStoreSchema` awaits its database calls in order; here the calls block in the same
// order, errors returned as Pi rejects. No context is taken (Pi takes none).

// Table names of the store's own tables (Pi's TABLES).
const (
	TableMeta      = "chord_meta"
	TableTables    = "chord_tables"
	TableDocuments = "chord_documents"
	TableRevisions = "chord_revisions"
	TableOutbox    = "chord_outbox"
)

// migrations is the schema history; never change an entry, append one (nothing is deployed yet: slice 6b changed
// entry 1 in place). `chord_meta` is one row: `last_seq`, the allocator's next id (D3; from 2, id 1 is Durable's root
// conversation), and the outbox setting with its floor (the seq it starts after, advanced by `trim`). A document
// record keeps its history rule (R1), its current base's version and fingerprint and a `revision` counter for the
// conditional `UPDATE`; the partial unique index refuses two live records at one address. Entry 2 adds the indexes
// of reads at a point (slice 6b §1.5): an address's incarnations by creation, an owner's namespace by id, a
// document's bases (a partial index: deltas write no entry).
var migrations = []sqlite.SqliteMigration{
	{
		Version: 1,
		Statements: []string{
			`CREATE TABLE ` + TableMeta + ` (
				singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
				last_seq INTEGER NOT NULL,
				next_id INTEGER NOT NULL,
				outbox INTEGER NOT NULL CHECK (outbox IN (0, 1)),
				outbox_floor INTEGER NOT NULL
			) STRICT`,
			`INSERT INTO ` + TableMeta + ` (singleton, last_seq, next_id, outbox, outbox_floor) VALUES (1, 0, 2, 0, 0)`,
			`CREATE TABLE ` + TableTables + ` (name TEXT PRIMARY KEY, definition TEXT NOT NULL) STRICT, WITHOUT ROWID`,
			`CREATE TABLE ` + TableDocuments + ` (
				id INTEGER PRIMARY KEY,
				owner TEXT NOT NULL,
				namespace TEXT NOT NULL,
				kind TEXT NOT NULL,
				key TEXT NOT NULL,
				history TEXT NOT NULL CHECK (history IN ('none', 'latest', 'rewindable')),
				created_at INTEGER NOT NULL,
				retired_at INTEGER,
				meta TEXT,
				revision INTEGER NOT NULL,
				version INTEGER NOT NULL,
				fingerprint TEXT NOT NULL
			) STRICT`,
			`CREATE UNIQUE INDEX chord_documents_live ON ` + TableDocuments + ` (owner, namespace, kind, key)
				WHERE retired_at IS NULL`,
			`CREATE TABLE ` + TableRevisions + ` (
				document_id INTEGER NOT NULL,
				seq INTEGER NOT NULL,
				kind TEXT NOT NULL CHECK (kind IN ('base', 'delta')),
				version INTEGER NOT NULL,
				content TEXT NOT NULL,
				PRIMARY KEY (document_id, seq)
			) STRICT, WITHOUT ROWID`,
			`CREATE TABLE ` + TableOutbox + ` (
				seq INTEGER PRIMARY KEY,
				format INTEGER NOT NULL,
				next_id INTEGER NOT NULL,
				writes TEXT NOT NULL
			) STRICT`,
		},
	},
	{
		Version: 2,
		Statements: []string{
			`CREATE INDEX chord_documents_address ON ` + TableDocuments + ` (owner, namespace, kind, key, created_at)`,
			`CREATE INDEX chord_documents_scope ON ` + TableDocuments + ` (owner, namespace, id)`,
			// Only bases are looked up by it (a load's newest base at or below a point): deltas write no entry.
			`CREATE INDEX chord_revisions_bases ON ` + TableRevisions + ` (document_id, seq) WHERE kind = 'base'`,
		},
	},
}

// StoreSchema is the store's schema version: the newest entry of its history (archives carry it).
var StoreSchema = int64(migrations[len(migrations)-1].Version)

// MetaRow is the store's metadata row, as OpenStore reads it once.
type MetaRow struct {
	LastSeq     int64
	NextID      int64
	Outbox      int64
	OutboxFloor int64
}

// ApplyStoreSchema brings db to the store's newest schema (one transaction) and checks that it stores text as UTF-8
// (the store compares text by its bytes); returns the metadata row. A store already there is read in one statement,
// without the migrations' write transaction (most opens: the cost of a reopen).
func ApplyStoreSchema(db sqlite.SqliteDatabase) (MetaRow, error) {
	if current, ok := currentMeta(db); ok {
		return current, nil
	}
	encoding, err := db.Get("PRAGMA encoding")
	if err != nil {
		return MetaRow{}, err
	}
	// Pi reads `encoding?.encoding`: absent prints as "undefined".
	name := "undefined"
	if encoding != nil {
		if text, isString := encoding["encoding"].(string); isString {
			name = text
		}
	}
	if name != "UTF-8" {
		return MetaRow{}, fmt.Errorf("store: the database encoding is %s, not UTF-8", name)
	}
	if err := sqlite.ApplySqliteMigrations(db, migrations, sqlite.ApplySqliteMigrationsOptions{Table: "chord_store_schema"}); err != nil {
		return MetaRow{}, err
	}
	row, err := db.Get(`SELECT last_seq, next_id, outbox, outbox_floor FROM ` + TableMeta + ` WHERE singleton = 1`)
	if err != nil {
		return MetaRow{}, err
	}
	if row == nil {
		return MetaRow{}, errors.New("store: the metadata row is missing")
	}
	return metaOf(row)
}

// currentMetaSQL reads the schema version, the encoding and the metadata row of a store, in one read.
const currentMetaSQL = `SELECT s.version, e.encoding, m.last_seq, m.next_id, m.outbox, m.outbox_floor
	FROM chord_store_schema s, pragma_encoding e, ` + TableMeta + ` m WHERE s.singleton = 1 AND m.singleton = 1`

// currentMeta is the metadata row of a store at the newest schema in UTF-8; ok is false for any other database.
func currentMeta(db sqlite.SqliteDatabase) (MetaRow, bool) {
	row, err := db.Get(currentMetaSQL)
	if err != nil {
		// A database without the store's tables (new, or not a store) or a backend without `pragma_encoding`: the
		// migrations' path creates, refuses or reads it.
		return MetaRow{}, false
	}
	if row == nil {
		return MetaRow{}, false
	}
	if version, err := sqlite.RowInt(row, "version"); err != nil || version != StoreSchema {
		return MetaRow{}, false
	}
	if encoding, _ := row["encoding"].(string); encoding != "UTF-8" {
		return MetaRow{}, false
	}
	meta, err := metaOf(row)
	return meta, err == nil
}

func metaOf(row sqlite.SqliteRow) (MetaRow, error) {
	var meta MetaRow
	for column, target := range map[string]*int64{"last_seq": &meta.LastSeq, "next_id": &meta.NextID, "outbox": &meta.Outbox, "outbox_floor": &meta.OutboxFloor} {
		value, err := sqlite.RowInt(row, column)
		if err != nil {
			return MetaRow{}, err
		}
		*target = value
	}
	return meta, nil
}

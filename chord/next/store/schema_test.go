package store

// pi: packages/chord/src/store/schema.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Pi has no test file of its own for schema.ts (`applyStoreSchema`, `TABLES`, `STORE_SCHEMA`, the migration history); these
// cases follow its source and the store tests that depend on it (store.test.ts "opening": a new store at seq 0, next id 2,
// no outbox, a reopen at its last seq and ids, a database that is not UTF-8 refused).

// schemaDatabase is a fresh in-memory database.
func schemaDatabase(t *testing.T) *node.NodeSqliteDatabase {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// transactionCounter counts the write transactions a database is asked for.
type transactionCounter struct {
	sqlite.SqliteDatabase
	transactions int
}

func (counter *transactionCounter) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	counter.transactions++
	return counter.SqliteDatabase.Transaction(callback)
}

func schemaNames(t *testing.T, db sqlite.SqliteDatabase, kind string) []string {
	t.Helper()
	rows, err := db.All(`SELECT name FROM sqlite_master WHERE type = ? AND name LIKE 'chord_%' ORDER BY name`, kind)
	noError(t, err)
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row["name"].(string)
	}
	return names
}

func TestStoreSchemaOfANewDatabase(t *testing.T) {
	db := schemaDatabase(t)
	meta, err := ApplyStoreSchema(db)
	noError(t, err)
	// The metadata row starts at seq 0, the allocator's next id 2 (id 1 is Durable's root conversation), no outbox, floor 0.
	qEqual(t, meta, MetaRow{LastSeq: 0, NextID: 2, Outbox: 0, OutboxFloor: 0})
	qEqual(t, StoreSchema, int64(3))
	qEqual(t, []string{TableMeta, TableTables, TableDocuments, TableRevisions, TableOutbox},
		[]string{"chord_meta", "chord_tables", "chord_documents", "chord_revisions", "chord_outbox"})
	qEqual(t, schemaNames(t, db, "table"), []string{"chord_documents", "chord_meta", "chord_outbox", "chord_revisions", "chord_store_schema", "chord_tables"})
	// Entry 1 has the partial unique index of live records; entry 2 the indexes of reads at a point; entry 3 the host's.
	qEqual(t, schemaNames(t, db, "index"), []string{"chord_documents_address", "chord_documents_host", "chord_documents_live", "chord_documents_scope", "chord_revisions_bases"})
	version, err := db.Get(`SELECT version FROM chord_store_schema WHERE singleton = 1`)
	noError(t, err)
	qEqual(t, version["version"], int64(3))
}

func TestStoreSchemaOfAStoreAlreadyThereIsReadWithoutAWriteTransaction(t *testing.T) {
	db := schemaDatabase(t)
	_, err := ApplyStoreSchema(db)
	noError(t, err)
	noError(t, db.Run(`UPDATE chord_meta SET last_seq = 7, next_id = 9, outbox = 1, outbox_floor = 3`))
	counted := &transactionCounter{SqliteDatabase: db}
	meta, err := ApplyStoreSchema(counted)
	noError(t, err)
	qEqual(t, meta, MetaRow{LastSeq: 7, NextID: 9, Outbox: 1, OutboxFloor: 3})
	qEqual(t, counted.transactions, 0)
	// A new database does take the migrations' write transaction.
	fresh := &transactionCounter{SqliteDatabase: schemaDatabase(t)}
	_, err = ApplyStoreSchema(fresh)
	noError(t, err)
	if fresh.transactions == 0 {
		t.Fatal("a new database was brought to the schema without a transaction")
	}
}

func TestStoreSchemaMigratesAnOlderVersionToTheNewest(t *testing.T) {
	db := schemaDatabase(t)
	_, err := ApplyStoreSchema(db)
	noError(t, err)
	// A store at entry 1 only (no indexes of entry 2, no host column of entry 3) is brought to the newest entry, keeping its metadata.
	for _, index := range []string{"chord_documents_address", "chord_documents_scope", "chord_revisions_bases", "chord_documents_host"} {
		noError(t, db.Exec(`DROP INDEX `+index))
	}
	noError(t, db.Exec(`ALTER TABLE chord_documents DROP COLUMN host`))
	noError(t, db.Run(`UPDATE chord_store_schema SET version = 1`))
	noError(t, db.Run(`UPDATE chord_meta SET last_seq = 4`))
	meta, err := ApplyStoreSchema(db)
	noError(t, err)
	qEqual(t, meta.LastSeq, int64(4))
	qEqual(t, schemaNames(t, db, "index"), []string{"chord_documents_address", "chord_documents_host", "chord_documents_live", "chord_documents_scope", "chord_revisions_bases"})
}

func TestStoreSchemaRefusesADatabaseThatIsNotUTF8(t *testing.T) {
	db := schemaDatabase(t)
	noError(t, db.Exec(`PRAGMA encoding = "UTF-16le"`))
	_, err := ApplyStoreSchema(db)
	if err == nil || !strings.Contains(err.Error(), "the database encoding is UTF-16le, not UTF-8") {
		t.Fatalf("ApplyStoreSchema = %v, want the encoding refused", err)
	}
	qEqual(t, schemaNames(t, db, "table"), []string{})
}

func TestStoreSchemaConstraints(t *testing.T) {
	db := schemaDatabase(t)
	_, err := ApplyStoreSchema(db)
	noError(t, err)
	const document = `INSERT INTO chord_documents (id, owner, namespace, kind, key, history, created_at, retired_at, revision, version, fingerprint)
		VALUES (?, 'o', 'n', 'state', 'k', ?, 1, ?, 1, 1, 'f')`
	t.Run("the metadata is one row", func(t *testing.T) {
		qFails(t, db.Run(`INSERT INTO chord_meta (singleton, last_seq, next_id, outbox, outbox_floor) VALUES (2, 0, 2, 0, 0)`), "CHECK constraint failed")
	})
	t.Run("the outbox setting is 0 or 1", func(t *testing.T) {
		qFails(t, db.Run(`UPDATE chord_meta SET outbox = 2`), "CHECK constraint failed")
	})
	t.Run("a document keeps a history rule of none, latest or rewindable", func(t *testing.T) {
		qFails(t, db.Run(document, int64(10), "forever", nil), "CHECK constraint failed")
		for i, rule := range []string{"none", "latest", "rewindable"} {
			noError(t, db.Run(document, int64(20+i), rule, int64(2)))
		}
	})
	t.Run("two live records at one address are refused; a retired one is not", func(t *testing.T) {
		noError(t, db.Run(document, int64(30), "none", nil))
		qFails(t, db.Run(document, int64(31), "none", nil), "UNIQUE constraint failed")
		noError(t, db.Run(document, int64(32), "none", int64(5)))
	})
	t.Run("a revision is a base or a delta", func(t *testing.T) {
		const revision = `INSERT INTO chord_revisions (document_id, seq, kind, version, content) VALUES (30, ?, ?, 1, '{}')`
		qFails(t, db.Run(revision, int64(1), "patch"), "CHECK constraint failed")
		noError(t, db.Run(revision, int64(2), "base"))
		noError(t, db.Run(revision, int64(3), "delta"))
	})
}

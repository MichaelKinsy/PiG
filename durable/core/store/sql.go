// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

import "github.com/MichaelKinsy/PiG/durable/core/history"

// Statement IDs: positions in sqlTable. The texts of Pi's writes are copied from pi-durable main's
// src/storage/sqlite/storage.ts (applyTableWrite, applyDocumentActions) and migrations.ts; sqlTable_test.go keeps this
// list and the table in step.
const (
	stmtClaimID uint16 = iota
	stmtInsertConversation
	stmtInsertEntry
	stmtUpsertTask
	stmtUpsertSubmission
	stmtInsertDocument
	stmtDeleteRevisions
	stmtInsertRevision
	stmtRetireDocument
	stmtMetadata
	// Reads.
	stmtSchemaVersion
	stmtMetadataRead
	stmtLiveTasks
	stmtLiveSubmissions
	stmtTaskByID
	stmtSubmissionByID
	stmtDocumentsByScope
	stmtDocumentByID
	stmtRevisions
	stmtConversation
	stmtBounds
	stmtEntries
	// Migration (applySqliteMigrations).
	stmtSchemaTable
	stmtSchemaSeed
	stmtSchemaVersionSet
	// The seven sidecar statements (ADR D6, sidecar.go) hold these positions; the store issues none of them while Idle
	// writes no sidecar.
	_
	_
	_
	_
	_
	_
	_
	// Full-state load (dump.go).
	stmtDumpConversations
	stmtDumpEntries
	stmtDumpTasks
	stmtDumpSubmissions
	stmtDumpDocuments
	stmtDumpRevisions
	// Live-state load and the reads a core with a synchronous host read (ABI section 4.2, pig.read) issues on demand (lazy.go).
	stmtDumpLiveTasks
	stmtDumpLiveSubmissions
	stmtSubmissionByRequest
	stmtRecordType
	stmtEntryByID
	stmtEntryIDsDesc
	stmtEntriesPage
	stmtEntryCount
	stmtEntriesDesc
	stmtTasksDesc
	// The context reads the host answers from the context it derived and holds, not from SQL (hostctx.go in the core).
	stmtContextSummary
	stmtContextMessages
	stmtContextRelease
	stmtMigrationFirst // the schema statements of migration 1 start here (stmtMigrationFirst + i), those of migration 2 after them
)

// sqlSchemaV2 is the second migration of src/storage/sqlite/migrations.ts: it adds the sleeping task status. SQLite cannot change a
// CHECK constraint in place, so the table is rebuilt (the temporary table's name is its own: a renaming adapter leaves it, and every
// statement that names it is its own). Read-only.
var sqlSchemaV2 = []string{
	`CREATE TABLE tasks_v2 (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'waiting', 'sleeping', 'completing', 'terminal')),
		abort_requested INTEGER NOT NULL CHECK (abort_requested IN (0, 1)),
		background INTEGER NOT NULL CHECK (background IN (0, 1)),
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`INSERT INTO tasks_v2 (id, conversation_id, kind, status, abort_requested, background, record)
		SELECT id, conversation_id, kind, status, abort_requested, background, record FROM tasks`,
	"DROP TABLE tasks",
	"ALTER TABLE tasks_v2 RENAME TO tasks",
	"CREATE INDEX tasks_by_status ON tasks (status, id)",
	"CREATE INDEX tasks_by_conversation ON tasks (conversation_id, id)",
	"CREATE INDEX tasks_by_kind ON tasks (kind, id)",
	"CREATE INDEX tasks_by_abort_requested ON tasks (abort_requested, id)",
	"CREATE INDEX tasks_by_background ON tasks (background, id)",
}

// sqlSchemaV1 is the INITIAL_SCHEMA of src/storage/sqlite/migrations.ts, schema version 1, statement for statement.
// Read-only.
var sqlSchemaV1 = []string{
	`CREATE TABLE durable_metadata (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		next_id TEXT NOT NULL,
		next_seq INTEGER NOT NULL
	) STRICT`,
	`INSERT INTO durable_metadata (singleton, next_id, next_seq) VALUES (1, '2', 1)`,
	`CREATE TABLE record_ids (
		id INTEGER PRIMARY KEY,
		record_type TEXT NOT NULL CHECK (record_type IN ('conversation', 'entry', 'task', 'submission', 'document'))
	) STRICT`,
	`CREATE TABLE conversations (
		id INTEGER PRIMARY KEY,
		owner_conversation_id INTEGER,
		owner_task_id INTEGER,
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	"CREATE INDEX conversations_by_owner_conversation ON conversations (owner_conversation_id, id)",
	"CREATE INDEX conversations_by_owner_task ON conversations (owner_task_id, id)",
	`CREATE TABLE entries (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		head INTEGER,
		commit_seq INTEGER NOT NULL,
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	"CREATE INDEX entries_by_conversation ON entries (conversation_id, id DESC)",
	"CREATE INDEX entry_heads_by_conversation ON entries (conversation_id, id DESC) WHERE head IS NOT NULL",
	`CREATE TABLE tasks (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'waiting', 'completing', 'terminal')),
		abort_requested INTEGER NOT NULL CHECK (abort_requested IN (0, 1)),
		background INTEGER NOT NULL CHECK (background IN (0, 1)),
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	"CREATE INDEX tasks_by_status ON tasks (status, id)",
	"CREATE INDEX tasks_by_conversation ON tasks (conversation_id, id)",
	"CREATE INDEX tasks_by_kind ON tasks (kind, id)",
	"CREATE INDEX tasks_by_abort_requested ON tasks (abort_requested, id)",
	"CREATE INDEX tasks_by_background ON tasks (background, id)",
	`CREATE TABLE submissions (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		request_id TEXT,
		status TEXT NOT NULL CHECK (status IN ('queued', 'placed', 'done', 'unanswered')),
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	"CREATE INDEX submissions_by_request ON submissions (conversation_id, request_id)",
	"CREATE INDEX submissions_by_conversation ON submissions (conversation_id, id)",
	"CREATE INDEX submissions_by_status ON submissions (status, id)",
	`CREATE TABLE documents (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		family INTEGER NOT NULL CHECK (family IN (0, 1)),
		key_value TEXT NOT NULL,
		scope_kind TEXT NOT NULL CHECK (scope_kind IN ('session', 'conversation', 'task')),
		owner_id INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		retired_at INTEGER,
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`CREATE INDEX documents_by_address
		ON documents (kind, scope_kind, owner_id, family, key_value, created_at DESC, retired_at)`,
	"CREATE INDEX documents_by_scope ON documents (scope_kind, owner_id, id)",
	"CREATE INDEX documents_by_scope_kind ON documents (scope_kind, owner_id, kind, id)",
	`CREATE TABLE document_revisions (
		document_id INTEGER NOT NULL,
		seq INTEGER NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('base', 'delta')),
		version INTEGER NOT NULL,
		content TEXT NOT NULL CHECK (json_valid(content)),
		PRIMARY KEY (document_id, seq)
	) STRICT`,
	"CREATE INDEX document_revisions_by_kind ON document_revisions (document_id, kind, seq DESC)",
}

// sqlTable is the table every host runs; ordering is stmt* above. Read-only.
var sqlTable = buildTable()

func buildTable() []string {
	t := make([]string, 0, int(stmtMigrationFirst)+len(sqlSchemaV1)+len(sqlSchemaV2))
	t = append(t,
		// stmtClaimID .. stmtMetadata: pi-durable's writes.
		history.SQLClaimID,
		history.SQLInsertConversation,
		history.SQLInsertEntry,
		`INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record)
						VALUES (?, ?, ?, ?, ?, ?, ?)
						ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id, kind = excluded.kind,
						status = excluded.status, abort_requested = excluded.abort_requested,
						background = excluded.background, record = excluded.record`,
		`INSERT INTO submissions (id, conversation_id, request_id, status, record) VALUES (?, ?, ?, ?, ?)
						ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id,
						request_id = excluded.request_id, status = excluded.status, record = excluded.record`,
		`INSERT INTO documents
						(id, kind, family, key_value, scope_kind, owner_id, created_at, retired_at, record)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"DELETE FROM document_revisions WHERE document_id = ?",
		"INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)",
		"UPDATE documents SET retired_at = ?, record = ? WHERE id = ?",
		// The metadata write carries the single-writer guard (ADR D13): the host checks that it changed one row.
		"UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1 AND next_seq = ?",
		// stmtSchemaVersion .. stmtEntries: reads. Record columns are read as BLOB (ABI section 4.3).
		"SELECT version FROM durable_schema WHERE singleton = 1",
		"SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1",
		"SELECT CAST(record AS BLOB) FROM tasks WHERE status IN ('pending', 'running', 'waiting', 'sleeping', 'completing') ORDER BY id",
		"SELECT CAST(record AS BLOB) FROM submissions WHERE status IN ('queued', 'placed') ORDER BY id",
		"SELECT CAST(record AS BLOB) FROM tasks WHERE id = ?",
		"SELECT CAST(record AS BLOB) FROM submissions WHERE id = ?",
		"SELECT CAST(record AS BLOB) FROM documents WHERE scope_kind = ? AND owner_id = ? AND retired_at IS NULL ORDER BY id",
		"SELECT CAST(record AS BLOB) FROM documents WHERE id = ?",
		// The newest base at or below the bound and every revision after it up to the bound (spec §10 document()).
		`SELECT seq, kind, version, CAST(content AS BLOB) FROM document_revisions
	WHERE document_id = ? AND seq <= ?
	AND seq >= (SELECT seq FROM document_revisions WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1)
	ORDER BY seq`,
		history.SQLConversation,
		history.SQLBounds,
		history.SQLEntries,
		// applySqliteMigrations.
		`CREATE TABLE IF NOT EXISTS durable_schema (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			version INTEGER NOT NULL CHECK (version >= 0)
		) STRICT`,
		"INSERT OR IGNORE INTO durable_schema (singleton, version) VALUES (1, 0)",
		"UPDATE durable_schema SET version = ? WHERE singleton = 1",
		// The sidecar (sidecar.go).
		sqlSidecarTable,
		sqlSidecarHeader,
		sqlSidecarLast,
		sqlSidecarDelta,
		sqlSidecarChunks,
		sqlSidecarInsert,
		sqlSidecarDelete,
		// The full-state load (dump.go).
		"SELECT CAST(record AS BLOB) FROM conversations ORDER BY id",
		"SELECT id, conversation_id, head IS NOT NULL, commit_seq, CAST(record AS BLOB) FROM entries WHERE id > ? ORDER BY id LIMIT "+dumpPageText,
		"SELECT id, conversation_id, kind, status, abort_requested, background, CAST(record AS BLOB) FROM tasks WHERE id > ? ORDER BY id LIMIT "+dumpPageText,
		"SELECT id, conversation_id, request_id, status, CAST(record AS BLOB) FROM submissions WHERE id > ? ORDER BY id LIMIT "+dumpPageText,
		"SELECT CAST(record AS BLOB) FROM documents ORDER BY id",
		"SELECT document_id, seq, kind, version, CAST(content AS BLOB) FROM document_revisions ORDER BY document_id, seq",
		"SELECT id, conversation_id, kind, status, abort_requested, background, CAST(record AS BLOB) FROM tasks WHERE id > ? AND status IN ('pending', 'running', 'waiting', 'sleeping', 'completing') ORDER BY id LIMIT "+dumpPageText,
		"SELECT id, conversation_id, request_id, status, CAST(record AS BLOB) FROM submissions WHERE id > ? AND status IN ('queued', 'placed') ORDER BY id LIMIT "+dumpPageText,
		// SqliteStorage.submissionByRequest; the index the WHERE uses orders equal keys by id.
		"SELECT CAST(record AS BLOB) FROM submissions WHERE conversation_id = ? AND request_id = ? ORDER BY id LIMIT 1",
		"SELECT record_type FROM record_ids WHERE id = ?",
		// The entry reads of a lazily opened core (lazy.go).
		"SELECT conversation_id, commit_seq, CAST(record AS BLOB) FROM entries WHERE id = ?",
		"SELECT id FROM entries WHERE conversation_id = ? AND id >= ? AND id <= ? ORDER BY id DESC LIMIT ?",
		"SELECT id, commit_seq, CAST(record AS BLOB) FROM entries WHERE conversation_id = ? AND id > ? AND id <= ? ORDER BY id LIMIT "+dumpPageText,
		"SELECT count(*) FROM entries WHERE conversation_id = ? AND id >= ? AND id <= ?",
		"SELECT id, commit_seq, CAST(record AS BLOB) FROM entries WHERE conversation_id = ? AND id >= ? AND id <= ? ORDER BY id DESC LIMIT ?",
		"SELECT id, conversation_id, kind, status, abort_requested, background, CAST(record AS BLOB) FROM tasks WHERE id < ? ORDER BY id DESC LIMIT "+dumpPageText,
		// The host's context reads (ABI section 4.2). They look like reads so every host answers them through pig.read, and name
		// functions SQLite does not have, so a host that ran one as SQL would fail loudly.
		TextContextSummary,
		TextContextMessages,
		TextContextRelease,
	)
	return append(append(t, sqlSchemaV1...), sqlSchemaV2...)
}

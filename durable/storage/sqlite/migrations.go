package sqlite

// Ports packages/durable/src/storage/sqlite/migrations.ts

import (
	"strings"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// SqliteMigration is Chord's migration type, re-exported as Pi's durable migrations entry point re-exports it.
type SqliteMigration = chordsqlite.SqliteMigration

// CreateSqliteMigrations builds the immutable, ordered schema history for a set of SQLite identifiers (createSqliteMigrations).
func CreateSqliteMigrations(schema SqliteSchema) []SqliteMigration {
	t, i := schema.Tables, schema.Indexes
	render := strings.NewReplacer(
		"${tables.durableMetadata}", t.DurableMetadata,
		"${tables.recordIds}", t.RecordIds,
		"${tables.conversations}", t.Conversations,
		"${tables.entries}", t.Entries,
		"${tables.tasks}", t.Tasks,
		"${tables.submissions}", t.Submissions,
		"${tables.documents}", t.Documents,
		"${tables.documentRevisions}", t.DocumentRevisions,
		"${indexes.conversationsByOwnerConversation}", i.ConversationsByOwnerConversation,
		"${indexes.conversationsByOwnerTask}", i.ConversationsByOwnerTask,
		"${indexes.entriesByConversation}", i.EntriesByConversation,
		"${indexes.entryHeadsByConversation}", i.EntryHeadsByConversation,
		"${indexes.tasksByStatus}", i.TasksByStatus,
		"${indexes.tasksByConversation}", i.TasksByConversation,
		"${indexes.tasksByKind}", i.TasksByKind,
		"${indexes.tasksByAbortRequested}", i.TasksByAbortRequested,
		"${indexes.tasksByBackground}", i.TasksByBackground,
		"${indexes.submissionsByRequest}", i.SubmissionsByRequest,
		"${indexes.submissionsByConversation}", i.SubmissionsByConversation,
		"${indexes.submissionsByStatus}", i.SubmissionsByStatus,
		"${indexes.documentsByAddress}", i.DocumentsByAddress,
		"${indexes.documentsByScope}", i.DocumentsByScope,
		"${indexes.documentsByScopeKind}", i.DocumentsByScopeKind,
		"${indexes.documentRevisionsByKind}", i.DocumentRevisionsByKind,
	).Replace
	// next_id is TEXT because node:sqlite rejects INTEGER results outside JavaScript's safe integer range; the column
	// type is part of the portable on-disk schema.
	statements := []string{
		`CREATE TABLE ${tables.durableMetadata} (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			next_id TEXT NOT NULL,
			next_seq INTEGER NOT NULL
		) STRICT`,
		`INSERT INTO ${tables.durableMetadata} (singleton, next_id, next_seq) VALUES (1, '2', 1)`,
		`CREATE TABLE ${tables.recordIds} (
			id INTEGER PRIMARY KEY,
			record_type TEXT NOT NULL CHECK (record_type IN ('conversation', 'entry', 'task', 'submission', 'document'))
		) STRICT`,
		`CREATE TABLE ${tables.conversations} (
			id INTEGER PRIMARY KEY,
			owner_conversation_id INTEGER,
			owner_task_id INTEGER,
			record TEXT NOT NULL CHECK (json_valid(record))
		) STRICT`,
		`CREATE INDEX ${indexes.conversationsByOwnerConversation} ON ${tables.conversations} (owner_conversation_id, id)`,
		`CREATE INDEX ${indexes.conversationsByOwnerTask} ON ${tables.conversations} (owner_task_id, id)`,
		`CREATE TABLE ${tables.entries} (
			id INTEGER PRIMARY KEY,
			conversation_id INTEGER NOT NULL,
			head INTEGER,
			commit_seq INTEGER NOT NULL,
			record TEXT NOT NULL CHECK (json_valid(record))
		) STRICT`,
		`CREATE INDEX ${indexes.entriesByConversation} ON ${tables.entries} (conversation_id, id DESC)`,
		`CREATE INDEX ${indexes.entryHeadsByConversation} ON ${tables.entries} (conversation_id, id DESC) WHERE head IS NOT NULL`,
		`CREATE TABLE ${tables.tasks} (
			id INTEGER PRIMARY KEY,
			conversation_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'waiting', 'completing', 'terminal')),
			abort_requested INTEGER NOT NULL CHECK (abort_requested IN (0, 1)),
			background INTEGER NOT NULL CHECK (background IN (0, 1)),
			record TEXT NOT NULL CHECK (json_valid(record))
		) STRICT`,
		`CREATE INDEX ${indexes.tasksByStatus} ON ${tables.tasks} (status, id)`,
		`CREATE INDEX ${indexes.tasksByConversation} ON ${tables.tasks} (conversation_id, id)`,
		`CREATE INDEX ${indexes.tasksByKind} ON ${tables.tasks} (kind, id)`,
		`CREATE INDEX ${indexes.tasksByAbortRequested} ON ${tables.tasks} (abort_requested, id)`,
		`CREATE INDEX ${indexes.tasksByBackground} ON ${tables.tasks} (background, id)`,
		`CREATE TABLE ${tables.submissions} (
			id INTEGER PRIMARY KEY,
			conversation_id INTEGER NOT NULL,
			request_id TEXT,
			status TEXT NOT NULL CHECK (status IN ('queued', 'placed', 'done', 'unanswered')),
			record TEXT NOT NULL CHECK (json_valid(record))
		) STRICT`,
		`CREATE INDEX ${indexes.submissionsByRequest} ON ${tables.submissions} (conversation_id, request_id)`,
		`CREATE INDEX ${indexes.submissionsByConversation} ON ${tables.submissions} (conversation_id, id)`,
		`CREATE INDEX ${indexes.submissionsByStatus} ON ${tables.submissions} (status, id)`,
		`CREATE TABLE ${tables.documents} (
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
		`CREATE INDEX ${indexes.documentsByAddress}
			ON ${tables.documents} (kind, scope_kind, owner_id, family, key_value, created_at DESC, retired_at)`,
		`CREATE INDEX ${indexes.documentsByScope} ON ${tables.documents} (scope_kind, owner_id, id)`,
		`CREATE INDEX ${indexes.documentsByScopeKind} ON ${tables.documents} (scope_kind, owner_id, kind, id)`,
		`CREATE TABLE ${tables.documentRevisions} (
			document_id INTEGER NOT NULL,
			seq INTEGER NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('base', 'delta')),
			version INTEGER NOT NULL,
			content TEXT NOT NULL CHECK (json_valid(content)),
			PRIMARY KEY (document_id, seq)
		) STRICT`,
		`CREATE INDEX ${indexes.documentRevisionsByKind} ON ${tables.documentRevisions} (document_id, kind, seq DESC)`,
	}
	for index, statement := range statements {
		statements[index] = render(statement)
	}
	// Version 2 adds the sleeping task status. SQLite cannot change a CHECK constraint in place, so the table is rebuilt.
	rebuiltTasks := t.Tasks + "_v2"
	sleepingStatus := []string{
		`CREATE TABLE ` + rebuiltTasks + ` (
			id INTEGER PRIMARY KEY,
			conversation_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'waiting', 'sleeping', 'completing', 'terminal')),
			abort_requested INTEGER NOT NULL CHECK (abort_requested IN (0, 1)),
			background INTEGER NOT NULL CHECK (background IN (0, 1)),
			record TEXT NOT NULL CHECK (json_valid(record))
		) STRICT`,
		`INSERT INTO ` + rebuiltTasks + ` (id, conversation_id, kind, status, abort_requested, background, record)
			SELECT id, conversation_id, kind, status, abort_requested, background, record FROM ` + t.Tasks,
		`DROP TABLE ` + t.Tasks,
		`ALTER TABLE ` + rebuiltTasks + ` RENAME TO ` + t.Tasks,
		`CREATE INDEX ` + i.TasksByStatus + ` ON ` + t.Tasks + ` (status, id)`,
		`CREATE INDEX ` + i.TasksByConversation + ` ON ` + t.Tasks + ` (conversation_id, id)`,
		`CREATE INDEX ` + i.TasksByKind + ` ON ` + t.Tasks + ` (kind, id)`,
		`CREATE INDEX ` + i.TasksByAbortRequested + ` ON ` + t.Tasks + ` (abort_requested, id)`,
		`CREATE INDEX ` + i.TasksByBackground + ` ON ` + t.Tasks + ` (background, id)`,
	}
	return []SqliteMigration{{Version: 1, Statements: statements}, {Version: 2, Statements: sleepingStatus}}
}

// SqliteMigrations is the immutable, ordered schema history of the unprefixed schema. Append new migrations after the initial schema ships.
var SqliteMigrations = CreateSqliteMigrations(DefaultSqliteSchema)

// CurrentSqliteSchemaVersion is the version of the last migration in SqliteMigrations.
var CurrentSqliteSchemaVersion = lastVersion(SqliteMigrations)

func lastVersion(migrations []SqliteMigration) int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].Version
}

// ApplySqliteMigrations applies all pending schema migrations of the unprefixed schema atomically. A nil migrations
// slice selects SqliteMigrations; a non-nil empty slice applies none.
func ApplySqliteMigrations(database SqliteDatabase, migrations []SqliteMigration) error {
	if migrations == nil {
		migrations = SqliteMigrations
	}
	return ApplySqliteMigrationsForSchema(database, DefaultSqliteSchema, migrations)
}

// ApplySqliteMigrationsForSchema applies migrations rendered for a particular schema. A nil migrations slice selects
// CreateSqliteMigrations(schema); a non-nil empty slice applies none.
func ApplySqliteMigrationsForSchema(database SqliteDatabase, schema SqliteSchema, migrations []SqliteMigration) error {
	if migrations == nil {
		migrations = CreateSqliteMigrations(schema)
	}
	return chordsqlite.ApplySqliteMigrations(database, migrations, chordsqlite.ApplySqliteMigrationsOptions{Table: schema.Tables.DurableSchema})
}

package sqlite

// Ports packages/durable/src/storage/sqlite/schema.ts

import (
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// SqliteTables are the table names of the durable schema. The values never change or disappear; a rename adds a new
// entry so historical migrations keep rendering their original object names (schema.ts TABLE_NAMES).
type SqliteTables struct {
	DurableSchema     string
	DurableMetadata   string
	RecordIds         string
	Conversations     string
	Entries           string
	Tasks             string
	Submissions       string
	Documents         string
	DocumentRevisions string
}

// SqliteIndexes are the index names of the durable schema, with the same stability rule as SqliteTables
// (schema.ts INDEX_NAMES).
type SqliteIndexes struct {
	ConversationsByOwnerConversation string
	ConversationsByOwnerTask         string
	EntriesByConversation            string
	EntryHeadsByConversation         string
	TasksByStatus                    string
	TasksByConversation              string
	TasksByKind                      string
	TasksByAbortRequested            string
	TasksByBackground                string
	SubmissionsByRequest             string
	SubmissionsByConversation        string
	SubmissionsByStatus              string
	DocumentsByAddress               string
	DocumentsByScope                 string
	DocumentsByScopeKind             string
	DocumentRevisionsByKind          string
}

// SqliteSchema is the identifiers every migration and storage query uses.
type SqliteSchema struct {
	Tables  SqliteTables
	Indexes SqliteIndexes
}

// ValidateSqliteSchemaPrefix is Chord's prefix check, re-exported as Pi's durable schema entry point re-exports it.
var ValidateSqliteSchemaPrefix = chordsqlite.ValidateSqliteSchemaPrefix

// CreateSqliteSchema builds the identifiers of every migration and storage query. A nil prefix leaves the names as they
// are; a present prefix, even an empty one, must pass ValidateSqliteSchemaPrefix.
func CreateSqliteSchema(prefix *string) (SqliteSchema, error) {
	actual := ""
	if prefix != nil {
		if err := ValidateSqliteSchemaPrefix(*prefix); err != nil {
			return SqliteSchema{}, err
		}
		actual = *prefix
	}
	return SqliteSchema{
		Tables: SqliteTables{
			DurableSchema:     actual + "durable_schema",
			DurableMetadata:   actual + "durable_metadata",
			RecordIds:         actual + "record_ids",
			Conversations:     actual + "conversations",
			Entries:           actual + "entries",
			Tasks:             actual + "tasks",
			Submissions:       actual + "submissions",
			Documents:         actual + "documents",
			DocumentRevisions: actual + "document_revisions",
		},
		Indexes: SqliteIndexes{
			ConversationsByOwnerConversation: actual + "conversations_by_owner_conversation",
			ConversationsByOwnerTask:         actual + "conversations_by_owner_task",
			EntriesByConversation:            actual + "entries_by_conversation",
			EntryHeadsByConversation:         actual + "entry_heads_by_conversation",
			TasksByStatus:                    actual + "tasks_by_status",
			TasksByConversation:              actual + "tasks_by_conversation",
			TasksByKind:                      actual + "tasks_by_kind",
			TasksByAbortRequested:            actual + "tasks_by_abort_requested",
			TasksByBackground:                actual + "tasks_by_background",
			SubmissionsByRequest:             actual + "submissions_by_request",
			SubmissionsByConversation:        actual + "submissions_by_conversation",
			SubmissionsByStatus:              actual + "submissions_by_status",
			DocumentsByAddress:               actual + "documents_by_address",
			DocumentsByScope:                 actual + "documents_by_scope",
			DocumentsByScopeKind:             actual + "documents_by_scope_kind",
			DocumentRevisionsByKind:          actual + "document_revisions_by_kind",
		},
	}, nil
}

// DefaultSqliteSchema is the schema without a prefix.
var DefaultSqliteSchema = mustDefaultSchema()

func mustDefaultSchema() SqliteSchema {
	schema, err := CreateSqliteSchema(nil)
	if err != nil {
		panic(err)
	}
	return schema
}

package store

// Ports packages/durable/src/storage/store/tables.ts
//
// Durable's records as declared tables of the store (slice 6b D2, §2.2): every row carries its record's indexed
// columns and the whole record as one `json(...)` cell. Indexed strings that must stay lossless (`kind`,
// `request_id`) hold Durable's JSON encoding; index columns are never `opt`, so absent values take sentinels no
// encoded value can be (`0` for an absent owner, which is never an id; `""` for an absent request id: an encoded
// string starts with `"`).

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/ordered"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// record is a record as one `json(...)` cell.
func record() chordstore.ColumnSchema {
	return chordstore.Json(contracts.Unknown(), chordstore.JsonColumnOptions{Version: 1})
}

func define(name string, options chordstore.TableOptions) *chordstore.TableDefinition {
	definition, err := chordstore.DefineTable(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

// RecordIds says which record type owns an id: Durable's one namespace across its five record types.
var RecordIds = define("record_ids", chordstore.TableOptions{
	Version: 1,
	Columns: []chordstore.ColumnProp{chordstore.Col("id", chordstore.Int()), chordstore.Col("record_type", chordstore.Str())},
	Key:     []string{"id"},
})

// Conversations are the conversation records.
var Conversations = define("conversations", chordstore.TableOptions{
	Version: 1,
	Columns: []chordstore.ColumnProp{
		chordstore.Col("id", chordstore.Int()),
		chordstore.Col("owner_conversation_id", chordstore.Int()),
		chordstore.Col("owner_task_id", chordstore.Int()),
		chordstore.Col("record", record()),
	},
	Key: []string{"id"},
	Indexes: chordstore.IndexRecords{
		{Name: "by_owner_conversation", Columns: []string{"owner_conversation_id"}},
		{Name: "by_owner_task", Columns: []string{"owner_task_id"}},
	},
})

// Entries are the entry records.
var Entries = define("entries", chordstore.TableOptions{
	Version: 1,
	Columns: []chordstore.ColumnProp{
		chordstore.Col("id", chordstore.Int()),
		chordstore.Col("conversation_id", chordstore.Int()),
		chordstore.Col("commit_seq", chordstore.Int()),
		chordstore.Col("record", record()),
	},
	Key:     []string{"id"},
	Indexes: chordstore.IndexRecords{{Name: "by_conversation", Columns: []string{"conversation_id"}}},
})

// EntryHeads are the entries with a head marker, by conversation: what `SqliteStorage`'s partial index on
// `head IS NOT NULL` is, as rows written only with such an entry, so a commit of an entry without one updates no
// second index.
var EntryHeads = define("entry_heads", chordstore.TableOptions{
	Version: 1,
	Columns: []chordstore.ColumnProp{chordstore.Col("id", chordstore.Int()), chordstore.Col("conversation_id", chordstore.Int())},
	Key:     []string{"id"},
	Indexes: chordstore.IndexRecords{{Name: "by_conversation", Columns: []string{"conversation_id"}}},
})

// Tasks are the task records.
var Tasks = define("tasks", chordstore.TableOptions{
	Version: 1,
	Columns: []chordstore.ColumnProp{
		chordstore.Col("id", chordstore.Int()),
		chordstore.Col("conversation_id", chordstore.Int()),
		chordstore.Col("kind", chordstore.Str()),
		chordstore.Col("status", chordstore.Str()),
		chordstore.Col("abort_requested", chordstore.Int()),
		chordstore.Col("background", chordstore.Int()),
		chordstore.Col("record", record()),
	},
	Key: []string{"id"},
	Indexes: chordstore.IndexRecords{
		{Name: "by_status", Columns: []string{"status"}},
		{Name: "by_conversation", Columns: []string{"conversation_id"}},
		{Name: "by_kind", Columns: []string{"kind"}},
		{Name: "by_abort_requested", Columns: []string{"abort_requested"}},
		{Name: "by_background", Columns: []string{"background"}},
	},
})

// Submissions are the submission records.
var Submissions = define("submissions", chordstore.TableOptions{
	Version: 1,
	Columns: []chordstore.ColumnProp{
		chordstore.Col("id", chordstore.Int()),
		chordstore.Col("conversation_id", chordstore.Int()),
		chordstore.Col("request_id", chordstore.Str()),
		chordstore.Col("status", chordstore.Str()),
		chordstore.Col("record", record()),
	},
	Key: []string{"id"},
	Indexes: chordstore.IndexRecords{
		{Name: "by_request", Columns: []string{"conversation_id", "request_id"}},
		{Name: "by_conversation", Columns: []string{"conversation_id"}},
		{Name: "by_status", Columns: []string{"status"}},
	},
})

// EncodeIndexed is Durable's JSON encoding of an indexed string: lossless whatever the string (lone surrogates,
// NUL).
func EncodeIndexed(value string) string {
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		panic(fmt.Sprintf("durable store: string is not JSON encodable: %v", err))
	}
	return string(encoded)
}

// recordCell is value as the `json(...)` cell a row holds: the record's JSON text and the tree it parses to, which the store's
// columns check and record.
func recordCell(value any) (any, error) {
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		var unsupported *sdkjson.UnsupportedValueError
		if errors.As(err, &unsupported) && strings.Contains(unsupported.Str, "cycle") {
			return nil, fmt.Errorf("Converting circular structure to JSON: %w", err)
		}
		return nil, err
	}
	tree, err := chordjson.DecodeUnquoting(text, unquoteJS) //perf:ok one per record the store writes; the cell carries both the text and the tree its columns check, and no encoder yields both
	if err != nil {
		return nil, err
	}
	// The text is the cell: the store neither serializes the tree again nor parses the text back.
	return chordstore.JsonText{Text: string(text), Value: tree}, nil
}

// unquoteJS decodes a JSON string with JavaScript string semantics: a lone surrogate escape becomes its WTF-8 form.
func unquoteJS(raw []byte, text *string) error { return sdkjson.Unmarshal(raw, text) }

// storedRecord is a record type of the store's tables: what parseJSON decodes a cell into (a concrete set, so no decode targets an
// interface tree).
type storedRecord interface {
	durable.DocumentRecord | durable.ConversationRecord | durable.EntryRecord | durable.SubmissionRecord | storedTask
}

// parseJSON parses a stored record cell into T, a JsonValue member holding the stored text's objects in order, as
// JSON.parse gives Pi.
func parseJSON[T storedRecord](text string) (T, error) {
	var value T
	if err := sdkjson.Unmarshal([]byte(text), &value); err != nil {
		return value, err
	}
	if ordered.HasDynamic(reflect.TypeFor[T]()) {
		tree, err := chordjson.DecodeUnquoting([]byte(text), unquoteJS)
		if err != nil {
			return value, err
		}
		ordered.Restore(&value, tree)
	}
	return value, nil
}

// ConversationRow is the row of a conversation record.
func ConversationRow(value durable.ConversationRecord) (chordstore.Row, error) {
	cell, err := recordCell(value)
	if err != nil {
		return nil, err
	}
	row := chordstore.Row{"id": int64(value.Id), "owner_conversation_id": int64(0), "owner_task_id": int64(0), "record": cell}
	if value.Owner != nil {
		row["owner_conversation_id"] = int64(value.Owner.ConversationId)
		row["owner_task_id"] = int64(value.Owner.TaskId)
	}
	return row, nil
}

// EntryRow is the row of an entry record; `commit_seq` is stamped by the step.
func EntryRow(value durable.EntryRecord) (chordstore.Row, error) {
	cell, err := recordCell(value)
	if err != nil {
		return nil, err
	}
	return chordstore.Row{"id": int64(value.Id), "conversation_id": int64(value.ConversationId), "commit_seq": int64(0), "record": cell}, nil
}

// TaskRow is the row of a task record.
func TaskRow(value durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) (chordstore.Row, error) {
	cell, err := recordCell(value)
	if err != nil {
		return nil, err
	}
	return chordstore.Row{
		"id":              int64(value.Id),
		"conversation_id": int64(value.ConversationId),
		"kind":            EncodeIndexed(value.Kind),
		"status":          string(value.State.Status),
		"abort_requested": boolInt(value.AbortRequested),
		"background":      boolInt(value.Background),
		"record":          cell,
	}, nil
}

// SubmissionRow is the row of a submission record.
func SubmissionRow(value durable.SubmissionRecord) (chordstore.Row, error) {
	cell, err := recordCell(value)
	if err != nil {
		return nil, err
	}
	requestID := ""
	if value.RequestId != nil {
		requestID = EncodeIndexed(*value.RequestId)
	}
	return chordstore.Row{
		"id":              int64(value.Id),
		"conversation_id": int64(value.ConversationId),
		"request_id":      requestID,
		"status":          string(value.Status),
		"record":          cell,
	}, nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

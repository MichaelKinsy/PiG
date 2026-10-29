package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/support.ts (the archive's helpers: ArchiveTables, archiveStore) for archive_test.go,
// export_test.go and import_test.go. Names carry the `archive` prefix; the package's shared helpers are
// support_test.go's.

// archiveTableSet is Pi's `ArchiveTables`.
type archiveTableSet struct {
	EditsV1, EditsV2, NotesV1, NotesV2 *TableDefinition
}

// archiveTables are the tables of `archiveStore`: edits (v1 with an index; v2 adds `done`) and notes, whose v2
// migration crashed.
var archiveTables = func() archiveTableSet {
	define := func(name string, options TableOptions) *TableDefinition {
		definition, err := DefineTable(name, options)
		if err != nil {
			panic(err)
		}
		return definition
	}
	evolve := func(previous *TableDefinition, edit func(t *TableEditor) *TableEditor) *TableDefinition {
		definition, err := EvolveTable(previous, edit)
		if err != nil {
			panic(err)
		}
		return definition
	}
	editsV1 := define("edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("path", Str()),
			Col("at", Int()),
			Col("note", Opt(Str())),
			Col("meta", Opt(Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))),
		},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
	})
	editsV2 := evolve(editsV1, func(t *TableEditor) *TableEditor {
		return t.Add("done", Bool(contracts.Default(false)))
	})
	notesV1 := define("notes", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Int()), Col("text", Str())}, Key: []string{"id"}})
	notesV2 := evolve(notesV1, func(t *TableEditor) *TableEditor {
		return t.Rows(func(row Row) (any, error) {
			if row["id"] == int64(120) {
				return nil, errors.New("crash")
			}
			return Row{"text": row["text"].(string) + "!"}, nil
		})
	})
	return archiveTableSet{editsV1, editsV2, notesV1, notesV2}
}()

// archiveStore is a store holding a bit of everything an archive carries: `app.store:edits` at version 2 with 150
// rows (two export pages; some written at version 1, `NULL` and `json(...)` cells, an index), `app.store:notes` left
// `migrating` at version 2 after one committed chunk, and documents: a singleton, keyed ones with deltas after their
// base, and a retired one.
func archiveStore(t *testing.T, outbox ...bool) *ChordStore {
	t.Helper()
	tables := archiveTables
	store := memoryStore(t, outbox...)
	edits := archiveTableAt(t, store, "app.store:edits", tables.EditsV1)
	notes := archiveTableAt(t, store, "app.store:notes", tables.NotesV1)
	var staged []StagedWrite
	editChanges := NewTableChanges(edits, &staged)
	for i := range 150 {
		row := Row{"id": fmt.Sprintf("e%03d", i), "path": fmt.Sprintf("p/%d", i%4), "at": int64(i)}
		if i%2 == 0 {
			row["note"] = fmt.Sprintf("n%d", i)
		}
		if i%3 == 0 {
			row["meta"] = delta.JsonObjectOf("a", float64(i))
		}
		archiveMust(t, editChanges.Put(row))
	}
	noteChanges := NewTableChanges(notes, &staged)
	for i := range 150 {
		archiveMust(t, noteChanges.Put(Row{"id": int64(i), "text": fmt.Sprintf("t%d", i)}))
	}
	if _, err := store.Commit(staged, openFence, background); err != nil {
		t.Fatalf("archiveStore: commit: %v", err)
	}
	prepare := func(definition *TableDefinition, table string) error {
		return PrepareTables([]DeclaredTable{{Definition: definition, Table: table}}, store, openFence, "app", background)
	}
	archiveMust(t, prepare(tables.EditsV2, "app.store:edits"))
	// The notes migration crashes after its first committed chunk: the table stays `migrating`.
	_ = prepare(tables.NotesV2, "app.store:notes")
	address := DocumentAddress{Namespace: "chord", Owner: "app.store", Kind: "memo"}
	base := func(value delta.JsonValue) DocumentBase {
		return DocumentBase{Version: 1, Fingerprint: "f1", Value: value}
	}
	var ids []int64
	created := CommitFence{
		Admit: func() bool { return true },
		Adopt: func(_ int64, created []int64) error {
			ids = created
			return nil
		},
	}
	var creations []StagedWrite
	for _, key := range []string{"", "a", "b", "gone"} {
		keyed := address
		keyed.Key = new(key)
		value := delta.JsonObjectOf("title", key)
		creations = append(creations, CreateDocument(keyed, base(value), nexttest.JSONText(t, value), nil))
	}
	if _, err := store.Commit(creations, created, background); err != nil {
		t.Fatalf("archiveStore: create documents: %v", err)
	}
	a, b, gone := ids[1], ids[2], ids[3]
	change := func(id, revision int64, title string) StagedWrite {
		ops := []delta.Op{{"s", []any{"title"}, title}}
		return ChangeDocument(id, revision, DocumentContent{Kind: "delta", Version: 1, Ops: ops}, nexttest.JSONText(t, ops))
	}
	commit := func(writes ...StagedWrite) {
		if _, err := store.Commit(writes, openFence, background); err != nil {
			t.Fatalf("archiveStore: commit: %v", err)
		}
	}
	commit(change(a, 1, "a2"), RetireDocument(gone, 1))
	commit(change(a, 2, "a3"))
	commit(change(b, 1, "b2"))
	return store
}

// archiveTableAt is `definition` prepared as `table` in store, and a handle to it.
func archiveTableAt(t *testing.T, store *ChordStore, table string, definition *TableDefinition) *TableHandle {
	t.Helper()
	err := PrepareTables([]DeclaredTable{{Definition: definition, Table: table}}, store, openFence, "app", background)
	archiveMust(t, err)
	return NewTableHandle(definition, table, store, tableOwner())
}

// archiveJSON is JSON text decoded as JSON.parse does; it panics on text that is no JSON (the tests' literals).
func archiveJSON(text string) any {
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		panic(err)
	}
	return value
}

// partJSON is a part as decoded JSON: what its JSON text says (Pi's `JSON.parse(JSON.stringify(part))`).
func partJSON(t testing.TB, part any) *delta.JsonObject {
	t.Helper()
	return nexttest.ParseJSON(t, nexttest.JSONText(t, part)).(*delta.JsonObject)
}

// partTexts is the JSON text of each part: two archives are equal when their texts are (key order and number format
// included).
func partTexts(t testing.TB, parts []ArchivePart) []string {
	t.Helper()
	texts := make([]string, len(parts))
	for index, part := range parts {
		texts[index] = nexttest.JSONText(t, part)
	}
	return texts
}

// archiveIsCode reports whether err is a ChordError of code.
func archiveIsCode(err error, code data.ChordErrorCode) bool {
	_, ok := data.IsChordErrorCode(err, code)
	return ok
}

// assertRowText expects the row of the handle at the `id` of want to read as want (JSON text).
func assertRowText(t *testing.T, handle *TableHandle, want string) {
	t.Helper()
	expected := nexttest.ParseJSON(t, want)
	id := expected.(*delta.JsonObject).Value("id")
	got, err := handle.Get(Row{"id": id}, background)
	if err != nil {
		t.Fatalf("get %v: %v", id, err)
	}
	nexttest.RequireJSONEqual(t, got, expected)
}

// archiveMust fails the test on err.
func archiveMust(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

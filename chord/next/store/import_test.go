package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/import.test.ts: tests of `ImportStore` (slice 6a §13, rules 12, 24, 25): round
// trips (old row versions, a migrating table, uncheckpointed deltas), the store it returns, every refusal of the
// archive's order and content, the rollback, and an input that stalls cancelled by `ctx`.

// importedStore is `parts` imported into a fresh `:memory:` backend; the store closes with the test.
func importedStore(t *testing.T, parts PartSource, outbox bool) *ChordStore {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	options := StoreOptions{}
	if outbox {
		options.Outbox = &outbox
	}
	imported, err := ImportStore(SqliteBackend(db), parts, chordctx.Background(), options)
	if err != nil {
		t.Fatalf("ImportStore: %v", err)
	}
	store := imported.(*ChordStore)
	t.Cleanup(func() { _ = store.Close(chordctx.Background()) })
	return store
}

// importArchive is the archive of `archiveStore`.
func importArchive(t *testing.T) []ArchivePart {
	t.Helper()
	source := archiveStore(t)
	return exported(t, source, chordctx.Background())
}

// partSource is the typed parts as an import's input.
func partSource(parts []ArchivePart) PartSource {
	values := make([]any, len(parts))
	for index, part := range parts {
		values[index] = part
	}
	return SlicePartSource(values)
}

// jsonParts are the parts of an archive as decoded JSON: a copy to edit (Pi's `structuredClone`).
func jsonParts(t *testing.T, parts []ArchivePart) []any {
	t.Helper()
	values := make([]any, len(parts))
	for index, part := range parts {
		values[index] = nexttest.ParseJSON(t, nexttest.JSONText(t, part))
	}
	return values
}

// importEdited is the archive with `edit` applied to a copy of its parts.
func importEdited(t *testing.T, edit func(parts []any) []any) []any {
	t.Helper()
	return edit(jsonParts(t, importArchive(t)))
}

// importKind is the `kind` of a part.
func importKind(part any) string {
	kind, _ := part.(*delta.JsonObject).Value("kind").(string)
	return kind
}

// importFirst is the index of the first part of `kind` (and table suffix).
func importFirst(parts []any, kind string, table ...string) int {
	for index, part := range parts {
		if importKind(part) != kind {
			continue
		}
		if len(table) == 0 {
			return index
		}
		if name, _ := part.(*delta.JsonObject).Value("table").(string); strings.HasSuffix(name, table[0]) {
			return index
		}
	}
	return -1
}

// importAll is the positions of every part of `kind`.
func importAll(parts []any, kind string) []int {
	var positions []int
	for index, part := range parts {
		if importKind(part) == kind {
			positions = append(positions, index)
		}
	}
	return positions
}

// importAt is part `index` as a writable object.
func importAt(parts []any, index int) *delta.JsonObject { return parts[index].(*delta.JsonObject) }

// importMember is the writable object under `key`.
func importMember(object *delta.JsonObject, key string) *delta.JsonObject {
	return object.Value(key).(*delta.JsonObject)
}

// importRevisionContent is the content of the revision part at `index`, writable.
func importRevisionContent(parts []any, index int) *delta.JsonObject {
	return importMember(importAt(parts, index), "content")
}

func TestImportRoundTrips(t *testing.T) {
	ctx := chordctx.Background()

	t.Run("an imported archive exports the same parts: lossless, at the manifest's lastSeq and next document id", func(t *testing.T) {
		parts := importArchive(t)
		store := importedStore(t, partSource(parts), false)
		got := exported(t, store, ctx)
		if want := partTexts(t, parts); !reflect.DeepEqual(partTexts(t, got), want) {
			t.Fatalf("exported parts differ:\n got %v\nwant %v", partTexts(t, got), want)
		}
		manifest := parts[0].(*ManifestPart)
		if store.LastSeq() != manifest.LastSeq || store.NextID() != manifest.NextID {
			t.Fatalf("[lastSeq, nextId] = [%d, %d], want [%d, %d]", store.LastSeq(), store.NextID(), manifest.LastSeq, manifest.NextID)
		}
	})

	t.Run("the store keeps its ids, seqs and folded documents; the outbox, when asked, starts after lastSeq", func(t *testing.T) {
		parts := importArchive(t)
		store := importedStore(t, partSource(parts), true)
		read, err := store.Outbox().Read(store.LastSeq(), 10, ctx)
		if err != nil || len(read) != 0 {
			t.Fatalf("outbox.read(lastSeq) = %v, %v, want none", read, err)
		}
		if _, err := store.Outbox().Read(0, 10, ctx); !archiveIsCode(err, data.CodeInvalid) {
			t.Fatalf("outbox.read(0) error = %v, want invalid", err)
		}
		address := DocumentAddress{Namespace: "chord", Owner: "app.store", Kind: "memo", Key: new("a")}
		record, err := Read(store, func(executor sqlite.SqliteExecutor) (*DocumentRecord, error) {
			return FindDocument(executor, address, CurrentPoint)
		}, ctx)
		if err != nil || record == nil {
			t.Fatalf("FindDocument = %v, %v", record, err)
		}
		var original *DocumentRecord
		for _, part := range parts {
			if create, ok := part.(*DocumentCreate); ok && create.Record.Key != nil && *create.Record.Key == "a" {
				original = &create.Record
			}
		}
		if original == nil || nexttest.JSONText(t, record) != nexttest.JSONText(t, original) {
			t.Fatalf("record = %s, want %s", nexttest.JSONText(t, record), nexttest.JSONText(t, original))
		}
		document, err := Read(store, func(executor sqlite.SqliteExecutor) (StoredDocument, error) {
			return LoadDocument(executor, *record, CurrentPoint)
		}, ctx)
		if err != nil {
			t.Fatalf("LoadDocument: %v", err)
		}
		if got, want := nexttest.JSONText(t, document.Value), `{"title":"a3"}`; got != want {
			t.Fatalf("value = %s, want %s", got, want)
		}
	})

	t.Run("a migrating table resumes on the imported store; rows written at version 1 read as the table's", func(t *testing.T) {
		store := importedStore(t, partSource(importArchive(t)), false)
		notesV2, editsV2 := archiveTables.NotesV2, archiveTables.EditsV2
		err := PrepareTables([]DeclaredTable{{Definition: notesV2, Table: "app.store:notes"}}, store, openFence, "app", ctx)
		if err == nil || !strings.Contains(err.Error(), "crash") {
			t.Fatalf("PrepareTables(notes) error = %v, want crash", err)
		}
		edits := NewTableHandle(editsV2, "app.store:edits", store, tableOwner())
		got, err := edits.Get(Row{"id": "e003"}, ctx)
		if err != nil {
			t.Fatalf("edits.get: %v", err)
		}
		if want := `{"id":"e003","path":"p/3","at":3,"meta":{"a":3},"done":false}`; nexttest.JSONText(t, got) != want {
			t.Fatalf("row = %s, want %s", nexttest.JSONText(t, got), want)
		}
	})
}

func TestImportJSONCellsHoldingNull(t *testing.T) {
	ctx := chordctx.Background()

	t.Run("a JSON null round-trips, required and optional, put and updated; an absent optional cell stays absent", func(t *testing.T) {
		source := memoryStore(t)
		cells, err := DefineTable("cells", TableOptions{
			Version: 1,
			Columns: []ColumnProp{
				Col("id", Str()),
				Col("data", Json(contracts.Nul(), JsonColumnOptions{Version: 1})),
				Col("more", Opt(Json(contracts.Nul(), JsonColumnOptions{Version: 1}))),
			},
			Key: []string{"id"},
		})
		must(t, err)
		handle := tableIn(t, source, cells)
		var staged []StagedWrite
		changes := NewTableChanges(handle, &staged)
		must(t, changes.Put(Row{"id": "a", "data": nil, "more": nil}))
		must(t, changes.Put(Row{"id": "b", "data": nil}))
		must(t, changes.Put(Row{"id": "c", "data": nil, "more": nil}))
		must(t, changes.Update(Row{"id": "c"}, Row{"more": Undefined}))
		if _, err := source.Commit(staged, openFence, ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		rows := []string{
			`{"id":"a","data":null,"more":null}`,
			`{"id":"b","data":null}`,
			`{"id":"c","data":null}`,
		}
		for _, row := range rows {
			assertRowText(t, handle, row)
		}
		parts := exported(t, source, ctx)
		store := importedStore(t, partSource(parts), false)
		if got, want := partTexts(t, exported(t, store, ctx)), partTexts(t, parts); !reflect.DeepEqual(got, want) {
			t.Fatalf("exported parts differ:\n got %v\nwant %v", got, want)
		}
		copied := NewTableHandle(cells, "app.store:cells", store, tableOwner())
		for _, row := range rows {
			assertRowText(t, copied, row)
		}
	})

	t.Run("a JSON null at an older cell version round-trips with its version and migrates on access in the copy", func(t *testing.T) {
		source := memoryStore(t)
		v1, err := DefineTable("cells", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Str()), Col("data", Json(contracts.Nul(), JsonColumnOptions{Version: 1}))},
			Key:     []string{"id"},
		})
		must(t, err)
		old := tableIn(t, source, v1)
		var staged []StagedWrite
		must(t, NewTableChanges(old, &staged).Put(Row{"id": "a", "data": nil}))
		if _, err := source.Commit(staged, openFence, ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		v2, err := EvolveTable(v1, func(t *TableEditor) *TableEditor {
			return t.Json("data", Json(contracts.Obj(contracts.Prop("was", contracts.Nul())), JsonColumnOptions{
				Version: 2,
				Migrate: func(value delta.JsonValue, _ int) (any, error) {
					return delta.JsonObjectOf("was", value), nil
				},
			}))
		})
		must(t, err)
		handle := tableIn(t, source, v2)
		assertRowText(t, handle, `{"id":"a","data":{"was":null}}`)
		parts := exported(t, source, ctx)
		var put ArchivePart
		for _, part := range parts {
			if part.StoreWriteKind() == "put" {
				put = part
				break
			}
		}
		if got := nexttest.ParseJSON(t, nexttest.JSONText(t, put)); !nexttest.JSONEqual(got, archiveJSON(`{"kind":"put","table":"app.store:cells","row":{"id":"a","data":null},"version":1,"columns":{"data":1}}`)) {
			t.Fatalf("first put = %s", nexttest.JSONText(t, put))
		}
		store := importedStore(t, partSource(parts), false)
		if got, want := partTexts(t, exported(t, store, ctx)), partTexts(t, parts); !reflect.DeepEqual(got, want) {
			t.Fatalf("exported parts differ:\n got %v\nwant %v", got, want)
		}
		copied := NewTableHandle(v2, "app.store:cells", store, tableOwner())
		assertRowText(t, copied, `{"id":"a","data":{"was":null}}`)
	})
}

func TestImportRefusals(t *testing.T) {
	two53 := float64(int64(1) << 53)
	cases := []struct {
		name    string
		edit    func(parts []any) []any
		message string
	}{
		{
			"a part outside the schema",
			func(parts []any) []any {
				return append(append(append([]any{}, parts[:1]...), archiveJSON(`{"kind":"put"}`)), parts[1:]...)
			},
			"not an archive part",
		},
		{"no manifest first", func(parts []any) []any { return parts[1:] }, "the manifest comes first"},
		{"a second manifest", func(parts []any) []any { return append([]any{parts[0]}, parts...) }, "the manifest comes first, once"},
		{
			"another format",
			func(parts []any) []any {
				importAt(parts, 0).Set("format", float64(2))
				return parts
			},
			"format 2 is not 1",
		},
		{
			"a newer store schema",
			func(parts []any) []any {
				importAt(parts, 0).Set("schema", float64(9))
				return parts
			},
			"schema 9 is newer",
		},
		{
			"a lastSeq of 2^53 (a later seq would repeat)",
			func(parts []any) []any {
				importAt(parts, 0).Set("lastSeq", two53)
				return parts
			},
			"part 0: not an archive part",
		},
		{
			"a document revision beyond 2^53 - 1",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "document.create")), "record").Set("revision", two53)
				return parts
			},
			"not an archive part",
		},
		{"a part after the end", func(parts []any) []any {
			return append(append([]any{}, parts...), archiveJSON(`{"kind":"end","writes":0}`))
		}, "after the end"},
		{"no end", func(parts []any) []any { return parts[:len(parts)-1] }, "has no end part"},
		{
			"a wrong end count",
			func(parts []any) []any {
				importAt(parts, len(parts)-1).Set("writes", float64(1))
				return parts
			},
			"counts 1 writes",
		},
		{
			"a put before its table",
			func(parts []any) []any {
				put, table := importFirst(parts, "put", ":notes"), importFirst(parts, "table")
				return concatParts(parts[:table], []any{parts[put]}, parts[table:])
			},
			"a put follows its table's table write",
		},
		{
			"a put after its table's indexes",
			func(parts []any) []any {
				index := importFirst(parts, "index")
				return concatParts(parts[:index+1], []any{parts[index-1]}, parts[index+1:])
			},
			"a put follows its table's table write, before its indexes",
		},
		{
			"a table twice",
			func(parts []any) []any {
				table := importFirst(parts, "table", ":notes")
				return concatParts(parts[:table], []any{parts[importFirst(parts, "table")]}, parts[table:])
			},
			"occurs twice",
		},
		{
			"a table after the documents",
			func(parts []any) []any {
				table, documents := importFirst(parts, "table", ":notes"), importFirst(parts, "document.create")
				return concatParts(parts[:table], parts[documents:len(parts)-1], parts[table:documents], []any{parts[len(parts)-1]})
			},
			"tables come before the documents",
		},
		{
			"a table name that is not owner:table",
			func(parts []any) []any {
				importAt(parts, importFirst(parts, "table")).Set("table", "edits")
				return parts
			},
			"not owner:table",
		},
		{
			"a column name breaking the rules",
			func(parts []any) []any {
				table := importAt(parts, importFirst(parts, "table"))
				table.Set("columns", withMember(importMember(table, "columns"), "bad name", archiveJSON(`{"type":"str","optional":true}`)))
				return parts
			},
			"not a column name",
		},
		{
			"a row with an unknown column",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "put")), "row").Set("extra", float64(1))
				return parts
			},
			`unknown column "extra"`,
		},
		{
			"a row without a non-opt column",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "put")), "row").Delete("path")
				return parts
			},
			`column "path" is missing`,
		},
		{
			"a value of another type",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "put")), "row").Set("at", "x")
				return parts
			},
			"is not a int value",
		},
		{
			"a json(...) cell newer than its column",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "put")), "columns").Set("meta", float64(5))
				return parts
			},
			"no readable version",
		},
		{
			"a row newer than its table",
			func(parts []any) []any {
				importAt(parts, importFirst(parts, "put")).Set("version", float64(7))
				return parts
			},
			"version 7 is newer",
		},
		{
			"an id not below the manifest's next id",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "document.create")), "record").Set("id", float64(10000))
				return parts
			},
			"is not below",
		},
		{
			"an id twice",
			func(parts []any) []any {
				creates := importAll(parts, "document.create")
				id := importMember(importAt(parts, creates[0]), "record").Value("id")
				importMember(importAt(parts, creates[1]), "record").Set("id", id)
				return parts
			},
			"occurs twice",
		},
		{
			"a record created after lastSeq",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "document.create")), "record").Set("createdAt", float64(10000))
				return parts
			},
			"a lifetime outside the manifest's lastSeq",
		},
		{
			"a base at another version than its record's",
			func(parts []any) []any {
				importRevisionContent(parts, importFirst(parts, "document.revision")).Set("version", float64(2))
				return parts
			},
			"has no base at its version",
		},
		{
			"a delta without a base at its version",
			func(parts []any) []any {
				importRevisionContent(parts, importFirstDelta(parts)).Set("version", float64(2))
				return parts
			},
			"a delta without a base at its version",
		},
		{
			"a revision out of seq order",
			func(parts []any) []any {
				importAt(parts, importFirstDelta(parts)).Set("seq", float64(1))
				return parts
			},
			"out of seq order",
		},
		{
			"a revision of another record",
			func(parts []any) []any {
				importAt(parts, importFirst(parts, "document.revision")).Set("id", float64(9999))
				return parts
			},
			"follows its record's create",
		},
		{
			"a live record without revisions",
			func(parts []any) []any {
				skip := importFirst(parts, "document.revision")
				return concatParts(parts[:skip], parts[skip+1:])
			},
			"has no base at its version",
		},
		{
			"revisions of a retired none record",
			func(parts []any) []any {
				record := importMember(importAt(parts, importFirst(parts, "document.create")), "record")
				record.Set("retiredAt", record.Value("createdAt"))
				return parts
			},
			"keeps no revisions",
		},
		{
			"a retired rewindable record without its history",
			func(parts []any) []any {
				record := importMember(importAt(parts, importFirst(parts, "document.create")), "record")
				record.Set("history", "rewindable")
				record.Set("retiredAt", record.Value("createdAt"))
				id := record.Value("id")
				var kept []any
				for _, part := range parts {
					if importKind(part) != "document.revision" || part.(*delta.JsonObject).Value("id") != id {
						kept = append(kept, part)
					}
				}
				return kept
			},
			"has no base at its version",
		},
		{
			"two live records at one address",
			func(parts []any) []any {
				creates := importAll(parts, "document.create")
				key, _ := importMember(importAt(parts, creates[0]), "record").Get("key")
				importMember(importAt(parts, creates[1]), "record").Set("key", key)
				return parts
			},
			"a second record at one address for an overlapping lifetime",
		},
		{
			"a record's meta with a NUL",
			func(parts []any) []any {
				importMember(importAt(parts, importFirst(parts, "document.create")), "record").Set("meta", "a\u0000b")
				return parts
			},
			"text may not contain NUL",
		},
		{
			"a record's meta with a lone surrogate",
			func(parts []any) []any {
				// A lone surrogate is WTF-8 in a Go string (internal/jsstring).
				importMember(importAt(parts, importFirst(parts, "document.create")), "record").Set("meta", "a\xed\xa0\x80")
				return parts
			},
			"the meta is not well-formed text",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parts := importEdited(t, c.edit)
			db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			_, err = ImportStore(SqliteBackend(db), SlicePartSource(parts), chordctx.Background(), StoreOptions{})
			chordErr, ok := data.IsChordError(err)
			if !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonArgs || !strings.Contains(chordErr.Message, c.message) {
				t.Fatalf("ImportStore error = %v, want invalid/args containing %q", err, c.message)
			}
		})
	}

	t.Run("a non-empty backend is refused; a refused import closes its backend and leaves it as it was", func(t *testing.T) {
		ctx := chordctx.Background()
		path := filepath.Join(t.TempDir(), "store.db")
		parts := importArchive(t)
		open := func() *Backend {
			db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			return SqliteBackend(db)
		}
		broken := append(append([]ArchivePart{}, parts[:len(parts)-1]...), &EndPart{Writes: 0})
		if _, err := ImportStore(open(), partSource(broken), ctx, StoreOptions{}); !archiveIsCode(err, data.CodeInvalid) {
			t.Fatalf("ImportStore(broken) error = %v, want invalid", err)
		}
		db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		rows, err := db.All("SELECT name FROM sqlite_master")
		if err != nil || len(rows) != 0 {
			t.Fatalf("sqlite_master = %v, %v, want empty", rows, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		imported, err := ImportStore(open(), partSource(parts), ctx, StoreOptions{})
		if err != nil {
			t.Fatalf("ImportStore: %v", err)
		}
		if err := imported.(*ChordStore).Close(ctx); err != nil {
			t.Fatalf("close: %v", err)
		}
		_, err = ImportStore(open(), partSource(parts), ctx, StoreOptions{})
		if err == nil || err.Error() != "import: the backend holds a store already" {
			t.Fatalf("ImportStore(again) error = %v, want import: the backend holds a store already", err)
		}
		_ = os.Remove(path)
	})

	t.Run("an input that stalls is cancelled by ctx: the import rejects aborted and the input is closed", func(t *testing.T) {
		parts := importArchive(t)
		stalled := &stalledSource{values: parts[:3], closed: make(chan struct{})}
		cancellable, cancel := chordctx.WithCancel(chordctx.Background())
		db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := ImportStore(SqliteBackend(db), stalled, cancellable, StoreOptions{})
			result <- err
		}()
		time.Sleep(10 * time.Millisecond)
		cancel(errors.New("stop"))
		err = <-result
		if !archiveIsCode(err, data.CodeAborted) {
			t.Fatalf("ImportStore error = %v, want aborted", err)
		}
		select {
		case <-stalled.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("the stalled input was not closed")
		}
	})
}

// stalledSource yields `values`, then waits for ever: a Close wakes nothing, as Pi's never-settling promise.
type stalledSource struct {
	values []ArchivePart
	index  int
	closed chan struct{}
}

func (source *stalledSource) Next(ctx context.Context) (any, bool, error) {
	if source.index < len(source.values) {
		source.index++
		return source.values[source.index-1], true, nil
	}
	<-make(chan struct{})
	return nil, false, nil
}

func (source *stalledSource) Close() error {
	close(source.closed)
	return nil
}

// importFirstDelta is the position of the first delta revision.
func importFirstDelta(parts []any) int {
	for index, part := range parts {
		if importKind(part) == "document.revision" && importMember(part.(*delta.JsonObject), "content").Value("kind") == "delta" {
			return index
		}
	}
	return -1
}

// concatParts is the parts of every list, in order, in a new slice.
func concatParts(lists ...[]any) []any {
	var all []any
	for _, list := range lists {
		all = append(all, list...)
	}
	return all
}

// withMember is a copy of object with `key` set after its own members.
func withMember(object *delta.JsonObject, key string, value any) *delta.JsonObject {
	copied := object.Clone()
	copied.Set(key, value)
	return copied
}

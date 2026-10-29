package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/store/table-writes.test.ts: table writes (slice 6a §6, §9): schema writes, row writes
// as the outbox records them, and alter writes.

const writesTable = "app.store:edits"

// writesEdits is the `edits` table of the test: version 2, a json(...) column at version 3 and an index.
func writesEdits(t *testing.T) *TableDefinition {
	t.Helper()
	definition, err := DefineTable("edits", TableOptions{
		Version: 2,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("path", Str()),
			Col("hits", Int(contracts.Default(0))),
			Col("note", Opt(Str())),
			Col("meta", Opt(Json(contracts.Obj(), JsonColumnOptions{Version: 3}))),
		},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_path", Columns: []string{"path"}}},
	})
	if err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return definition
}

// openFence is the fence of a commit that is admitted and adopted by nobody.
var openFence = CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}

// writesJSON is writes as the JSON a replica reads (what the outbox stores).
func writesJSON(t *testing.T, writes []StoreWrite) any {
	t.Helper()
	return nexttest.ParseJSON(t, nexttest.JSONText(t, writes))
}

// wantJSON is the expected JSON of text.
func wantJSON(t *testing.T, text string) any { return nexttest.ParseJSON(t, text) }

// commitChanges commits what fill stages into handle's TableChanges in one commit.
func commitChanges(t *testing.T, store *ChordStore, handle *TableHandle, fill func(changes *TableChanges)) {
	t.Helper()
	var staged []StagedWrite
	fill(NewTableChanges(handle, &staged))
	if _, err := store.Commit(staged, openFence, background); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// noError fails the test on err.
func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// physicalColumns are the physical columns of the edits table.
func physicalColumns(t *testing.T, store *ChordStore) []string {
	t.Helper()
	rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(`SELECT name FROM pragma_table_info('` + writesTable + `')`)
	}, background)
	noError(t, err)
	names := make([]string, len(rows))
	for index, row := range rows {
		names[index], _ = row["name"].(string)
	}
	return names
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00") && len(a) == len(b)
}

func TestTableWrites(t *testing.T) {
	t.Run("schema writes", func(t *testing.T) {
		t.Run("record the table and its indexes; the table write carries version, state, columns, key, fingerprint", func(t *testing.T) {
			edits := writesEdits(t)
			store := memoryStore(t, true)
			tableIn(t, store, edits)
			commits, err := store.Outbox().Read(0, 1, background)
			noError(t, err)
			want := `[
				{"kind":"table","table":"app.store:edits","version":2,"state":"ready","columns":` + nexttest.JSONText(t, edits.Record) +
				`,"key":["id"],"fingerprint":"` + edits.Fingerprint + `"},
				{"kind":"index","table":"app.store:edits","name":"by_path","columns":["path"]}
			]`
			nexttest.RequireJSONEqual(t, writesJSON(t, commits[0].Writes), wantJSON(t, want))
			record := store.Table(writesTable)
			if record == nil || record.Version != 2 || record.State != StateReady {
				t.Fatalf("table record = %+v", record)
			}
			if columns, ok := record.Indexes.Get("by_path"); !ok || !equalStrings(columns, []string{"path"}) {
				t.Fatalf("indexes = %+v", record.Indexes)
			}
			indexes, err := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ?`, writesTable)
			}, background)
			noError(t, err)
			found := false
			for _, row := range indexes {
				if row["name"] == writesTable+":by_path" {
					found = true
				}
			}
			if !found {
				t.Fatalf("sqlite_master indexes = %v", indexes)
			}
		})

		t.Run("an index write for a table the store does not have refuses the step (D4: the store goes on)", func(t *testing.T) {
			store := memoryStore(t, true)
			tableIn(t, store, writesEdits(t))
			write := StageIndex(&IndexWrite{Table: "app.store:nope", Name: "x", Columns: []string{"id"}})
			_, err := store.Commit([]StagedWrite{write}, openFence, background)
			if err == nil || !strings.Contains(err.Error(), "not in the store") {
				t.Fatalf("Commit = %v, want not in the store", err)
			}
			one, err := Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
			if err != nil || one != 1 {
				t.Fatalf("Read after refusal = %v, %v", one, err)
			}
		})
	})

	t.Run("row writes in the outbox", func(t *testing.T) {
		t.Run("carry what a replica needs: put with v and json versions, update and delete with counts, the canonical set", func(t *testing.T) {
			store := memoryStore(t, true)
			handle := tableIn(t, store, writesEdits(t))
			commitChanges(t, store, handle, func(changes *TableChanges) {
				noError(t, changes.Put(Row{"id": "a", "path": "x/1", "meta": delta.NewJsonObject(0)}))
				noError(t, changes.Put(Row{"id": "b", "path": "x/2"}))
				noError(t, changes.Update(Row{"id": "a"}, func(e ColumnRefs) SetOf {
					return SetOf{Assign("hits", Add(e["hits"], 1)), Assign("note", Undefined), Assign("meta", delta.NewJsonObject(0))}
				}))
				noError(t, changes.Update(Row{"id": "zz"}, SetOf{Assign("note", "n")}))
				noError(t, changes.Delete(Row{"id": "b"}))
				noError(t, changes.Delete(Row{"id": "b"}))
			})
			commits, err := store.Outbox().Read(0, 10, background)
			noError(t, err)
			want := `[
				{"kind":"put","table":"app.store:edits","row":{"id":"a","path":"x/1","hits":0,"meta":{}},"version":2,"columns":{"meta":3}},
				{"kind":"put","table":"app.store:edits","row":{"id":"b","path":"x/2","hits":0},"version":2,"columns":{}},
				{"kind":"update","table":"app.store:edits","key":{"id":"a"},
					"set":{"hits":{"add":[{"col":"hits"},1]},"note":null,"meta":{"json":{}}},"columns":{"meta":3},"count":1},
				{"kind":"update","table":"app.store:edits","key":{"id":"zz"},"set":{"note":"n"},"columns":{},"count":0},
				{"kind":"delete","table":"app.store:edits","key":{"id":"b"},"count":1},
				{"kind":"delete","table":"app.store:edits","key":{"id":"b"},"count":0}
			]`
			if len(commits) != 2 {
				t.Fatalf("commits = %d, want 2", len(commits))
			}
			nexttest.RequireJSONEqual(t, writesJSON(t, commits[1].Writes), wantJSON(t, want))
		})

		t.Run("a put is an upsert: the whole row replaced", func(t *testing.T) {
			store := memoryStore(t, true)
			handle := tableIn(t, store, writesEdits(t))
			commitChanges(t, store, handle, func(changes *TableChanges) {
				noError(t, changes.Put(Row{"id": "a", "path": "x", "hits": int64(5), "note": "n"}))
			})
			commitChanges(t, store, handle, func(changes *TableChanges) {
				noError(t, changes.Put(Row{"id": "a", "path": "y"}))
			})
			row, err := handle.Get(Row{"id": "a"}, background)
			noError(t, err)
			nexttest.RequireJSONEqual(t, row, wantJSON(t, `{"id":"a","path":"y","hits":0}`))
		})

		t.Run("updateWhere and deleteWhere are one statement each, recorded with where, set and count", func(t *testing.T) {
			store := memoryStore(t, true)
			handle := tableIn(t, store, writesEdits(t))
			commitChanges(t, store, handle, func(changes *TableChanges) {
				for i := range 5 {
					path := "b/" + string(rune('0'+i))
					if i < 3 {
						path = "a/" + string(rune('0'+i))
					}
					noError(t, changes.Put(Row{"id": "e" + string(rune('0'+i)), "path": path}))
				}
			})
			meter := metered(t)
			var staged []StagedWrite
			changes := NewTableChanges(handle, &staged)
			noError(t, changes.UpdateWhere(
				func(e ColumnRefs) Condition { return Prefix(e["path"], "a/") },
				func(e ColumnRefs) SetOf { return SetOf{Assign("path", ReplacePrefix(e["path"], "a/", "c/"))} },
			))
			noError(t, changes.DeleteWhere(func(e ColumnRefs) Condition { return Eq(e["path"], "b/4") }))
			_, err := store.Commit(staged, openFence, meter.Context)
			noError(t, err)
			// The two bulk writes, the outbox commit, lastSeq.
			if got := meter.Counts()["chord.store.statements"]; got != 4 {
				t.Fatalf("chord.store.statements = %v, want 4", got)
			}
			commits, err := store.Outbox().Read(0, 10, background)
			noError(t, err)
			want := `[
				{"kind":"updateWhere","table":"app.store:edits","where":{"prefix":["path","a/"]},
					"set":{"path":{"replacePrefix":[{"col":"path"},"a/","c/"]}},"columns":{},"count":3},
				{"kind":"deleteWhere","table":"app.store:edits","where":{"eq":["path","b/4"]},"count":1}
			]`
			nexttest.RequireJSONEqual(t, writesJSON(t, commits[2].Writes), wantJSON(t, want))
			page, err := handle.Page(NewQuery(writesEditsDef(t, handle)), 10, background, PageOptions{})
			noError(t, err)
			var paths []string
			for _, row := range page.Items {
				paths = append(paths, row["path"].(string))
			}
			if !equalStrings(paths, []string{"c/0", "c/1", "c/2", "b/3"}) {
				t.Fatalf("paths = %v", paths)
			}
		})
	})

	t.Run("alter writes", func(t *testing.T) {
		// altered applies edits to the edits table as the migration to version 3 does, one commit.
		altered := func(t *testing.T, store *ChordStore, edits ...TableEdit) {
			t.Helper()
			staged := make([]StagedWrite, len(edits))
			for index, edit := range edits {
				staged[index] = StageEdit(writesTable, 3, edit)
			}
			_, err := store.Commit(staged, openFence, background)
			noError(t, err)
		}

		t.Run("rename a column: a json(...) one's version column, the key and the indexes follow", func(t *testing.T) {
			store := memoryStore(t, true)
			handle := tableIn(t, store, writesEdits(t))
			commitChanges(t, store, handle, func(changes *TableChanges) { noError(t, changes.Put(Row{"id": "a", "path": "p"})) })
			altered(t, store, TableEdit{Kind: "rename", From: "meta", To: "info"}, TableEdit{Kind: "rename", From: "path", To: "file"})
			if got := physicalColumns(t, store); !equalStrings(got, []string{"id", "file", "hits", "note", "info", "info$v", "$v"}) {
				t.Fatalf("physical = %v", got)
			}
			record := store.Table(writesTable)
			if columns, ok := record.Indexes.Get("by_path"); !ok || !equalStrings(columns, []string{"file"}) {
				t.Fatalf("indexes = %+v", record.Indexes)
			}
			if got := record.Columns.Names(); !equalStrings(got, []string{"id", "file", "hits", "note", "info"}) {
				t.Fatalf("record columns = %v", got)
			}
		})

		t.Run("add a column: existing rows get its default; an opt(...) json(...) one adds its version column", func(t *testing.T) {
			store := memoryStore(t, true)
			handle := tableIn(t, store, writesEdits(t))
			commitChanges(t, store, handle, func(changes *TableChanges) { noError(t, changes.Put(Row{"id": "a", "path": "p"})) })
			version := 1
			altered(t, store,
				TableEdit{Kind: "add", Name: "level", Column: ColumnRecord{Type: ColInt, Default: int64(3)}},
				TableEdit{Kind: "add", Name: "extra", Column: ColumnRecord{Type: ColJson, Optional: true, Version: &version, Fingerprint: "f"}},
			)
			want := []string{"id", "path", "hits", "note", "meta", "meta$v", "$v", "level", "extra", "extra$v"}
			if got := physicalColumns(t, store); !equalStrings(got, want) {
				t.Fatalf("physical = %v", got)
			}
			rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All(`SELECT level FROM "` + writesTable + `"`)
			}, background)
			noError(t, err)
			if len(rows) != 1 || rows[0]["level"] != int64(3) {
				t.Fatalf("rows = %v", rows)
			}
			level, _ := store.Table(writesTable).Columns.Get("level")
			if level.Type != ColInt || level.Default != int64(3) || level.Optional {
				t.Fatalf("level = %+v", level)
			}
		})

		t.Run("drop a column (with its version column); a json edit changes the record only", func(t *testing.T) {
			store := memoryStore(t, true)
			handle := tableIn(t, store, writesEdits(t))
			commitChanges(t, store, handle, func(changes *TableChanges) { noError(t, changes.Put(Row{"id": "a", "path": "p"})) })
			version := 4
			column := ColumnRecord{Type: ColJson, Optional: true, Version: &version, Fingerprint: "g"}
			altered(t, store, TableEdit{Kind: "drop", Name: "note"}, TableEdit{Kind: "json", Name: "meta", Column: column})
			if got := physicalColumns(t, store); !equalStrings(got, []string{"id", "path", "hits", "meta", "meta$v", "$v"}) {
				t.Fatalf("physical = %v", got)
			}
			meta, _ := store.Table(writesTable).Columns.Get("meta")
			if meta.Type != ColJson || !meta.Optional || meta.Version == nil || *meta.Version != 4 || meta.Fingerprint != "g" {
				t.Fatalf("meta = %+v", meta)
			}
			altered(t, store, TableEdit{Kind: "drop", Name: "meta"})
			if got := physicalColumns(t, store); !equalStrings(got, []string{"id", "path", "hits", "$v"}) {
				t.Fatalf("physical after dropping meta = %v", got)
			}
		})

		t.Run("are recorded as alter writes with the migration's version; index edits as index and dropIndex", func(t *testing.T) {
			store := memoryStore(t, true)
			tableIn(t, store, writesEdits(t))
			seq := store.LastSeq()
			altered(t, store, TableEdit{Kind: "dropIndex", Name: "by_path"}, TableEdit{Kind: "drop", Name: "path"})
			commits, err := store.Outbox().Read(seq, 1, background)
			noError(t, err)
			want := `[
				{"kind":"dropIndex","table":"app.store:edits","name":"by_path"},
				{"kind":"alter","table":"app.store:edits","version":3,"edit":{"kind":"drop","name":"path"}}
			]`
			nexttest.RequireJSONEqual(t, writesJSON(t, commits[0].Writes), wantJSON(t, want))
		})
	})
}

// writesEditsDef is the definition a handle was made from.
func writesEditsDef(_ *testing.T, handle *TableHandle) *TableDefinition { return handle.Definition }

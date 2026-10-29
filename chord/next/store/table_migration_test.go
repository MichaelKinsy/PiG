package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next/store/table-migration.test.ts: tables brought to their version at activation
// (slice 6a §9): fresh tables created at the newest version, eager migrations one version at a time in their commits
// ((A), chunks of 100, (Z)), `rows` and `update` steps, index edits, resumption at `migrating`, and the refusals that
// leave the data as it was.
//
// Designed out: the two "the chunk yield" cases. They watch JavaScript's timer mechanics (`setImmediate` removed,
// `setTimeout` spied, a due timer starving under a microtask-only loop); Go has none of these: the yield between
// chunks is `runtime.Gosched()` and goroutines are preempted. Their other assertion (the table ends version 2, ready
// after 300 and 1000 rows) is covered by "one version at a time" (250 rows) and "resumption".

const migrationTable = "app.store:edits"

type migrationVersions struct{ v1, v2, v3 *TableDefinition }

func migrationEdits(t *testing.T) migrationVersions {
	t.Helper()
	v1 := qDefine(t, "edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Str()), Col("file", Str()), Col("at", Int())},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_file", Columns: []string{"file"}}},
	})
	v2 := qEvolve(t, v1, func(e *TableEditor) *TableEditor {
		return e.
			Rename("file", "path").
			Add("dir", Str(contracts.Default(""))).
			Rows(func(row Row) (any, error) {
				path := row["path"].(string)
				return Row{"dir": path[:strings.LastIndex(path, "/")]}, nil
			})
	})
	v3 := qEvolve(t, v2, func(e *TableEditor) *TableEditor { return e.Drop("dir").Index("by_path", []string{"path", "at"}) })
	return migrationVersions{v1, v2, v3}
}

// prepare prepares definition as the edits table of store.
func prepare(store *ChordStore, definition *TableDefinition) error {
	return PrepareTables([]DeclaredTable{{Definition: definition, Table: migrationTable}}, store, openFence, "app.store", background)
}

// prepared prepares definition as the edits table of store; a handle to it.
func prepared(t *testing.T, store *ChordStore, definition *TableDefinition) *TableHandle {
	t.Helper()
	must(t, prepare(store, definition))
	return NewTableHandle(definition, migrationTable, store, tableOwner())
}

func migrationID(i int) string { return fmt.Sprintf("e%03d", i) }

// filledEdits is a store whose edits table is at version 1 with count rows e000.. (file `d<i % 3>/f<i>`, at i).
func filledEdits(t *testing.T, count int, v1 *TableDefinition) *ChordStore {
	t.Helper()
	store := memoryStore(t, true)
	edits := prepared(t, store, v1)
	var staged []StagedWrite
	changes := NewTableChanges(edits, &staged)
	for i := range count {
		must(t, changes.Put(Row{"id": migrationID(i), "file": fmt.Sprintf("d%d/f%d", i%3, i), "at": int64(i)}))
	}
	if _, err := store.Commit(staged, openFence, background); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return store
}

// commitsAfter is the kinds of the writes of every commit after seq, each kind with its count.
func commitsAfter(t *testing.T, store *ChordStore, seq int64) [][]string {
	t.Helper()
	commits, err := store.Outbox().Read(seq, 100, background)
	must(t, err)
	out := [][]string{}
	for _, commit := range commits {
		var order []string
		counts := map[string]int{}
		for _, write := range commit.Writes {
			kind := write.StoreWriteKind()
			if counts[kind] == 0 {
				order = append(order, kind)
			}
			counts[kind]++
		}
		kinds := []string{}
		for _, kind := range order {
			if counts[kind] == 1 {
				kinds = append(kinds, kind)
			} else {
				kinds = append(kinds, fmt.Sprintf("%s x%d", kind, counts[kind]))
			}
		}
		out = append(out, kinds)
	}
	return out
}

// allRows is every row of handle, in key order.
func allRows(t *testing.T, handle *TableHandle, definition *TableDefinition) []Row {
	t.Helper()
	iterator := handle.Rows(NewQuery(definition), background)
	defer iterator.Close()
	var rows []Row
	for {
		row, ok, err := iterator.Next(background)
		must(t, err)
		if !ok {
			return rows
		}
		rows = append(rows, row)
	}
}

func TestFreshTables(t *testing.T) {
	versions := migrationEdits(t)

	t.Run("are created at the newest version, ready, with their indexes, in one commit (no replay)", func(t *testing.T) {
		store := memoryStore(t, true)
		prepared(t, store, versions.v3)
		record := store.Table(migrationTable)
		qEqual(t, []any{record.Version, record.State, record.Fingerprint}, []any{3, StateReady, versions.v3.Fingerprint})
		qEqual(t, record.Indexes, IndexRecords{
			{Name: "by_file", Columns: []string{"path"}},
			{Name: "by_path", Columns: []string{"path", "at"}},
		})
		qEqual(t, commitsAfter(t, store, 0), [][]string{{"table", "index x2"}})
	})

	t.Run("a table at the newest version, ready, is used as recorded: nothing is written", func(t *testing.T) {
		store := filledEdits(t, 1, versions.v1)
		seq := store.LastSeq()
		prepared(t, store, versions.v1)
		qEqual(t, store.LastSeq(), seq)
	})
}

func TestEagerMigrations(t *testing.T) {
	versions := migrationEdits(t)

	t.Run("one version at a time: (A), chunks of 100 in key order, (Z) with the later edits and ready", func(t *testing.T) {
		store := filledEdits(t, 250, versions.v1)
		seq := store.LastSeq()
		edits := prepared(t, store, versions.v3)
		qEqual(t, commitsAfter(t, store, seq), [][]string{
			{"alter x2", "table"},
			{"update x100"},
			{"update x100"},
			{"update x50", "table"},
			{"alter", "index", "table"},
		})
		record := store.Table(migrationTable)
		qEqual(t, []any{record.Version, record.State}, []any{3, StateReady})
		qEqual(t, record.Columns.Names(), []string{"id", "path", "at"})
		qEqual(t, record.Indexes, IndexRecords{
			{Name: "by_file", Columns: []string{"path"}},
			{Name: "by_path", Columns: []string{"path", "at"}},
		})
		rows := allRows(t, edits, versions.v3)
		qEqual(t, len(rows), 250)
		qEqual(t, rows[4], Row{"id": "e004", "path": "d1/f4", "at": int64(4)})
	})

	t.Run("the data step reads the columns as they stand at its version and fills the one it adds", func(t *testing.T) {
		store := filledEdits(t, 3, versions.v1)
		edits := prepared(t, store, versions.v2)
		qEqual(t, allRows(t, edits, versions.v2), []Row{
			{"id": "e000", "path": "d0/f0", "at": int64(0), "dir": "d0"},
			{"id": "e001", "path": "d1/f1", "at": int64(1), "dir": "d1"},
			{"id": "e002", "path": "d2/f2", "at": int64(2), "dir": "d2"},
		})
	})

	t.Run("the migration's rows carry the internal form: the patch and the version stamped as $v", func(t *testing.T) {
		store := filledEdits(t, 1, versions.v1)
		seq := store.LastSeq()
		prepared(t, store, versions.v2)
		commits, err := store.Outbox().Read(seq, 10, background)
		must(t, err)
		chunk := commits[1]
		qEqual(t, len(chunk.Writes), 2)
		qEqual(t, qJSON(t, chunk.Writes[0]),
			`{"kind":"update","table":"app.store:edits","key":{"id":"e000"},"set":{"dir":"d0"},"columns":{},"count":1,"version":2}`)
		record, ok := chunk.Writes[1].(*TableDefinitionWrite)
		if !ok || record.Version != 2 || record.State != StateReady {
			t.Fatalf("second write = %#v, want the ready version 2 table write", chunk.Writes[1])
		}
	})

	t.Run("an empty table still runs (A) and (Z); a version without a data step is one commit", func(t *testing.T) {
		store := filledEdits(t, 0, versions.v1)
		seq := store.LastSeq()
		prepared(t, store, versions.v3)
		qEqual(t, commitsAfter(t, store, seq), [][]string{{"alter x2", "table"}, {"table"}, {"alter", "index", "table"}})
	})

	t.Run("a dropped column's indexes are dropped first, in the same commit", func(t *testing.T) {
		store := filledEdits(t, 2, versions.v1)
		dropped := qEvolve(t, versions.v1, func(e *TableEditor) *TableEditor { return e.Drop("file") })
		seq := store.LastSeq()
		edits := prepared(t, store, dropped)
		commits, err := store.Outbox().Read(seq, 10, background)
		must(t, err)
		first, ok := commits[0].Writes[0].(*DropIndexWrite)
		if !ok || first.Table != migrationTable || first.Name != "by_file" {
			t.Fatalf("first write = %#v, want dropIndex by_file", commits[0].Writes[0])
		}
		second, ok := commits[0].Writes[1].(*AlterWrite)
		if !ok || second.Table != migrationTable || second.Version != 2 || second.Edit.Kind != EditDrop || second.Edit.Name != "file" {
			t.Fatalf("second write = %#v, want alter drop file at version 2", commits[0].Writes[1])
		}
		record := store.Table(migrationTable)
		qEqual(t, record.Columns.Names(), []string{"id", "at"})
		qEqual(t, len(record.Indexes), 0)
		qEqual(t, allRows(t, edits, dropped), []Row{{"id": "e000", "at": int64(0)}, {"id": "e001", "at": int64(1)}})
	})

	t.Run("an update step is one updateWhere over the rows below its version, stamping it, with ready", func(t *testing.T) {
		store := filledEdits(t, 5, versions.v1)
		bumped := qEvolve(t, versions.v1, func(e *TableEditor) *TableEditor {
			return e.Update(UpdateStep{
				Where: func(r ColumnRefs) Condition { return Lt(r["at"], 3) },
				Set:   SetOf{Assign("file", "old")},
			})
		})
		seq := store.LastSeq()
		edits := prepared(t, store, bumped)
		commits, err := store.Outbox().Read(seq, 10, background)
		must(t, err)
		qEqual(t, commitsAfter(t, store, seq), [][]string{{"table"}, {"updateWhere", "table"}})
		update, ok := commits[1].Writes[0].(*UpdateWhereWrite)
		if !ok || update.Count != 3 || update.Version == nil || *update.Version != 2 {
			t.Fatalf("write = %#v, want updateWhere count 3 version 2", commits[1].Writes[0])
		}
		qEqual(t, update.Set, SetNode{{Column: "file", Node: litNode("old")}})
		var files []string
		for _, row := range allRows(t, edits, bumped) {
			files = append(files, row["file"].(string))
		}
		qEqual(t, files, []string{"old", "old", "old", "d0/f3", "d1/f4"})
	})

	t.Run("a json(...) version change records the column's version: no DDL, cells migrate on access", func(t *testing.T) {
		notes1 := qDefine(t, "edits", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Str()), Col("meta", Opt(Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1})))},
			Key:     []string{"id"},
		})
		notes2 := qEvolve(t, notes1, func(e *TableEditor) *TableEditor {
			return e.Json("meta", Json(
				contracts.Obj(contracts.Prop("a", contracts.Int()), contracts.Prop("b", contracts.Int())),
				JsonColumnOptions{Version: 2, Migrate: func(old delta.JsonValue, from int) (any, error) {
					return delta.JsonObjectOf("a", old.(*delta.JsonObject).Value("a"), "b", float64(0)), nil
				}},
			))
		})
		store := memoryStore(t, true)
		v1 := prepared(t, store, notes1)
		var staged []StagedWrite
		must(t, NewTableChanges(v1, &staged).Put(Row{"id": "a", "meta": delta.JsonObjectOf("a", float64(1))}))
		if _, err := store.Commit(staged, openFence, background); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		seq := store.LastSeq()
		v2 := prepared(t, store, notes2)
		commits, err := store.Outbox().Read(seq, 10, background)
		must(t, err)
		qEqual(t, len(commits[0].Writes), 2)
		alter, ok := commits[0].Writes[0].(*AlterWrite)
		if !ok || alter.Table != migrationTable || alter.Version != 2 || alter.Edit.Kind != EditJSON || alter.Edit.Name != "meta" ||
			alter.Edit.Column.Type != ColJson || alter.Edit.Column.Version == nil || *alter.Edit.Column.Version != 2 {
			t.Fatalf("first write = %#v, want the json alter of meta at version 2", commits[0].Writes[0])
		}
		record, ok := commits[0].Writes[1].(*TableDefinitionWrite)
		if !ok || record.Version != 2 || record.State != StateReady {
			t.Fatalf("second write = %#v, want the ready version 2 table write", commits[0].Writes[1])
		}
		row, err := v2.Get(Row{"id": "a"}, background)
		must(t, err)
		qEqual(t, row["id"], "a")
		qEqual(t, qJSON(t, row["meta"]), `{"a":1,"b":0}`)
	})
}

func TestMigrationPatches(t *testing.T) {
	t.Run("a rows patch writes a JSON null into a json(...) cell and clears an opt column with undefined", func(t *testing.T) {
		cells1 := qDefine(t, "edits", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Str()), Col("data", Json(contracts.Nul(), JsonColumnOptions{Version: 1})), Col("note", Opt(Str()))},
			Key:     []string{"id"},
		})
		cells2 := qEvolve(t, cells1, func(e *TableEditor) *TableEditor {
			return e.
				Add("more", Opt(Json(contracts.Nul(), JsonColumnOptions{Version: 1}))).
				Rows(func(Row) (any, error) { return Row{"more": nil, "note": Undefined}, nil })
		})
		store := memoryStore(t)
		v1 := prepared(t, store, cells1)
		var staged []StagedWrite
		must(t, NewTableChanges(v1, &staged).Put(Row{"id": "a", "data": nil, "note": "n"}))
		if _, err := store.Commit(staged, openFence, background); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		v2 := prepared(t, store, cells2)
		row, err := v2.Get(Row{"id": "a"}, background)
		must(t, err)
		qEqual(t, row, Row{"id": "a", "data": nil, "more": nil})
	})
}

func TestMigrationResumption(t *testing.T) {
	versions := migrationEdits(t)

	t.Run("a data step that throws leaves the table migrating with its committed chunks; the next run continues", func(t *testing.T) {
		store := filledEdits(t, 250, versions.v1)
		fail := true
		failing := qEvolve(t, versions.v1, func(e *TableEditor) *TableEditor {
			return e.Rows(func(row Row) (any, error) {
				if fail && row["id"] == migrationID(150) {
					return nil, errors.New("crash")
				}
				return Row{"file": row["file"].(string) + "!"}, nil
			})
		})
		err := prepare(store, failing)
		if err == nil || err.Error() != "crash" {
			t.Fatalf("prepare error = %v, want crash", err)
		}
		record := store.Table(migrationTable)
		qEqual(t, []any{record.Version, record.State}, []any{2, StateMigrating})
		seq := store.LastSeq()
		fail = false
		edits := prepared(t, store, failing)
		qEqual(t, commitsAfter(t, store, seq), [][]string{{"update x100"}, {"update x50", "table"}})
		// Every row patched once: the first chunk's rows were skipped on the second run.
		for _, row := range allRows(t, edits, failing) {
			file := row["file"].(string)
			if !strings.HasSuffix(file, "!") || strings.HasSuffix(file, "!!") {
				t.Fatalf("file %q was not patched exactly once", file)
			}
		}
		record = store.Table(migrationTable)
		qEqual(t, []any{record.Version, record.State}, []any{2, StateReady})
	})

	t.Run("a refused commit (the activation abandoned) stops the migration between chunks", func(t *testing.T) {
		store := filledEdits(t, MigrationChunk*2+1, versions.v1)
		admitted := 2
		fence := CommitFence{
			Admit: func() bool {
				open := admitted > 0
				admitted--
				return open
			},
			Adopt: func(int64, []int64) error { return nil },
		}
		err := PrepareTables([]DeclaredTable{{Definition: versions.v2, Table: migrationTable}}, store, fence, "app.store", background)
		_ = rejected(t, err, data.CodeReleased, "")
		record := store.Table(migrationTable)
		qEqual(t, []any{record.Version, record.State}, []any{2, StateMigrating})
	})
}

func TestMigrationRefusals(t *testing.T) {
	versions := migrationEdits(t)
	for _, c := range []struct {
		name    string
		prepare func() *TableDefinition
		message string
	}{
		{"a newer version recorded", func() *TableDefinition { return versions.v1 }, "newer than the code's 1"},
		{"a version below the history's base", func() *TableDefinition {
			return qDefine(t, "edits", TableOptions{Version: 7, Columns: []ColumnProp{Col("id", Str())}, Key: []string{"id"}})
		}, "older than the history's base 7"},
		{"a history edited in place", func() *TableDefinition {
			return qEvolve(t, versions.v1, func(e *TableEditor) *TableEditor { return e.Rename("file", "name") })
		}, "recorded with another definition"},
	} {
		t.Run(c.name+" (the store at version 2) throws before anything is written", func(t *testing.T) {
			store := filledEdits(t, 1, versions.v1)
			prepared(t, store, versions.v2)
			seq := store.LastSeq()
			qFails(t, prepare(store, c.prepare()), c.message)
			qEqual(t, store.LastSeq(), seq)
		})
	}
}

package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/catalog.test.ts: the catalog (slice 6a §8): the store's records of its tables,
// read once at open, changed after COMMIT returned.

const catalogTable = "app.store:edits"

func catalogEdits(t *testing.T) *TableDefinition {
	t.Helper()
	definition, err := DefineTable("edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Str()), Col("at", Int())},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_at", Columns: []string{"at"}}},
	})
	if err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return definition
}


func TestCatalog(t *testing.T) {
	edits := catalogEdits(t)

	t.Run("is read once at open: a reopened store knows its tables", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.sqlite")
		open := func() *ChordStore {
			db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			opened, err := OpenStore(SqliteBackend(db), background, StoreOptions{})
			if err != nil {
				t.Fatalf("OpenStore: %v", err)
			}
			return opened.(*ChordStore)
		}
		first := open()
		tableIn(t, first, edits)
		if err := first.Close(background); err != nil {
			t.Fatalf("Close: %v", err)
		}
		second := open()
		got := second.Table(catalogTable)
		want := &TableRecord{
			Table:       catalogTable,
			Version:     1,
			State:       StateReady,
			Columns:     edits.Record,
			Key:         []string{"id"},
			Fingerprint: edits.Fingerprint,
			Indexes:     IndexRecords{{Name: "by_at", Columns: []string{"at"}}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Table = %+v, want %+v", got, want)
		}
		if err := second.Close(background); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	t.Run("changes in memory only after COMMIT returned", func(t *testing.T) {
		store := memoryStore(t)
		failing := stagedWrite{
			order: OrderWrite,
			apply: func(step *Step) ([]StoreWrite, error) {
				if err := step.Run("INSERT INTO no_such_table VALUES (1)"); err != nil {
					return nil, err
				}
				return []StoreWrite{&DropIndexWrite{Table: catalogTable, Name: "x"}}, nil
			},
		}
		table := StageTable(&TableDefinitionWrite{
			Table: catalogTable, Version: edits.Version, State: StateReady, Columns: edits.Record, Key: edits.Key, Fingerprint: edits.Fingerprint,
		})
		fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
		_, err := store.Commit([]StagedWrite{table, failing}, fence, background)
		if err == nil || !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("Commit error = %v, want no such table", err)
		}
		if record := store.Table(catalogTable); record != nil {
			t.Fatalf("Table = %+v, want none", record)
		}
		if err := store.Close(background); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

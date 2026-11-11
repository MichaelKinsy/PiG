package cloudflare_test

// pi: packages/chord/src/sqlite/cloudflare.ts

// Ports the "a store on it tells a CHECK refusal by its message, its error having no extended code" case of
// packages/chord/test/sqlite/cloudflare.test.ts (Pi 14b4c56e), which runs Chord v2's store (chord/next/store).

import (
	"context"
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
)

// messageOnlySql is the Durable Object binding as the facade sees it: its errors carry a message only.
type messageOnlySql struct{ cloudflare.DurableObjectSql }

func (sql messageOnlySql) Exec(query string, bindings ...cloudflare.DurableObjectSqlValue) (cloudflare.DurableObjectSqlCursor, error) {
	cursor, err := sql.DurableObjectSql.Exec(query, bindings...)
	if err != nil {
		return nil, errors.New(err.Error())
	}
	return cursor, nil
}

type messageOnlyStorage struct{ *fakeStorage }

func (storage messageOnlyStorage) Sql() cloudflare.DurableObjectSql {
	return messageOnlySql{storage.fakeStorage.Sql()}
}

// checkedWrite is the staged write of the Pi case: an insert that violates the table's CHECK.
type checkedWrite struct{}

func (checkedWrite) StageOrder() store.WriteOrder { return store.OrderWrite }

func (checkedWrite) Apply(step *store.Step) ([]store.StoreWrite, error) {
	return nil, step.Run(`INSERT INTO "checked" VALUES (11)`)
}

func TestStoreOnDurableObjectTellsCheckByMessage(t *testing.T) {
	t.Run("a store on it tells a CHECK refusal by its message, its error having no extended code", func(t *testing.T) {
		ctx := chordctx.Background()
		opened, err := store.OpenStore(store.SqliteBackend(cloudflare.NewDurableObjectSqliteDatabase(messageOnlyStorage{newFakeStorage(t)})), ctx, store.StoreOptions{})
		if err != nil {
			t.Fatal(err)
		}
		chordStore := opened.(*store.ChordStore)
		if _, err := store.Read(chordStore, func(executor chordsqlite.SqliteExecutor) (struct{}, error) {
			return struct{}{}, executor.Exec(`CREATE TABLE "checked" (n INTEGER CHECK (n < 10))`)
		}, ctx); err != nil {
			t.Fatal(err)
		}
		fence := store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
		_, err = chordStore.Commit([]store.StagedWrite{checkedWrite{}}, fence, ctx)
		if err == nil {
			t.Fatal("the commit succeeded")
		}
		refusal := store.RefusalOf(err, "x")
		if refusal == nil || refusal.Kind != store.RefusalCheck {
			t.Fatalf("refusal = %+v, want kind check", refusal)
		}
		got, err := store.Read(chordStore, func(chordsqlite.SqliteExecutor) (int, error) { return 1, nil }, context.Background())
		if err != nil || got != 1 {
			t.Fatalf("read after the refusal = %v, %v", got, err)
		}
	})
}

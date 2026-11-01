package harness

// Ports packages/durable/vitest.store.config.ts and test/store-storage-node.ts (+ test/store-storage-support.ts)
//
// Pi re-runs a list of harness test files on Durable's storage over Chord's store (`StoreStorage`) by aliasing
// `../src/storage/sqlite/node.ts` to `test/store-storage-node.ts`: every `openNodeSqliteStorage(path)` of those files
// then opens a StoreStorage over the same SQLite file. The Go counterpart: the tests open their SQLite storage through
// openNodeSqliteStorage below, which opens a StoreStorage when the environment variable PIG_DURABLE_TEST_STORAGE is
// `store` and the SQLite storage otherwise.
//
//	make test-durable-store
//
// which runs, with the variable set, the tests of the Go files that mirror Pi's `include` list (DURABLE_STORE_FILES in
// the Makefile). The variable changes the opener of every test that calls it, so the run is limited to those files.
//
// Pi's config leaves four tests out (they edit or read SqliteStorage's own tables with raw SQL): its testNamePattern
// excludes a test whose full name contains one of the phrases of storeVariantExcluded. The opener skips a test whose
// name contains one, as the pattern does (a test that does not open its storage here is not affected).

import (
	"context"
	"os"
	"strings"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/storage/store"
)

// storeStorageEnv selects the storage the harness tests open over a SQLite file: `store` is StoreStorage.
const storeStorageEnv = "PIG_DURABLE_TEST_STORAGE"

// storeVariantOwner is the owner name of the StoreStorage the tests open (test/store-storage-support.ts).
const storeVariantOwner = "durable.test"

// storeVariantExcluded are the test names `vitest.store.config.ts` leaves out of the store run (its testNamePattern).
var storeVariantExcluded = []string{
	"resumes a retry backoff after reopen",
	"sleeps again for a retry stored as a running phase",
	"polls a checkpoint stored without a thinking level",
	"refuses to create the root of an empty storage",
}

// storeVariantOn reports whether the harness tests open StoreStorage.
func storeVariantOn() bool { return os.Getenv(storeStorageEnv) == "store" }

// openNodeSqliteStorage is `openNodeSqliteStorage` of `../src/storage/sqlite/node.ts`, or of
// `test/store-storage-node.ts` for the store run: Durable's storage over the SQLite file at path.
func openNodeSqliteStorage(t *testing.T, path string) durable.Storage {
	t.Helper()
	if !storeVariantOn() {
		storage, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return storage
	}
	name := strings.ReplaceAll(t.Name(), "_", " ")
	for _, excluded := range storeVariantExcluded {
		if strings.Contains(name, strings.ReplaceAll(excluded, "_", " ")) {
			t.Skipf("left out of the store run (vitest.store.config.ts): %s", excluded)
		}
	}
	return openStoreStorage(t, path)
}

// ownedStoreStorage is a StoreStorage whose Close closes its store too (in tests the storage is the store's only
// owner).
type ownedStoreStorage struct {
	*store.StoreStorage
	store chordstore.Store
}

// Close closes the storage, then its store (idempotent).
func (owned *ownedStoreStorage) Close(ctx context.Context) error {
	if err := owned.StoreStorage.Close(ctx); err != nil {
		return err
	}
	return owned.store.Close(ctx)
}

// openStoreStorage opens a StoreStorage on a store over the SQLite file at path whose Close closes the store too
// (test/store-storage-support.ts openStoreStorage).
func openStoreStorage(t *testing.T, path string) durable.Storage {
	t.Helper()
	background := chordctx.Background()
	db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	chord, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(background, chord, storeVariantOwner)
	if err != nil {
		_ = chord.Close(background)
		t.Fatal(err)
	}
	return &ownedStoreStorage{StoreStorage: storage, store: chord}
}

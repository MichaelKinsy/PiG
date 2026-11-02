package cloudflare_test

// pi: packages/chord/src/sqlite/cloudflare.ts

// Ports the "the outcome of a transaction (slice 6b D4)" driver case of packages/chord/test/sqlite/cloudflare.test.ts (Pi 14b4c56e): "rejects with the callback's own error after a rollback, another one when the commit fails". The case "a store on it tells a CHECK refusal by its message" runs Chord v2's store (chord/next/store) and belongs to the store port.

import (
	"errors"
	"strings"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
)

// failingCommitStorage is cloudflare.test.ts `failingCommit`: the storage whose transaction fails after the closure resolved, as a failed commit does, when asked.
type failingCommitStorage struct {
	*fakeStorage
	failing bool
}

func (storage *failingCommitStorage) Transaction(closure func() error) error {
	err := storage.fakeStorage.Transaction(closure)
	if err != nil || !storage.failing {
		return err
	}
	storage.failing = false
	return errors.New("commit failed")
}

func TestDurableObjectSqliteTransactionOutcome(t *testing.T) {
	t.Run("rejects with the callback's own error after a rollback, another one when the commit fails", func(t *testing.T) {
		storage := &failingCommitStorage{fakeStorage: newFakeStorage(t)}
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		own := errors.New("refused")
		err := database.Transaction(func(chordsqlite.SqliteExecutor) error { return own })
		if err != own { //nolint:errorlint // identity: the caller must get the callback's own error value
			t.Fatalf("error = %v, want the callback's own error", err)
		}
		storage.failing = true
		err = database.Transaction(func(chordsqlite.SqliteExecutor) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "commit failed") {
			t.Fatalf("error = %v, want the commit failure", err)
		}
	})
}

package session

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

type forkContextKey struct{}

// forkStorage scripts the two storage reads prepareForkDocumentCopies makes and records what it was asked.
type forkStorage struct {
	durable.Storage
	entry    *durable.EntryAt
	scanFail map[int]error // scan number (0-based) -> failure
	contexts []context.Context
	scans    []durable.DocumentQuery
}

func (storage *forkStorage) VisibleEntry(ctx context.Context, _ durable.ConversationId, _ durable.EntryId) (*durable.EntryAt, error) {
	storage.contexts = append(storage.contexts, ctx)
	return storage.entry, nil
}

func (storage *forkStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, _ int, _ durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	storage.contexts = append(storage.contexts, ctx)
	storage.scans = append(storage.scans, query)
	return durable.Page[durable.DocumentRecord, durable.Cursor]{}, storage.scanFail[len(storage.scans)-1]
}

// forks.ts prepareForkDocumentCopies awaits the entry lookup, then the as-of scan of the entry-owning conversation at the entry's commit, then the current scan of
// the parent, each with the caller's context; a failure of any step is the failure of the whole selection, and no later step runs.
func TestPrepareForkDocumentCopiesAwaitsItsReadsInOrderWithTheCallersContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), forkContextKey{}, "caller")
	storage := &forkStorage{entry: &durable.EntryAt{Entry: durable.EntryRecord{ConversationId: 3}, CommitSeq: 9}}

	copies, err := prepareForkDocumentCopies(ctx, storage, 5, 7, 11)

	if err != nil || len(copies) != 0 {
		t.Fatalf("copies = %v, err = %v", copies, err)
	}
	if len(storage.contexts) != 3 {
		t.Fatalf("%d storage reads, want the entry lookup and two scans", len(storage.contexts))
	}
	for i, got := range storage.contexts {
		if got.Value(forkContextKey{}) != "caller" {
			t.Errorf("storage read %d did not receive the caller's context", i)
		}
	}
	if len(storage.scans) != 2 ||
		storage.scans[0].Scope.ConversationId != 3 || storage.scans[0].At != durable.AtSeq(9) ||
		storage.scans[1].Scope.ConversationId != 5 || storage.scans[1].At != durable.CurrentPoint {
		t.Fatalf("scans = %+v, want the entry-owning conversation as of its commit, then the parent's current state", storage.scans)
	}

	t.Run("an entry that is not visible ends the selection before any scan", func(t *testing.T) {
		storage := &forkStorage{}
		if _, err := prepareForkDocumentCopies(ctx, storage, 5, 7, 11); err == nil || err.Error() != "Entry 7 is not visible from conversation 5" {
			t.Fatalf("err = %v", err)
		}
		if len(storage.scans) != 0 {
			t.Fatalf("%d scans after the entry was refused", len(storage.scans))
		}
	})
	for scan, name := range []string{"the as-of scan", "the current scan"} {
		t.Run(name+" failing is the selection's failure", func(t *testing.T) {
			failure := errors.New("scan failed")
			storage := &forkStorage{entry: &durable.EntryAt{Entry: durable.EntryRecord{ConversationId: 3}, CommitSeq: 9}, scanFail: map[int]error{scan: failure}}
			copies, err := prepareForkDocumentCopies(ctx, storage, 5, 7, 11)
			if !errors.Is(err, failure) || copies != nil {
				t.Fatalf("copies = %v, err = %v", copies, err)
			}
			if want := scan + 1; len(storage.scans) != want {
				t.Fatalf("%d scans, want %d: a scan ran after the failure", len(storage.scans), want)
			}
		})
	}
}

// Ports packages/durable/test/harness-context-range.test.ts.

package harness

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// pausedStorage holds the next entry scan, once its page is read, until released.
type pausedStorage struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	pause   bool
	entered chan struct{}
	release chan struct{}
}

func (store *pausedStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	page, err := store.MemoryStorage.ScanEntries(ctx, query, limit, cursor)
	store.mu.Lock()
	pause := store.pause
	store.pause = false
	store.mu.Unlock()
	if pause {
		close(store.entered)
		<-store.release
	}
	return page, err
}

func TestKeptContextRanges(t *testing.T) {
	// harness-context-range.test.ts:8 it("is not advanced when an entry was committed while its scan ran off the line")
	t.Run("is not advanced when an entry was committed while its scan ran off the line", func(t *testing.T) {
		store := &pausedStorage{MemoryStorage: storage.NewMemoryStorage(), entered: make(chan struct{}), release: make(chan struct{})}
		line := session.CreateSession(store)
		conversationId, err := durable.Commit(testContext, line, func(tx durable.Tx) (durable.ConversationId, error) {
			record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownerless})
			return record.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		appendKind := func(kind string) durable.EntryRecord {
			t.Helper()
			entry, err := durable.Commit(testContext, line, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(conversationId, durable.EntryDraft{Kind: kind})
			})
			if err != nil {
				t.Fatal(err)
			}
			return entry
		}
		appendKind("first")
		store.mu.Lock()
		store.pause = true
		store.mu.Unlock()
		type read struct {
			view durable.ContextView
			rng  *contextRange
			err  error
		}
		reading := make(chan read, 1)
		go func() {
			view, rng, err := readContextFrom(testContext, sessionLine{line}, store, conversationId, nil, nil)
			reading <- read{view, rng, err}
		}()
		<-store.entered
		appendKind("missed")
		close(store.release)
		got := <-reading
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.rng == nil || got.rng.current {
			t.Fatalf("range %+v, want one that is not current", got.rng)
		}
		last := appendKind("last")
		// Not current: left as it is, so the next read extends it from Storage instead of skipping "missed".
		if advanced, keep := advanceRange(got.rng, []durable.EntryRecord{last}); !keep || advanced != got.rng {
			t.Fatalf("advanceRange = %p %v, want the range itself", advanced, keep)
		}
		again, againRange, err := readContextFrom(testContext, sessionLine{line}, store, conversationId, nil, got.rng)
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := readContext(testContext, sessionLine{line}, store, conversationId, nil)
		if err != nil {
			t.Fatal(err)
		}
		kinds := []string{}
		for _, entry := range again.Entries {
			kinds = append(kinds, entry.Kind)
		}
		if want := []string{"first", "missed", "last"}; !reflect.DeepEqual(kinds, want) {
			t.Fatalf("kinds %v, want %v", kinds, want)
		}
		if !reflect.DeepEqual(again, fresh) {
			t.Fatalf("extended view %+v differs from a fresh read %+v", again, fresh)
		}
		if againRange == nil || !againRange.current {
			t.Fatalf("range %+v, want a current one", againRange)
		}
		if err := line.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})
}

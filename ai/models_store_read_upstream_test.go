package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func freshSharedModelsReadState(t *testing.T) {
	t.Helper()
	sharedModelsFileReadState.Lock()
	oldPath, oldState := sharedModelsFileReadState.path, sharedModelsFileReadState.state
	sharedModelsFileReadState.path, sharedModelsFileReadState.state = "", nil
	sharedModelsFileReadState.Unlock()
	t.Cleanup(func() {
		sharedModelsFileReadState.Lock()
		sharedModelsFileReadState.path, sharedModelsFileReadState.state = oldPath, oldState
		sharedModelsFileReadState.Unlock()
	})
}

func TestFileModelsStoreCoalescesReadsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/models-store.test.ts:67
	t.Run("coalesces file reloads across concurrent readers and interleaved storage instances", func(t *testing.T) {
		freshSharedModelsReadState(t)
		path := filepath.Join(t.TempDir(), "models-store.json")
		write := func(value any) {
			t.Helper()
			if err := os.WriteFile(path, []byte(mustStoreJSON(t, value)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write(map[string]ModelsStoreEntry{"one": {Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "old")})}, "two": {Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("two", "m2")})}})
		var locks atomic.Int32
		lock := func(ctx context.Context, path string, fn func(func() error) error) error {
			locks.Add(1)
			return withSidecarLock(ctx, path, fn)
		}
		first, second := NewFileModelsStore(path), NewFileModelsStore(path)
		first.lock, second.lock = lock, lock
		type result struct {
			entry *ModelsStoreEntry
			err   error
		}
		synctest.Test(t, func(t *testing.T) {
			one, two, missing := make(chan result, 1), make(chan result, 1), make(chan result, 1)
			go func() { e, err := first.Read(t.Context(), "one"); one <- result{e, err} }()
			go func() { e, err := second.Read(t.Context(), "two"); two <- result{e, err} }()
			go func() { e, err := first.Read(t.Context(), "missing"); missing <- result{e, err} }()
			for _, tc := range []struct {
				done chan result
				id   string
			}{{one, "old"}, {two, "m2"}, {missing, ""}} {
				got := <-tc.done
				if got.err != nil {
					t.Fatal(got.err)
				}
				if tc.id == "" {
					if got.entry != nil {
						t.Fatal("missing provider returned catalog")
					}
					continue
				}
				assertStoredID(t, got.entry, tc.id)
			}
		})
		if got := locks.Load(); got != 1 {
			t.Fatalf("initial locks=%d, want one shared reload", got)
		}
		entry, err := second.Read(t.Context(), "one")
		if err != nil {
			t.Fatal(err)
		}
		assertStoredID(t, entry, "old")
		if got := locks.Load(); got != 1 {
			t.Fatalf("cached read locked again: %d", got)
		}
		otherPath := filepath.Join(t.TempDir(), "other-models-store.json")
		if err := os.WriteFile(otherPath, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := NewFileModelsStore(otherPath)
		other.lock = lock
		for range 2 {
			if got, err := other.Read(t.Context(), "one"); err != nil || got != nil {
				t.Fatalf("other read=%v, %v", got, err)
			}
		}
		third := NewFileModelsStore(path)
		third.lock = lock
		write(map[string]ModelsStoreEntry{"one": {Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "newest-model")})}})
		synctest.Test(t, func(t *testing.T) {
			done := make(chan result, 2)
			for _, store := range []*FileModelsStore{first, third} {
				go func() { e, err := store.Read(t.Context(), "one"); done <- result{e, err} }()
			}
			for range 2 {
				got := <-done
				if got.err != nil {
					t.Fatal(got.err)
				}
				assertStoredID(t, got.entry, "newest-model")
			}
		})
		if got := locks.Load(); got != 3 {
			t.Fatalf("locks=%d, want initial + other path + replacement", got)
		}
	})
}

func TestFileModelsStoreKeepsSharedReadAliveUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/models-store.test.ts:105
	t.Run("keeps a coalesced reload alive while another reader is still waiting", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models-store.json")
		if err := os.WriteFile(path, []byte(mustStoreJSON(t, map[string]ModelsStoreEntry{"one": {Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "stored")})}})), 0o600); err != nil {
			t.Fatal(err)
		}
		store := NewFileModelsStore(path)
		synctest.Test(t, func(t *testing.T) {
			grant := make(chan struct{})
			var locks, releases atomic.Int32
			store.lock = func(ctx context.Context, _ string, fn func(func() error) error) error {
				locks.Add(1)
				<-grant
				defer releases.Add(1)
				if err := ctx.Err(); err != nil {
					return err
				}
				return fn(ctx.Err)
			}
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			defer cancelFirst()
			first := make(chan error, 1)
			second := make(chan *ModelsStoreEntry, 1)
			secondErr := make(chan error, 1)
			go func() { _, err := store.Read(firstCtx, "one"); first <- err }()
			go func() { e, err := store.Read(t.Context(), "one"); second <- e; secondErr <- err }()
			synctest.Wait()
			cancelFirst()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Errorf("first error=%v", err)
			}
			close(grant)
			assertStoredID(t, <-second, "stored")
			if err := <-secondErr; err != nil {
				t.Fatal(err)
			}
			if locks.Load() != 1 || releases.Load() != 1 {
				t.Fatalf("locks=%d releases=%d", locks.Load(), releases.Load())
			}
		})
	})
}

func BenchmarkFileModelsStoreCachedCatalog(b *testing.B) {
	for _, size := range []int{0, 8, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			store := NewFileModelsStore(filepath.Join(b.TempDir(), "models-store.json"))
			entry := ModelsStoreEntry{Models: make([]AnyModel, size)}
			for i := range entry.Models {
				entry.Models[i] = mustStoredModels([]json.RawMessage{upstreamStoredModel("one", fmt.Sprintf("model-%d", i))})[0]
			}
			if err := store.Write(b.Context(), "one", entry); err != nil {
				b.Fatal(err)
			}
			if _, err := store.Read(b.Context(), "one"); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := store.Read(b.Context(), "one"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func assertStoredID(t *testing.T, entry *ModelsStoreEntry, id string) {
	t.Helper()
	if entry == nil || len(entry.Models) != 1 {
		t.Fatalf("entry=%+v", entry)
	}
	if model := (struct{ ID string }{entry.Models[0].ModelID()}); model.ID != id {
		t.Fatalf("id=%s, want %s", model.ID, id)
	}
}

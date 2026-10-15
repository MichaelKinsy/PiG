package ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// countRevisionStats counts fileRevisionOf calls per path for the test.
func countRevisionStats(t *testing.T) func(path string) int64 {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int64{}
	previous := fileRevisionOf
	fileRevisionOf = func(path string) (fileRevision, bool) {
		mu.Lock()
		counts[path]++
		mu.Unlock()
		return previous(path)
	}
	t.Cleanup(func() { fileRevisionOf = previous })
	return func(path string) int64 {
		mu.Lock()
		defer mu.Unlock()
		return counts[path]
	}
}

func readKey(t *testing.T, ctx context.Context, store *AuthStorage, provider string) string {
	t.Helper()
	credential, err := store.Read(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	if credential == nil {
		return ""
	}
	return credential.Key
}

// The availability pass and the catalog restore read auth.json once per provider, from up to 45 goroutines (lever-RegistryAuth Q1: about 440
// auth.json and 84 models-store.json stats per print run). Pi makes those reads in one macrotask; one operation's reads now share one stat.
func TestFileReadSnapshotStatsOncePerOperation(t *testing.T) {
	freshSharedModelsReadState(t)
	authPath := seedAuthFile(t, `{"anthropic":{"type":"api_key","key":"a"},"openai":{"type":"api_key","key":"o"}}`)
	store, err := NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	models := NewFileModelsStore(filepath.Join(t.TempDir(), "models-store.json"))
	if err := models.Write(t.Context(), "anthropic", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("anthropic", "m")})}); err != nil {
		t.Fatal(err)
	}
	stats := countRevisionStats(t)
	const readers = 45
	for operation := 1; operation <= 2; operation++ {
		ctx := WithFileReadSnapshot(t.Context())
		authBefore, modelsBefore := stats(authPath), stats(models.path)
		var wg sync.WaitGroup
		var failures atomic.Int32
		for i := range readers {
			wg.Go(func() {
				provider := []string{"anthropic", "openai"}[i%2]
				credential, err := store.Read(ctx, provider)
				if err != nil || credential == nil || credential.Key != provider[:1] {
					failures.Add(1)
				}
				if _, err := store.List(ctx); err != nil {
					failures.Add(1)
				}
				if _, err := models.Read(ctx, "anthropic"); err != nil {
					failures.Add(1)
				}
			})
		}
		wg.Wait()
		if failures.Load() != 0 {
			t.Fatalf("operation %d: %d failed reads", operation, failures.Load())
		}
		// The first operation's models-store read reloads the store Write left unpublished; the reload stats it once more.
		wantModels := int64(1)
		if operation == 1 {
			wantModels = 2
		}
		if got := stats(authPath) - authBefore; got != 1 {
			t.Errorf("operation %d: %d auth.json stats for %d reads, want 1", operation, got, 2*readers)
		}
		if got := stats(models.path) - modelsBefore; got != wantModels {
			t.Errorf("operation %d: %d models-store.json stats for %d reads, want %d", operation, got, readers, wantModels)
		}
	}
}

// Pi's readLatestData stats on every read, but an operation without extension code makes all of its reads in one macrotask, so an outside write
// lands before all of them or after all of them. The view therefore ends with the operation, and only this process's writes and reloads end it
// sooner: a write to auth.json by another process during the operation reaches the next operation, as one landing just after this one would.
func TestFileReadSnapshotFreshness(t *testing.T) {
	authPath := seedAuthFile(t, `{"anthropic":{"type":"api_key","key":"old"}}`)
	store, err := NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	rewrite := func(content string) {
		t.Helper()
		if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("another process writes between operations", func(t *testing.T) {
		first := WithFileReadSnapshot(t.Context())
		if got := readKey(t, first, store, "anthropic"); got != "old" {
			t.Fatalf("first operation read %q", got)
		}
		rewrite(`{"anthropic":{"type":"api_key","key":"between"}}`)
		if got := readKey(t, WithFileReadSnapshot(t.Context()), store, "anthropic"); got != "between" {
			t.Fatalf("next operation read %q, want the rewrite", got)
		}
	})
	t.Run("another process writes during an operation", func(t *testing.T) {
		ctx := WithFileReadSnapshot(t.Context())
		before := readKey(t, ctx, store, "anthropic")
		rewrite(`{"anthropic":{"type":"api_key","key":"during-and-longer"}}`)
		if got := readKey(t, ctx, store, "anthropic"); got != before {
			t.Fatalf("the operation read %q after %q: its reads must see one version", got, before)
		}
		if got := readKey(t, t.Context(), store, "anthropic"); got != "during-and-longer" {
			t.Fatalf("a read outside the operation read %q", got)
		}
	})
	t.Run("this process writes during an operation", func(t *testing.T) {
		ctx := WithFileReadSnapshot(t.Context())
		readKey(t, ctx, store, "anthropic")
		if _, err := store.Modify(ctx, "anthropic", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: "modified"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := readKey(t, ctx, store, "anthropic"); got != "modified" {
			t.Fatalf("the operation read %q after its own Modify", got)
		}
		if err := store.Delete(ctx, "anthropic"); err != nil {
			t.Fatal(err)
		}
		if got := readKey(t, ctx, store, "anthropic"); got != "" {
			t.Fatalf("the operation read %q after its own Delete", got)
		}
	})
	t.Run("a reload by another reader during an operation", func(t *testing.T) {
		rewrite(`{"anthropic":{"type":"api_key","key":"v1"}}`)
		ctx := WithFileReadSnapshot(t.Context())
		readKey(t, ctx, store, "anthropic")
		rewrite(`{"anthropic":{"type":"api_key","key":"v2-reloaded"}}`)
		if got := readKey(t, t.Context(), store, "anthropic"); got != "v2-reloaded" {
			t.Fatalf("a read outside the operation read %q", got)
		}
		// The process has published the newer version; the operation never reads older than what the process already returned.
		if got := readKey(t, ctx, store, "anthropic"); got != "v2-reloaded" {
			t.Fatalf("the operation read %q after the process published the rewrite", got)
		}
	})
}

// A "!command" key runs its command on read, and the command can rewrite auth.json between the operation's reads, as it can in Pi. Such a view is
// not reused: every read stats.
func TestFileReadSnapshotStatsEveryReadAfterACommandKey(t *testing.T) {
	authPath := seedAuthFile(t, `{"anthropic":{"type":"api_key","key":"!echo k"},"openai":{"type":"api_key","key":"o"}}`)
	store, err := NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	stats := countRevisionStats(t)
	ctx := WithFileReadSnapshot(t.Context())
	for range 3 {
		readKey(t, ctx, store, "openai")
	}
	if got := stats(authPath); got != 3 {
		t.Fatalf("%d auth.json stats for 3 reads of a store with a command key, want 3", got)
	}
}

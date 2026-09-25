package ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileModelsStoreCreatesFilePreservesOrderAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "models-store.json")
	store := NewFileModelsStore(path)
	ctx := context.Background()

	entry, err := store.Read(ctx, "llama.cpp")
	if err != nil || entry != nil {
		t.Fatalf("Read(empty) = %v, %v; want nil, nil", entry, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{}" {
		t.Fatalf("store file after first read = %q, %v; want {}", data, err)
	}
	// Windows file modes carry no group/other bits (Go reports 0666 or
	// 0444, as Node ignores the mode there), so owner-only is checked on Unix.
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("store mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}

	if err := os.WriteFile(path, []byte(`{"zeta":{"models":[]},"alpha":{"models":[{"id":"a<b"}],"etag":"\"x\""}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	checkedAt := 1700000000000.0
	if err := store.Write(ctx, "llama.cpp", ModelsStoreEntry{Models: []json.RawMessage{json.RawMessage(`{"id":"m"}`)}, CheckedAt: &checkedAt}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "zeta": {
    "models": []
  },
  "alpha": {
    "models": [
      {
        "id": "a<b"
      }
    ],
    "etag": "\"x\""
  },
  "llama.cpp": {
    "models": [
      {
        "id": "m"
      }
    ],
    "checkedAt": 1700000000000
  }
}`
	if string(data) != want {
		t.Fatalf("store file =\n%s\nwant\n%s", data, want)
	}
	entry, err = store.Read(ctx, "llama.cpp")
	if err != nil || entry == nil || len(entry.Models) != 1 || entry.CheckedAt == nil || *entry.CheckedAt != checkedAt {
		t.Fatalf("Read(llama.cpp) = %+v, %v", entry, err)
	}
	alpha, err := store.Read(ctx, "alpha")
	if err != nil || alpha == nil || alpha.ETag != `"x"` {
		t.Fatalf("Read(alpha) = %+v, %v", alpha, err)
	}
}

func TestFileModelsStoreRejectsInvalidJSONAndCancelledContexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileModelsStore(path)
	if _, err := store.Read(context.Background(), "x"); err == nil {
		t.Fatal("Read of a non-object store succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Write(ctx, "x", ModelsStoreEntry{}); err == nil {
		t.Fatal("Write with a cancelled context succeeded")
	}
}

// JSON.parse rejects incomplete and trailing documents before either read or write.
func TestFileModelsStoreRejectsTruncatedAndTrailingJSON(t *testing.T) {
	for _, content := range []string{`{`, `{"x":{"models":[]}`, `{} garbage`, `{} {}`, "   "} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models-store.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewFileModelsStore(path)
			if _, err := store.Read(context.Background(), "x"); err == nil {
				t.Error("read accepted invalid JSON")
			}
			if err := store.Write(context.Background(), "x", ModelsStoreEntry{}); err == nil {
				t.Error("write accepted invalid JSON")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatalf("invalid source changed: %q, %v", data, err)
			}
		})
	}
}

func TestFileModelsStoreWriteReplacesDuplicateProviderKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.WriteFile(path, []byte(`{"x":{"models":[],"etag":"first"},"x":{"models":[],"etag":"last"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewFileModelsStore(path)
	before, err := store.Read(context.Background(), "x")
	if err != nil || before == nil || before.ETag != "last" {
		t.Fatalf("initial read: %+v, %v", before, err)
	}
	if err := store.Write(context.Background(), "x", ModelsStoreEntry{Models: []json.RawMessage{}, ETag: "new"}); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(context.Background(), "x")
	if err != nil || after == nil || after.ETag != "new" {
		t.Fatalf("updated read: %+v, %v", after, err)
	}
}

var (
	_ ModelsStore = (*FileModelsStore)(nil)
	_ ModelsStore = (*InMemoryModelsStore)(nil)
)

// FileModelsStore.delete rewrites JSON.stringify(current, null, 2) without the
// provider and keeps the remaining keys in order.
func TestFileModelsStoreDeleteKeepsOtherProvidersInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.WriteFile(path, []byte(`{"zeta":{"models":[]},"radius":{"models":[{"id":"auto"}],"checkedAt":1},"alpha":{"models":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileModelsStore(path)
	ctx := context.Background()
	if err := store.Delete(ctx, "radius"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"zeta\": {\n    \"models\": []\n  },\n  \"alpha\": {\n    \"models\": []\n  }\n}"
	if string(data) != want {
		t.Fatalf("store file =\n%s\nwant\n%s", data, want)
	}
	if entry, err := store.Read(ctx, "radius"); err != nil || entry != nil {
		t.Fatalf("Read(deleted) = %+v, %v; want nil, nil", entry, err)
	}
	if err := store.Delete(ctx, "zeta"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{}" {
		t.Fatalf("store file after deleting every provider = %q, %v; want {}", data, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Delete(cancelled, "x"); err == nil {
		t.Fatal("Delete with a cancelled context succeeded")
	}
}

func TestFileModelsStoreDeleteRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.WriteFile(path, []byte(`{"x":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewFileModelsStore(path).Delete(context.Background(), "x"); err == nil {
		t.Fatal("Delete accepted invalid JSON")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != `{"x":` {
		t.Fatalf("invalid source changed: %q, %v", data, err)
	}
}

// InMemoryModelsStore structuredClones on read and write, and honours the
// abort signal (a cancelled context) on every operation.
func TestInMemoryModelsStoreClonesAndDeletes(t *testing.T) {
	store := NewInMemoryModelsStore()
	ctx := context.Background()
	if entry, err := store.Read(ctx, "radius"); err != nil || entry != nil {
		t.Fatalf("Read(empty) = %+v, %v; want nil, nil", entry, err)
	}
	checkedAt := 5.0
	entry := ModelsStoreEntry{Models: []json.RawMessage{json.RawMessage(`{"id":"a"}`)}, CheckedAt: &checkedAt, ETag: `"e"`}
	if err := store.Write(ctx, "radius", entry); err != nil {
		t.Fatal(err)
	}
	entry.Models[0][7] = 'b'
	checkedAt = 6
	read, err := store.Read(ctx, "radius")
	if err != nil || read == nil || string(read.Models[0]) != `{"id":"a"}` || *read.CheckedAt != 5 || read.ETag != `"e"` {
		t.Fatalf("Read after mutating the written entry = %+v, %v", read, err)
	}
	read.Models[0][7] = 'c'
	*read.CheckedAt = 7
	again, err := store.Read(ctx, "radius")
	if err != nil || again == nil || string(again.Models[0]) != `{"id":"a"}` || *again.CheckedAt != 5 {
		t.Fatalf("Read after mutating a read entry = %+v, %v", again, err)
	}
	if err := store.Delete(ctx, "radius"); err != nil {
		t.Fatal(err)
	}
	if entry, err := store.Read(ctx, "radius"); err != nil || entry != nil {
		t.Fatalf("Read(deleted) = %+v, %v; want nil, nil", entry, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Read(cancelled, "radius"); err == nil {
		t.Fatal("Read with a cancelled context succeeded")
	}
	if err := store.Write(cancelled, "radius", ModelsStoreEntry{}); err == nil {
		t.Fatal("Write with a cancelled context succeeded")
	}
	if err := store.Delete(cancelled, "radius"); err == nil {
		t.Fatal("Delete with a cancelled context succeeded")
	}
}

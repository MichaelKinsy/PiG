package node

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// node.ts openNodeJsonlStorage awaits JsonlStorage.open with the caller's context and returns what it settled with: a cancelled context ends the open with the
// aborted error of the file system, and a read-only open of a directory that does not exist fails with the storage's own error instead of creating it.
func TestOpenNodeJsonlStorageSettlesWithTheOpenOfItsStorage(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	storage, err := OpenNodeJsonlStorage(ctx, "cancelled", jsonl.JsonlStorageOptions{})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		if storage != nil {
			_ = storage.Close(t.Context())
		}
		t.Fatalf("open on a cancelled context = %v, want the aborted error", err)
	}
	if _, err := OpenNodeJsonlStorage(t.Context(), "missing", jsonl.JsonlStorageOptions{ReadOnly: true}); err == nil {
		t.Fatal("a read-only open of a missing directory succeeded")
	}
}

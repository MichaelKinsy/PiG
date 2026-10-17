package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// pi: packages/durable/src/storage/memory.ts:126,498,684,710 and scan.ts:21-32 (42a3497d): a read that names an unknown conversation or carries a cursor this backend did not write throws StorageRequestError, which fails only that read and not the Session.
func TestMemoryStorageReadsRejectInvalidRequestsWithStorageRequestError(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, err := store.FindLatestHeadMarker(context.Background(), 99, nil)
	var request *durable.StorageRequestError
	if !errors.As(err, &request) || request.Message != "Unknown conversation: 99" {
		t.Fatalf("an unknown conversation read returned %T %v, want a StorageRequestError", err, err)
	}
}

package durabletest

// Ports packages/durable/src/testing/commit-writes.ts

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable"
)

// CommitWrites commits writes as one batch through Storage.Commit and returns its seq. It returns the refusal as the
// error if the storage refused the batch.
func CommitWrites(ctx context.Context, storage durable.Storage, writes []durable.StorageWrite) (durable.Seq, error) {
	var stored durable.Seq
	result, err := storage.Commit(ctx, [][]durable.StorageWrite{writes}, func(_ int, seq durable.Seq) {
		stored = seq
	})
	if err != nil {
		return 0, err
	}
	if result.Stored == 0 {
		return 0, result.Refused
	}
	return stored, nil
}

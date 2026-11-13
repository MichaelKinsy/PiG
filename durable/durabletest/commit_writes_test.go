package durabletest_test

// pi: packages/durable/src/testing/commit-writes.ts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// commitStorage is a Storage whose Commit is a function: the one method CommitWrites calls.
type commitStorage struct {
	durable.Storage
	commit func(ctx context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error)
}

func (storage commitStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
	return storage.commit(ctx, batches, receive)
}

type commitContextKey struct{}

// pi: commitWrites() sends `[writes]`, one batch, with the caller's context; it resolves with the seq the storage's receipt
// gave that batch, rejects with `result.refused` when `result.stored === 0`, and rejects with what the call rejected with.
// mutation-checked: sending the writes as more than one batch or a different batch, dropping the caller's context, returning the index instead of the seq, returning no seq, returning a seq when the batch was refused, dropping the refusal, or swallowing the call's error fails it
func TestCommitWrites(t *testing.T) {
	writes := []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 3}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 4}},
	}
	ctx := context.WithValue(context.Background(), commitContextKey{}, "caller")

	t.Run("resolves with the seq of the one batch it sent", func(t *testing.T) {
		var batches [][]durable.StorageWrite
		var gotCtx context.Context
		storage := commitStorage{commit: func(ctx context.Context, sent [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			gotCtx, batches = ctx, sent
			receive(0, 7)
			return durable.CommitResult{Stored: 1}, nil
		}}
		seq, err := durabletest.CommitWrites(ctx, storage, writes)
		if err != nil || seq != 7 {
			t.Fatalf("CommitWrites = %d, %v; want 7, nil", seq, err)
		}
		if len(batches) != 1 || len(batches[0]) != 2 || !reflect.DeepEqual(batches[0], writes) {
			t.Fatalf("batches = %v; want one batch of the writes", batches)
		}
		if gotCtx == nil || gotCtx.Value(commitContextKey{}) != "caller" {
			t.Fatal("Commit did not get the caller's context")
		}
	})

	t.Run("rejects with the refusal when the storage stored nothing", func(t *testing.T) {
		refusal := errors.New("refused")
		storage := commitStorage{commit: func(_ context.Context, _ [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			receive(0, 5)
			return durable.CommitResult{Stored: 0, Refused: refusal}, nil
		}}
		seq, err := durabletest.CommitWrites(ctx, storage, writes)
		if !errors.Is(err, refusal) || seq != 0 {
			t.Fatalf("CommitWrites = %d, %v; want 0, the refusal", seq, err)
		}
	})

	t.Run("rejects with what the call rejected with", func(t *testing.T) {
		failure := errors.New("connection lost")
		storage := commitStorage{commit: func(_ context.Context, _ [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			receive(0, 9)
			return durable.CommitResult{}, failure
		}}
		seq, err := durabletest.CommitWrites(ctx, storage, writes)
		if !errors.Is(err, failure) || seq != 0 {
			t.Fatalf("CommitWrites = %d, %v; want 0, the call's error", seq, err)
		}
	})
}

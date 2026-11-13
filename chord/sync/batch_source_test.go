package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports the pull-iterator contract of packages/chord/src/sync/batch.ts: the context of Next reaches the source's wait, a source's error
// ends the body and comes back from Next, and closing the body closes the source it wraps.

// watchedCommits is a CommitSource that answers a cancelled context with its error, else fails with fail (or with a marker when the
// context that reached it was not the caller's), and counts its closes.
type watchedCommits struct {
	fail   error
	closes int
}

var errContextLost = errors.New("the context of Next did not reach the source")

func (source *watchedCommits) Next(ctx context.Context) (store.OutboxCommit, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.OutboxCommit{}, false, err
	}
	if source.fail != nil {
		return store.OutboxCommit{}, false, source.fail
	}
	return store.OutboxCommit{}, false, errContextLost
}

func (source *watchedCommits) Close() error {
	source.closes++
	return nil
}

func TestABatchBodyPassesTheContextOfNextToItsSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &watchedCommits{}
	_, _, err := EncodeBatch(source).Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next(cancelled) = %v, want context.Canceled from the source's wait", err)
	}
}

func TestABatchBodyEndsWithTheErrorOfItsSourceAndGivesTheSourceBack(t *testing.T) {
	boom := errors.New("boom")
	source := &watchedCommits{fail: boom}
	body := EncodeBatch(source)
	if _, _, err := body.Next(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Next = %v, want the source's error", err)
	}
	if source.closes == 0 {
		t.Fatal("the source was not closed after it failed")
	}
	if chunk, ok, err := body.Next(context.Background()); ok || err != nil {
		t.Fatalf("Next after the failure = %d bytes, %v, %v; want the end", len(chunk), ok, err)
	}
}

func TestClosingABatchBodyClosesTheSourceItWraps(t *testing.T) {
	source := &watchedCommits{}
	body := EncodeBatch(source)
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if source.closes == 0 {
		t.Fatal("closing the body did not close its source")
	}
	if err := body.Close(); err != nil {
		t.Fatalf("closing again = %v, want nothing", err)
	}
}

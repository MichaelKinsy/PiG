package session_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// session.ts snapshot (163-173) and snapshotAsOf (317-332) resolve the address before queueing, and a read that has to queue re-checks the Session's health
// when it takes its turn, before it reads Storage, so a read queued behind a commit sees that commit poison the Session. DocumentReader is the Go form of
// Pick<Session, "snapshot" | "snapshotAsOf">. At the pin (Pi 42a3497d) a failed Storage call fails the Session, and the read rejects with SessionFailed,
// "Session failed after a storage error; close and reopen it" (errors.ts:43).

func TestSnapshotQueuedBehindACommitThatPoisonsTheSessionRejectsWithoutReadingStorage(t *testing.T) {
	harness := createWatchHarness(t)
	err, lookedUp := settleQueuedBehindPoisoningCommit(t, harness, func() error {
		_, err := harness.Session.SnapshotErased(context.Background(), absentWatchDoc)
		return err
	})
	expectErrorContains(t, err, "Session failed after a storage error")
	if lookedUp {
		t.Fatal("the rejected snapshot looked the document up")
	}
}

func TestSnapshotAsOfQueuedBehindACommitThatPoisonsTheSessionRejects(t *testing.T) {
	harness := createWatchHarness(t)
	conversation := createConversation(t, harness)
	err, _ := settleQueuedBehindPoisoningCommit(t, harness, func() error {
		_, err := harness.Session.SnapshotAsOfErased(context.Background(), rewindableLiveDoc, durable.EntryId(1), conversation)
		return err
	})
	expectErrorContains(t, err, "Session failed after a storage error")
}

func TestSnapshotsRejectAnAddressTheyCannotResolveBeforeQueueing(t *testing.T) {
	harness := open()
	commits := harness.Storage.HoldCommits()
	defer commits.Release()
	committed := make(chan error, 1)
	go func() {
		committed <- tryCommit(harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, watchStateDoc).Set("value", 1) })
	}()
	waitClosed(t, commits.Entered())
	jobs := harness.Session.LineJobs()
	value, err := harness.Session.SnapshotErased(context.Background(), liveDoc)
	if value != nil {
		t.Fatalf("snapshot of an unresolvable address returned %v", value)
	}
	expectErrorContains(t, err, "test.live requires a conversation ID")
	_, err = harness.Session.SnapshotAsOfErased(context.Background(), rewindableLiveDoc, durable.EntryId(1))
	expectErrorContains(t, err, "test.live requires a conversation ID")
	if after := harness.Session.LineJobs(); after != jobs {
		t.Fatalf("the unresolvable address took a place on the mutation line: %d jobs, was %d", after, jobs)
	}
	commits.Release()
	must(t, <-committed)
}

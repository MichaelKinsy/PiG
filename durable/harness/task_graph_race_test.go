package harness

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// task-graph.ts advances its mount from a commit's publication and derives the blocked reasons again from a registry change, each in one
// synchronous turn of Pi's event loop. Go's publications and the Session's line are goroutines of their own, so the mount's turn is a
// lock: a publication that creates tasks and a registry change that refreshes the reasons of the ones it holds, at the same time, must
// not read and write the mount's records and value concurrently (run with -race).
func TestTheTaskGraphAdvancesFromPublicationsWhileTheBlockedReasonsRefresh(t *testing.T) {
	const commits, refreshes = 60, 60
	ctx := context.Background()
	line := session.CreateSession(storage.NewMemoryStorage())
	var blocked atomic.Bool
	var refresh func()
	view := NewTaskGraphView(line, TaskGraphBlocking{
		Blocked: func(BlockedSubject) durable.BlockedReason {
			if blocked.Load() {
				return durable.BlockedMissingTask
			}
			return ""
		},
		SubscribeBlocked: func(listener func()) func() { refresh = listener; return func() {} },
	})
	watch, err := view.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var told atomic.Int64
	watch.Start(onValue(func(context.Context, chord.WatchCall[TaskGraph]) error { told.Add(1); return nil }))
	var conversation durable.ConversationId
	if _, err := line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			return nil, err
		}
		conversation = record.Id
		return nil, nil
	}, session.TransactionScope{}); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range refreshes {
			blocked.Store(!blocked.Load())
			refresh()
		}
	}()
	for range commits {
		if _, err := line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
			_, err := tx.CreateTaskErased(graphProbeTask.Erased(), float64(1), durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &conversation})
			return nil, err
		}, session.TransactionScope{}); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
	if err := line.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

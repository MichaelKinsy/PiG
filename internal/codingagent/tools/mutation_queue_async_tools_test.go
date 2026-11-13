package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
)

// file-mutation-queue.ts chains every call behind the previous one for its file (:52 `await currentQueue`), so no mutation starts before every earlier one has
// finished. A ticket whose WaitContext gave up keeps its place: it passes the queue on only after every earlier mutation completed. synctest.Wait returns once every
// goroutine of the bubble is blocked, so the successor's state is read after the hand-off goroutine ran as far as it can, with no timing in the test.
func TestMutationTicketGaveUpTicketHoldsItsPlaceUntilEveryPredecessorEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "shared.txt")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		q := NewFileMutationQueue()
		reserve := func() *MutationTicket {
			ticket, err := q.Reserve(path)
			if err != nil {
				t.Fatal(err)
			}
			return ticket
		}
		first, second, third, successor := reserve(), reserve(), reserve(), reserve()
		first.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := third.WaitContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitContext = %v, want context.Canceled", err)
		}
		admitted := make(chan struct{})
		go func() {
			successor.Wait()
			close(admitted)
		}()
		first.Release()
		synctest.Wait()
		select {
		case <-admitted:
			t.Fatal("the successor ran while an earlier mutation of the file was still inside the queue")
		default:
		}
		second.Wait()
		second.Release()
		synctest.Wait()
		select {
		case <-admitted:
		default:
			t.Fatal("the successor was not admitted after every earlier mutation completed")
		}
		successor.Release()
		q.mu.Lock()
		defer q.mu.Unlock()
		if len(q.chains) != 0 || len(q.files) != 0 {
			t.Fatalf("queue kept bookkeeping after draining: %d chains, %d files", len(q.chains), len(q.files))
		}
	})
}

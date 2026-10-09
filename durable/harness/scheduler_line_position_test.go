package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Upstream's #kick queues a microtask in the publishing turn, so the reservation pass takes its place on the Session
// line before any other code runs: a commit that code starts after the publication, such as a handler's or a host's,
// lines up behind the pass. Pi Durable 1.0.4 on a task created and then, from a subscriber of that publication, a
// second task created, commits "x pending", "x running", "m pending", "m running"; if the second creation overtook the
// pass, one reservation commit would start both tasks.
func TestReservationPassTakesItsLinePositionInThePublishingTurn(t *testing.T) {
	for run := range 50 {
		complete := func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return completed[stepState, durable.JsonValue](nil), nil
			})
		}
		first := tkOneStep("test.line-first", complete)
		second := tkOneStep("test.line-second", complete)
		opened := tkOpenRoot(t, []durable.AnyTask{first, second})
		impl := opened.harness.(*harnessImpl)
		var mu sync.Mutex
		var commits []string
		var started atomic.Bool
		secondErr := make(chan error, 1)
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			var writes []string
			for _, change := range publication.Changes {
				if write, ok := change.(durable.TaskWrite); ok && write.Value.State.Status != durable.TaskTerminal {
					writes = append(writes, fmt.Sprintf("%s:%s", write.Value.Kind, write.Value.State.Status))
				}
			}
			if len(writes) == 0 {
				return
			}
			mu.Lock()
			commits = append(commits, strings.Join(writes, ","))
			mu.Unlock()
			if len(writes) != 1 || writes[0] != "test.line-first:pending" || !started.CompareAndSwap(false, true) {
				return
			}
			// Still in the publishing turn: hold the scheduler's own goroutine back, start the second creation, and
			// return once it is queued. Whatever the scheduler took on the line before this point stays ahead of it.
			impl.tasks.mu.Lock()
			defer impl.tasks.mu.Unlock()
			jobs := impl.LineJobs()
			go func() {
				_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
					return tx.CreateTaskErased(second, nil, durable.TaskOptions{Ownership: conversationOwned})
				})
				secondErr <- err
			}()
			for impl.LineJobs() <= jobs {
				flush()
			}
		})
		opened.harness.Resume()
		// Resume's own pass has ended, so the creation below starts a pass of its own.
		eventually(t, func() bool {
			impl.tasks.mu.Lock()
			defer impl.tasks.mu.Unlock()
			return !impl.tasks.draining
		})
		firstId := tkStart(t, opened.root, first)
		if _, err := opened.harness.WaitForTask(testContext, firstId); err != nil {
			t.Fatal(err)
		}
		if err := opened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if err := tkWaitErr(t, secondErr); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := strings.Join(commits, " | ")
		mu.Unlock()
		want := "test.line-first:pending | test.line-first:running | test.line-second:pending | test.line-second:running"
		if got != want {
			t.Fatalf("run %d: task commits %s\nwant %s", run, got, want)
		}
		if err := opened.harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
	}
}

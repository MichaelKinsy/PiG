package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// pig divergence (D224), round 4 review B1. Every blocking point a listener can reach goes through the Session's wait helper, so the
// listener that calls the Harness has entered and the hop that waits for its synchronous part does not wait for a publication that
// waits for the hop. Two hold tasks end in one storage call behind a commit in flight: their ends are two parts of the call, and the
// listener of the first part (one's end) calls the Harness with a wait that the second part's publication ends. Pi's listener awaits
// there, and the hop publishes.

// listenerAtTheEndOfOne opens two hold tasks, observes the commits, and calls act from the listener that sees the publication of
// one's end. It returns once both ends were published behind a held storage call, with the result of act.
// endOfOne is the value the watch is advanced with at the publication of one's end.
const endOfOne = 1

func listenerAtTheEndOfOne(t *testing.T, act func(harness Harness, one, two durable.TaskId) error) error {
	t.Helper()
	fixtures := newGCFixtures()
	store := newControlledStorage()
	harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
	root := mustRoot(t, harness, nil)
	other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
	one := must(gcCreateHold(fixtures, root, "one")())
	two := must(gcCreateHold(fixtures, other, "two")())
	harness.Resume()
	eventually(t, func() bool {
		return gcStatus(t, harness, one) == "running" && gcStatus(t, harness, two) == "running"
	})
	acted := make(chan error, 1)
	// A started watch (chord.Watch) fed by the publications of the Session: its listener is a delivery, the kind whose synchronous
	// part the hop waits for. It acts at the publication of one's end.
	impl := harness.(*harnessImpl)
	watch := session.NewSessionWatch(impl.SessionImpl, 0, func() {})
	var once sync.Once
	watch.Start(func(ctx context.Context, call chord.WatchCall[int]) error {
		if call.Kind == chord.WatchCallValue && call.Value == endOfOne {
			once.Do(func() { acted <- act(harness, one, two) })
		}
		return nil
	})
	impl.FollowCommits(func(callCtx context.Context, commit session.ObservedCommit) {
		for _, change := range commit.Changes {
			if write, ok := change.(durable.TaskWrite); ok && write.Value.Id == one && write.Value.State.Status == durable.TaskTerminal {
				watch.Advance(callCtx, chord.WatchCall[int]{Kind: chord.WatchCallValue, Value: endOfOne})
			}
		}
	})
	// Both end in one call, behind a commit in flight.
	held := store.holdCommits()
	blocker := gcGo(gcAppendNote(root))
	gcEntered(t, held)
	fixtures.gate("one").resolve()
	gcFlush(t)
	fixtures.gate("two").resolve()
	gcFlush(t)
	held.release()
	blocker.wait(t)
	select {
	case err := <-acted:
		return err
	case <-time.After(gcTimeout):
		t.Fatal("the listener of one's end never returned: the hop waits for its synchronous part, and its wait is for the publication behind the hop")
		return nil
	}
}

func TestAListenerThatCallsTheHarnessAtOneOfTwoEndsOfACallDoesNotStallTheHop(t *testing.T) {
	t.Run("WaitForTask of the task whose end is the call's next part", func(t *testing.T) {
		err := listenerAtTheEndOfOne(t, func(harness Harness, _, two durable.TaskId) error {
			ctx, cancel := context.WithTimeout(testContext, gcTimeout)
			defer cancel()
			_, err := harness.WaitForTask(ctx, two)
			return err
		})
		if err != nil {
			t.Fatalf("WaitForTask = %v, want nil: two's end is the next part of the call", err)
		}
	})
	t.Run("Close", func(t *testing.T) {
		err := listenerAtTheEndOfOne(t, func(harness Harness, _, _ durable.TaskId) error {
			ctx, cancel := context.WithTimeout(testContext, gcTimeout)
			defer cancel()
			return harness.Close(ctx)
		})
		if err != nil {
			t.Fatalf("Close = %v, want nil: the close settles the call the listener is a delivery of", err)
		}
	})
}

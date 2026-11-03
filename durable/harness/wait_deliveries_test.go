package harness

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi packages/chord/src/watch.ts #schedule/#drain, harness/submissions.ts wait, harness/scheduler.ts waitForIdle and
// waitForTask: a commit publishes a batch to a started watch whose callback is not running, and queueMicrotask(#drain) runs the
// listener, up to its first await, before the code that awaits a wait's promise resumes. A Go caller resumes on another goroutine, so
// each wait awaits the deliveries scheduled before it (Session.AwaitDeliveriesEntered), until each one's listener call returned or
// went on to a wait of the Session (chord.DeliveryEntered), never until a listener finishes a wait of its own. A listener that blocks
// on a plain lock or channel the Session cannot see has not entered until it returns: the one gap Go has that upstream, which has no
// locks, does not.

// settledChat is a chat whose one run finished, so every wait of it returns at once: nothing but the delivery decides when it does.
type settledChat struct {
	harness Harness
	root    Conversation
	first   durable.Submission
	task    durable.TaskId
}

func openSettledChat(t *testing.T) settledChat {
	t.Helper()
	setup := chatSetup(t)
	setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}})})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	first := submitInput(t, root, "go")
	must(first.Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	task := conversationTasks(t, harness, root)[0].Id
	t.Cleanup(func() { _ = harness.Close(testContext) })
	return settledChat{harness: harness, root: root, first: first, task: task}
}

// publishBatch commits a display-only write, which publishes one batch to every started view watch of the conversation.
func (chat settledChat) publishBatch(t *testing.T) {
	t.Helper()
	must(chat.root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}))
}

func closedChannel(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// waitKinds are the waits that resume their caller after the deliveries.
var waitKinds = []struct {
	name string
	wait func(ctx context.Context, chat settledChat) error
}{
	{"Submission.Wait", func(ctx context.Context, chat settledChat) error { _, err := chat.first.Wait(ctx); return err }},
	{"Harness.WaitForIdle", func(ctx context.Context, chat settledChat) error { return chat.harness.WaitForIdle(ctx) }},
	{"Conversation.WaitForIdle", func(ctx context.Context, chat settledChat) error { return chat.root.WaitForIdle(ctx) }},
	{"Harness.WaitForTask", func(ctx context.Context, chat settledChat) error {
		_, err := chat.harness.WaitForTask(ctx, chat.task)
		return err
	}},
}

// spinningListener is a view watch whose listener is at work, not at a wait, until the test lets it go: it runs and yields, so
// its delivery goroutine has not entered the listener's first await, as the synchronous part of an upstream listener has not
// completed.
type spinningListener struct {
	chat    settledChat
	stream  ConversationWatch
	release atomic.Bool
	given   atomic.Int32
	spins   atomic.Int32
}

func startSpinningListener(t *testing.T) *spinningListener {
	t.Helper()
	spinning := &spinningListener{chat: openSettledChat(t)}
	t.Cleanup(func() { spinning.release.Store(true) })
	spinning.stream = must(spinning.chat.root.Watch(testContext, nil))
	spinning.stream.Start(onValue(func(_ context.Context, _ chord.WatchCall[ConversationView]) error {
		for !spinning.release.Load() {
			spinning.spins.Add(1)
			runtime.Gosched()
		}
		spinning.given.Add(1)
		return nil
	}))
	spinning.chat.publishBatch(t)
	return spinning
}

// waitCall is one wait on its own goroutine.
type waitCall struct {
	returned      chan struct{}
	err           error
	givenAtReturn int32
}

func (spinning *spinningListener) call(ctx context.Context, wait func(ctx context.Context, chat settledChat) error) *waitCall {
	call := &waitCall{returned: make(chan struct{})}
	go func() {
		call.err = wait(ctx, spinning.chat)
		call.givenAtReturn = spinning.given.Load()
		close(call.returned)
	}()
	return call
}

// spunAtLeast waits until the listener spun n times: the wait has been in the Session's wait for the deliveries all that while, as a wait
// that returned at once would have returned before.
func (spinning *spinningListener) spunAtLeast(n int32) {
	for spinning.spins.Load() < n {
		runtime.Gosched()
	}
}

// Pi: queueMicrotask(#drain) runs every listener's synchronous part to its end before the code that awaited a wait's promise
// resumes, so the wait returns after the listeners of the batches committed before it ran. The listener here is at work (running, not at a
// wait) until the test releases it: the wait must still be waiting after the listener spun for a while, and return only once it ended.
func TestAHarnessWaitResumesItsCallerAfterTheListenersOfEarlierBatchesEntered(t *testing.T) {
	for _, test := range waitKinds {
		t.Run(test.name, func(t *testing.T) {
			spinning := startSpinningListener(t)
			call := spinning.call(testContext, test.wait)
			spinning.spunAtLeast(2000)
			if closedChannel(call.returned) {
				t.Fatal("the wait returned while the listener of an earlier batch was still at work")
			}
			spinning.release.Store(true)
			<-call.returned
			if call.err != nil {
				t.Fatal(call.err)
			}
			drained(spinning.chat.harness)
			if total := spinning.given.Load(); total == 0 || call.givenAtReturn != total {
				t.Fatalf("the wait returned after %d of the %d batches committed before it were given to the listener", call.givenAtReturn, total)
			}
			if _, err := spinning.stream.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// ctx's cancellation ends only this wait, while the listener is still at work.
func TestACancelledContextEndsAHarnessWaitForTheListenersAtOnce(t *testing.T) {
	for _, test := range waitKinds {
		t.Run(test.name, func(t *testing.T) {
			spinning := startSpinningListener(t)
			ctx, cancel := context.WithCancelCause(testContext)
			cause := errors.New("stop waiting")
			call := spinning.call(ctx, test.wait)
			spinning.spunAtLeast(2000)
			if closedChannel(call.returned) {
				t.Fatal("the wait returned while the listener was still at work")
			}
			// The listener is still at work: only the cancellation can end the wait.
			cancel(cause)
			select {
			case <-call.returned:
			case <-time.After(10 * time.Second):
				t.Fatal("the cancellation did not end the wait")
			}
			if !errors.Is(call.err, cause) {
				t.Fatalf("the wait returned %v, want the cancellation cause", call.err)
			}
			if given := spinning.given.Load(); given != 0 {
				t.Fatalf("%d batches were given before the listener was released", given)
			}
			spinning.release.Store(true)
			if _, err := spinning.stream.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAHarnessWaitDoesNotWaitForAListenerParkedInAWait(t *testing.T) {
	chat := openSettledChat(t)
	release, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stream := must(chat.root.Watch(testContext, nil))
	stream.Start(onValue(func(_ context.Context, _ chord.WatchCall[ConversationView]) error {
		once.Do(func() { close(entered) })
		// A wait of the Session, as a listener awaiting the Harness makes: the delivery has entered.
		chat.harness.(*harnessImpl).Await(testContext, release)
		return nil
	}))
	chat.publishBatch(t)
	<-entered
	// The listener is parked in a wait of the Session, as an upstream listener awaiting I/O is: its first part ran, and the wait returns.
	if _, err := chat.first.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := chat.harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	close(release)
	if _, err := stream.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestListenersThatWaitForTheHarnessDoNotWaitForEachOther(t *testing.T) {
	chat := openSettledChat(t)
	// Two listeners start together and each waits for the Harness. Each wait awaits the other's delivery, which is itself in
	// such a wait: they must find each other entered, and the one on its own goroutine must skip itself.
	var waiting sync.WaitGroup
	var failed atomic.Int32
	for range 2 {
		stream := must(chat.root.Watch(testContext, nil))
		waiting.Add(1)
		var once sync.Once
		stream.Start(onValue(func(_ context.Context, _ chord.WatchCall[ConversationView]) error {
			once.Do(func() {
				defer waiting.Done()
				if err := chat.harness.WaitForIdle(testContext); err != nil {
					failed.Add(1)
				}
				if _, err := chat.first.Wait(testContext); err != nil {
					failed.Add(1)
				}
			})
			return nil
		}))
		t.Cleanup(func() { _, _ = stream.Stop() })
	}
	chat.publishBatch(t)
	waiting.Wait()
	if failed.Load() != 0 {
		t.Fatalf("%d waits of the listeners failed", failed.Load())
	}
}

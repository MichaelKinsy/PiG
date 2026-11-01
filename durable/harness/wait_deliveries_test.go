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
// waitForTask: a commit publishes a batch to a started watch whose callback is not running, and queueMicrotask(#drain) enters the
// listener, up to its first await, before the code that awaits a wait's promise resumes. A Go caller resumes on another goroutine, so
// each wait awaits the deliveries scheduled before it (Session.AwaitDeliveriesEntered), until they started: Go cannot tell when a
// listener reaches its first wait, and one that waits for a lock the caller holds would never be entered, so the guarantee is the start,
// never the listener's return or its first wait.

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
}

func startSpinningListener(t *testing.T) *spinningListener {
	t.Helper()
	spinning := &spinningListener{chat: openSettledChat(t)}
	t.Cleanup(func() { spinning.release.Store(true) })
	spinning.stream = must(spinning.chat.root.Watch(testContext, nil))
	spinning.stream.Start(onValue(func(_ context.Context, _ chord.WatchCall[ConversationView]) error {
		for !spinning.release.Load() {
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
	returned        chan struct{}
	err             error
	givenAtReturn   int32
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

// settle returns once the wait returned: a wait that returns has nothing more to await, and one that does not return is stuck on the
// listener, which the test then fails.
func (call *waitCall) settle(t *testing.T) {
	t.Helper()
	select {
	case <-call.returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not return while the listener was running")
	}
}

// The guarantee of a Harness wait that resumes its caller is that the deliveries of the batches committed before it have started
// (chord.DeliveryScheduler.AwaitEntered): a listener that is still at work, not at a wait, does not hold the wait.
func TestAHarnessWaitResumesItsCallerOnceTheListenersOfEarlierBatchesStarted(t *testing.T) {
	for _, test := range waitKinds {
		t.Run(test.name, func(t *testing.T) {
			spinning := startSpinningListener(t)
			call := spinning.call(testContext, test.wait)
			call.settle(t)
			if call.err != nil {
				t.Fatal(call.err)
			}
			if given := call.givenAtReturn; given != 0 {
				t.Fatalf("%d batches were given before the listener was released", given)
			}
			spinning.release.Store(true)
			drained(spinning.chat.harness)
			if total := spinning.given.Load(); total == 0 {
				t.Fatal("the listener was never given the batch")
			}
			if _, err := spinning.stream.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestACancelledContextEndsAHarnessWaitForTheListeners(t *testing.T) {
	for _, test := range waitKinds {
		t.Run(test.name, func(t *testing.T) {
			spinning := startSpinningListener(t)
			ctx, cancel := context.WithCancelCause(testContext)
			cause := errors.New("stop waiting")
			cancel(cause)
			call := spinning.call(ctx, test.wait)
			call.settle(t)
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

func TestAHarnessWaitDoesNotWaitForAListenerBlockedOnALockTheCallerHolds(t *testing.T) {
	for _, test := range waitKinds {
		t.Run(test.name, func(t *testing.T) {
			chat := openSettledChat(t)
			var held sync.Mutex
			var given atomic.Int32
			stream := must(chat.root.Watch(testContext, nil))
			stream.Start(onValue(func(_ context.Context, _ chord.WatchCall[ConversationView]) error {
				held.Lock()
				defer held.Unlock()
				given.Add(1)
				return nil
			}))
			held.Lock()
			chat.publishBatch(t)
			// The caller holds the lock the listener needs: a wait for the listener to finish would never return.
			if err := test.wait(testContext, chat); err != nil {
				t.Fatal(err)
			}
			held.Unlock()
			drained(chat.harness)
			if given.Load() == 0 {
				t.Fatal("the listener was not given the batch once the lock was free")
			}
			if _, err := stream.Stop(); err != nil {
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
		<-release
		return nil
	}))
	chat.publishBatch(t)
	<-entered
	// The listener is parked, as an upstream listener awaiting I/O is: its first part ran, and the wait returns.
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

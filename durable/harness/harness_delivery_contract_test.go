package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
)

// pig divergence (D224) contract test. Pi's listener awaits, so a Harness wait never waits for a listener's own blocking; a Go
// listener is a blocking call, so a wait that resumes its caller after the deliveries of earlier batches (Session.AwaitDeliveriesEntered)
// waits until each listener call returned or said it went on to a wait of the host: chord.DeliveryEntered. A listener that blocks on
// something the host cannot see (here an unbuffered channel send) without saying so holds the wait back until it ends; the same
// listener that calls chord.DeliveryEntered first, or blocks on its own goroutine, does not. The tests state both halves.

// blockingUpdates opens a settled chat whose view-watch listener sends on an unbuffered channel, calls enter first when it is
// set, and publishes one batch to it.
func blockingUpdates(t *testing.T, enter bool) (settledChat, chan struct{}) {
	t.Helper()
	chat := openSettledChat(t)
	updates := make(chan struct{})
	stream := must(chat.root.Watch(testContext, nil))
	stream.Start(onValue(func(context.Context, chord.WatchCall[ConversationView]) error {
		if enter {
			chord.DeliveryEntered()
		}
		updates <- struct{}{}
		return nil
	}))
	chat.publishBatch(t)
	return chat, updates
}

func TestAListenerThatBlocksOnAChannelStallsHarnessWaitForTaskUntilItCallsDeliveryEntered(t *testing.T) {
	t.Run("an unmarked listener holds the wait back until its channel send ends", func(t *testing.T) {
		chat, updates := blockingUpdates(t, false)
		ctx, cancel := context.WithTimeout(testContext, 3*time.Second)
		defer cancel()
		_, err := chat.harness.WaitForTask(ctx, chat.task)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitForTask = %v, want the deadline: the listener blocks on a channel the host cannot see, so its delivery has not entered", err)
		}
		<-updates // the listener's send ends; its delivery ends
		if _, err := chat.harness.WaitForTask(testContext, chat.task); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a listener that calls chord.DeliveryEntered first does not hold it back", func(t *testing.T) {
		chat, updates := blockingUpdates(t, true)
		ctx, cancel := context.WithTimeout(testContext, 3*time.Second)
		defer cancel()
		if _, err := chat.harness.WaitForTask(ctx, chat.task); err != nil {
			t.Fatalf("WaitForTask = %v, want nil: the listener said it went on to a wait before it blocked", err)
		}
		<-updates
	})
}

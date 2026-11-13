package link

import (
	"runtime"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// What a link end keeps of its queue. Pi has no test of it: `this.#queued.splice(0)` empties the queue at the hand-over
// and the engine frees what the listener let go. Go hands the queue over one message at a time (a message that arrives
// meanwhile queues behind), so each slot is cleared as its message goes to the listener and the array goes with the last
// one: a link lives as long as its host, and its listener is set once, so nothing else would ever replace the array and
// every message queued ahead of the listener would stay until the close.

// queuedMessages delivers count messages to end, each on a buffer of its own, and returns weak pointers to the buffers:
// the caller holds none of them.
func queuedMessages(end *LinkEnd, count int) []weak.Pointer[byte] {
	refs := make([]weak.Pointer[byte], count)
	for i := range refs {
		message := make([]byte, 1024)
		refs[i] = weak.Make(&message[0])
		end.deliver(message)
	}
	return refs
}

func TestALinkEndKeepsNothingOfTheQueueItHandedOver(t *testing.T) {
	end := &LinkEnd{}
	messages := queuedMessages(end, 8)
	queue := weak.Make(&end.queued[0])
	handed := 0
	end.OnMessage(func([]byte) { handed++ })
	// The link stays open with its listener: a later message goes straight to it, so nothing replaces the queue.
	end.deliver([]byte("later"))
	if handed != len(messages)+1 {
		t.Fatalf("handed over %d messages, want %d", handed, len(messages)+1)
	}
	t.Run("the messages", func(t *testing.T) { nexttest.ExpectCollected(t, messages...) })
	t.Run("the queue's array", func(t *testing.T) { nexttest.ExpectCollected(t, queue) })
	runtime.KeepAlive(end)
}

func TestALinkEndKeepsNoMessageItHandedOverWhileALaterOneIsWithTheListener(t *testing.T) {
	end := &LinkEnd{}
	messages := queuedMessages(end, 3)
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		handed := 0
		end.OnMessage(func([]byte) {
			handed++
			if handed == 2 {
				close(held)
				<-release
			}
		})
	}()
	<-held
	// The first message was the listener's and is let go; the second is with it and the third still queued, so the queue's
	// array is in use: only its cleared slot lets the first one go.
	nexttest.ExpectCollected(t, messages[0])
	if messages[2].Value() == nil {
		t.Error("the message still queued was collected")
	}
	close(release)
	<-done
	nexttest.ExpectCollected(t, messages...)
	runtime.KeepAlive(end)
}

func TestALinkEndKeepsNoQueuedMessageAfterItsEnd(t *testing.T) {
	end := &LinkEnd{}
	messages := queuedMessages(end, 4)
	end.ended()
	nexttest.ExpectCollected(t, messages...)
	runtime.KeepAlive(end)
}

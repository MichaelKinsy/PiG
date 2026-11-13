package host

import (
	"runtime"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// PiG's own (white box): a call that waited in a link's call window and was sent is kept by the window no longer. The
// queue is a Go slice: popping its head by a reslice leaves the popped call in the backing array (with its arguments,
// its result and the binding that made it) until the array is replaced, unless the slot is cleared first. Pi's queue is
// an array it shifts: nothing to port; the Pi case next to it is remote-object.test.ts "a released joiner with queued
// calls and unseen members is kept by no queue, call record or sibling" (the calls taken out of the queue).
func TestCallWindowKeepsNoCallItSent(t *testing.T) {
	shared := sharedCallersOn(t, 1)
	// B's call takes the window's one slot: A's call waits in its queue.
	first := startCall(t, shared.b, "add", jsonObject("text", "b"), background)
	queued := func() weak.Pointer[outgoingCall] {
		second := startCall(t, shared.a, "add", jsonObject("text", "a"), background)
		var waiting weak.Pointer[outgoingCall]
		var count int
		shared.b.binding.loop.Run(func() {
			side := rhRemoteObjectOf(shared.b).held.side
			if count = len(side.window.waiting); count == 1 {
				waiting = weak.Make(side.window.waiting[0])
			}
		})
		if count != 1 {
			t.Fatalf("calls waiting in the window = %d, want 1", count)
		}
		for index, call := range []*parkedCallResult{first, second} {
			shared.peer.Send(sharedResult(t, shared.peer.Next("call").(*link.CallMessage).Call, float64(index+1)))
			value, err := call.wait(background)
			must(t, err)
			if value != float64(index+1) {
				t.Fatalf("call %d = %v, want %d", index, value, index+1)
			}
		}
		return waiting
	}()
	rhSync(t, shared.peer)
	nexttest.ExpectCollected(t, queued)
	runtime.KeepAlive(shared)
}

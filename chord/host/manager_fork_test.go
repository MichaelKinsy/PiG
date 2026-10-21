package host

import (
	"net"
	"testing"
)

// manager.ts registers onControl in the tick that spawns the child, so a child that answers its init at once is always heard. Here
// the channel's reader is its own goroutine: control messages that arrive before the first listener registers are kept and
// replayed to it, in order, ahead of later ones.
func TestForkInstanceReplaysControlMessagesReceivedBeforeTheFirstListener(t *testing.T) {
	parentConn, childConn := net.Pipe()
	child := newNodeChannel(childConn)
	marker := make(chan struct{}, 1)
	instance := newForkInstance(ForkDriverOptions{OnMessage: func(message any) {
		// Messages reach OnMessage in order and each is dispatched to the listeners before the next arrives, so the marker
		// (not a control message) shows that the control messages sent before it have been dispatched.
		if object, ok := message.(interface{ Has(string) bool }); ok && object.Has("marker") {
			marker <- struct{}{}
		}
	}}, 0, newNodeChannel(parentConn), func() {})

	child.Send(map[string]any{"chord2control": ControlMessage{Type: ControlReady}}, nil)
	child.Send(map[string]any{"chord2control": ControlMessage{Type: ControlStatus, ActiveCalls: new(2.0)}}, nil)
	child.Send(map[string]any{"marker": true}, nil)
	<-marker

	var heard []ControlMessage
	later := make(chan struct{})
	instance.OnControl(func(message ControlMessage) {
		heard = append(heard, message)
		if len(heard) == 3 {
			close(later)
		}
	})
	if len(heard) != 2 || heard[0].Type != ControlReady || heard[1].Type != ControlStatus || valueOr(heard[1].ActiveCalls) != 2 {
		t.Fatalf("a listener registered after a ready and a status heard %+v, want both in order", heard)
	}
	var second []ControlMessage
	instance.OnControl(func(message ControlMessage) { second = append(second, message) })
	if len(second) != 0 {
		t.Fatalf("a second listener heard %+v of the messages before it, want none", second)
	}
	child.Send(map[string]any{"chord2control": ControlMessage{Type: ControlFatal, Message: "later"}}, nil)
	<-later
	if heard[2].Type != ControlFatal || heard[2].Message != "later" {
		t.Fatalf("a later message reached the first listener as %+v", heard[2])
	}
}

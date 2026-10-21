package host

import (
	"encoding/json"
	"net"
	"testing"
)

// flushed returns once the reader of the channel that peer writes to has emitted every message sent before it. net.Pipe's Write returns when the
// reader has taken the bytes, the reader handles one line at a time, and an internal Node message is read but never emitted, so by the time this
// one has been taken, every earlier message has been emitted (or queued).
func flushed(t *testing.T, peer *nodeChannel) {
	t.Helper()
	if !peer.Send(map[string]any{"cmd": "NODE_FLUSH"}, nil) {
		t.Fatal("send flush failed")
	}
}

func pipeChannels(t *testing.T) (parent, child *nodeChannel) {
	t.Helper()
	parentConn, childConn := net.Pipe()
	t.Cleanup(func() { _ = parentConn.Close(); _ = childConn.Close() })
	return newNodeChannel(parentConn), newNodeChannel(childConn)
}

func messageText(t *testing.T, message any) string {
	t.Helper()
	text, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// In Node a channel's owner registers its "message" listener in the tick that creates the channel, so no message is emitted without one. Here the
// reader is its own goroutine: a message that arrives first (the parent's init, written before the child opened its pipe) must wait for the first
// listener, which hears it, in order, ahead of later messages.
func TestNodeChannelGivesTheFirstMessageListenerTheMessagesThatCameBeforeIt(t *testing.T) {
	parent, child := pipeChannels(t)
	parent.Send(map[string]any{"n": 1}, nil)
	parent.Send(map[string]any{"n": 2}, nil)
	flushed(t, parent)

	received := make(chan string, 8)
	first := IpcListener(func(message any) { received <- messageText(t, message) })
	child.On("message", &first)
	if len(received) != 2 || <-received != `{"n":1}` || <-received != `{"n":2}` {
		t.Fatalf("the first listener did not hear the two messages sent before it in order")
	}

	other := make(chan string, 8)
	second := IpcListener(func(message any) { other <- messageText(t, message) })
	child.On("message", &second)
	parent.Send(map[string]any{"n": 3}, nil)
	if got := <-received; got != `{"n":3}` {
		t.Fatalf("first listener heard %s after the replay, want n 3", got)
	}
	if got := <-other; got != `{"n":3}` {
		t.Fatalf("a later listener heard %s, want only the messages after it (n 3)", got)
	}
}

// A message that arrives while the first listener is still being caught up waits behind the backlog.
func TestNodeChannelQueuesAMessageThatArrivesDuringTheReplayBehindTheBacklog(t *testing.T) {
	parent, child := pipeChannels(t)
	parent.Send(map[string]any{"n": 1}, nil)
	flushed(t, parent)

	inReplay, release := make(chan struct{}), make(chan struct{})
	received := make(chan string, 8)
	listener := IpcListener(func(message any) {
		text := messageText(t, message)
		if text == `{"n":1}` {
			close(inReplay)
			<-release
		}
		received <- text
	})
	registered := make(chan struct{})
	go func() {
		child.On("message", &listener)
		close(registered)
	}()
	select {
	case <-inReplay:
	case <-registered:
		t.Fatal("the first listener was registered without hearing the message that came before it")
	}
	parent.Send(map[string]any{"n": 2}, nil)
	flushed(t, parent)
	close(release)
	<-registered
	if first, second := <-received, <-received; first != `{"n":1}` || second != `{"n":2}` {
		t.Fatalf("the listener heard %s then %s, want n 1 then n 2", first, second)
	}
}

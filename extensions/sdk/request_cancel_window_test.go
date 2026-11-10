package sdk

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
)

// windowObservation is what a handler saw of its own cancellation: Done at its first line, and, after the host call it made, the call's outcome, Done and the reason.
type windowObservation struct {
	firstDone bool
	laterDone bool
	reason    string
	callErr   error
	callText  string
}

// windowHarness serves a one-tool extension through Extension.loop on an in-process socket, with the loop held on a notify so that frames queue behind it. The tool's handler
// reports a windowObservation, and the host side answers every host call the handler makes.
type windowHarness struct {
	ext      *Extension
	host     *conn
	observed chan windowObservation
	first    chan bool
	pongs    chan struct{}
	freeLoop func()

	freeSecond func()
}

func newWindowHarness(t *testing.T) *windowHarness {
	t.Helper()
	client, server := net.Pipe()
	// The loop ends when the connection closes; the cleanups run last to first, so the locks it is held on are free, the connection is closed and the loop is waited for, in that order.
	var loop sync.WaitGroup
	t.Cleanup(loop.Wait)
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	connection, host := newConn(client), newConn(server)
	h := &windowHarness{ext: New("cancel-window"), host: host, observed: make(chan windowObservation, 1), first: make(chan bool, 1), pongs: make(chan struct{}, 1)}
	h.ext.conn = connection
	closed := func(c <-chan struct{}) bool {
		select {
		case <-c:
			return true
		default:
			return false
		}
	}
	h.ext.Tool("probe", "Probe", Schema{"type": "object"}, func(ctx Context, _ map[string]any) (any, error) {
		var got windowObservation
		got.firstDone = closed(ctx.Done())
		h.first <- got.firstDone
		result, err := ctx.callHost("ui.getEditorText", nil)
		got.callErr = err
		if result != nil {
			got.callText = string(result.Result)
		}
		got.laterDone = closed(ctx.Done())
		got.reason = ctx.CancellationReason()
		h.observed <- got
		return map[string]any{"content": "ok"}, nil
	})
	connection.start()
	host.start()
	go func() {
		for env := range host.incoming {
			switch env.Type {
			case msgCall:
				_ = host.send(envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Result: json.RawMessage(`"answered"`)}})
			case msgPong:
				h.pongs <- struct{}{}
			}
		}
	}()
	// The loop blocks on the first notify below until freeLoop, which lets it arm the request and start the handler, and then on the second one until freeSecond; both locks are free again before the connection closes.
	h.ext.bashOperationsMu.Lock()
	h.ext.providerMu.Lock()
	h.freeLoop = sync.OnceFunc(h.ext.bashOperationsMu.Unlock)
	h.freeSecond = sync.OnceFunc(h.ext.providerMu.Unlock)
	loop.Go(func() { _ = h.ext.loop() })
	t.Cleanup(func() { h.freeLoop(); h.freeSecond() })
	return h
}

// queue sends the frames of one tool_call in the order the host writes them when a cancel follows the request closely: a notify the loop is held on, the request, a second
// notify the loop blocks on once it has started the handler, and the cancels. It returns once the reader has read the cancels.
func (h *windowHarness) queue(t *testing.T, cancels ...envelope) {
	t.Helper()
	send := func(env envelope) {
		t.Helper()
		if err := h.host.send(env); err != nil {
			t.Fatal(err)
		}
	}
	send(envelope{Type: msgNotify, Notify: &notifyMsg{Method: notifyBashOperationsRelease, Args: json.RawMessage(`{"handle":"held"}`)}})
	send(envelope{Type: msgRequest, ID: "probe-1", Request: &requestMsg{Method: "tool_call", Tool: "probe", ToolCallID: "call", Args: json.RawMessage(`{}`)}})
	send(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "provider_release", Args: json.RawMessage(`{"key":"held"}`)}})
	for _, cancel := range cancels {
		send(cancel)
	}
	// The reader answers a ping after it has read every frame before it.
	send(envelope{Type: msgPing, Ping: &pingMsg{Nonce: "after-cancel"}})
	<-h.pongs
}

// A signal-only cancel the reader read before the loop armed its request is applied when the request is armed: the handler starts with its signal aborted, and the answer of
// a host call it makes reaches it with the signal still aborted, though the loop has not yet taken the cancel frame (it is held on a notify queued between the two).
func TestSignalOnlyCancelReadBeforeTheRequestIsArmedAbortsItsSignalAtStart(t *testing.T) {
	h := newWindowHarness(t)
	h.queue(t, envelope{Type: msgCancel, ID: "probe-1", Cancel: &cancelMsg{RequestID: "probe-1", Reason: "tool aborted", SignalOnly: true}})
	h.freeLoop()
	if !<-h.first {
		t.Errorf("Done at the handler's first line was open, want the signal the cancel frame aborted")
	}
	h.freeSecond()
	got := <-h.observed
	if got.callErr != nil || got.callText != `"answered"` {
		t.Errorf("host call = %q, %v, want the host's answer", got.callText, got.callErr)
	}
	if !got.laterDone || got.reason != "tool aborted" {
		t.Errorf("after the answer: Done closed = %v, CancellationReason = %q, want the signal aborted with the cancel's reason", got.laterDone, got.reason)
	}
}

// An ordinary cancel read before the request is armed ends it as well: the handler starts with its context cancelled and its host call refused.
func TestOrdinaryCancelReadBeforeTheRequestIsArmedEndsItAtStart(t *testing.T) {
	h := newWindowHarness(t)
	h.queue(t, envelope{Type: msgCancel, ID: "probe-1", Cancel: &cancelMsg{RequestID: "probe-1", Reason: "user aborted"}})
	h.freeLoop()
	if !<-h.first {
		t.Errorf("Done at the handler's first line was open, want the request ended by the cancel frame the reader read")
	}
	h.freeSecond()
	got := <-h.observed
	if !got.firstDone || !got.laterDone || got.reason != "user aborted" {
		t.Errorf("Done at the first line = %v, after the call = %v, CancellationReason = %q, want the request ended with the cancel's reason", got.firstDone, got.laterDone, got.reason)
	}
	if got.callErr == nil || got.callText != "" {
		t.Errorf("host call = %q, %v, want it refused", got.callText, got.callErr)
	}
}

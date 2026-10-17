package mcp_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// packages/mcp/src/transports/transport.ts:20-55 TransportEvents keeps its listeners in Sets and emits with `for (const listener of set)`: a Set iteration
// is live, so a listener removed by an earlier listener during the same emit is not called, and one added during the emit is. Close listeners run in
// registration order too.
func TestTransportEventsEmitOverTheLiveListenerSet(t *testing.T) {
	var events mcp.TransportEvents
	var order []string
	var disposeSecond func()
	var messageListener mcp.MessageListener = func(mcp.JSONRPCMessage) {
		order = append(order, "first")
		disposeSecond()
		events.OnMessage(func(mcp.JSONRPCMessage) { order = append(order, "added") })
	}
	events.OnMessage(messageListener)
	disposeSecond = events.OnMessage(func(mcp.JSONRPCMessage) { order = append(order, "second") })
	events.OnMessage(func(mcp.JSONRPCMessage) { order = append(order, "third") })
	events.EmitMessage(mcp.NewNotification("x", nil))
	if want := []string{"first", "third", "added"}; !slices.Equal(order, want) {
		t.Fatalf("message listeners ran %v, want %v: the one disposed during the emit is skipped and the one added during it runs", order, want)
	}

	order = nil
	var errorListener mcp.ErrorListener = func(err error) {
		order = append(order, "error1:"+err.Error())
		events.OnError(func(error) { order = append(order, "error-added") })
	}
	events.OnError(errorListener)
	events.OnError(func(error) { order = append(order, "error2") })
	events.EmitError(errors.New("boom"))
	if want := []string{"error1:boom", "error2", "error-added"}; !slices.Equal(order, want) {
		t.Fatalf("error listeners ran %v, want %v", order, want)
	}

	order = nil
	var closeListener mcp.CloseListener = func() { order = append(order, "close1") }
	events.OnClose(closeListener)
	events.OnClose(func() { order = append(order, "close2") })
	events.OnClose(func() { order = append(order, "close3") })
	events.EmitClose()
	if want := []string{"close1", "close2", "close3"}; !slices.Equal(order, want) {
		t.Fatalf("close listeners ran %v, want %v", order, want)
	}
}

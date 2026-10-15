package codemode

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// RemoteExchange is the function the client passes to Remote.Execute; the remote calls it to report what its VM did
// and to ask for the result of a tool call. Over an RPC boundary it is a proxy that calls back into the client.
//
// Ports packages/codemode/src/runtime/remote.ts (CodemodeRemoteExchange).
//
// Go mapping of a Promise that the caller does not await before going on: upstream runs the delivery of the messages
// synchronously inside the Promise executor and settles the reply later, and a remote that has several calls in
// flight depends on it, because "calls on one proxy are delivered in order". A blocking Go function cannot deliver in
// call order and also return the reply later, so the messages are delivered in order before exchange returns and the
// reply reaches the remote through reply, which exchange calls exactly once: from a client goroutine, or before
// exchange returns when there is nothing to wait for. A reply that carries an error is a rejected Promise (the RPC
// failed). A nil reply is upstream's `null`: the exchange carried no call. reply must not block.
type RemoteExchange func(ctx context.Context, messages []RemoteMessage, reply func(*RemoteReply, error))

// Remote runs one execution's VM. It is usually a proxy for a Durable Object that serves ServeRemote; any value with
// this method works, for example an in-process object in tests.
//
// Ports packages/codemode/src/runtime/remote.ts (CodemodeRemote).
//
// Execute blocks until the VM has stopped (the script settled, or the client asked to stop). Upstream's Promise
// settles at the same point. The client cancels ctx once it no longer wants the execution, and an Execute that
// honors it returns promptly instead of computing until its budget is used up.
type Remote interface {
	Execute(ctx context.Context, request RemoteRequest, exchange RemoteExchange) error
}

// Output buffered on the remote before it is flushed without waiting for a call.
//
// Ports packages/codemode/src/runtime/remote.ts (FLUSH_ITEMS, FLUSH_CHARS).
const (
	flushItems = 1_000
	flushChars = 1 << 20
)

// outputChars is the `.length` of the text or data of an output item (UTF-16 units).
func outputChars(item *OutputItem) int {
	if item == nil {
		return 0
	}
	if item.Type == OutputItemText {
		return jsstring.Length(item.Text)
	}
	return jsstring.Length(item.Data)
}

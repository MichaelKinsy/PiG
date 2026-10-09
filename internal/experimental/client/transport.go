// Package client implements the experimental Pi client over ordered byte transports.
package client

// Ports packages/client/src/transport.ts.
// Ports packages/client/src/errors.ts.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// ByteTransport is Pi's ByteTransport (packages/client/src/transport.ts): Send writes one chunk and returns when the write settles, and
// calls are delivered in invocation order. Close is idempotent and does not report a local close as a remote callback. A cancelled ctx
// stops the wait for a write that is already admitted, and refuses one that is not.
type ByteTransport interface {
	Send(ctx context.Context, chunk []byte) error
	Close()
}

// callbackByteTransport is the completion-callback form of ByteTransport that Connection runs. Submit admits a chunk synchronously, in
// invocation order, and completes it exactly once; it returns promptly, and its callback is the upstream Promise settlement. A ByteTransport
// reaches Connection through this form (adaptFactory).
type callbackByteTransport interface {
	Submit(chunk []byte, complete func(error))
	Close()
}

// ByteTransportHandlers receive ordered data and exactly one remote terminal notification.
type ByteTransportHandlers struct {
	OnData  func([]byte)
	OnClose func()
	OnError func(error)
}

// ByteTransportFactory is Pi's ByteTransportFactory: it returns one fresh connected transport, or the error that rejects the attempt. The
// context owns the attempt, not the established transport. handlers deliver nothing until the Connection has taken the transport, as Node's
// socket events follow the continuation that awaits the factory.
type ByteTransportFactory func(context.Context, ByteTransportHandlers) (ByteTransport, error)

// callbackByteTransportFactory starts one connection attempt and returns promptly. Completion supplies one fresh connected transport or an
// error. The context owns the attempt, not the established transport. This callback boundary preserves the synchronous prefix of upstream's
// possibly Promise-returning factory.
type callbackByteTransportFactory func(context.Context, ByteTransportHandlers, func(callbackByteTransport, error))

// ServerError retains the server's opaque error code and message.
type ServerError struct {
	Code, Message string
}

func (err *ServerError) Error() string { return err.Message }

// NewServerError is `new ServerError(error)`.
func NewServerError(failure protocol.ProtocolError) *ServerError {
	return &ServerError{Code: failure.Code, Message: failure.Message}
}

// Name is the `name` property, "ServerError".
func (*ServerError) Name() string { return "ServerError" }

// DisconnectedError retains an underlying transport failure when there is one.
type DisconnectedError struct {
	Message string
	Cause   error
}

// NewDisconnectedError is `new DisconnectedError(message, cause)`; the default message of an omitted one is "Client is disconnected" (see disconnectedError).
func NewDisconnectedError(message string, cause error) *DisconnectedError {
	return &DisconnectedError{Message: message, Cause: cause}
}

// Name is the `name` property, "DisconnectedError".
func (*DisconnectedError) Name() string { return "DisconnectedError" }

func (err *DisconnectedError) Error() string { return err.Message }
func (err *DisconnectedError) Unwrap() error { return err.Cause }

// ClientDisposedError rejects operations after client disposal.
type ClientDisposedError struct{}

// NewClientDisposedError is `new ClientDisposedError()`.
func NewClientDisposedError() *ClientDisposedError {
	return &ClientDisposedError{}
}

func (*ClientDisposedError) Error() string { return "Client is disposed" }

// Name is the `name` property, "ClientDisposedError".
func (*ClientDisposedError) Name() string { return "ClientDisposedError" }

// Cause is the `cause` property. The upstream constructor sets none, so it is always nil.
func (*ClientDisposedError) Cause() error { return nil }

func disconnectedError() error { return NewDisconnectedError("Client is disconnected", nil) }
func toDisconnectedError(err error) error {
	if _, ok := err.(*DisconnectedError); ok { //nolint:errorlint // Upstream instanceof tests the outer error, not its cause chain.
		return err
	}
	return NewDisconnectedError(err.Error(), err)
}
func panicError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

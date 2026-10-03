// Package client implements the experimental Pi client over ordered byte transports.
package client

// Ports packages/client/src/transport.ts.
// Ports packages/client/src/errors.ts.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// ByteTransport admits sends synchronously, in invocation order, and completes each send exactly once. Send returns promptly; its completion callback represents the upstream Promise settlement. Close is idempotent and does not report a local close as a remote callback.
type ByteTransport interface {
	Send(chunk []byte, complete func(error))
	Close()
}

// ByteTransportHandlers receive ordered data and exactly one remote terminal notification.
type ByteTransportHandlers struct {
	OnData  func([]byte)
	OnClose func()
	OnError func(error)
}

// ByteTransportFactory starts one connection attempt and returns promptly. Completion supplies one fresh connected transport or an error. The context owns the attempt, not the established transport. This callback boundary preserves the synchronous prefix of upstream's possibly Promise-returning factory.
type ByteTransportFactory func(context.Context, ByteTransportHandlers, func(ByteTransport, error))

// ServerError retains the server's opaque error code and message.
type ServerError struct{ Code, Message string }

func (err *ServerError) Error() string { return err.Message }
func NewServerError(failure protocol.ProtocolError) *ServerError {
	return &ServerError{Code: failure.Code, Message: failure.Message}
}

// DisconnectedError retains an underlying transport failure when there is one. A composite literal is its constructor; disconnectedError supplies the upstream default message (stubgen:omit NewDisconnectedError).
type DisconnectedError struct {
	Message string
	Cause   error
}

func (err *DisconnectedError) Error() string { return err.Message }
func (err *DisconnectedError) Unwrap() error { return err.Cause }

// ClientDisposedError rejects operations after client disposal.
type ClientDisposedError struct{}

func (*ClientDisposedError) Error() string { return "Client is disposed" }

func disconnectedError() error { return &DisconnectedError{Message: "Client is disconnected"} }
func toDisconnectedError(err error) error {
	if _, ok := err.(*DisconnectedError); ok { //nolint:errorlint // Upstream instanceof tests the outer error, not its cause chain.
		return err
	}
	return &DisconnectedError{Message: err.Error(), Cause: err}
}
func panicError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

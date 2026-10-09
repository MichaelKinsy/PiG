package client

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// client/src/errors.ts:13-18: DisconnectedError(message, cause) keeps both; an absent cause is no cause; the default message is "Client is disconnected".
func TestNewDisconnectedErrorKeepsMessageAndCause(t *testing.T) {
	cause := errors.New("socket closed")
	for _, c := range []struct {
		name    string
		err     *DisconnectedError
		message string
		cause   error
	}{
		{"message only", NewDisconnectedError("Client is already closed", nil), "Client is already closed", nil},
		{"with cause", NewDisconnectedError("Byte transport closed", cause), "Byte transport closed", cause},
		{"default message", asDisconnectedError(t, disconnectedError()), "Client is disconnected", nil},
		{"wrapped transport failure", asDisconnectedError(t, toDisconnectedError(cause)), "socket closed", cause},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.err.Error() != c.message || c.err.Cause != c.cause || !errors.Is(c.err, c.cause) && c.cause != nil { //nolint:errorlint // identity: errors.ts keeps the cause object itself; a wrapped cause would pass errors.Is
				t.Fatalf("%+v, want message %q cause %v", c.err, c.message, c.cause)
			}
		})
	}
}

// asDisconnectedError requires err itself to be a *DisconnectedError, as Pi's `instanceof DisconnectedError` tests the thrown error and not its cause chain.
func asDisconnectedError(t *testing.T, err error) *DisconnectedError {
	t.Helper()
	disconnected, ok := err.(*DisconnectedError) //nolint:errorlint // identity: the returned error is the DisconnectedError, not a wrapper of one
	if !ok {
		t.Fatalf("%v (%T) is not a DisconnectedError", err, err)
	}
	return disconnected
}

// client/src/errors.ts:3-11: ServerError(error) keeps the protocol error's code and message and names itself "ServerError".
func TestNewServerErrorKeepsTheProtocolErrorCodeAndMessage(t *testing.T) {
	err := NewServerError(protocol.ProtocolError{Code: "session_not_found", Message: "No such session"})
	if err.Code != "session_not_found" || err.Error() != "No such session" || err.Name() != "ServerError" {
		t.Fatalf("NewServerError = %+v", err)
	}
}

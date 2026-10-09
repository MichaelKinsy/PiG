package routing

// pi: packages/server/src/errors.ts

import (
	"errors"
	"fmt"
	"testing"
)

// Pi errors.ts: WrongServerError, SessionNotFoundError, SessionAmbiguousError, SessionNotAttachedError and ServerDrainingError extend ServerError with a fixed code and message, so the ServerError stays reachable through the cause chain, also when the failure is wrapped.
func TestServerErrorSubclassesUnwrapToTheirServerError(t *testing.T) {
	for _, tc := range []struct {
		err     interface{ Unwrap() error }
		code    ServerOperationErrorCode
		message string
	}{
		{NewWrongServerError(), "wrong_server", "Request was addressed to another server"},
		{NewSessionNotFoundError(), "session_not_found", "Session was not found"},
		{NewSessionNotFoundError("gone"), "session_not_found", "gone"},
		{NewSessionNotAttachedError(), "session_not_attached", "Session is not attached to this client"},
		{NewServerDrainingError(), "server_draining", "Server is draining"},
	} {
		failure := tc.err.(error)
		unwrapped := tc.err.Unwrap()
		var server *ServerError
		wrapped := fmt.Errorf("while routing: %w", failure)
		if !errors.As(wrapped, &server) || !errors.Is(unwrapped, server) || server.Code != tc.code || failure.Error() != tc.message {
			t.Fatalf("%T: As = %+v, Unwrap = %v, message %q; want code %q message %q", failure, server, unwrapped, failure.Error(), tc.code, tc.message)
		}
	}
}

// Pi errors.ts: `new ServerError(code, message)` carries the code and the message verbatim, and a subclass is a ServerError with its own fixed pair.
func TestNewServerErrorCarriesItsCodeAndMessage(t *testing.T) {
	err := NewServerError("service_invalid_value", "bad value")
	if err.Code != "service_invalid_value" || err.Message != "bad value" || err.Error() != "bad value" {
		t.Fatalf("NewServerError = %+v", err)
	}
	var server *ServerError
	if !errors.As(fmt.Errorf("wrapped: %w", err), &server) || server != err {
		t.Fatalf("errors.As lost the ServerError: %v", server)
	}
	if got := NewSessionNotFoundError("gone").ServerError; *got != *NewServerError("session_not_found", "gone") {
		t.Fatalf("subclass pair = %+v", got)
	}
}

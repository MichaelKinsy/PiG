package routing

import (
	"errors"
	"testing"
)

// Pi server/src/errors.ts SessionNotFoundError: name, default message and code session_not_found.
func TestSessionNotFoundErrorUpstream(t *testing.T) {
	e := NewSessionNotFoundError()
	var server *ServerError
	if e.Name() != "SessionNotFoundError" || e.Error() != "Session was not found" || !errors.As(error(e), &server) || server.Code != "session_not_found" {
		t.Fatalf("got %+v", e)
	}
	if NewSessionNotFoundError("gone").Error() != "gone" {
		t.Fatal("custom message not kept")
	}
}

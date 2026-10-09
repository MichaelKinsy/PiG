package routing

import (
	"errors"
	"testing"
)

// Pi errors.ts: SessionAmbiguousError extends ServerError, so the server error stays reachable through the cause chain.
func TestSessionAmbiguousErrorUnwrapsToItsServerError(t *testing.T) {
	err := NewSessionAmbiguousError()
	var server *ServerError
	if !errors.As(err, &server) || server != err.ServerError || server.Code != "session_ambiguous" || err.Error() != "Session ID matches more than one session" {
		t.Fatalf("err=%v server=%+v", err, server)
	}
	if !errors.Is(err.Unwrap(), error(err.ServerError)) {
		t.Fatalf("Unwrap = %v", err.Unwrap())
	}
}

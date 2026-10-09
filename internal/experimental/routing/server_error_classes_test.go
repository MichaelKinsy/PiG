package routing_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// packages/server/src/errors.ts:14-58: ServerError and its five subclasses are Error classes whose constructors set `code`, `message` and their own `name`.
func TestServerErrorClassesCarryNameAndCode(t *testing.T) {
	cases := []struct {
		err                 interface{ Error() string }
		name, code, message string
	}{
		{routing.NewServerError("service_not_found", "gone"), "ServerError", "service_not_found", "gone"},
		{routing.NewWrongServerError(), "WrongServerError", "wrong_server", "Request was addressed to another server"},
		{routing.NewSessionNotFoundError(), "SessionNotFoundError", "session_not_found", "Session was not found"},
		{routing.NewSessionNotFoundError("custom"), "SessionNotFoundError", "session_not_found", "custom"},
		{routing.NewSessionAmbiguousError(), "SessionAmbiguousError", "session_ambiguous", "Session ID matches more than one session"},
		{routing.NewSessionNotAttachedError(), "SessionNotAttachedError", "session_not_attached", "Session is not attached to this client"},
		{routing.NewServerDrainingError(), "ServerDrainingError", "server_draining", "Server is draining"},
	}
	for _, c := range cases {
		named := c.err.(interface{ Name() string })
		base := serverErrorOf(c.err)
		if named.Name() != c.name || string(base.Code) != c.code || c.err.Error() != c.message {
			t.Errorf("%T: name=%q code=%q message=%q, want %q %q %q", c.err, named.Name(), base.Code, c.err.Error(), c.name, c.code, c.message)
		}
	}
}

func serverErrorOf(err error) *routing.ServerError {
	if base, ok := errors.AsType[*routing.ServerError](err); ok {
		return base
	}
	return nil
}

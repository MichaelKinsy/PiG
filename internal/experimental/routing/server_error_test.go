package routing

import (
	"errors"
	"testing"
)

// Pi packages/server/src/errors.ts: ServerError carries a code and message and names itself; each subclass sets its own name and a fixed code.
// Pi source: packages/server/src/errors.ts:14-56 (ServerError and subclasses).
// mutation-checked: the mutant "ServerError name changed" fails it.
func TestServerErrorsAreNamedAndCodedLikeUpstream(t *testing.T) {
	base := NewServerError("custom", "message")
	if base.Code != "custom" || base.Error() != "message" || base.Name() != "ServerError" {
		t.Fatalf("base %+v name %q", base, base.Name())
	}
	for _, tc := range []struct {
		err  error
		name string
		code ServerOperationErrorCode
	}{
		{NewWrongServerError(), "WrongServerError", "wrong_server"},
		{NewSessionNotFoundError("s1"), "SessionNotFoundError", "session_not_found"},
		{NewSessionAmbiguousError(), "SessionAmbiguousError", "session_ambiguous"},
		{NewSessionNotAttachedError(), "SessionNotAttachedError", "session_not_attached"},
		{NewServerDrainingError(), "ServerDrainingError", "server_draining"},
	} {
		named, ok := tc.err.(interface{ Name() string })
		var server *ServerError
		if !ok || named.Name() != tc.name || !errors.As(tc.err, &server) || server.Code != tc.code {
			t.Errorf("%T: name ok=%v server=%+v", tc.err, ok, server)
		}
	}
}

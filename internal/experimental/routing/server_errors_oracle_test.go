package routing_test

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// packages/server/src/errors.ts:14-58 (ServerError, WrongServerError, SessionNotFoundError, SessionAmbiguousError, SessionNotAttachedError, ServerDrainingError): the pinned upstream classes are constructed under Node and each answer
// (`name`, `code`, `message`, `instanceof ServerError`) is compared with the Go error. Go answers `name` through Name() and `instanceof ServerError` through errors.As.
// mutation-checked: changing the code, message or name of WrongServerError, SessionAmbiguousError or SessionNotAttachedError, or dropping a subclass's Unwrap, fails it.
func TestServerErrorClassesMatchPi(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "testdata/server_errors.mjs", root).Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	var pi map[string]struct {
		Name          string `json:"name"`
		Code          string `json:"code"`
		Message       string `json:"message"`
		IsServerError bool   `json:"isServerError"`
		IsError       bool   `json:"isError"`
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	cases := map[string]error{
		"ServerError":             routing.NewServerError("service_not_found", "gone"),
		"WrongServerError":        routing.NewWrongServerError(),
		"SessionNotFoundError":    routing.NewSessionNotFoundError(),
		"SessionNotFoundErrorMsg": routing.NewSessionNotFoundError("custom"),
		"SessionAmbiguousError":   routing.NewSessionAmbiguousError(),
		"SessionNotAttachedError": routing.NewSessionNotAttachedError(),
		"ServerDrainingError":     routing.NewServerDrainingError(),
	}
	if len(pi) != len(cases) {
		t.Fatalf("Pi oracle answered %d classes, Go checks %d", len(pi), len(cases))
	}
	for key, err := range cases {
		want := pi[key]
		named, ok := err.(interface{ Name() string })
		server, isServer := errors.AsType[*routing.ServerError](err)
		if !ok || !isServer || named.Name() != want.Name || string(server.Code) != want.Code || err.Error() != want.Message || !want.IsServerError || !want.IsError {
			t.Errorf("%s: Go %T has Name %v, ServerError %v (%+v), message %q; Pi %+v", key, err, ok, isServer, server, err.Error(), want)
		}
	}
}

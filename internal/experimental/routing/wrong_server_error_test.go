package routing_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// packages/server/src/errors.ts WrongServerError extends ServerError: code "wrong_server" with Pi's message. Unwrap exposes the ServerError
// it extends, so errors.As reaches the base class through a wrapped error as `instanceof ServerError` does.
// Pi source: packages/server/src/errors.ts:24-30 (WrongServerError extends ServerError).
// mutation-checked: the mutant "WrongServerError message changed" fails it.
func TestWrongServerErrorUnwrapsToItsServerError(t *testing.T) {
	err := routing.NewWrongServerError()
	if cause := err.Unwrap(); !errors.Is(cause, error(err.ServerError)) {
		t.Fatalf("Unwrap = %#v, want the embedded ServerError", cause)
	}
	if !errors.Is(errors.Unwrap(err), error(err.ServerError)) {
		t.Fatalf("errors.Unwrap = %#v, want the embedded ServerError", errors.Unwrap(err))
	}
	var target *routing.ServerError
	if !errors.As(fmt.Errorf("handle: %w", err), &target) {
		t.Fatal("errors.As did not find the ServerError through the wrapped WrongServerError")
	}
	if target.Code != "wrong_server" || target.Message != "Request was addressed to another server" {
		t.Fatalf("ServerError = %+v", target)
	}
	if !errors.Is(err, err.ServerError) {
		t.Fatal("errors.Is does not match the base ServerError")
	}
}

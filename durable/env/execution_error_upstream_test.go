package env

import (
	"errors"
	"testing"
)

// packages/durable/src/env/index.ts:65-72 ExecutionError: name "ExecutionError", message, code and cause are kept.
func TestExecutionErrorUpstream(t *testing.T) {
	cause := errors.New("spawn failed")
	e := NewExecutionError(ExecutionErrorSpawnError, "cannot start", cause)
	if e.Name() != "ExecutionError" || e.Error() != "cannot start" || e.Code != ExecutionErrorSpawnError || e.Cause != cause || !errors.Is(e, cause) {
		t.Fatalf("got %+v", e)
	}
	if NewExecutionError(ExecutionErrorTimeout, "slow", nil).Unwrap() != nil {
		t.Fatal("nil cause must stay nil")
	}
}

package env

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

func newTestEnv(t *testing.T) (*NodeExecutionEnv, string) {
	t.Helper()
	root := t.TempDir()
	return NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root}), root
}

// must returns value or panics, which fails the running test with a stack.
func must[T any](value T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return value
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func fileErrorCode(t *testing.T, err error) harness.FileErrorCode {
	t.Helper()
	var fileErr *harness.FileError
	if !errors.As(err, &fileErr) {
		t.Fatalf("error %v (%T) is not a FileError", err, err)
	}
	return fileErr.Code
}

func executionError(t *testing.T, err error) *harness.ExecutionError {
	t.Helper()
	var executionErr *harness.ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("error %v (%T) is not an ExecutionError", err, err)
	}
	return executionErr
}

func abortedContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

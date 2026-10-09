package env

import (
	"errors"
	"testing"
)

// env/index.ts:45-55: `new FileError(code, message, path?, cause?)` keeps the code, message and path, and a cause is reachable as Error.cause (errors.Unwrap in Go).
// packages/durable/src/env/index.ts:45-55 (FileError keeps its cause).
func TestNewFileErrorKeepsCodeMessagePathAndCause(t *testing.T) {
	cause := errors.New("EACCES")
	fileErr := NewFileError(FileErrorPermissionDenied, "denied", "/a/b", cause)
	if fileErr.Code != FileErrorPermissionDenied || fileErr.Message != "denied" || fileErr.Path != "/a/b" {
		t.Fatalf("fields = %+v", fileErr)
	}
	if fileErr.Error() != "denied" {
		t.Fatalf("Error() = %q, want the message", fileErr.Error())
	}
	if !errors.Is(fileErr, cause) || fileErr.Cause != cause { //nolint:errorlint // identity: index.ts:50 stores the cause object itself as Error.cause
		t.Fatalf("cause is not reachable through Unwrap or Cause = %v, want %v", fileErr.Cause, cause)
	}
	bare := NewFileError(FileErrorNotFound, "gone", "", nil)
	if bare.Path != "" || errors.Unwrap(bare) != nil {
		t.Fatalf("an absent path and cause stay absent: %+v", bare)
	}
}

// packages/durable/src/env/index.ts:65-72: `new ExecutionError(code, message, cause?)`; a cause is reachable as Error.cause and the spill path starts unset.
// packages/durable/src/env/index.ts:65-90 (ExecutionError keeps its cause).
func TestNewExecutionErrorKeepsCodeMessageAndCause(t *testing.T) {
	cause := errors.New("spawn failed")
	execErr := NewExecutionError(ExecutionErrorSpawnError, "cannot spawn", cause)
	if execErr.Code != ExecutionErrorSpawnError || execErr.Message != "cannot spawn" || execErr.SpillPath != "" {
		t.Fatalf("fields = %+v", execErr)
	}
	if execErr.Error() != "cannot spawn" {
		t.Fatalf("Error() = %q, want the message", execErr.Error())
	}
	if !errors.Is(execErr, cause) || execErr.Cause != cause { //nolint:errorlint // identity: index.ts:71 stores the cause object itself as Error.cause
		t.Fatalf("cause is not reachable through Unwrap or Cause = %v, want %v", execErr.Cause, cause)
	}
	if errors.Unwrap(NewExecutionError(ExecutionErrorAborted, "aborted", nil)) != nil {
		t.Fatal("a nil cause must stay nil")
	}
}

// env/index.ts:19-21 getOrUndefined: the value of an ok result, undefined (nil) of a failure.
func TestGetOrUndefinedReturnsTheValueOfAnOkResultAndNilOfAFailure(t *testing.T) {
	value := "x"
	if got := GetOrUndefined(Ok[*string, *FileError](&value)); got == nil || *got != "x" {
		t.Fatalf("ok result: got %v", got)
	}
	failed := Result[*string, *FileError]{Value: &value, Error: NewFileError(FileErrorNotFound, "gone", "", nil)}
	if got := GetOrUndefined(failed); got != nil {
		t.Fatalf("failed result: got %v, want nil", got)
	}
}

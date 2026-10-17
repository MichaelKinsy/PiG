package durable

import (
	"errors"
	"testing"
)

// pi: packages/durable/src/errors.ts:21-50 (42a3497d): StorageRequestError(message) names itself and carries only the message; SessionFailed(cause) has the fixed message "Session failed after a storage error; close and reopen it", name "SessionFailed", and `cause` is the first error.
func TestStorageRequestErrorAndSessionFailedMatchErrorsTS(t *testing.T) {
	request := NewStorageRequestError("Invalid storage cursor")
	if request.Name() != "StorageRequestError" || request.Error() != "Invalid storage cursor" || request.Unwrap() != nil {
		t.Fatalf("StorageRequestError = %q, %q, %v", request.Name(), request.Error(), request.Unwrap())
	}
	cause := errors.New("disk full")
	failed := NewSessionFailed(cause)
	if failed.Name() != "SessionFailed" || failed.Error() != "Session failed after a storage error; close and reopen it" {
		t.Fatalf("SessionFailed = %q, %q", failed.Name(), failed.Error())
	}
	if !errors.Is(failed.Cause, cause) || !errors.Is(failed, cause) {
		t.Fatalf("SessionFailed does not keep its cause: %v", failed.Cause)
	}
	if _, ok := errors.AsType[*SessionFailed](failed); !ok {
		t.Fatal("a SessionFailed is not found by errors.AsType")
	}
	// A nil cause is a Pi `cause: undefined`: the error still reads as a SessionFailed with no cause to unwrap.
	if NewSessionFailed(nil).Unwrap() != nil {
		t.Fatal("a nil cause unwraps to something")
	}
}

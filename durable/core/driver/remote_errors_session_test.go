package driver

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// pi: packages/durable/src/errors.ts (42a3497d): a caller matches StorageRequestError and SessionFailed with instanceof; a caller of the driver matches the same Go types the in-process Session returns.
func TestCoreErrorMatchesStorageRequestErrorAndSessionFailed(t *testing.T) {
	var request error = &CoreError{Name: "StorageRequestError", Message: "Unknown conversation: 9"}
	got, ok := errors.AsType[*durable.StorageRequestError](request)
	if !ok || got.Message != "Unknown conversation: 9" {
		t.Fatalf("StorageRequestError = %v, %v", got, ok)
	}
	var failed error = &CoreError{Name: "SessionFailed", Message: "Session failed after a storage error; close and reopen it"}
	if _, ok := errors.AsType[*durable.SessionFailed](failed); !ok {
		t.Fatal("a CoreError named SessionFailed is not a *durable.SessionFailed")
	}
	if _, ok := errors.AsType[*durable.SessionFailed](request); ok {
		t.Fatal("a StorageRequestError matched SessionFailed")
	}
	if _, ok := errors.AsType[*durable.StorageRequestError](failed); ok {
		t.Fatal("a SessionFailed matched StorageRequestError")
	}
}

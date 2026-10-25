package durable

import (
	"errors"
	"testing"
)

// pi: packages/durable/src/errors.ts

// errors.ts sets `this.name` on every error class; the Go Name methods are those properties.
func TestDurableErrorNamesMatchPi(t *testing.T) {
	if got := NewReadAfterWrite("x").Name(); got != "ReadAfterWrite" {
		t.Errorf("ReadAfterWrite name = %q", got)
	}
	if got := NewStorageRequestError("m").Name(); got != "StorageRequestError" {
		t.Errorf("StorageRequestError name = %q", got)
	}
	if got := NewSessionFailed(nil).Name(); got != "SessionFailed" {
		t.Errorf("SessionFailed name = %q", got)
	}
	if got := NewConversationBusy(1).Name(); got != "ConversationBusy" {
		t.Errorf("ConversationBusy name = %q", got)
	}
	if got := NewReadOnlySession("x").Name(); got != "ReadOnlySession" {
		t.Errorf("ReadOnlySession name = %q", got)
	}
}

func TestDurableErrorMessagesAndCauseMatchErrorsTS(t *testing.T) {
	if got, want := NewReadAfterWrite("get").Error(), "Tx.get() cannot read tables after the first table write"; got != want {
		t.Errorf("ReadAfterWrite = %q, want %q", got, want)
	}
	if got, want := NewConversationBusy(12).Error(), "Conversation 12 is busy"; got != want {
		t.Errorf("ConversationBusy = %q, want %q", got, want)
	}
	if got, want := NewReadOnlySession("a commit with writes").Error(), "Read-only Harness: a commit with writes refused"; got != want {
		t.Errorf("ReadOnlySession = %q, want %q", got, want)
	}
	cause := errors.New("disk full")
	failed := NewSessionFailed(cause)
	if failed.Error() != "Session failed after a storage error; close and reopen it" || !errors.Is(failed, cause) {
		t.Errorf("SessionFailed = %q, cause reachable %v", failed.Error(), errors.Is(failed, cause))
	}
}

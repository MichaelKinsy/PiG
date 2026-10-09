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
	if got := NewStorageRejected("m", nil).Name(); got != "StorageRejected" {
		t.Errorf("StorageRejected name = %q", got)
	}
	if got := NewConversationBusy(1).Name(); got != "ConversationBusy" {
		t.Errorf("ConversationBusy name = %q", got)
	}
}

func TestDurableErrorMessagesAndCauseMatchErrorsTS(t *testing.T) {
	if got, want := NewReadAfterWrite("get").Error(), "Tx.get() cannot read tables after the first table write"; got != want {
		t.Errorf("ReadAfterWrite = %q, want %q", got, want)
	}
	if got, want := NewConversationBusy(12).Error(), "Conversation 12 is busy"; got != want {
		t.Errorf("ConversationBusy = %q, want %q", got, want)
	}
	cause := errors.New("disk full")
	rejected := NewStorageRejected("batch rejected", cause)
	if rejected.Error() != "batch rejected" || !errors.Is(rejected, cause) {
		t.Errorf("StorageRejected = %q, cause reachable %v", rejected.Error(), errors.Is(rejected, cause))
	}
}

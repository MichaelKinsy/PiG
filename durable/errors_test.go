package durable

import (
	"errors"
	"testing"
)

// The messages are Pi's: packages/durable/src/errors.ts:6 (ReadAfterWrite) and :24 (ConversationBusy).
// mutation-checked: zeroing the results of ConversationBusy.Error, ReadAfterWrite.Error fails it
// mutation-checked: dropping the reads and writes of ConversationBusy.ConversationId fails it
func TestDurableErrorMessagesMatchPi(t *testing.T) {
	if got, want := NewReadAfterWrite("task").Error(), "Tx.task() cannot read tables after the first table write"; got != want {
		t.Fatalf("ReadAfterWrite message %q, want %q", got, want)
	}
	if got, want := NewConversationBusy(7).Error(), "Conversation 7 is busy"; got != want {
		t.Fatalf("ConversationBusy message %q, want %q", got, want)
	}
	if got := NewConversationBusy(7).ConversationId; got != 7 {
		t.Fatalf("ConversationBusy carries conversation %d, want 7", got)
	}
}

// packages/durable/src/errors.ts:12-17: StorageRejected passes `options` (the cause) to Error, so the cause stays reachable.
// mutation-checked: dropping the reads and writes of StorageRejected.Cause fails it
// Pi: packages/durable/src/env/index.ts:49 (cause)
// packages/durable/src/errors.ts:12: StorageRejected is an Error with a message and a cause.
func TestStorageRejectedKeepsItsMessageAndCause(t *testing.T) {
	cause := errors.New("disk full")
	rejected := NewStorageRejected("batch rejected", cause)
	if rejected.Error() != "batch rejected" {
		t.Fatalf("message %q, want the message alone", rejected.Error())
	}
	if !errors.Is(rejected, cause) || rejected.Cause != cause { //nolint:errorlint // identity: errors.ts passes the cause object itself as Error.cause; a wrapped cause would pass errors.Is
		t.Fatalf("errors.Is does not reach the cause, or Cause = %v, want %v", rejected.Cause, cause)
	}
	if errors.Unwrap(NewStorageRejected("no cause", nil)) != nil {
		t.Fatal("a rejection without a cause unwraps to a cause")
	}
	if NewStorageRejected("no cause", nil).Cause != nil {
		t.Fatal("a rejection without a cause reports a Cause")
	}
}

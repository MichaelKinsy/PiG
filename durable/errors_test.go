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

// packages/durable/src/errors.ts SessionFailed: the constructor passes the cause to Error, so it stays reachable.
// mutation-checked: dropping the reads and writes of SessionFailed.Cause fails it
func TestSessionFailedKeepsItsMessageAndCause(t *testing.T) {
	cause := errors.New("disk full")
	failed := NewSessionFailed(cause)
	if failed.Error() != "Session failed after a storage error; close and reopen it" {
		t.Fatalf("message %q, want the fixed message", failed.Error())
	}
	if !errors.Is(failed, cause) || failed.Cause != cause { //nolint:errorlint // identity: errors.ts passes the cause object itself as Error.cause; a wrapped cause would pass errors.Is
		t.Fatalf("errors.Is does not reach the cause, or Cause = %v, want %v", failed.Cause, cause)
	}
	if errors.Unwrap(NewSessionFailed(nil)) != nil {
		t.Fatal("a failure without a cause unwraps to a cause")
	}
}

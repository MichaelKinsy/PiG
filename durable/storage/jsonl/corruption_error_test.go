package jsonl

import (
	"errors"
	"testing"
)

// packages/durable/src/storage/jsonl/storage.ts:81-86: JsonlCorruptionError(message, cause) keeps both; an absent cause is no cause.
func TestNewCorruptionKeepsMessageAndCause(t *testing.T) {
	cause := errors.New("bad line")
	withCause := NewJsonlCorruptionError("corrupt journal", cause)
	if withCause.Message != "corrupt journal" || withCause.Error() != "corrupt journal" || !errors.Is(withCause, cause) {
		t.Fatalf("with cause: %+v", withCause)
	}
	if withCause.Message != "corrupt journal" || !errors.Is(withCause.Cause, cause) {
		t.Fatalf("fields: Message=%q Cause=%v", withCause.Message, withCause.Cause)
	}
	if bare := NewJsonlCorruptionError("corrupt", nil); bare.Error() != "corrupt" || bare.Unwrap() != nil || bare.Cause != nil || bare.Message != "corrupt" {
		t.Fatalf("without cause: %+v", bare)
	}
}

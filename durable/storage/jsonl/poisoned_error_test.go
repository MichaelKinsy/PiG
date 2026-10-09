package jsonl

import (
	"errors"
	"testing"
)

// Ports packages/durable/src/storage/jsonl/storage.ts JsonlStoragePoisonedError: message, `name` and `cause` (Unwrap, as D103 maps it).
// Pi: packages/durable/src/storage/jsonl/storage.ts:82 (cause)
// Ports packages/durable/src/storage/jsonl/storage.ts:88 JsonlStoragePoisonedError (storage.ts:88-93): message, `name` and `cause` (Unwrap, as D103 maps it).
// Ports packages/durable/src/storage/jsonl/storage.ts:88-95 JsonlStoragePoisonedError: message, `name` and `cause` (Unwrap, as D103 maps it).
func TestJsonlStoragePoisonedErrorKeepsItsCauseAndName(t *testing.T) {
	cause := errors.New("disk full")
	e := NewJsonlStoragePoisonedError(cause)
	if e.Message != "JSONL storage is poisoned and must be reopened" || e.Error() != e.Message || e.Name() != "JsonlStoragePoisonedError" {
		t.Fatalf("error=%q name=%q", e.Error(), e.Name())
	}
	if !errors.Is(e.Cause, cause) || !errors.Is(e, cause) {
		t.Fatalf("cause = %v", e.Cause)
	}
}

// Ports packages/durable/src/storage/jsonl/storage.ts:81-86 JsonlCorruptionError: the message as given, `name` "JsonlCorruptionError", and the
// optional cause (Unwrap, as D103 maps it).
func TestJsonlCorruptionErrorKeepsItsMessageNameAndCause(t *testing.T) {
	cause := errors.New("bad record")
	e := NewJsonlCorruptionError("record 3 is truncated", cause)
	if e.Message != "record 3 is truncated" || !errors.Is(e.Cause, cause) {
		t.Fatalf("message=%q cause=%v", e.Message, e.Cause)
	}
	if e.Error() != "record 3 is truncated" || e.Name() != "JsonlCorruptionError" || !errors.Is(e, cause) {
		t.Fatalf("error=%q name=%q unwrap=%v", e.Error(), e.Name(), errors.Unwrap(e))
	}
	if bare := NewJsonlCorruptionError("bad", nil); errors.Unwrap(bare) != nil {
		t.Fatalf("a corruption without a cause unwraps to %v", errors.Unwrap(bare))
	}
}

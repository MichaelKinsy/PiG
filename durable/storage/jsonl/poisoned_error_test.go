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

// storage.ts:836-839 `this.poisonError ??= new JsonlStoragePoisonedError(cause)`: the first failure owns the poison, so a later failure returns the same
// error with the first cause, and assertUsable keeps throwing it.
func TestJsonlStoragePoisonKeepsTheFirstFailure(t *testing.T) {
	storage := &JsonlStorage{}
	first, second := errors.New("first failure"), errors.New("second failure")
	poisoned := storage.poison(first)
	again := storage.poison(second)
	if !errors.Is(again, poisoned) {
		t.Fatalf("a later poison returned %v (%p), want the first error (%p)", again, again, poisoned)
	}
	var typed *JsonlStoragePoisonedError
	if !errors.As(poisoned, &typed) || !errors.Is(typed.Cause, first) {
		t.Fatalf("poisoned error = %#v, want the first failure as its cause", poisoned)
	}
	if err := storage.assertUsable(); !errors.Is(err, poisoned) {
		t.Fatalf("assertUsable = %v, want the poisoned error", err)
	}
}

package jsonl

import (
	"errors"
	"testing"
)

// Pi durable/src/storage/jsonl/storage.ts JsonlCorruptionError: name, message and cause.
// mutation-checked: dropping the reads and writes of JsonlCorruptionError.Message fails it
// Pi: packages/durable/src/storage/jsonl/storage.ts:82 (cause)
// Pi: packages/durable/src/storage/jsonl/storage.ts:82 (message)
// Pi packages/durable/src/storage/jsonl/storage.ts:81 JsonlCorruptionError: name, message and cause.
// packages/durable/src/storage/jsonl/storage.ts:81-86 JsonlCorruptionError: name, message and cause.
func TestJsonlCorruptionErrorUpstream(t *testing.T) {
	cause := errors.New("bad line")
	e := NewJsonlCorruptionError("corrupt log", cause)
	var as *JsonlCorruptionError
	if !errors.As(error(e), &as) || as.Name() != "JsonlCorruptionError" || as.Error() != "corrupt log" || as.Message != "corrupt log" || as.Cause != cause || !errors.Is(e, cause) {
		t.Fatalf("got %+v", e)
	}
}

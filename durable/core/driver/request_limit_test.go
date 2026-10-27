// SPDX-License-Identifier: MIT

package driver

import (
	"errors"
	"testing"
)

func TestMergeAddsTheOp(t *testing.T) {
	t.Parallel()
	got, err := merge("get", map[string]any{"id": 1})
	if err != nil || got["op"] != "get" || got["id"] != 1 || len(got) != 2 {
		t.Fatalf("merge() = %v, %v; want the args and op", got, err)
	}
}

func TestMergeRejectsMoreFieldsThanTheLimitHolds(t *testing.T) {
	t.Parallel()
	args := map[string]any{"a": 1, "b": 2, "c": 3}
	if _, err := mergeWithin("x", args, 4); err != nil {
		t.Fatalf("mergeWithin() under the limit: %v", err)
	}
	if got, err := mergeWithin("x", args, 3); !errors.Is(err, ErrRequestTooLarge) || got != nil {
		t.Fatalf("mergeWithin() at the limit = %v, %v; want ErrRequestTooLarge", got, err)
	}
}

func TestMarshalBodyRejectsABodyOverTheLimit(t *testing.T) {
	t.Parallel()
	body := map[string]any{"op": "x"} // {"op":"x"} is 10 bytes
	if got, err := marshalBodyWithin(body, 10); err != nil || string(got) != `{"op":"x"}` {
		t.Fatalf("marshalBodyWithin() at the limit = %q, %v", got, err)
	}
	if got, err := marshalBodyWithin(body, 9); !errors.Is(err, ErrRequestTooLarge) || got != nil {
		t.Fatalf("marshalBodyWithin() over the limit = %q, %v; want ErrRequestTooLarge", got, err)
	}
}

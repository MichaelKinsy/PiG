package delta

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"testing"
)

// parse decodes JSON text into a strict JSON value, the Go form of an object literal.
func parse(t testing.TB, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

// canon is the JSON round trip of a value: ints become float64 and Op, WireOp and Path types become plain slices.
func canon(t testing.TB, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return parse(t, string(encoded))
}

// clone is JSON.parse(JSON.stringify(value)).
func clone(t testing.TB, value any) any { return canon(t, value) }

// expectJSON is toEqual against a JSON literal.
func expectJSON(t testing.TB, got any, want string) {
	t.Helper()
	if g, w := canon(t, got), parse(t, want); !reflect.DeepEqual(g, w) {
		gotText, _ := json.Marshal(g)
		t.Fatalf("got  %s\nwant %s", gotText, want)
	}
}

// expectEqual is toEqual: deep equality of two values after JSON normalization.
func expectEqual(t testing.TB, got, want any) {
	t.Helper()
	if g, w := canon(t, got), canon(t, want); !reflect.DeepEqual(g, w) {
		gotText, _ := json.Marshal(g)
		wantText, _ := json.Marshal(w)
		t.Fatalf("got  %s\nwant %s", gotText, wantText)
	}
}

// expectSame is toBe on containers: the same map, or the same slice header over one backing array.
func expectSame(t testing.TB, got, want any, what string) {
	t.Helper()
	if !strictEqual(got, want) {
		t.Fatalf("%s: not the same container", what)
	}
}

func expectNotSame(t testing.TB, got, want any, what string) {
	t.Helper()
	if strictEqual(got, want) {
		t.Fatalf("%s: unexpectedly the same container", what)
	}
}

// expectErr asserts that err is non-nil and, when pattern is non-empty, that its message matches it.
func expectErr(t testing.TB, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error matching %q, got none", pattern)
	}
	if pattern != "" && !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error %q does not match %q", err, pattern)
	}
}

func expectNoErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func expectTypeError(t testing.TB, err error) {
	t.Helper()
	if _, ok := errors.AsType[*TypeError](err); !ok {
		t.Fatalf("expected TypeError, got %v", err)
	}
}

func expectUnsafePath(t testing.TB, err error) {
	t.Helper()
	if _, ok := errors.AsType[*UnsafePathError](err); !ok {
		t.Fatalf("expected UnsafePathError, got %v", err)
	}
}

func expectPathError(t testing.TB, err error) {
	t.Helper()
	if _, ok := errors.AsType[*PathError](err); !ok {
		t.Fatalf("expected PathError, got %v", err)
	}
}

// ops parses a JSON operation batch.
func ops(t testing.TB, text string) []Op {
	t.Helper()
	raw, ok := parse(t, text).([]any)
	if !ok {
		t.Fatalf("ops %q is not an array", text)
	}
	out := make([]Op, len(raw))
	for at, item := range raw {
		out[at] = Op(item.([]any))
	}
	return out
}

// obj and arr read a child as the container it is.
func obj(t testing.TB, value any) map[string]any {
	t.Helper()
	typed, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %T", value)
	}
	return typed
}

func arr(t testing.TB, value any) []any {
	t.Helper()
	typed, ok := value.([]any)
	if !ok {
		t.Fatalf("not an array: %T", value)
	}
	return typed
}

// mustApply and friends fail the test on an error.
func mustApply(t testing.TB, target any, operations []Op) any {
	t.Helper()
	out, err := Apply(target, operations)
	expectNoErr(t, err)
	return out
}

func mustApplyImmutable(t testing.TB, target any, operations []Op) any {
	t.Helper()
	out, err := ApplyImmutable(target, operations)
	expectNoErr(t, err)
	return out
}

func mustPrepare(t testing.TB, c *Change) *Prepared {
	t.Helper()
	prepared, err := c.Prepare()
	expectNoErr(t, err)
	return prepared
}

func mustState(t testing.TB, c *Change) any {
	t.Helper()
	state, err := c.State()
	expectNoErr(t, err)
	return state
}

func nan() float64 { return math.NaN() }
func inf() float64 { return math.Inf(1) }

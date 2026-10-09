package delta

import (
	"errors"
	"testing"
)

// delta/index.ts:133-142 (UnsafePathError(segment)) and :310-317 (PathError(path)): the constructors keep their argument and build the
// message `unsafe path segment: ${String(segment)}` / `unresolvable path: ${JSON.stringify(path)}`; the applier raises them.
func TestPathErrorConstructorsAndTheApplierRaisingThem(t *testing.T) {
	path := Path{"a", 0, "b"}
	if err := NewPathError(path); err.Path == nil || err.Error() != `unresolvable path: ["a",0,"b"]` {
		t.Fatalf("NewPathError = %+v / %q", err, err.Error())
	}
	if err := NewPathError(7); err.Path != 7 || err.Error() != "unresolvable path: 7" {
		t.Fatalf("NewPathError(7) = %+v / %q", err, err.Error())
	}
	for _, c := range []struct {
		segment any
		want    string
	}{{"__proto__", "unsafe path segment: __proto__"}, {1.5, "unsafe path segment: 1.5"}, {nil, "unsafe path segment: null"}} {
		if err := NewUnsafePathError(c.segment); err.Segment != c.segment || err.Error() != c.want {
			t.Errorf("NewUnsafePathError(%v) = %+v / %q, want %q", c.segment, err, err.Error(), c.want)
		}
	}
	if err := AssertSafePath(Path{"constructor"}); err == nil {
		t.Fatal("a reserved segment passed AssertSafePath")
	} else if unsafe := (*UnsafePathError)(nil); !errors.As(err, &unsafe) || unsafe.Segment != "constructor" {
		t.Fatalf("AssertSafePath = %v, want *UnsafePathError{constructor}", err)
	}
}

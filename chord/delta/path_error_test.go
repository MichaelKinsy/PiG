package delta

import (
	"errors"
	"math"
	"testing"
)

// delta/index.ts:133-142,310-317: UnsafePathError's message is `unsafe path segment: ${String(segment)}` and PathError's is
// `unresolvable path: ${JSON.stringify(path)}`, so a path prints as a JSON array and not as Go's %v.
func TestPathErrorsPrintLikePi(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"path", &PathError{Path: []any{"a", float64(0), "b"}}, `unresolvable path: ["a",0,"b"]`},
		{"empty path", &PathError{Path: []any{}}, `unresolvable path: []`},
		{"dictionary id", &PathError{Path: 7}, `unresolvable path: 7`},
		// JSON.stringify leaves <, >, & and U+2028/U+2029 unescaped and prints -0 as 0; a literal backslash-u2028 text stays escaped.
		{"html and line separators", &PathError{Path: []any{"<a&b>", "x\u2028y\u2029", `\u2028`, math.Copysign(0, -1)}}, "unresolvable path: [\"<a&b>\",\"x\u2028y\u2029\",\"\\\\u2028\",0]"},
		// Any other segment (null, a boolean, an object) is JSON.stringify'd the same way: an object key keeps <, > and & as written.
		{"null, boolean and object segments", &PathError{Path: []any{nil, true, JsonObjectOf("<k&>", "v\u2028")}}, "unresolvable path: [null,true,{\"<k&>\":\"v\u2028\"}]"},
		{"negative zero segment", &UnsafePathError{Segment: math.Copysign(0, -1)}, `unsafe path segment: 0`},
		{"string segment", &UnsafePathError{Segment: "__proto__"}, `unsafe path segment: __proto__`},
		{"integer segment", &UnsafePathError{Segment: float64(-1)}, `unsafe path segment: -1`},
		{"fraction segment", &UnsafePathError{Segment: 1.5}, `unsafe path segment: 1.5`},
		{"null segment", &UnsafePathError{Segment: nil}, `unsafe path segment: null`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.err.Error(); got != c.want {
				t.Fatalf("Error() = %q, want %q", got, c.want)
			}
		})
	}
}

// ApplyImmutable raises them with the container path that fails to resolve: Pi 1.1.0 applyImmutable({a:{}}, [["s",["a","b","c"],1]]) throws PathError `unresolvable path: ["a","b"]` (run under Node 24 against .upstream/current/packages/chord/src/delta/index.ts).
func TestApplyImmutableReportsTheUnresolvablePathAsJSON(t *testing.T) {
	_, err := ApplyImmutable(JsonObjectOf("a", NewJsonObject(0)), []Op{{"s", []any{"a", "b", "c"}, float64(1)}})
	if _, ok := errors.AsType[*PathError](err); !ok {
		t.Fatalf("err = %v, want *PathError", err)
	}
	if got := err.Error(); got != `unresolvable path: ["a","b"]` {
		t.Fatalf("Error() = %q", got)
	}
}

// delta/index.ts:140 and :315 set `this.name` to "UnsafePathError" and "PathError". The appliers raise the concrete classes, and their names reach a
// caller that reads `error.name`: ai.ExtractDiagnosticError and the telemetry span status read it through interface{ Name() string }.
// The method is called on the concrete type so the test is evidence for these two classes only.
func TestPathErrorsCarryTheirPiNames(t *testing.T) {
	_, unresolvable := ApplyImmutable(JsonObjectOf("a", NewJsonObject(0)), []Op{{"s", []any{"a", "b", "c"}, float64(1)}})
	pathErr, ok := errors.AsType[*PathError](unresolvable)
	if !ok {
		t.Fatalf("unresolvable path error = %T, want *PathError", unresolvable)
	}
	if got := pathErr.Name(); got != "PathError" {
		t.Fatalf("unresolvable path error name = %q, want PathError", got)
	}
	_, unsafe := ApplyImmutable(JsonObjectOf("a", 1), []Op{{"s", []any{"__proto__"}, float64(1)}})
	unsafeErr, ok := errors.AsType[*UnsafePathError](unsafe)
	if !ok {
		t.Fatalf("reserved segment error = %T, want *UnsafePathError", unsafe)
	}
	if got := unsafeErr.Name(); got != "UnsafePathError" {
		t.Fatalf("reserved segment error name = %q, want UnsafePathError", got)
	}
}

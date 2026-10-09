package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// pi: packages/durable/src/harness/json.ts

func assignOps(t *testing.T, base map[string]any, key string, value any) []any {
	t.Helper()
	// A Go map literal has no key order; the Chord boundary copies it in own-key order (ascending here), as a Pi literal written in that order.
	object, err := chord.CopyJSONObject(base)
	if err != nil {
		t.Fatal(err)
	}
	tracker := delta.Track(object)
	change := tracker.BeginChange()
	if err := AssignJson(change.State(), key, value); err != nil {
		t.Fatal(err)
	}
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	ops := make([]any, 0, len(prepared.Ops()))
	for _, op := range prepared.Ops() {
		ops = append(ops, op)
	}
	return ops
}

// assignJson (json.ts:9-29) writes a value leaf by leaf, so Chord records only the changed members: a longer string is one append, a record
// keeps shared members and sets/deletes the rest, and an array that does not shrink is extended by pushes.
func TestAssignJsonWritesLeafByLeafLikePi(t *testing.T) {
	cases := []struct {
		name  string
		base  map[string]any
		key   string
		value any
		want  []any
	}{
		{"a longer string is one append", map[string]any{"s": "hello"}, "s", "hello world", []any{[]any{"a", []any{"s"}, " world"}}},
		{"an identical leaf records nothing", map[string]any{"s": "hello"}, "s", "hello", []any{}},
		{"a record sets new members and deletes missing ones without a full set", map[string]any{"a": map[string]any{"x": 1.0, "y": 2.0}}, "a", map[string]any{"x": 1.0, "z": 3.0},
			[]any{[]any{"s", []any{"a", "z"}, 3.0}, []any{"d", []any{"a", "y"}}}},
		{"a growing array is extended by a push", map[string]any{"l": []any{"p", "q"}}, "l", []any{"p", "q", "r"}, []any{[]any{"p", []any{"l"}, 2, 0, []any{"r"}}}},
		{"an unchanged array records nothing", map[string]any{"l": []any{"p", "q"}}, "l", []any{"p", "q"}, []any{}},
		{"a scalar replacing a record is one set", map[string]any{"a": map[string]any{"x": 1.0}}, "a", "gone", []any{[]any{"s", []any{"a"}, "gone"}}},
		{"a new key is one set", map[string]any{}, "k", 5.0, []any{[]any{"s", []any{"k"}, 5.0}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := assignOps(t, c.base, c.key, c.value)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ops = %#v, want %#v", got, c.want)
			}
		})
	}
}

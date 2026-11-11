package modelgen

import (
	"testing"
)

// parse is JSON.parse of a test fixture.
func parse(t testing.TB, text string) any {
	t.Helper()
	value, err := ParseJSON([]byte(text))
	if err != nil {
		t.Fatalf("fixture %s: %v", text, err)
	}
	return value
}

// parseObj parses a JSON object fixture.
func parseObj(t testing.TB, text string) *Obj {
	t.Helper()
	object, ok := parse(t, text).(*Obj)
	if !ok {
		t.Fatalf("fixture %s is not an object", text)
	}
	return object
}

// parseObjs parses a JSON array of objects fixture.
func parseObjs(t testing.TB, text string) []*Obj {
	t.Helper()
	list, ok := parse(t, text).([]any)
	if !ok {
		t.Fatalf("fixture %s is not an array", text)
	}
	out := make([]*Obj, len(list))
	for i, item := range list {
		out[i], ok = item.(*Obj)
		if !ok {
			t.Fatalf("fixture %s: item %d is not an object", text, i)
		}
	}
	return out
}

// requireJSON fails unless got serializes to want, whitespace aside: member order, number text and escapes must match exactly,
// because catalog output is compared with Pi's byte for byte. A nil *Obj is undefined, which wants `null` here.
func requireJSON(t testing.TB, got any, want string) {
	t.Helper()
	if object, ok := got.(*Obj); ok && object == nil {
		got = nil
	}
	if list, ok := got.([]*Obj); ok {
		items := make([]any, len(list))
		for i, item := range list {
			items[i] = item
		}
		got = items
	}
	canonical := string(Stringify(parse(t, want), ""))
	if actual := string(Stringify(got, "")); actual != canonical {
		t.Fatalf("got  %s\nwant %s", actual, canonical)
	}
}

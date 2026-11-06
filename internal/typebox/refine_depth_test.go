package typebox

import "testing"

func overflows(t *testing.T, name string, walk func()) {
	t.Helper()
	defer func() {
		if _, ok := recover().(*StackOverflowError); !ok {
			t.Errorf("%s: a schema that refers to itself did not end in a *StackOverflowError", name)
		}
	}()
	walk()
}

// TestWalkersStopASelfReferringSchema: each walker (Errors, RefineErrors and RefineCheck) counts its depth, so `{"$ref":"#"}` and `{"$id":"A","$ref":"A"}` end in a panic the caller recovers
// instead of in Go's fatal stack overflow, which no recover catches. Without the limit this test kills the test binary.
func TestWalkersStopASelfReferringSchema(t *testing.T) {
	hook := func(int, bool, any) any { return true }
	for name, schema := range map[string]any{
		"hash": map[string]any{"$ref": "#"},
		"id":   map[string]any{"$id": "A", "$ref": "A"},
		"not":  map[string]any{"not": map[string]any{"$ref": "#"}},
	} {
		overflows(t, "Errors "+name, func() { Errors(schema, 1.0) })
		overflows(t, "RefineErrors "+name, func() { RefineErrors(schema, 1.0, hook) })
		overflows(t, "RefineCheck "+name, func() { RefineCheck(schema, 1.0, hook) })
	}
}

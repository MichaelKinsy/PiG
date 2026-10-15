package chordjson

import (
	"math"
	"testing"
)

// Pi packages/chord/src/json.ts copies object properties in order and throws the first failing property's TypeError (json.ts:24
// non-finite number, json.ts:27 non-JSON value), so an object with several bad properties always reports the same one. A Go
// map has no insertion order; Copy reports the first failing property in OwnKeys order, the order the delta package uses.
// Before this order was fixed, the reported error followed Go's random map iteration.
func TestCopyReportsTheFirstFailingPropertyDeterministically(t *testing.T) {
	value := map[string]any{"a": math.Inf(1)}
	for _, key := range []string{"b", "c", "d", "e", "f", "g", "h"} {
		value[key] = make(chan int)
	}
	nested := map[string]any{"outer": []any{map[string]any{"x": func() {}, "w": math.NaN()}}}
	for range 200 {
		if _, err := Copy(value); err == nil || err.Error() != "Value contains a non-finite number and is not strict JSON" {
			t.Fatalf("Copy = %v, want the non-finite error of key a", err)
		}
		if _, err := Copy(nested); err == nil || err.Error() != "Value contains a non-finite number and is not strict JSON" {
			t.Fatalf("nested Copy = %v, want the non-finite error of key w", err)
		}
	}
}

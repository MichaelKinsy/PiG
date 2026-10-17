package delta

import (
	"testing"
	"time"
)

// upstream: packages/chord/src/json.ts:26-27 and :46-49. Measured on the pinned source with node --experimental-strip-types: copyJson({a: new Date()}) and copyJson({a: new Map()}) throw "Value must contain strict JSON plain objects or arrays", and copyJson({a: () => 1}) throws "Value contains a non-JSON function; expected strict JSON". A Go struct or pointer stands for the non-plain object and a Go func for the function; a Go chan has no JavaScript counterpart and keeps its kind name.
func TestCloneJSONReportsPisMessageForEachGoKind(t *testing.T) {
	type date struct{ At int64 }
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"struct", date{}, "Value must contain strict JSON plain objects or arrays"},
		{"time", time.Unix(0, 0), "Value must contain strict JSON plain objects or arrays"},
		{"pointer", &date{}, "Value must contain strict JSON plain objects or arrays"},
		{"func", func() {}, "Value contains a non-JSON function; expected strict JSON"},
		{"chan", make(chan int), "Value contains a non-JSON chan; expected strict JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CloneJSON(JsonObjectOf("a", test.value))
			if err == nil || err.Error() != test.want {
				t.Fatalf("got %v, want %q", err, test.want)
			}
			expectStrictJSONError(t, err, "strict JSON")
		})
	}
}

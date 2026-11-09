package data

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// TestChordErrorMarshalIsTheStringifiedToJSON pins ChordError's JSON to Pi's JSON.stringify of toJSON (packages/chord/src/data/errors.ts:57): code, reason, message and details in that order, `<`, `>`, `&` and U+2028/U+2029 written literally, and a negative zero in details as 0.
func TestChordErrorMarshalIsTheStringifiedToJSON(t *testing.T) {
	cases := []struct {
		name string
		err  *ChordError
		want string
	}{
		{"plain", &ChordError{Code: "x", Message: "plain", Reason: "r"}, `{"code":"x","reason":"r","message":"plain"}`},
		{"html and separators", &ChordError{Code: "x", Message: "a<b>&\u2028\u2029é"}, "{\"code\":\"x\",\"message\":\"a<b>&\u2028\u2029é\"}"},
		{"negative zero in details", &ChordError{Code: "x", Message: "m", HasDetails: true, Details: delta.JsonObjectOf("n", math.Copysign(0, -1), "list", []any{math.Copysign(0, -1), 1.5})}, `{"code":"x","message":"m","details":{"n":0,"list":[0,1.5]}}`},
		{"separators in details", &ChordError{Code: "x", Message: "m", HasDetails: true, Details: delta.JsonObjectOf("k\u2028", "v\u2029")}, "{\"code\":\"x\",\"message\":\"m\",\"details\":{\"k\u2028\":\"v\u2029\"}}"},
	}
	for _, test := range cases {
		direct, err := test.err.MarshalJSON()
		if err != nil || string(direct) != test.want {
			t.Errorf("%s: MarshalJSON got %q, %v; want %q", test.name, direct, err, test.want)
		}
		wrapped, err := jsstring.MarshalJSON(struct{ E any }{test.err})
		if want := `{"E":` + test.want + `}`; err != nil || string(wrapped) != want {
			t.Errorf("%s: JSON.stringify got %q, %v; want %q", test.name, wrapped, err, want)
		}
	}
}

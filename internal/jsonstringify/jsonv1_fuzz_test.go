package jsonstringify

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

// FuzzObjectMatchesV1: object (the JSON.parse then JSON.stringify rewrite of one object) writes the same bytes as the encoding/json v1
// walker did, and fails on the same input. A cut-off document is compared too: the v1 walker stopped at the cut-off and returned what it
// had, and canonicalize reaches object only for text that json.Valid or IsCanonical has accepted, so only complete JSON is compared.
func FuzzObjectMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 || data[0] != '{' || !json.Valid(data) {
			t.Skip("object reads a complete JSON object")
		}
		got, gotErr := object(data)
		want, wantErr := objectV1(data)
		jsonseed.Same(t, data, "object", got, want, gotErr, wantErr)
	})
}

func BenchmarkObject(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { _, _ = objectV1(d) }, func(d []byte) { _, _ = object(d) })
}

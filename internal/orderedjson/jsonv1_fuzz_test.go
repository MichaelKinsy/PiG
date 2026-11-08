package orderedjson

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

// FuzzUnmarshalJSONMatchesV1: the jsontext reader keeps the same keys in the same order with the same values as the encoding/json v1 one
// (jsonv1_ref_test.go), and rejects the same documents.
func FuzzUnmarshalJSONMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		var got, want Object
		gotErr := got.UnmarshalJSON(data)
		wantErr := want.UnmarshalJSONV1(data)
		jsonseed.Same(t, data, "Object.UnmarshalJSON", &got, &want, gotErr, wantErr)
	})
}

func BenchmarkUnmarshalJSON(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { var o Object; _ = o.UnmarshalJSONV1(d) }, func(d []byte) { var o Object; _ = o.UnmarshalJSON(d) })
}

package main

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzObjectUnmarshalJSONMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		var got, want object
		gotErr := got.UnmarshalJSON(data)
		wantErr := want.UnmarshalJSONV1(data)
		jsonseed.Same(t, data, "object.UnmarshalJSON", got, want, gotErr, wantErr)
	})
}

func BenchmarkObjectUnmarshalJSON(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { var o object; _ = o.UnmarshalJSONV1(d) }, func(d []byte) { var o object; _ = o.UnmarshalJSON(d) })
}

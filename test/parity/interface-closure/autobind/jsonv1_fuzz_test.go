package main

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzOrderedRowUnmarshalJSONMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if !jsonseed.IsObject(data) || !json.Valid(data) {
			t.Skip("an ordered row is a complete object; the reader never checked the closing token")
		}
		var got, want orderedRow
		gotErr := got.UnmarshalJSON(data)
		wantErr := want.UnmarshalJSONV1(data)
		jsonseed.Same(t, data, "orderedRow.UnmarshalJSON", got, want, gotErr, wantErr)
	})
}

func BenchmarkOrderedRowUnmarshalJSON(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { var r orderedRow; _ = r.UnmarshalJSONV1(d) }, func(d []byte) { var r orderedRow; _ = r.UnmarshalJSON(d) })
}

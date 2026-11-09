package closure

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzDecodeJSONObjectMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		got, gotErr := decodeJSONObject(data)
		want, wantErr := decodeJSONObjectV1(data)
		jsonseed.Same(t, data, "decodeJSONObject", got, want, gotErr, wantErr)
	})
}

func BenchmarkDecodeJSONObject(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { _, _ = decodeJSONObjectV1(d) }, func(d []byte) { _, _ = decodeJSONObject(d) })
}

package telemetrysqlite

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzDecodeAttributesMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Add([]byte(`{"a":"x","b":true,"c":["p","q"],"d":[true],"e":null,"a":"y"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, gotErr := decodeAttributes(string(data))
		want, wantErr := decodeAttributesV1(string(data))
		jsonseed.Same(t, data, "decodeAttributes", got, want, gotErr, wantErr)
	})
}

func BenchmarkDecodeAttributes(b *testing.B) {
	doc := []byte(`{"http.method":"GET","http.url":"https://example.test/x","retry":true,"tags":["a","b","c"],"ok":[true,false],"n":null,"user":"u"}`)
	jsonseed.Bench(b, doc, func(d []byte) { _, _ = decodeAttributesV1(string(d)) }, func(d []byte) { _, _ = decodeAttributes(string(d)) })
}

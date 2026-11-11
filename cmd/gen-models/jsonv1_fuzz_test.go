package main

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzDecodeJSONModelGroupMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Add([]byte(`{"chat:a":{"type":"chat","id":"a","provider":"p","api":"x"},"chat:a":{"type":"chat","id":"a","provider":"p","api":"x"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, gotErr := decodeJSONModelGroup(data, map[string]string{})
		want, wantErr := decodeJSONModelGroupV1(data, map[string]string{})
		jsonseed.Same(t, data, "decodeJSONModelGroup", got, want, gotErr, wantErr)
	})
}

func BenchmarkDecodeJSONModelGroup(b *testing.B) {
	doc := []byte(`{"chat:a":{"type":"chat","id":"a","provider":"p","api":"x"},"chat:b":{"type":"chat","id":"b","provider":"p","api":"x"}}`)
	jsonseed.Bench(b, doc, func(d []byte) { _, _ = decodeJSONModelGroupV1(d, map[string]string{}) }, func(d []byte) { _, _ = decodeJSONModelGroup(d, map[string]string{}) })
}

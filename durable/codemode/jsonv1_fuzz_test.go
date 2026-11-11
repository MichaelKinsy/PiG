package codemode

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzJvalKeysMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SameResult(t, data, "jval.keys", jval(data).keys(), jval(data).keysV1())
	})
}

func BenchmarkJvalKeys(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { jval(d).keysV1() }, func(d []byte) { jval(d).keys() })
}

package modeldata

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzParseModelDataObjectMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		jsonseed.SameResult(t, data, "parseModelDataObject", parseModelDataObject(data), parseModelDataObjectV1(data))
	})
}

func BenchmarkParseModelDataObject(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { parseModelDataObjectV1(d) }, func(d []byte) { parseModelDataObject(d) })
}

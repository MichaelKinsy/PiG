package configschema

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzParseJSONNodeMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Add([]byte(`[1,2.5,-0,1e999,"x",true,false,null,{"b":1,"a":2,"1":3,"b":4}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		got, gotErr := parseJSONNode(data)
		want, wantErr := parseJSONNodeV1(data)
		jsonseed.Same(t, data, "parseJSONNode", got, want, gotErr, wantErr)
	})
}

func BenchmarkParseJSONNode(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { _, _ = parseJSONNodeV1(d) }, func(d []byte) { _, _ = parseJSONNode(d) })
}

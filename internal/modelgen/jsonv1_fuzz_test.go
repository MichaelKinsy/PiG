package modelgen

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzParseJSONMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Add([]byte(`{"a":[1,2.50,1e3,-0,0.1,"x",true,false,null],"a":{"b":1}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		got, gotErr := ParseJSON(data)
		want, wantErr := ParseJSONV1(data)
		jsonseed.Same(t, data, "ParseJSON", got, want, gotErr, wantErr)
	})
}

func BenchmarkParseJSON(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { _, _ = ParseJSONV1(d) }, func(d []byte) { _, _ = ParseJSON(d) })
}

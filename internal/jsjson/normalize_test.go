package jsjson

import (
	"testing"
)

func TestNormalizeIntegers(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"a":12.0}`, `{"a":12}`},
		{`{"a":1.2e1,"b":12E0,"c":120e-1}`, `{"a":12,"b":12,"c":12}`},
		{`{"a":-3.0}`, `{"a":-3}`},
		{`{"a":-0.0}`, `{"a":0}`},
		{`{"a":1.5,"b":"12.0","c":"\"1.0\""}`, `{"a":1.5,"b":"12.0","c":"\"1.0\""}`},
		{`[1,2,3]`, `[1,2,3]`},
		{`{"a":9007199254740993.0}`, `{"a":9007199254740993.0}`},
		{`{"a":1e400}`, `{"a":1e400}`},
		{`{"a":2.50e1}`, `{"a":25}`},
		{`{"a":00e-1,"b":01.0,"c":1.,"d":1e,"e":-.5e1,"f":1.0e+0}`, `{"a":00e-1,"b":01.0,"c":1.,"d":1e,"e":-.5e1,"f":1}`},
	} {
		if got := string(NormalizeIntegers([]byte(tc.in))); got != tc.want {
			t.Errorf("%s -> %s, want %s", tc.in, got, tc.want)
		}
	}
	unchanged := []byte(`{"a":1,"b":"x.0"}`)
	if got := NormalizeIntegers(unchanged); &got[0] != &unchanged[0] {
		t.Error("an unchanged input was copied")
	}
}

func BenchmarkNormalizeIntegersNoRewrite(b *testing.B) {
	data := []byte(`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hello world, this is a typical chunk. 3.14"},"finish_reason":null}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)
	b.ReportAllocs()
	for range b.N {
		NormalizeIntegers(data)
	}
}

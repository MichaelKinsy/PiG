package json

import (
	"math"
	"testing"
)

// JSON.stringify writes NaN and the infinities as null and negative zero as 0; Marshal keeps encoding/json's refusal.
func TestMarshalJSWritesNumbersAsJSONStringifyDoes(t *testing.T) {
	value := map[string]any{"n": math.NaN(), "p": math.Inf(1), "m": math.Inf(-1), "z": math.Copysign(0, -1), "l": []float64{math.NaN(), 1.5}, "s": struct{ F float32 }{float32(math.Inf(1))}}
	got, err := MarshalJS(value)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"l":[null,1.5],"m":null,"n":null,"p":null,"s":{"F":null},"z":0}`
	if string(got) != want {
		t.Fatalf("MarshalJS = %s, want %s", got, want)
	}
	if _, err := Marshal(value); err == nil {
		t.Fatal("Marshal accepted a non-finite number")
	}
}

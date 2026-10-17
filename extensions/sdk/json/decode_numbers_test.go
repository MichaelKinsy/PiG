package json

import (
	"math"
	"testing"
)

// JSON.parse reads a number outside the float64 range (1e400) as Infinity, and the frames Pi writes spell it null. The decoder refused it, so a model's tool
// arguments of {"v":1e400} failed the call in the Go SDK where Node, Python and (after this change) Rust take it.
func TestUnmarshalReadsANumberOutOfRangeAsInfinity(t *testing.T) {
	var generic map[string]any
	if err := Unmarshal([]byte(`{"p":1e400,"m":-1e999,"u":1e-400,"f":2.5}`), &generic); err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(generic["p"].(float64), 1) || !math.IsInf(generic["m"].(float64), -1) || generic["u"].(float64) != 0 || generic["f"].(float64) != 2.5 {
		t.Fatalf("decoded %v", generic)
	}
	var typed struct {
		P float64
		Q float32
	}
	if err := Unmarshal([]byte(`{"P":1e400}`), &typed); err != nil || !math.IsInf(typed.P, 1) {
		t.Fatalf("typed float64 = %v, %v", typed.P, err)
	}
	if err := Unmarshal([]byte(`{"Q":1e400}`), &typed); err == nil {
		t.Fatal("a float32 that overflows was accepted")
	}
	if err := Unmarshal([]byte(`{"p":1e400`), &generic); err == nil {
		t.Fatal("a truncated frame was accepted")
	}
}

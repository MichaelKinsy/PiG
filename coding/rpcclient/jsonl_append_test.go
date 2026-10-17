package rpcclient

import (
	"bytes"
	"math"
	"testing"
)

type failingJSON struct{}

func (failingJSON) MarshalJSON() ([]byte, error) { return nil, bytes.ErrTooLarge }

// AppendJsonLine appends the line SerializeJsonLine returns, and an error leaves dst as it was.
func TestAppendJsonLineMatchesSerializeJsonLine(t *testing.T) {
	values := []any{
		nil, "plain", "line\u2028para\u2029 <&> \xff \xed\xa0\x80 \U0001F600", 1, math.Copysign(0, -1), 1e21, math.NaN(), true,
		map[string]any{"b": []any{1, "x\n"}, "a": nil}, []string{"a"}, struct {
			A int    `json:"a"`
			B string `json:"b"`
		}{1, "x"},
	}
	for i, value := range values {
		want, wantErr := SerializeJsonLine(value)
		got, err := AppendJsonLine([]byte("kept"), value)
		if (err == nil) != (wantErr == nil) || !bytes.Equal(got, append([]byte("kept"), want...)) {
			t.Fatalf("value %d: AppendJsonLine = %q (%v), SerializeJsonLine = %q (%v)", i, got, err, want, wantErr)
		}
	}
	got, err := AppendJsonLine([]byte("kept"), []any{1, failingJSON{}})
	if err == nil || string(got) != "kept" {
		t.Fatalf("a failing value returned %q, %v; want dst unchanged and an error", got, err)
	}
}

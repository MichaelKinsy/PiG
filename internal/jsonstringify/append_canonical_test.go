package jsonstringify

import (
	"bytes"
	"testing"
)

func TestAppendCanonicalAppendsWhatCanonicalizeReturns(t *testing.T) {
	prefix := []byte("keep:")
	for _, text := range []string{`{"a":1,"b":[true,null,"x"]}`, ` { "b" : 2 , "1" : 1 } `, `[-0,1.50,1e3]`, `"\u2028"`, `{"a":1,"a":2}`, `7`} {
		want, err := Canonicalize([]byte(text))
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		got, err := AppendCanonical(bytes.Clone(prefix), []byte(text))
		if err != nil || !bytes.Equal(got, append(bytes.Clone(prefix), want...)) {
			t.Errorf("%q: got %q (%v), want %q", text, got, err, append(bytes.Clone(prefix), want...))
		}
	}
}

func TestAppendCanonicalLeavesDstAloneOnAnError(t *testing.T) {
	dst := []byte("keep")
	for _, text := range []string{``, `{`, `{"a":}`} {
		got, err := AppendCanonical(dst, []byte(text))
		if err == nil || string(got) != "keep" {
			t.Errorf("%q: got %q (%v)", text, got, err)
		}
	}
}

// Text the scan accepts is appended without a copy of its own: a caller that reuses dst allocates nothing.
func TestAppendCanonicalOfCanonicalTextAllocatesNothing(t *testing.T) {
	text := []byte(`{"name":"lookup","n":[1,2,3],"ok":true}`)
	dst := make([]byte, 0, 256)
	if allocs := testing.AllocsPerRun(100, func() { dst, _ = AppendCanonical(dst[:0], text) }); allocs != 0 {
		t.Fatalf("%.0f allocs", allocs)
	}
}

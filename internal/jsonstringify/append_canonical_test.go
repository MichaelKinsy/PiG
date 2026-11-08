package jsonstringify

import (
	"bytes"
	"math/rand"
	"strings"
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

// referenceCanonical is AppendCanonical without the whitespace fast path: the scan, then the full rewrite.
func referenceCanonical(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if IsCanonical(raw) {
		return raw, nil
	}
	return canonicalize(raw)
}

// Spacing alone never changes what Canonicalize returns: spaced text, invalid text with spaces inside a token, and text the rewrite changes all agree with the full rewrite, error or not.
func TestAppendCanonicalSpacedTextMatchesTheRewrite(t *testing.T) {
	fixed := []string{
		`{"a": 1, "b": [true, null, "x y"]}`, "{\n  \"a\": 1,\n  \"b\": [\n    1,\n    2\n  ]\n}", `[ 1 , 2 ]`, `{ }`, `[ ]`, `{"a" : "b" , "c" : { "d" : [ ] } }`,
		`{"s": "a \" b  ,  : c"}`, `{"s": "tab\t in  string"}`, `{"a": -0}`, `{"a": 1.50}`, `{"1": 1, "b": 2}`, `{"a": 1, "a": 2}`, `{"a": "\u00e9"}`,
		`{"a": tr ue}`, `[1 2]`, `[- 1]`, `{"a" "b"}`, `{"a": 1 "b": 2}`, `["a" "b"]`, `{"a": "x` + " ", `[1, 2`, `{"a": nu ll}`, `[1 ,, 2]`, `{ "a" : 1 , }`, `"s p"`, `[ "a" , "b" ]`,
	}
	for _, text := range fixed {
		checkAppendCanonical(t, text)
	}
	rng := rand.New(rand.NewSource(7))
	for _, text := range fastPathCorpus(rng, 20000) {
		checkAppendCanonical(t, text)
		checkAppendCanonical(t, spaced(rng, text))
	}
}

// spaced inserts whitespace at random bytes outside, and sometimes inside, the text's strings.
func spaced(rng *rand.Rand, text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if rng.Intn(6) == 0 {
			out.WriteString([]string{" ", "\t", "\n", "\r\n", "  "}[rng.Intn(5)])
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

func checkAppendCanonical(t *testing.T, text string) {
	t.Helper()
	want, wantErr := referenceCanonical([]byte(text))
	got, err := AppendCanonical([]byte("keep:"), []byte(text))
	if (err != nil) != (wantErr != nil) {
		t.Fatalf("%q: error %v, reference %v", text, err, wantErr)
	}
	if wantErr != nil {
		if string(got) != "keep:" {
			t.Fatalf("%q: dst changed on an error: %q", text, got)
		}
		return
	}
	if string(got) != "keep:"+string(want) {
		t.Fatalf("%q: got %q, want %q", text, got, want)
	}
}

func TestAppendCanonicalOfSpacedCanonicalTextAllocatesNothing(t *testing.T) {
	text := []byte(`{"name": "lookup", "n": [1, 2, 3], "ok": true}`)
	dst := make([]byte, 0, 256)
	if allocs := testing.AllocsPerRun(100, func() { dst, _ = AppendCanonical(dst[:0], text) }); allocs != 0 {
		t.Fatalf("%.0f allocs", allocs)
	}
	if string(dst) != `{"name":"lookup","n":[1,2,3],"ok":true}` {
		t.Fatalf("%q", dst)
	}
}

func BenchmarkAppendCanonicalSpacedMessage(b *testing.B) {
	text := []byte(`{"role": "assistant", "content": [{"type": "thinking", "thinking": "alpha beta gamma delta", "thinkingSignature": "reasoning_content"}, {"type": "text", "text": "## Heading\n\n- **item**: text ` + "`code()`" + `"}, {"type": "toolCall", "id": "call_h1", "name": "bash", "arguments": {"command": "echo history 1"}}], "api": "openai-completions", "provider": "fake", "model": "fake-model", "usage": {"input": 1000, "output": 500, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 1500, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "toolUse", "timestamp": 1700000001000}`)
	dst := make([]byte, 0, 2*len(text))
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		dst, _ = AppendCanonical(dst[:0], text)
	}
}

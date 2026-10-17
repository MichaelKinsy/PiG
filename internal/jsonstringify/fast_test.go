package jsonstringify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// fastPathCorpus builds JSON text that exercises every reason the full rewrite changes text (and every near miss that it does not): numbers around the shortest-form rules, escapes, integer-like and repeated keys, whitespace, invalid UTF-8 and truncation.
func fastPathCorpus(rng *rand.Rand, count int) []string {
	pick := func(options ...string) string { return options[rng.Intn(len(options))] }
	numbers := []string{"0", "-0", "1", "-1", "10", "100", "0.5", "-0.5", "0.1", "0.10", "1.0", "1.5", "1.50", "0.30000000000000004", "123456789012345", "1234567890123456", "12345678901234567890", "-123456789012345", "123456789.012345", "123456789.0123456", "0.000001", "0.0000010", "0.0000001", "0.00000123", "0.000123", "1e5", "1E5", "1e-7", "1e21", "1e+21", "5e-324", "1e400", "00", "01", "-", "1.", ".5", "0.", "+1", "1e", "0.1e1", "9007199254740993", "999999999999999", "0.999999999999999", "0.9999999999999999", "100000000000000000000", "-0.0", "-0.5e-7"}
	keys := []string{"a", "b", "type", "name", "1", "0", "01", "10", "4294967295", "4294967294", "12345", "4294967296", "-1", "1.5", "", " ", "é", "😀", `\u0061`, `\/`, `\"`, `a\\b`, "constructor", "__proto__"}
	strs := []string{"", "x", "hello world", "é", "日本語", "😀", "\u2028", "\u2029", "\u007f", "a\\nb\\tc\\rd", `\"quoted\"`, `back\\slash`, `\u0000`, `\u001f`, `\u0041`, `\u2028`, `\/`, `\b`, `\f`, `\ud83d\ude00`, `\ud800`, "\x01", "\x1f", "a b", "\xff", "\xed\xa0\x80", "ok\ufeff"}
	var value func(depth int) string
	value = func(depth int) string {
		switch k := rng.Intn(12); {
		case k < 3 || depth > 4:
			return `"` + strs[rng.Intn(len(strs))] + `"`
		case k < 6:
			return numbers[rng.Intn(len(numbers))]
		case k == 6:
			return pick("true", "false", "null", "nul", "True", "truee")
		case k < 9:
			var members []string
			for range rng.Intn(7) {
				members = append(members, `"`+keys[rng.Intn(len(keys))]+`":`+value(depth+1))
			}
			return "{" + strings.Join(members, pick(",", ",", ",", " , ", ",\n")) + "}"
		default:
			var items []string
			for range rng.Intn(6) {
				items = append(items, value(depth+1))
			}
			return "[" + strings.Join(items, pick(",", ",", ", ")) + "]"
		}
	}
	corpus := make([]string, 0, count)
	for range count {
		text := value(0)
		switch rng.Intn(8) {
		case 0: // a byte of the text replaced, inserted or removed
			if len(text) > 0 {
				at := rng.Intn(len(text))
				switch rng.Intn(3) {
				case 0:
					text = text[:at] + string([]byte{byte(rng.Intn(256))}) + text[at+1:]
				case 1:
					text = text[:at] + string([]byte{byte(rng.Intn(256))}) + text[at:]
				default:
					text = text[:at] + text[at+1:]
				}
			}
		case 1: // truncated
			text = text[:rng.Intn(len(text)+1)]
		case 2:
			text = " \n" + text + "\t "
		}
		corpus = append(corpus, text)
	}
	return corpus
}

// A text the scan accepts must come back from the full rewrite byte for byte: the fast path may only skip work, never change output.
func checkFastPath(t *testing.T, text string) (accepted bool) {
	t.Helper()
	trimmed := bytes.TrimSpace([]byte(text))
	if !IsCanonical(trimmed) {
		return false
	}
	want, err := canonicalize(trimmed)
	if err != nil || !bytes.Equal(want, trimmed) {
		t.Fatalf("scan accepted %q but the rewrite gives %q (err %v)", trimmed, want, err)
	}
	return true
}

func TestFastPathNeverChangesCanonicalize(t *testing.T) {
	rng := rand.New(rand.NewSource(20261011))
	accepted, rejected := 0, 0
	for _, text := range fastPathCorpus(rng, 200000) {
		if checkFastPath(t, text) {
			accepted++
		} else {
			rejected++
		}
		// The public entry point must agree with the rewrite for every text, valid or not.
		got, gotErr := Canonicalize([]byte(text))
		want, wantErr := canonicalize([]byte(text))
		if (gotErr == nil) != (wantErr == nil) || !bytes.Equal(got, want) {
			t.Fatalf("Canonicalize(%q) = %q, %v; rewrite gives %q, %v", text, got, gotErr, want, wantErr)
		}
	}
	t.Logf("%d accepted by the scan, %d sent to the rewrite", accepted, rejected)
	if accepted < 5000 || rejected < 50000 {
		t.Errorf("corpus is one-sided: %d accepted, %d rejected", accepted, rejected)
	}
}

// Encodings of ordinary Go values, which is what SafeJsonStringify feeds the scan.
func TestFastPathAcceptsEncodedGoValues(t *testing.T) {
	values := []any{
		map[string]any{"command": "ls -la", "timeout": 30, "env": map[string]any{"A": "1"}},
		map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "File path"}}, "required": []string{"path"}},
		[]any{1, 2.5, "x", true, nil, map[string]any{"k": []int{1, 2, 3}}},
		"plain text with é and 日本語 and 😀",
		0.1, 1234567, -42,
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !IsCanonical(encoded) {
			t.Errorf("scan refused ordinary encoding %s", encoded)
		}
	}
	for _, text := range []string{`-0`, `{"2":1,"1":2}`, `"\u2028"`, `1.50`, `{"a":1,"a":2}`, `[1, 2]`, `"\/"`, `1e5`, `123456789012345678`, `"` + "\xff" + `"`, `"` + "\x1f" + `"`} {
		if IsCanonical([]byte(text)) {
			t.Errorf("scan accepted %q, which the rewrite changes", text)
		}
	}
}

func FuzzFastPathNeverChangesCanonicalize(f *testing.F) {
	for _, text := range fastPathCorpus(rand.New(rand.NewSource(1)), 300) {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) { checkFastPath(t, text) })
}

// The result never aliases the input: callers reuse their buffer after the call (the rewrite always builds a new one).
func TestFastPathResultDoesNotAliasItsInput(t *testing.T) {
	for _, text := range []string{`{"b":1,"a":2}`, `[1,2,3]`, `"text"`, `{"nested":{"k":[true,null]}}`} {
		input := []byte(text)
		got, err := Canonicalize(input)
		if err != nil {
			t.Fatal(err)
		}
		for i := range input {
			input[i] = 'X'
		}
		if string(got) != text {
			t.Errorf("Canonicalize(%q) changed with its input: %q", text, got)
		}
	}
}

// BenchmarkCanonicalize measures the scan against the full rewrite on text the rewrite leaves unchanged: tool declarations and a transcript of messages, as the faux provider's usage estimate stringifies them every turn.
func BenchmarkCanonicalize(b *testing.B) {
	var messages []any
	for i := range 200 {
		messages = append(messages, map[string]any{
			"role": "assistant", "timestamp": 1700000000000 + i,
			"content": []any{
				map[string]any{"type": "text", "text": strings.Repeat("The quick brown fox jumps over the lazy dog. ", 4)},
				map[string]any{"type": "toolCall", "id": fmt.Sprint("call_", i), "name": "bash", "arguments": map[string]any{"command": "ls -la /tmp", "timeout": 30}},
			},
			"usage": map[string]any{"input": 120, "output": 40, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 160, "cost": map[string]any{"input": 0.0003, "output": 0.0006, "cacheRead": 0, "cacheWrite": 0, "total": 0.0009}},
		})
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		b.Fatal(err)
	}
	if !IsCanonical(encoded) {
		b.Fatal("benchmark text is not on the fast path")
	}
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(encoded)))
		for range b.N {
			if _, err := Canonicalize(encoded); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("rewrite", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(encoded)))
		for range b.N {
			if _, err := canonicalize(encoded); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Every string the scanner accepts is one the full rewrite leaves unchanged, and the escapes JSON.stringify writes are accepted.
func TestIsCanonicalEscapesAgreeWithRewrite(t *testing.T) {
	accepted := []string{`"a\u001fb"`, `"\u0000"`, `"\ud800"`, `"\udc00x"`, `"\udc00\ud800"`, `"\ud800x"`, `"\b\f\n\r\t"`, `"\u001b[0m"`}
	for _, text := range accepted {
		if !IsCanonical([]byte(text)) {
			t.Errorf("%s: scan refused a canonical string", text)
		}
	}
	candidates := []string{`"\u0008"`, `"\u000a"`, `"ÿ"`, `"\u001F"`, `"\uD800"`, `"𐀀"`, `"😀"`, `"\/"`, `"\u12"`, `"\u12g4"`, `"\ud800\n\udc00"`, `"A"`}
	for _, text := range append(candidates, accepted...) {
		got, err := Canonicalize([]byte(text))
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if IsCanonical([]byte(text)) && string(got) != text {
			t.Errorf("%s: scan accepted text the rewrite changes to %s", text, got)
		}
	}
}

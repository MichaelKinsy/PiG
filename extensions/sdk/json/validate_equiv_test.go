package json

import (
	"errors"
	"strings"
	"testing"
)

// validateCases are documents on the edges of the grammar: every one is judged by checkValid, and validate must answer with the same
// verdict and, for a rejected one, the same *SyntaxError.
var validateCases = []string{
	``, ` `, `null`, `true`, `false`, `nul`, `nulll`, `tru`, `truee`, `fals`, `falsee`, `n`, `t`, `f`,
	`0`, `-0`, `-`, `01`, `1.`, `1.e3`, `.5`, `1e`, `1e+`, `1E5`, `1e-5`, `1.5e+10`, `-1.5E-10`, `+1`, `0x10`, `1_000`, `-a`, `--1`, `0e0`, `0.0`,
	`""`, `"a"`, `"`, `"a`, `"\"`, `"\\"`, `"\/"`, `"\b\f\n\r\t"`, `"\a"`, `"\u00e9"`, `"\u00E9"`, `"\u00g9"`, `"\u00"`, `"\u"`, `"\`, "\"\x00\"", "\"\x1f\"", "\"\x20\"", "\"\x7f\"", "\"\xff\"", "\"\xc3\x28\"", "\"\n\"", "\"\t\"",
	`"\ud800"`, `"\udc00\ud800"`, `"é日本語😀"`, "\"\xed\xa0\x80\"",
	`[]`, `[ ]`, `[1]`, `[1,]`, `[,1]`, `[1,,2]`, `[1 2]`, `[[]]`, `[[],[]]`, `[1,2`, `[1,2,`, `[`, `]`, `[}`, `[1}`,
	`{}`, `{ }`, `{"a":1}`, `{"a":1,}`, `{,"a":1}`, `{"a"}`, `{"a":}`, `{"a" 1}`, `{a:1}`, `{"a":1 "b":2}`, `{"a":1,"b":2}`, `{"a":{"b":[1,{"c":null}]}}`, `{"a":1]`, `{`, `}`, `{"a"`, `{"a":`, `{"a":1`, `{"a":1,`, `{1:2}`, `{"a":1,2}`, `{"a":1,"b"}`,
	` {"a":1} `, "\t\r\n{\"a\":1}\r\n", `{"a":1} x`, `{"a":1}{`, `1 2`, `[] []`, `null null`, "\v1", "\f1", "\x00", " \x00", "[\x00]", `{"a":1}` + "\x00",
	"{\"a\":\"b\"\n}", `{"a":"b"` + "\x01" + `}`, `{"x":-}`, `{"x":--1}`, `[1e]`, `[1.]`, `[-]`, `[tru]`, `[nul]`, `["a" "b"]`, `["a",]`, `{"a":[1,2,{"b":"c"},[],{}]}`,
}

func sameVerdict(t *testing.T, doc string, limit int) {
	t.Helper()
	var want, got scanner
	want.maxDepth, got.maxDepth = limit, limit
	wantErr := checkValid([]byte(doc), &want)
	gotErr := validate([]byte(doc), &got)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("limit %d, %q: checkValid err %v, validate err %v", limit, doc, wantErr, gotErr)
	}
	if wantErr == nil {
		return
	}
	var wantSyntax, gotSyntax *SyntaxError
	if !errors.As(wantErr, &wantSyntax) || !errors.As(gotErr, &gotSyntax) {
		t.Fatalf("limit %d, %q: errors %T %T are not *SyntaxError", limit, doc, wantErr, gotErr)
	}
	if wantSyntax.Error() != gotSyntax.Error() || wantSyntax.Offset != gotSyntax.Offset {
		t.Fatalf("limit %d, %q: checkValid %q at %d, validate %q at %d", limit, doc, wantSyntax.Error(), wantSyntax.Offset, gotSyntax.Error(), gotSyntax.Offset)
	}
}

func TestValidateMatchesCheckValidOnGrammarEdges(t *testing.T) {
	for _, doc := range validateCases {
		for _, limit := range []int{0, 1, 2, 3} {
			sameVerdict(t, doc, limit)
		}
	}
}

// Every prefix and every single-byte mutation of valid documents: truncated frames and corrupted bytes give the error checkValid gives.
func TestValidateMatchesCheckValidOnTruncationsAndMutations(t *testing.T) {
	seeds := []string{
		`{"type":"request","id":"r","request":{"method":"event","args":{"a":[1,2.5e3,-0,true,false,null,"x\n\u00e9\"y"],"b":{}}}}`,
		`[{"a":"b"},[],{},"\\","",0,-1.5E+2]`,
	}
	bytesToTry := []byte{'"', '\\', '{', '}', '[', ']', ',', ':', 0, 0x1f, ' ', 'x', '1', '-', '.', 'e', 'u', 0xff, 't'}
	for _, seed := range seeds {
		for n := 0; n <= len(seed); n++ {
			sameVerdict(t, seed[:n], 0)
		}
		for i := range len(seed) {
			for _, b := range bytesToTry {
				mutated := seed[:i] + string([]byte{b}) + seed[i+1:]
				sameVerdict(t, mutated, 0)
				sameVerdict(t, seed[:i]+string([]byte{b})+seed[i:], 0)
			}
			sameVerdict(t, seed[:i]+seed[i+1:], 0)
		}
	}
}

// The nesting limit counts a container as it opens, empty or not, as the scanner does, and a document past it gets the scanner's error.
func TestValidateNestingLimits(t *testing.T) {
	for _, limit := range []int{0, frameNestingDepth} {
		max := limit
		if max == 0 {
			max = maxNestingDepth
		}
		for _, depth := range []int{max - 1, max, max + 1, max + 2} {
			arrays := strings.Repeat("[", depth) + strings.Repeat("]", depth)
			objects := strings.Repeat(`{"a":`, depth) + "null" + strings.Repeat("}", depth)
			emptyAtLimit := strings.Repeat("[", depth-1) + "{}" + strings.Repeat("]", depth-1)
			unclosed := strings.Repeat("[", depth)
			for _, doc := range []string{arrays, objects, emptyAtLimit, unclosed} {
				sameVerdict(t, doc, limit)
			}
		}
	}
}

func FuzzValidateMatchesCheckValid(f *testing.F) {
	for _, doc := range validateCases {
		f.Add([]byte(doc))
	}
	f.Add([]byte(`{"type":"request","request":{"args":{"messages":[{"role":"user","content":[{"type":"text","text":"a\nb\"c é"}]}]}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		sameVerdict(t, string(data), 0)
		sameVerdict(t, string(data), 2)
	})
}

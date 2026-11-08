// Package jsonseed holds the documents the differential fuzz targets of the ordered-object decoders start from: the shapes on which the
// encoding/json v1 walkers and their jsontext replacements could disagree.
package jsonseed

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Documents returns JSON texts, valid and not, covering duplicate members, nesting, escapes, invalid UTF-8, whitespace, truncation and
// trailing data.
func Documents() [][]byte {
	valid := []string{
		`{}`, `{"a":1}`, `{"a":1,"b":2}`, `{"a":1,"a":2}`, `{"a":1,"b":2,"a":3}`, `{"b":1,"a":{"x":1,"x":2},"a":[1,2]}`,
		`{"a":{"b":{"c":[1,{"d":null}]}}}`, `{ "a" : 1 , "b" : [ 1 , 2 ] }`, "{\n\t\"a\": 1,\n\t\"b\": \"x\"\n}\n",
		`{"headers":{"X-A":"1","X-B":null,"X-A":"2"},"other":[1]}`, `{"headers":{}}`, `{"headers":[]}`, `{"headers":{"a":1}}`,
		`{"colors":{"a":"#fff","b":"x","a":"y"},"name":"colors"}`, `{"colors":{"a":[],"b":"x"}}`, `{"colors":{"a":{"x":1},"b":"x"}}`,
		`{"vars":{"colors":1},"colors":{"k":"v"}}`, `{"x":"colors","colors":{"q":1}}`,
		`{"":1,"":2}`, `{"\u0061":1,"a":2}`, `{"\ud800":1,"\udc00":2}`, `{"é":"日本語","😀":"\ud83d\ude00"}`, `{"a":"\u0000\u001f\n\t\"\\\/"}`,
		`{"1":1,"0":2,"b":3,"01":4,"4294967295":5}`, `{"a":1e999,"b":-0,"c":1E+2,"d":0.5,"e":123456789012345678901234567890}`,
		`{"a":true,"b":false,"c":null}`, `{"a":"` + strings.Repeat("x", 300) + `"}`, `{"properties":{"b":{},"a":{},"1":{}},"required":["b","a"]}`,
		`[]`, `[1]`, `[{"a":1}]`, `null`, `1`, `"s"`, `true`, ` {"a":1} `, "{\"a\":1}\n",
	}
	invalid := []string{
		``, ` `, `{`, `{"a"`, `{"a":`, `{"a":1`, `{"a":1,`, `{"a":1,}`, `{"a":1}x`, `{"a":1} {"b":2}`, `{"a":1}}`, `{"a":1]`, `{a:1}`, `{'a':1}`, `{"a" 1}`,
		`{"a":1 "b":2}`, `{1:2}`, `{"a":}`, `{"a":01}`, `{"a":1.}`, `{"a":.5}`, `{"a":+1}`, `{"a":NaN}`, `{"a":tru}`, `{"a":"\x"}`, `{"a":"\u12"}`,
		"{\"a\":\"\t\"}", `{"a":"`, `["a":1]`, `[1,]`, `[,1]`, `{"a":[1,2}`, `{"a":{"b":1]}`, `{"a":1}{`, `{"a":1}[`, `{"a":1}}}`, `{,}`, `{"a":1,,"b":2}`,
		"\ufeff{\"a\":1}", "{\"a\":1}\x00", `{"a":1}//c`, `{"a":1/*c*/}`,
	}
	raw := []string{
		"{\"a\":\"\xff\"}", "{\"a\xff\":1}", "{\"a\xe2\x82\":1,\"a\":\"\xf0\x9f\x98\"}", "{\"\xc0\xaf\":\"\xed\xa0\x80\"}", "\xff", "{\xff}",
		"{\"colors\":{\"\xff\":1}}",
	}
	var out [][]byte
	for _, set := range [][]string{valid, invalid, raw} {
		for _, text := range set {
			out = append(out, []byte(text))
		}
	}
	// Every prefix of a few valid documents: truncation at each byte.
	for _, text := range []string{`{"a":{"b":[1,"x\u00e9"]},"a":2,"c":null}`, `{"colors":{"a":"x","b":[1]},"n":1}`} {
		for i := 1; i < len(text); i++ {
			out = append(out, []byte(text[:i]))
		}
	}
	return out
}

// Add registers Documents as the seed corpus of a fuzz target.
func Add(f *testing.F) {
	for _, doc := range Documents() {
		f.Add(doc)
	}
}

// Same fails the test unless the jsontext reader and its encoding/json v1 original agree on data: both succeed with DeepEqual results,
// or both fail (their error texts differ, so only the fact of the error is compared).
func Same(t *testing.T, data []byte, name string, got, want any, gotErr, wantErr error) {
	t.Helper()
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("%s(%q): error %v, v1 error %v", name, data, gotErr, wantErr)
	}
	if gotErr == nil && !equal(got, want) {
		t.Fatalf("%s(%q): %#v, v1 %#v", name, data, got, want)
	}
}

// equal is reflect.DeepEqual, except that results which print the same are equal too: NaN is not equal to itself.
func equal(got, want any) bool {
	return reflect.DeepEqual(got, want) || fmt.Sprintf("%#v", got) == fmt.Sprintf("%#v", want)
}

// IsObject reports whether data, past leading JSON whitespace, starts an object. The readers that do not check their first token (they
// are only called with objects) answered an array with garbage or a panic, and answer it with an error now.
func IsObject(data []byte) bool {
	data = bytes.TrimLeft(data, " \t\r\n")
	return len(data) > 0 && data[0] == '{'
}

// SameResult is Same for a reader without an error result: the results must agree on every input.
func SameResult(t *testing.T, data []byte, name string, got, want any) {
	t.Helper()
	if !equal(got, want) {
		t.Fatalf("%s(%q): %#v, v1 %#v", name, data, got, want)
	}
}

// Typical is a mid-size object with the members the readers look for (headers, colors, providers, properties) next to a tool call's
// arguments: a few scalars, two long strings and a nested array.
func Typical() []byte {
	long := strings.Repeat("for i := range parts {\\n\\tparts[i] = total / n\\n}\\n", 40)
	return []byte(`{"path":"internal/budget/split.go","oldText":"` + long + `","newText":"` + long + `","edits":[{"line":11,"op":"replace"},{"line":14,"op":"insert"}],"dryRun":false,` +
		`"headers":{"Authorization":"Bearer x","X-Org":"o","X-Trace":"t"},"colors":{"accent":"#8abeb7","border":"#5f87ff","muted":"#808080","text":"","dim":"#666666"},` +
		`"providers":{"anthropic":{"baseUrl":"https://a"},"openai":{"baseUrl":"https://o"}},"properties":{"b":{"type":"string"},"a":{"type":"number"}},"name":"x"}`)
}

// Bench runs old (the encoding/json v1 reader) and new (its jsontext replacement) on doc as the sub-benchmarks "v1" and "jsontext".
func Bench(b *testing.B, doc []byte, old, new func([]byte)) {
	for _, c := range []struct {
		name string
		read func([]byte)
	}{{"v1", old}, {"jsontext", new}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(doc)))
			for b.Loop() {
				c.read(doc)
			}
		})
	}
}

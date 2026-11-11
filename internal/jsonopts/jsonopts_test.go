package jsonopts

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// nodeResult is what the Node that runs the pinned Pi gives for one document: Buffer.toString("utf8") of the bytes (Pi reads files and
// streams that way), JSON.parse, then JSON.stringify with member names sorted so the Go side, which sorts map keys, compares equal.
type nodeResult struct {
	OK  bool   `json:"ok"`
	Out string `json:"out"`
	// Unprintable is set when JSON.parse succeeded but JSON.stringify overflowed Node's stack, which happens for the deepest documents.
	Unprintable bool `json:"unprintable"`
}

// nodeRoundTrip runs docs through JSON.parse and JSON.stringify in Node.
func nodeRoundTrip(t *testing.T, docs [][]byte) []nodeResult {
	t.Helper()
	encoded := make([]string, len(docs))
	for i, doc := range docs {
		encoded[i] = base64.StdEncoding.EncodeToString(doc)
	}
	var results []nodeResult
	pioracle.Run(t, `
const sorted = (_key, value) => {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return value;
  const copy = {};
  for (const key of Object.keys(value).sort()) Object.defineProperty(copy, key, { value: value[key], enumerable: true, writable: true, configurable: true });
  return copy;
};
emit(input.map((b64) => {
  const text = Buffer.from(b64, "base64").toString("utf8");
  let value;
  try { value = JSON.parse(text); } catch { return { ok: false, out: "", unprintable: false }; }
  try { return { ok: true, out: JSON.stringify(value, sorted), unprintable: false }; } catch { return { ok: true, out: "", unprintable: true }; }
}));`, encoded, &results)
	if len(results) != len(docs) {
		t.Fatalf("node returned %d results for %d documents", len(results), len(docs))
	}
	return results
}

// goRoundTrip is the same round trip with the shared options.
func goRoundTrip(doc []byte) (string, error) {
	var value any
	if err := jsonv2.Unmarshal(doc, &value, Parse); err != nil {
		return "", err
	}
	out, err := jsonv2.Marshal(value, Stringify, jsonv2.Deterministic(true))
	return string(out), err
}

// TestRoundTripMatchesNode binds the option set to JSON.parse and JSON.stringify. A case with divergence is a difference encoding/json
// cannot express (see Stringify): the test fixes what Go gives instead and fails if Go starts to agree with Node, so the record stays true.
func TestRoundTripMatchesNode(t *testing.T) {
	nest := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	type tc struct {
		name, in string
		// diverge is what Go gives where Node gives something else; goErr says Go reports an error where Node parses.
		diverge string
		goErr   bool
	}
	cases := []tc{
		// Duplicate names: the last value wins, at any depth.
		{name: "duplicate", in: `{"a":1,"a":2}`},
		{name: "duplicate nested", in: `{"a":{"x":1,"x":2,"x":3},"b":[{"k":1,"k":null}]}`},
		{name: "duplicate object replaces object", in: `{"a":{"x":1},"a":{"y":2}}`},
		{name: "duplicate nested object replaces object", in: `{"a":{"b":{"x":1,"z":3}},"a":{"b":{"y":2}}}`},
		{name: "duplicate different type", in: `{"a":{"x":1},"a":[1],"a":"s"}`},
		{name: "duplicate scalar then object", in: `{"a":1,"a":{"b":2}}`},
		{name: "duplicate array replaces array", in: `{"a":[1,2,3],"a":[4]}`},
		{name: "duplicate then null", in: `{"a":{"x":1},"a":null,"b":[1],"b":null}`},
		{name: "duplicate escaped name", in: `{"a":1,"\u0061":2}`},
		{name: "names differing in case are distinct", in: `{"A":1,"a":2,"Ab":3,"aB":4}`},
		{name: "empty name", in: `{"":1,"":2}`},
		{name: "__proto__ name", in: `{"__proto__":{"x":1},"y":2}`},
		// Lone UTF-16 surrogates: a Go string cannot hold them, so they become U+FFFD.
		{name: "lone high surrogate", in: `"\ud800"`, diverge: "\"\ufffd\""},
		{name: "lone low surrogate", in: `"\udc00"`, diverge: "\"\ufffd\""},
		{name: "high surrogate then letter", in: `"\ud800x"`, diverge: "\"\ufffdx\""},
		{name: "reversed pair", in: `"\ude00\ud83d"`, diverge: "\"\ufffd\ufffd\""},
		{name: "valid pair", in: `"\ud83d\ude00"`},
		{name: "surrogate in name", in: `{"\ud800":1}`, diverge: "{\"\ufffd\":1}"},
		// Invalid UTF-8 in the bytes: Node replaces each maximal subpart with one U+FFFD.
		{name: "invalid byte", in: "\"a\xffb\""},
		{name: "invalid byte in name", in: "{\"a\xff\":1}"},
		// Go replaces every invalid byte, so a truncated sequence gives one U+FFFD per byte read where Node gives one.
		{name: "truncated 3-byte sequence", in: "\"a\xe2\x82b\"", diverge: "\"a\ufffd\ufffdb\""},
		{name: "truncated 4-byte sequence", in: "\"\xf0\x9f\x98\"", diverge: "\"\ufffd\ufffd\ufffd\""},
		{name: "overlong slash", in: "\"\xc0\xaf\""},
		{name: "encoded surrogate", in: "\"\xed\xa0\x80\""},
		{name: "continuation byte alone", in: "\"\x80\x80\""},
		{name: "invalid byte outside a string", in: "\xff"},
		// Numbers.
		{name: "negative zero", in: `-0`, diverge: `-0`},
		{name: "negative zero in array", in: `[-0,-0.0,-0e0,0]`, diverge: `[-0,-0,-0,0]`},
		{name: "1e21", in: `1e21`},
		{name: "1E+21", in: `1E+21`},
		{name: "1e20", in: `100000000000000000000`},
		{name: "1e-6", in: `0.000001`},
		{name: "1e-7", in: `1e-7`},
		{name: "integer beyond 2^53", in: `9007199254740993`},
		{name: "30 digits", in: `123456789012345678901234567890`},
		{name: "smallest and largest double", in: `[5e-324,1.7976931348623157e308,2.2250738585072014e-308]`},
		{name: "trailing zeros", in: `[1.0,1.10,100,1e2,0e5,0.1,0.30000000000000004]`},
		{name: "exponent form", in: `[1e0,1E1,1e+2,1e-2,12e-1]`},
		{name: "out of range", in: `1e400`, goErr: true},
		{name: "negative out of range", in: `-1e400`, goErr: true},
		// Escapes and output escaping.
		{name: "u escapes", in: `"\u0041\u00e9\u20ac\ud83d\ude00"`},
		{name: "control escapes", in: `"\u0000\u0001\u001f\u007f\b\f\n\r\t"`},
		{name: "line separators", in: "\"\\u2028\\u2029\u2028\u2029\""},
		{name: "html characters", in: `"<script>&amp;</script>\u003c\u003e\u0026"`},
		{name: "solidus and quote", in: `"\/\"\\"`},
		{name: "uppercase hex", in: `"\u00E9\uD83D\uDE00"`},
		{name: "plain unicode", in: "\"é日本語😀\""},
		// Depth.
		{name: "depth 100", in: nest(100)},
		{name: "depth 10000", in: nest(10000)},
		// Node parses any depth that fits in memory; the decoder stops at 10000 levels, as encoding/json v1 did.
		{name: "depth 10001", in: nest(10001), goErr: true},
		{name: "depth 50000", in: nest(50000), goErr: true},
		// Documents that must not parse.
		{name: "trailing comma in array", in: `[1,]`},
		{name: "trailing comma in object", in: `{"a":1,}`},
		{name: "trailing data", in: `{} x`},
		{name: "two values", in: `1 2`},
		{name: "empty", in: ``},
		{name: "whitespace only", in: " \n"},
		{name: "byte order mark", in: "\ufeff1"},
		{name: "no-break space", in: "\u00a01"},
		{name: "line separator as space", in: "\u20281"},
		{name: "single quotes", in: `'a'`},
		{name: "unquoted name", in: `{a:1}`},
		{name: "NaN", in: `NaN`},
		{name: "Infinity", in: `Infinity`},
		{name: "undefined", in: `undefined`},
		{name: "plus sign", in: `+1`},
		{name: "leading zero", in: `01`},
		{name: "bare dot", in: `.5`},
		{name: "trailing dot", in: `1.`},
		{name: "hex", in: `0x1`},
		{name: "comment", in: `1 // c`},
		{name: "raw tab in string", in: "\"a\tb\""},
		{name: "raw newline in string", in: "\"a\nb\""},
		{name: "bad escape", in: `"\x41"`},
		{name: "short unicode escape", in: `"\u12"`},
		{name: "unterminated string", in: `"abc`},
		{name: "unterminated object", in: `{"a":1`},
		{name: "missing colon", in: `{"a" 1}`},
		{name: "number name", in: `{1:1}`},
		{name: "capital literal", in: `True`},
		{name: "valid whitespace", in: " \t\r\n[ 1 , 2 ]\n"},
		{name: "null", in: `null`},
		{name: "literals", in: `[true,false,null]`},
	}
	docs := make([][]byte, len(cases))
	for i, c := range cases {
		docs[i] = []byte(c.in)
	}
	want := nodeRoundTrip(t, docs)
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			node := want[i]
			got, err := goRoundTrip(docs[i])
			switch {
			case !node.OK:
				if err == nil {
					t.Fatalf("node rejects %q, Go gives %q", c.in, got)
				}
			case node.Unprintable:
				if c.goErr == (err == nil) {
					t.Fatalf("node parses %.40q; Go error %v, want error %v", c.in, err, c.goErr)
				}
			case c.goErr:
				if err == nil {
					t.Fatalf("Go now accepts %.40q (node gives %.40q); drop goErr", c.in, node.Out)
				}
			case err != nil:
				t.Fatalf("node gives %.60q, Go fails: %v", node.Out, err)
			case c.diverge != "":
				if got != c.diverge {
					t.Fatalf("Go gives %q, want the recorded %q", got, c.diverge)
				}
				if got == node.Out {
					t.Fatalf("Go now agrees with node (%q); drop diverge", got)
				}
			default:
				if got != node.Out {
					t.Fatalf("Go gives %.80q, node gives %.80q", got, node.Out)
				}
			}
		})
	}
}

// TestStringifyNumbersMatchNode formats float64 values the way Number.prototype.toString does through JSON.stringify.
func TestStringifyNumbersMatchNode(t *testing.T) {
	values := []float64{0, 1, -1, 0.1, 0.5, 1e20, 1e21, 1.5e21, 1e-6, 1e-7, 1.2345e-7, 123456789012345680000, 1e300, 5e-324, math.MaxFloat64, math.MaxInt64, 1 << 53, 0.30000000000000004, 100, 4.35, 0.000123}
	docs := make([][]byte, len(values))
	for i, v := range values {
		docs[i] = []byte(strconv.FormatFloat(v, 'g', -1, 64))
	}
	want := nodeRoundTrip(t, docs)
	for i, v := range values {
		got, err := jsonv2.Marshal(v, Stringify)
		if err != nil || string(got) != want[i].Out {
			t.Errorf("%v: Go %q %v, node %q", v, got, err, want[i].Out)
		}
	}
}

// TestStringifyStringsMatchNode writes strings the way JSON.stringify does: no escaping of <, >, & or U+2028 and U+2029.
func TestStringifyStringsMatchNode(t *testing.T) {
	strs := []string{"<>&", "\u2028\u2029", "\u007f", "\x00\x1f", "\"\\", "\b\f\n\r\t", "é日本語😀", "\ufffd", "\ufeff", "a/b", "'", "\u00a0\u200b"}
	docs := make([][]byte, len(strs))
	for i, s := range strs {
		quoted, err := jsonv2.Marshal(s, Stringify)
		if err != nil {
			t.Fatal(err)
		}
		docs[i] = quoted
	}
	want := nodeRoundTrip(t, docs)
	for i, s := range strs {
		got, err := jsonv2.Marshal(s, Stringify)
		if err != nil || string(got) != want[i].Out {
			t.Errorf("%q: Go %q %v, node %q", s, got, err, want[i].Out)
		}
	}
}

// TestCaseSensitiveMembers: JSON.parse then a property read finds only the exact name; encoding/json v1 matched "NAME" to a field
// tagged "name". A typed decode with Parse behaves like the property read.
func TestCaseSensitiveMembers(t *testing.T) {
	type typed struct {
		Name *string  `json:"name,omitempty"`
		N    *float64 `json:"n,omitempty"`
	}
	docs := []string{`{"name":"a","n":1}`, `{"NAME":"y","N":3}`, `{"Name":"x","name":"z"}`, `{"name":"a","name":"b","n":1,"n":2}`, `{"nAmE":"q"}`, `{"name":"a","extra":1}`}
	var want []string
	pioracle.Run(t, `emit(input.map((text) => { const o = JSON.parse(text); return JSON.stringify({ name: o.name, n: o.n }); }));`, docs, &want)
	for i, d := range docs {
		var v typed
		if err := jsonv2.Unmarshal([]byte(d), &v, Parse); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		got, err := jsonv2.Marshal(v, Stringify)
		if err != nil || string(got) != want[i] {
			t.Errorf("%s: Go %q %v, node %q", d, got, err, want[i])
		}
	}
}

// TestDecoderReadsWhatParseReads: the token decoder the ordered-object readers use takes the same documents, in order, with duplicates,
// and rejects data after the value.
func TestDecoderReadsWhatParseReads(t *testing.T) {
	d := jsontext.NewDecoder(strings.NewReader("{\"b\":1,\"a\":2,\"b\":\"\xff\"}"), Parse)
	var names []string
	var last string
	if tok, err := d.ReadToken(); err != nil || tok.Kind() != '{' {
		t.Fatal(tok, err)
	}
	for d.PeekKind() != '}' {
		name, err := d.ReadToken()
		if err != nil {
			t.Fatal(err)
		}
		key := name.String() // a Token is void after the next call on the decoder
		value, err := d.ReadValue()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, key)
		last = string(bytes.Clone(value))
	}
	if strings.Join(names, ",") != "b,a,b" || last != "\"\xff\"" {
		t.Fatalf("names %v last %q", names, last)
	}
	if _, err := jsontext.NewDecoder(strings.NewReader(`{"a":1,"a":2}`)).ReadValue(); err == nil {
		t.Fatal("the v2 default must reject a duplicate name, or Parse changes nothing")
	}
}

// TestStructDuplicatesKeepV1Merge records where a typed decode differs from JSON.parse for a repeated member: an object-valued
// duplicate merges into the field and a later null leaves a scalar field alone. encoding/json v1 did both, so a site that moves to
// Parse keeps its result; a site that needs the JSON.parse value reads the members itself.
func TestStructDuplicatesKeepV1Merge(t *testing.T) {
	type inner struct {
		X *int `json:"x,omitempty"`
		Y *int `json:"y,omitempty"`
	}
	type typed struct {
		A inner `json:"a"`
		N *int  `json:"n,omitempty"`
		S int   `json:"s"`
	}
	docs := []string{`{"a":{"x":1},"a":{"y":2}}`, `{"s":1,"s":null}`, `{"s":1,"s":2}`}
	var want []string
	pioracle.Run(t, `emit(input.map((text) => { const o = JSON.parse(text); return JSON.stringify({ a: o.a, s: o.s }); }));`, docs, &want)
	wantGo := []string{`{"a":{"x":1,"y":2},"s":0}`, `{"a":{},"s":1}`, `{"a":{},"s":2}`}
	nodeJS := []string{`{"a":{"y":2}}`, `{"s":null}`, `{"s":2}`}
	for i, d := range docs {
		var v typed
		if err := jsonv2.Unmarshal([]byte(d), &v, Parse); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		got, err := jsonv2.Marshal(struct {
			A inner `json:"a"`
			S int   `json:"s"`
		}{v.A, v.S}, Stringify)
		if err != nil || string(got) != wantGo[i] {
			t.Errorf("%s: Go %q %v, recorded %q", d, got, err, wantGo[i])
		}
		if want[i] != nodeJS[i] {
			t.Errorf("%s: node gives %q, the record says %q", d, want[i], nodeJS[i])
		}
	}
}

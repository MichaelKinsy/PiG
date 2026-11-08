package jsonseed

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"math"
	"slices"
	"strconv"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Member is one own property of a JSON.parse result: the key as UTF-16 code units (a Go string cannot tell "\ud800" from U+FFFD) and a
// short description of the value.
type Member struct {
	Key   []uint16 `json:"key"`
	Value string   `json:"value"`
}

// NodeDocuments are objects on which an ordered-object reader and JSON.parse could disagree about which members exist: repeated names
// spelled differently, __proto__, lone and paired surrogates, raw ill-formed UTF-8 and the numbers JSON.parse reads as -0 and Infinity.
// Every value is a string, null or a number, so Value gives it the same way on both sides. No key is an array index, because the readers
// that sort those do it apart from the dedup.
func NodeDocuments() [][]byte {
	docs := []string{
		`{"a":1,"b":2,"a":3}`,
		`{"\u0061":1,"a":2}`,
		`{"\/":1,"/":2}`,
		`{"__proto__":1,"x":2}`,
		`{"__proto__":1,"__proto__":2}`,
		`{"x":-0,"y":1e999,"z":-1e999,"w":1E2}`,
		`{"\ud800":1,"\ud801":2}`,
		`{"\ud800":1,"\ud800":2}`,
		"{\"\\ud800\":1,\"\xed\xa0\x80\":2}",
		"{\"\xed\xa0\x80\":1,\"\xed\xa0\x81\":2}",
		`{"\ud800\udc00":1,"` + "\U00010000" + `":2}`,
		`{"\ud83d":1,"\ude00":2,"\ud83d\ude00":3}`,
		`{"\ude00\ud83d":1,"\ud83d\ude00":2}`,
		`{"\ud800":1,"\uFFFD":2,"` + "\uFFFD" + `":3}`,
		"{\"a\xff\":1,\"a\xfe\":2,\"a\\ufffd\":3}",
		"{\"\xe2\x82\":1,\"\xe2\":2,\"\xef\xbf\xbd\":3}",
		"{\"\xf0\x9f\x98\":1,\"\xef\xbf\xbd\":2}",
		"{\"\xf0\x80\x80\":1,\"\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\":2}",
		"{\"\xc0\xaf\":1,\"\xef\xbf\xbd\xef\xbf\xbd\":2}",
		`{"\ud800x":1,"\ud800y":2,"\ud800x":3}`,
		`{"":1,"":null,"s":"v","s":"w"}`,
	}
	out := make([][]byte, len(docs))
	for i, doc := range docs {
		out[i] = []byte(doc)
	}
	return out
}

// NodeMembers is the own properties JSON.parse gives for each document, in Object.keys order.
func NodeMembers(t testing.TB, docs [][]byte) [][]Member {
	t.Helper()
	encoded := make([]string, len(docs))
	for i, doc := range docs {
		encoded[i] = base64.StdEncoding.EncodeToString(doc)
	}
	var out [][]Member
	pioracle.Run(t, `
const describe = (v) => v === null ? "null" : typeof v === "number" ? (Object.is(v, -0) ? "-0" : String(v)) : "s:" + v;
emit(input.map((b64) => {
  const value = JSON.parse(Buffer.from(b64, "base64").toString("utf8"));
  return Object.keys(value).map((key) => ({ key: Array.from({ length: key.length }, (_, i) => key.charCodeAt(i)), value: describe(value[key]) }));
}));`, encoded, &out)
	if len(out) != len(docs) {
		t.Fatalf("node returned %d results for %d documents", len(out), len(docs))
	}
	return out
}

// Units is the UTF-16 code units of a Go string in the WTF-8 form internal/jsstring uses: a lone surrogate is its three bytes.
func Units(s string) []uint16 {
	var units []uint16
	for i := 0; i < len(s); {
		if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
			units = append(units, uint16(rune(s[i]&0x0F)<<12|rune(s[i+1]&0x3F)<<6|rune(s[i+2]&0x3F)))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		units = utf16.AppendRune(units, r)
		i += size
	}
	return units
}

// Describe is NodeMembers' Value for the raw JSON text of a string, null or number.
func Describe(raw []byte) string {
	switch {
	case string(raw) == "null":
		return "null"
	case len(raw) > 0 && raw[0] == '"':
		s, err := strconv.Unquote(`"` + string(raw[1:len(raw)-1]) + `"`)
		if err != nil {
			return "s:?"
		}
		return "s:" + s
	}
	f, _ := strconv.ParseFloat(string(raw), 64)
	switch {
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0 && math.Signbit(f):
		return "-0"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// SameMembers fails the test unless got is want, member by member.
func SameMembers(t testing.TB, doc []byte, got, want []Member) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b Member) bool { return a.Value == b.Value && slices.Equal(a.Key, b.Key) }) {
		t.Errorf("%q:\n  Go   %v\n  Node %v", doc, got, want)
	}
}

// ValidKey is the key as encoding/json holds it: a lone surrogate is U+FFFD.
func ValidKey(units []uint16) string { return string(utf16.Decode(units)) }

// SkipJSKeys skips the test when an object name in data is one that encoding/json v1 and Token.String() read differently from JSON.parse: an
// escaped surrogate or ill-formed UTF-8 in a name. The ordered-object readers key a repeated name on the JavaScript key (jsonopts.ReadName),
// so on such a document they differ from the v1 walkers they replaced on purpose; TestOrderedObjectReadersMatchNode and the jsonopts name
// table hold them to Node there. Every other document, including ill-formed UTF-8 in a value, still compares.
//
// Intention check (what the v1 comparison guards and whether PiG can test it for these documents): it guards that a converted reader
// returns what the walker it replaced did, so no document changes meaning by accident. For a name JSON.parse reads differently from v1,
// "what v1 did" is the wrong answer and the intention is "what JSON.parse does"; PiG can observe that directly, so the Node-backed
// tables (NodeDocuments, NodeMembers) port it at the jsonopts, ai and orderedjson readers instead of leaving those documents unchecked.
func SkipJSKeys(t *testing.T, data []byte) {
	t.Helper()
	d := jsontext.NewDecoder(bytes.NewReader(data), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	for {
		if k, n := d.StackIndex(d.StackDepth()); k == '{' && n%2 == 0 && d.PeekKind() == '"' {
			raw, err := d.ReadValue()
			if err != nil {
				return
			}
			if !utf8.Valid(raw) || jsstring.HasSurrogateEscape(raw) {
				t.Skip("a name with a surrogate escape or ill-formed UTF-8: the reader keys on the JavaScript key, as JSON.parse does, and v1 did not")
			}
			continue
		}
		if _, err := d.ReadToken(); err != nil {
			return
		}
	}
}

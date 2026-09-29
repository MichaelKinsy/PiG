package json_test

import (
	"bytes"
	stdjson "encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi JSON.stringify/parse preserves UTF-16 units, including a lone half from
// packages/tui/src/stdin-buffer.ts:251. Literal backslashes are not escapes.
func TestJavaScriptStringRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, text, wire string }{
		{"empty", "", `""`}, {"ascii", "a\n\"\\", `"a\n\"\\"`},
		{"high", "\xed\xa0\xbd", `"\ud83d"`}, {"low", "\xed\xb8\x80", `"\ude00"`},
		{"pair", "😀", `"😀"`}, {"literal", `\ud83d`, `"\\ud83d"`},
		{"mixed", "A\xed\xa0\xbdZ😀\xed\xb8\x80", `"A\ud83dZ😀\ude00"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.wire || !utf8.Valid(encoded) || !stdjson.Valid(encoded) {
				t.Fatalf("encoded %q, want %q", encoded, tc.wire)
			}
			var got string
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if got != tc.text {
				t.Fatalf("decoded %x, want %x", got, tc.text)
			}
			var stream strings.Builder
			if err := json.NewEncoder(&stream).Encode(tc.text); err != nil {
				t.Fatal(err)
			}
			if err := json.NewDecoder(strings.NewReader(stream.String())).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.text {
				t.Fatalf("stream decoded %x, want %x", got, tc.text)
			}
		})
	}
}

func TestJavaScriptStringsInEveryJSONPosition(t *testing.T) {
	type payload struct {
		Data       *string            `json:"data,omitempty"`
		EditorText string             `json:"editorText"`
		Lines      []string           `json:"lines"`
		Values     map[string]any     `json:"values"`
		Raw        stdjson.RawMessage `json:"raw"`
	}
	high, low := "\xed\xa0\xbd", "\xed\xb8\x80"
	want := payload{&high, low, []string{high, low, "😀"}, map[string]any{high: low}, stdjson.RawMessage(`"\ud83d"`)}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got payload
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %#v != %#v", got, want)
	}
	var pair string
	if err := json.Unmarshal([]byte(`"\ud83d\ude00"`), &pair); err != nil || pair != "😀" {
		t.Fatalf("pair: %q %v", pair, err)
	}
	for _, wire := range []string{`{"editorText":"\ud83d"`, `"\ud83"`, `"\uxxxx"`, `"\ud83d\x"`} {
		if json.Valid([]byte(wire)) || json.Unmarshal([]byte(wire), &got) == nil {
			t.Fatalf("accepted malformed JSON %s", wire)
		}
	}
	var absent payload
	if err := json.Unmarshal([]byte(`{"data":null}`), &absent); err != nil || absent.Data != nil {
		t.Fatalf("null: %#v %v", absent, err)
	}
}

func BenchmarkTerminalInputJSON(b *testing.B) {
	for _, size := range []int{0, 1024, 65536} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			value := struct{ Data, EditorText string }{"\xed\xa0\xbd", strings.Repeat("a", size) + "\xed\xb8\x80"}
			b.ReportAllocs()
			for b.Loop() {
				encoded, err := json.Marshal(value)
				if err != nil {
					b.Fatal(err)
				}
				if err := json.NewDecoder(bytes.NewReader(encoded)).Decode(&value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

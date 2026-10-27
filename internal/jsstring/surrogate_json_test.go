package jsstring

import (
	"bytes"
	stdjson "encoding/json"
	"math"
	"testing"
)

const loneSurrogate = "\xed\xa0\xbd" // the UTF-16 unit U+D83D, in WTF-8

func TestMarshalKeepingSurrogatesIsEncodingJSONExceptForALoneSurrogate(t *testing.T) {
	// No lone surrogate: the bytes are encoding/json's, whether or not the text holds a literal U+FFFD.
	for _, value := range []any{"plain <text> & more", "a\ufffdb", map[string]any{"k": []any{"é日本😀", float64(1.5), nil, true}}, struct {
		A string `json:"a"`
		B int    `json:"b,omitempty"`
	}{"x", 0}} {
		want, err := stdjson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := MarshalKeepingSurrogates(value)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("Marshal(%v) = %s, %v; encoding/json %s", value, got, err, want)
		}
	}
	// A lone surrogate is the escape JSON.stringify writes, in a string, a map key's value and a nested list.
	for value, want := range map[string]string{
		"a" + loneSurrogate + "<": `"a\ud83d\u003c"`,
	} {
		got, err := MarshalKeepingSurrogates(value)
		if err != nil || string(got) != want {
			t.Errorf("Marshal(%q) = %s, %v; want %s", value, got, err, want)
		}
	}
	got, err := MarshalKeepingSurrogates(map[string]any{"k": []any{loneSurrogate}})
	if err != nil || string(got) != `{"k":["\ud83d"]}` {
		t.Errorf("nested = %s, %v", got, err)
	}
	// A value that is not JSON stays the error encoding/json gives.
	if _, err := MarshalKeepingSurrogates(math.NaN()); err == nil {
		t.Error("NaN must stay an error")
	}
}

func TestUnmarshalKeepingSurrogatesReadsALoneSurrogateEscapeAsItsUnit(t *testing.T) {
	var got any
	if err := UnmarshalKeepingSurrogates([]byte(`{"a":"x\ud83d<","b":["\ud83d","é"]}`), &got); err != nil {
		t.Fatal(err)
	}
	object := got.(map[string]any)
	if object["a"] != "x"+loneSurrogate+"<" || object["b"].([]any)[0] != loneSurrogate || object["b"].([]any)[1] != "é" {
		t.Errorf("decoded %q", object)
	}
	var plain, want any
	text := []byte(`{"a":"x\u003c\n","b":[1,2.5,null]}`)
	if err := UnmarshalKeepingSurrogates(text, &plain); err != nil {
		t.Fatal(err)
	}
	if err := stdjson.Unmarshal(text, &want); err != nil {
		t.Fatal(err)
	}
	if !equalJSONValues(plain, want) {
		t.Errorf("without a surrogate escape %v, encoding/json %v", plain, want)
	}
}

func equalJSONValues(a, b any) bool {
	x, _ := stdjson.Marshal(a)
	y, _ := stdjson.Marshal(b)
	return bytes.Equal(x, y)
}

func TestHasSurrogateEscape(t *testing.T) {
	for text, want := range map[string]bool{
		``: false, `"plain"`: false, `"\u0041"`: false, `"\ud7ff"`: false, `"\ue000"`: false, `"\n\\"`: false, `"\u00d8"`: false,
		`"\ud800"`: true, `"\uD83D"`: true, `"\udfff"`: true, `"\udc00"`: true, `"a\ud83dz"`: true, `"\\ud83d"`: true, `"\ud83`: true, `\ud8`: true,
	} {
		if got := HasSurrogateEscape([]byte(text)); got != want {
			t.Errorf("HasSurrogateEscape(%s) = %v, want %v", text, got, want)
		}
	}
}

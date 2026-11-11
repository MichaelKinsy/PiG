package jsonwire

import (
	"strings"
	"testing"
)

func TestHasLoneSurrogateEscape(t *testing.T) {
	for text, want := range map[string]bool{
		``: false, `"abc"`: false, `"\u0041"`: false, `"\ud83d\ude00"`: false, `"\uD83D\uDE00x\uD83D\uDE00"`: false,
		`"\ud800"`: true, `"\udc00"`: true, `"\ud83d"`: true, `"\ud83dx"`: true, `"\ud83d\u0041"`: true, `"\ude00\ud83d"`: true,
		`"\ud83d\ud83d\ude00"`: true, `"\ud83d\ude00\ud800"`: true, `"\u00e9\uD7FF\ue000"`: false,
		// An escaped backslash starts nothing: the "ud800" after it is text, not an escape.
		`"\\ud800"`: false, `"\\\\ud800"`: false, `"\\ud83d\ude00"`: true, `"\\ud800\udc00"`: true, `"\\\\ud800\udc00"`: true,
		`"a\\ud800\udfff"`: true, `"\\\ud800"`: true, `"\\\ud83d\ude00"`: false, `"\\u0041"`: false, `"\n\ud800"`: true,
		`"\uDC00"`: true, `"\uD800x"`: true, `"\ud83dxudc00"`: true,
		// The three-byte form of a surrogate, which is not UTF-8, is a lone surrogate too.
		"\"\xed\xa0\x80\"": true, "\"\xed\xbf\xbf\"": true, "\"a\xed\xaf\xbfb\"": true, "\"\xed\x9f\xbf\"": false, "\"\xee\x80\x80\"": false,
		"\"\xed\"": false, "\xed": false, "\"\xc3\xa9\xed\x80\x80\"": false,
		// A truncated escape at the end of the text.
		`"\ud8`: false, `"\ud800`: true, `"\`: false, `\`: false, `"\u`: false,
	} {
		if got := HasLoneSurrogateEscape([]byte(text)); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

// BenchmarkHasLoneSurrogateEscape scans the frames a host reads: mostly text without a backslash, and text dense in escapes.
func BenchmarkHasLoneSurrogateEscape(b *testing.B) {
	plain := []byte(`{"type":"response","id":"7","response":{"result":{"content":[{"type":"text","text":"` + strings.Repeat("file contents é ", 4096) + `"}]}}}`)
	escaped := []byte(`{"type":"response","response":{"result":"` + strings.Repeat(`line\n\t\"q\" \u00e9 \ud83d\ude00 \\ `, 2048) + `"}}`)
	for name, data := range map[string][]byte{"plain": plain, "escaped": escaped} {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if HasLoneSurrogateEscape(data) {
					b.Fatal("reported a lone surrogate")
				}
			}
		})
	}
}

package json

import (
	"bytes"
	"strings"
	"testing"
)

// AppendStringifyString writes the text AppendStringify writes for a string.
func TestAppendStringifyStringMatchesAppendStringify(t *testing.T) {
	for _, text := range []string{"", "plain", "q\"b\\s/", "\x00\x1f\x7f\b\f\n\r\t", "<&>", "\u2028\u2029", "\xff\xc3(\xed\xa0\x80\xed\xb8\x80", "\U0001F600 é 日本", strings.Repeat("abcdefgh", 40) + "\n"} {
		want, err := AppendStringify([]byte("p"), text)
		if err != nil {
			t.Fatal(err)
		}
		if got := AppendStringifyString([]byte("p"), text); !bytes.Equal(got, want) {
			t.Fatalf("%q: got %q want %q", text, got, want)
		}
	}
}

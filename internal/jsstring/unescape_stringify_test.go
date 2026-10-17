package jsstring

import (
	"bytes"
	"encoding/json"
	"testing"
)

// JSON.stringify writes <, >, &, U+2028 and U+2029 as they are; encoding/json escapes them. An escaped backslash followed by the text
// "u003c" is a backslash and five letters, not an escape, and stays as written.
func TestUnescapeForStringifyRewritesOnlyRealEscapes(t *testing.T) {
	for _, value := range []string{"a <b> & c \u2028 d \u2029", `back\u003cslash`, `\\u0026`, "plain"} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		want, err := MarshalJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if got := UnescapeForStringify(encoded); !bytes.Equal(got, want) {
			t.Errorf("UnescapeForStringify(%s) = %s, want %s", encoded, got, want)
		}
	}
	plain := []byte(`{"a":"b"}`)
	if got := UnescapeForStringify(plain); &got[0] != &plain[0] {
		t.Error("text without an escape was copied")
	}
}

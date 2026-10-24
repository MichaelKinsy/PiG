package ai

import "testing"

// appendStoreString writes what marshalStoreJSON writes, on the plain ASCII it takes itself and on every string it hands to the encoder.
func TestAppendStoreStringMatchesTheEncoder(t *testing.T) {
	for _, s := range []string{"", "api", "baseUrl", "https://example.test/v1?a=1&b=<2>", `quo"te`, `back\slash`, "tab\there", "new\nline", "\x7f", "caf\u00e9", "\u2028\u2029", "\xff\xfe", "emoji \U0001F437", "\x00"} {
		want, err := marshalStoreJSON(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := appendStoreString([]byte("prefix:"), s)
		if err != nil || string(got) != "prefix:"+string(want) {
			t.Errorf("appendStoreString(%q) = %q, %v, want %q", s, got, err, "prefix:"+string(want))
		}
	}
}

package text

import (
	"encoding/json"
	"testing"
)

func TestSplitBom(t *testing.T) {
	cases := []struct {
		name, in, mark, text string
	}{
		{"leading mark", "\xef\xbb\xbfhello", "\xef\xbb\xbf", "hello"},
		{"no mark", "hello", "", "hello"},
		{"empty", "", "", ""},
		{"only mark", "\xef\xbb\xbf", "\xef\xbb\xbf", ""},
		{"mark not leading", "a\xef\xbb\xbfb", "", "a\xef\xbb\xbfb"},
		{"only the first mark", "\xef\xbb\xbf\xef\xbb\xbfx", "\xef\xbb\xbf", "\xef\xbb\xbfx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mark, text := SplitBom(tc.in)
			if mark != tc.mark || text != tc.text {
				t.Fatalf("SplitBom(%q) = (%q, %q), want (%q, %q)", tc.in, mark, text, tc.mark, tc.text)
			}
			if got := StripBom(tc.in); got != tc.text {
				t.Fatalf("StripBom(%q) = %q, want %q", tc.in, got, tc.text)
			}
			if got := string(StripBomBytes([]byte(tc.in))); got != tc.text {
				t.Fatalf("StripBomBytes(%q) = %q, want %q", tc.in, got, tc.text)
			}
		})
	}
}

// Upstream strips the mark before JSON.parse because the parser rejects it;
// Go's decoder rejects it too, so readers must strip it the same way.
func TestStripBomBytesMakesJSONDecodable(t *testing.T) {
	raw := []byte("\xef\xbb\xbf{\"a\":1}")
	var v map[string]int
	if err := json.Unmarshal(raw, &v); err == nil {
		t.Fatal("encoding/json accepted a leading BOM; the strip would be unnecessary")
	}
	if err := json.Unmarshal(StripBomBytes(raw), &v); err != nil || v["a"] != 1 {
		t.Fatalf("decode after strip: %v %v", v, err)
	}
}

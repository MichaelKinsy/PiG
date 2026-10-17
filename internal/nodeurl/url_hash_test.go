package nodeurl

import "testing"

// Node: new URL("https://h/#a b<é").hash is "#a%20b%3C%C3%A9": url.hash is the percent-encoded fragment for a special URL as for any other.
func TestParseURLHashIsTheEncodedFragment(t *testing.T) {
	for raw, want := range map[string]string{
		"https://h/#a b<é": "#a%20b%3C%C3%A9",
		"ws://h/#x\x7fy":   "#x%7Fy",
		"ftp://h/#":        "",
		"git+ssh:x#a b":    "#a%20b",
	} {
		parsed, err := ParseURL(raw)
		if err != nil {
			t.Fatalf("ParseURL(%q): %v", raw, err)
		}
		if parsed.Hash != want {
			t.Errorf("ParseURL(%q).Hash = %q, want %q", raw, parsed.Hash, want)
		}
	}
}

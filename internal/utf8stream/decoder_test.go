package utf8stream

import "testing"

// Decoder matches TextDecoder: a split character waits for the
// next chunk and each maximal invalid subpart becomes one U+FFFD.
func TestUTF8StreamDecoderMatchesTextDecoder(t *testing.T) {
	var d Decoder
	if got := d.Decode([]byte("a\xe2\x82"), true); got != "a" {
		t.Fatalf("partial = %q", got)
	}
	if got := d.Decode([]byte("\xacb"), true); got != "€b" {
		t.Fatalf("completed = %q", got)
	}
	for in, want := range map[string]string{
		"\xe2\x82a":          "�a",
		"\xc0\x80":           "��",
		"\xed\xa0\x80":       "���",
		"\xf0\x9f\x98":       "�",
		"\xff":               "�",
		"ok\xf0\x9f\x98\x80": "ok😀",
	} {
		var d Decoder
		if got := d.Decode([]byte(in), false); got != want {
			t.Errorf("decode(%q) = %q, want %q", in, got, want)
		}
	}
}

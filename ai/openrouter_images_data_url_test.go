package ai

import "testing"

// openrouter-images.ts:101 matches generated image URLs with /^data:([^;]+);base64,(.+)$/. PiG used to accept media type parameters
// and an empty media type, reject payloads that are not valid base64, and accept a payload with a line break.
func TestParseDataImageURLMatchesPiRegex(t *testing.T) {
	for _, tc := range []struct {
		url       string
		mediaType string
		data      string
		ok        bool
	}{
		{"data:image/png;base64,ZmFrZQ==", "image/png", "ZmFrZQ==", true},
		{"data:image/png;base64,not base64 at all!", "image/png", "not base64 at all!", true},
		{"data:image/png;base64,a;b", "image/png", "a;b", true},
		{"data:image/png;charset=x;base64,ZmFrZQ==", "", "", false},
		{"data:;base64,ZmFrZQ==", "", "", false},
		{"data:image/png;base64,", "", "", false},
		{"data:image/png;base64,ab\ncd", "", "", false},
		{"data:image/png;base64,ab\u2028cd", "", "", false},
		{"data:image/png;base64,ab\rcd", "", "", false},
		{"data:image/png;base64,ab\u2029cd", "", "", false},
		{"data:image/png;base64,ab\u0085cd", "image/png", "ab\u0085cd", true},
		{"data:ima\nge/png;base64,ZmFrZQ==", "ima\nge/png", "ZmFrZQ==", true},
		{"data:image/png,ZmFrZQ==", "", "", false},
		{"data:image/png;BASE64,ZmFrZQ==", "", "", false},
		{"https://example.test/a.png", "", "", false},
		// Node 24 runs of the regex: "." stops at every line terminator, while [^;] accepts a line break and a comma.
		{"data:image/png;base64,ab\rcd", "", "", false},
		{"data:image/png;base64,ab\u2029cd", "", "", false},
		{"data:im\nage;base64,QQ", "im\nage", "QQ", true},
		{"data:a,b;base64,QQ", "a,b", "QQ", true},
	} {
		mediaType, data, ok := parseDataImageURL(tc.url)
		if mediaType != tc.mediaType || data != tc.data || ok != tc.ok {
			t.Errorf("parseDataImageURL(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.url, mediaType, data, ok, tc.mediaType, tc.data, tc.ok)
		}
	}
}

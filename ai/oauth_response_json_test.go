package ai

import "testing"

// Node's Response.text() and json() drop up to two leading byte order marks: undici's utf8DecodeBytes drops one and its TextDecoder drops the next. Expected values were probed with `new Response(Buffer.from(s)).text()` on Node 22.13, 22.19, 24.15, 25.9 and 26.10; Blob.text() drops only one.
func TestFetchBodyTextDropsUpToTwoByteOrderMarks(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"", ""},
		{"hi", "hi"},
		{"\ufeffhi", "hi"},
		{"\ufeff\ufeffhi", "hi"},
		{"\ufeff\ufeff\ufeffhi", "\ufeffhi"},
		{" \ufeffhi", " \ufeffhi"},
		{"\ufeff \ufeffhi", " \ufeffhi"},
		{"\ufeff\ufeff\xe2\x82", "\ufffd"},
	} {
		if got := fetchBodyText([]byte(tc.raw)); got != tc.want {
			t.Errorf("fetchBodyText(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if value, text, ok := oauthJSONValue([]byte("\ufeff\ufeff{}")); !ok || text != "{}" || value == nil {
		t.Errorf("json() of two byte order marks and {}: %v %q %v, want {}", value, text, ok)
	}
	if _, _, ok := oauthJSONValue([]byte("\ufeff\ufeff\ufeff{}")); ok {
		t.Error("json() of three byte order marks and {} parsed, want a SyntaxError")
	}
}

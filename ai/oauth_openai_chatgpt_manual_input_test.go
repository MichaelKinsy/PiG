package ai

import "testing"

// authorizationResultFromManualInput reports "Paste the full callback URL" only when new URL throws; a URL that parses
// with another origin gets the "must start with" message (openai-chatgpt.ts:64-78). Node 24 classifies the inputs below
// this way: new URL throws for "abc", "http://" and "127.0.0.1:1455/..." and parses "localhost:1455/..." (origin
// "null") and "http:foo" (origin "http://foo").
func TestChatGPTManualInputErrorsFollowURLParsing(t *testing.T) {
	const paste = "Paste the full callback URL from the browser"
	const origin = "The pasted callback URL must start with http://127.0.0.1:1455/auth/callback"
	for _, tc := range []struct{ input, want string }{
		{"abc", paste},
		{"http://", paste},
		{"127.0.0.1:1455/auth/callback?code=c&state=s", paste},
		{"localhost:1455/auth/callback?code=c&state=s", origin},
		{"http:foo", origin},
		{"http://localhost:1455/auth/callback?code=c&state=s", origin},
		{"HTTP://127.0.0.1:1455/auth/callback?code=c&state=other&client_id=id", "OAuth state mismatch"},
	} {
		if _, err := chatgptResultFromManualInput(tc.input, "s"); err == nil || err.Error() != tc.want {
			t.Errorf("%q: err=%v want %q", tc.input, err, tc.want)
		}
	}
}

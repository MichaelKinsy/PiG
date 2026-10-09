package ai

// pi: packages/ai/src/auth/oauth/openai-chatgpt.ts

import (
	"reflect"
	"testing"
)

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

// authorizationResultFromManualInput compares url.origin and url.pathname of new URL(input) and reads url.searchParams.
// Node 24 resolves "x/.." and "%2e" segments and backslashes, drops the default-port zero padding, keeps "%63" encoded,
// and URLSearchParams keeps a ";" in a value and turns "+" into a space.
func TestChatGPTManualInputUsesTheWHATWGURL(t *testing.T) {
	for _, tc := range []struct{ input, code, err string }{
		{"http://127.0.0.1:1455/auth/x/../callback?code=c;d&state=s&client_id=id", "c;d", ""},
		{`http://127.0.0.1:01455\auth\%2e\callback?code=a+b&state=s&client_id=id`, "a b", ""},
		{"http://127.0.0.1:1455/auth/%63allback?code=c&state=s&client_id=id", "", "The pasted callback URL must start with http://127.0.0.1:1455/auth/callback"},
		{"http://127.0.0.1:99999/auth/callback?code=c", "", "Paste the full callback URL from the browser"},
	} {
		result, err := chatgptResultFromManualInput(tc.input, "s")
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%q: err=%v want %q", tc.input, err, tc.err)
			}
			continue
		}
		if want := (chatgptAuthorizationResult{code: tc.code, clientID: "id"}); err != nil || !reflect.DeepEqual(result, want) {
			t.Errorf("%q: result=%+v err=%v want %+v", tc.input, result, err, want)
		}
	}
}

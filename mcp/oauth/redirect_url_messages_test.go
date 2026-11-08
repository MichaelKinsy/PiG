package oauth

import "testing"

// loopback.ts responseFromRedirectUrl: each failure has its own message, checked in this order: unparsable input, origin or path mismatch, provider error (error_description first), wrong state, missing code; a code returns with its optional iss (an empty one counts as none, as `...(iss ? { iss } : {})` does).
func TestResponseFromRedirectURLMessagesAndOrder(t *testing.T) {
	authorization := McpSignInRedirect{State: "s", RedirectURL: "http://127.0.0.1:1/callback"}
	for _, tc := range []struct {
		name, input, wantErr, wantCode string
		wantISS                        *string
	}{
		{"not a URL", "callback?code=c", "Expected the full redirect URL from the browser address bar", "", nil},
		{"other origin", "http://127.0.0.1:2/callback?code=c&state=s", "The redirect URL does not match this sign-in's redirect URI", "", nil},
		{"other path", "http://127.0.0.1:1/other?code=c&state=s", "The redirect URL does not match this sign-in's redirect URI", "", nil},
		{"mismatch wins over a provider error", "http://127.0.0.1:1/other?error=denied", "The redirect URL does not match this sign-in's redirect URI", "", nil},
		{"provider error text", "http://127.0.0.1:1/callback?error=access_denied&state=other", "access_denied", "", nil},
		{"error description wins", "http://127.0.0.1:1/callback?error=access_denied&error_description=User+said+no", "User said no", "", nil},
		{"empty error description is still the description", "http://127.0.0.1:1/callback?error=access_denied&error_description=", "", "", nil},
		{"wrong state", "http://127.0.0.1:1/callback?code=c&state=other", "The redirect URL belongs to a different sign-in", "", nil},
		{"missing state", "http://127.0.0.1:1/callback?code=c", "The redirect URL belongs to a different sign-in", "", nil},
		{"state wins over a missing code", "http://127.0.0.1:1/callback?state=other", "The redirect URL belongs to a different sign-in", "", nil},
		{"missing code", "http://127.0.0.1:1/callback?state=s", "The redirect URL does not contain an authorization code", "", nil},
		{"empty code", "http://127.0.0.1:1/callback?state=s&code=", "The redirect URL does not contain an authorization code", "", nil},
		{"code without iss", "  http://127.0.0.1:1/callback?state=s&code=abc  ", "", "abc", nil},
		{"code with iss", "http://127.0.0.1:1/callback?state=s&code=abc&iss=https%3A%2F%2Fas.example", "", "abc", new("https://as.example")},
		{"empty iss is dropped", "http://127.0.0.1:1/callback?state=s&code=abc&iss=", "", "abc", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := ResponseFromRedirectURL(tc.input, authorization)
			if tc.wantErr != "" || tc.name == "empty error description is still the description" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || response.Code != tc.wantCode {
				t.Fatalf("response = %+v, %v, want code %q", response, err, tc.wantCode)
			}
			if (response.Iss == "") != (tc.wantISS == nil) || (tc.wantISS != nil && response.Iss != *tc.wantISS) {
				t.Fatalf("iss = %q, want %v", response.Iss, tc.wantISS)
			}
		})
	}
}

// loopback.ts responseFromRedirectUrl: `new URL(input.trim())`. A BOM around the pasted URL is removed; U+0085 stays and
// makes the URL invalid (node: `new URL("\u0085http://…")` throws "Invalid URL").
func TestResponseFromRedirectURLTrimsLikeJavaScript(t *testing.T) {
	authorization := McpSignInRedirect{State: "s", RedirectURL: "http://127.0.0.1:1/callback"}
	response, err := ResponseFromRedirectURL("\uFEFFhttp://127.0.0.1:1/callback?code=c&state=s\uFEFF", authorization)
	if err != nil || response.Code != "c" {
		t.Fatalf("BOM-padded URL: %+v, %v", response, err)
	}
	if _, err := ResponseFromRedirectURL("\u0085http://127.0.0.1:1/callback?code=c&state=s", authorization); err == nil || err.Error() != "Expected the full redirect URL from the browser address bar" {
		t.Fatalf("U+0085-prefixed URL: err = %v", err)
	}
}

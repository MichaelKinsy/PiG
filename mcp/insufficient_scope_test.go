package mcp

import (
	"net/http"
	"testing"
)

// streamable-http.ts:163 matches /(?:^|[\s,])error="?insufficient_scope"?/i against response.headers.get("www-authenticate"). JavaScript's \s includes
// U+00A0 and U+FEFF (RE2's \s is ASCII only), and Node's fetch returns the header bytes as Latin-1, one code point per byte. Node 24 over a raw socket:
// "Bearer" C2 A0 "error=..." reads "BearerÂ\u00a0error=..." (true); a lone A0 byte reads U+00A0 (true); EF BB BF reads "ï»¿" (false); C2 85 reads
// "Â\u0085" (false).
func TestNeedsAuthorizationReadsAJavaScriptWhitespaceSeparatedInsufficientScope(t *testing.T) {
	for challenge, want := range map[string]bool{
		`Bearer error="insufficient_scope"`:          true,
		"Bearer\xc2\xa0error=insufficient_scope":     true,
		"Bearer\xa0error=Insufficient_Scope":         true,
		"Bearer\u3000error=insufficient_scope":       false,
		"Bearer\xef\xbb\xbferror=insufficient_scope": false,
		"Bearer\xc2\x85error=insufficient_scope":     false,
		`Bearer xerror=insufficient_scope`:           false,
		// A non-unicode JavaScript /i folds ASCII letters only. U+017F (long s, bytes C5 BF) reads as "Å¿" and is not "s".
		"Bearer error=in\u017fufficient_\u017fcope": false,
		"Bearer ERROR=INSUFFICIENT_SCOPE":           true,
	} {
		response := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Www-Authenticate": {challenge}}}
		if got := needsAuthorization(response); got != want {
			t.Errorf("needsAuthorization(%q) = %v, want %v", challenge, got, want)
		}
	}
}

// streamable-http.ts:156 trims the Latin-1 content-type with String.prototype.trim: a lone A0 byte is U+00A0 and trimmed; the UTF-8 no-break space
// C2 A0 leaves "Â", so the type is not application/json.
func TestResponseContentTypeTrimsTheLatin1HeaderValue(t *testing.T) {
	for value, want := range map[string]string{
		"Application/JSON; charset=utf-8": "application/json",
		"\xa0application/json\xa0":        "application/json",
		"application/json\xc2\xa0":        "application/json\u00e2",
	} {
		response := &http.Response{Header: http.Header{"Content-Type": {value}}}
		if got := responseContentType(response); got != want {
			t.Errorf("responseContentType(%q) = %q, want %q", value, got, want)
		}
	}
	response := &http.Response{Header: http.Header{"Www-Authenticate": {"Bearer realm=\"\xe9\""}}}
	if got := NewMcpAuthRequiredError(response, "").WWWAuthenticate; got != "Bearer realm=\"\u00e9\"" {
		t.Errorf("wwwAuthenticate = %q, want the Latin-1 header value", got)
	}
}

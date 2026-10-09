package mcp

import (
	"net/http"
	"testing"
)

// streamable-http.ts:163 matches /(?:^|[\s,])error="?insufficient_scope"?/i against www-authenticate. JavaScript's \s includes U+00A0 and U+FEFF (RE2's \s is ASCII only).
func TestNeedsAuthorizationReadsAJavaScriptWhitespaceSeparatedInsufficientScope(t *testing.T) {
	for challenge, want := range map[string]bool{
		`Bearer error="insufficient_scope"`:        true,
		"Bearer\u00a0error=insufficient_scope":     true,
		"Bearer\ufefferror=\"Insufficient_Scope\"": true,
		"Bearer\u0085error=insufficient_scope":     false,
		`Bearer xerror=insufficient_scope`:         false,
		// A non-unicode JavaScript /i folds ASCII letters only: U+017F (long s) is not "s" there, while RE2's (?i) folds it.
		"Bearer error=in\u017fufficient_\u017fcope": false,
		"Bearer ERROR=INSUFFICIENT_SCOPE":           true,
	} {
		response := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Www-Authenticate": {challenge}}}
		if got := needsAuthorization(response); got != want {
			t.Errorf("needsAuthorization(%q) = %v, want %v", challenge, got, want)
		}
	}
}

package codingagent

import (
	"strings"
	"testing"
)

// copilotRateLimitRaw is the error text observed in a live session: GitHub
// answered the token refresh with 403 "API rate limit exceeded". pig reported
// "GitHub Copilot auth expired: run pig login", asserting a cause that was
// not true and that re-authenticating cannot fix.
//
// Upstream does not classify this failure. refreshGitHubCopilotToken throws,
// the credential resolves to undefined, and agent-session.ts reports
// "Credentials may have expired or network is unavailable. Run '/login ...'".
// pig mirrors that wording: name both causes, assert neither.
const copilotRateLimitRaw = `github-copilot: GetBaseURL: github-copilot: token refresh failed: credentials may have expired or the network is unavailable: HTTP 403: {"message": "API rate limit exceeded for user ID 186620145.", "status": "403"}`

func TestProviderErrorDisplay_DoesNotAssertExpiry(t *testing.T) {
	status, chat := formatProviderErrorForDisplay("error", copilotRateLimitRaw)

	if strings.Contains(status, "auth expired") {
		t.Errorf("status = %q asserts expiry; a refresh also fails on rate limits, network loss and outages", status)
	}
	// Upstream names both causes, so the user is not sent down one path only.
	for _, want := range []string{"may have expired", "network is unavailable"} {
		if !strings.Contains(status, want) {
			t.Errorf("status = %q, want it to mention %q as upstream does", status, want)
		}
	}
	// The provider's own text carries the specific reason and must survive.
	if !strings.Contains(chat, "API rate limit exceeded") {
		t.Errorf("chat text lost the provider error: %q", chat)
	}
}

// A genuine credential failure reports the same hedged message, matching
// upstream, which does not distinguish either.
func TestProviderErrorDisplay_AuthFailureIsHedgedToo(t *testing.T) {
	for _, raw := range []string{
		`github-copilot: token refresh failed: credentials may have expired or the network is unavailable: HTTP 401: {"message":"Bad credentials"}`,
		`github-copilot: GetBaseURL: HTTP 401: unauthorized`,
	} {
		status, _ := formatProviderErrorForDisplay("error", raw)
		if !strings.Contains(status, "pig login") {
			t.Errorf("raw %q → status %q, want login guidance", raw, status)
		}
		if strings.Contains(status, "auth expired") {
			t.Errorf("raw %q → status %q, want the hedged wording", raw, status)
		}
	}
}

// Errors from other providers keep the generic path; the copilot branch must
// not widen to everything that mentions a refresh.
func TestProviderErrorDisplay_OtherProvidersUnaffected(t *testing.T) {
	status, _ := formatProviderErrorForDisplay("error", `anthropic: HTTP 401: {"message":"invalid x-api-key"}`)
	if strings.Contains(status, "pig login") {
		t.Errorf("status = %q, want the generic provider-failure path", status)
	}
}

package release

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestGitHubURLOverrideAcceptsOnlyAnOptedInLoopbackOrigin pins every branch of the PIG_PIGLET_GITHUB_URL gate. Each refusal is checked by its own message, so a branch removed in githubURL cannot pass through validateRemoteURL's HTTPS rule instead: the HTTPS cases have no later check that would refuse them.
func TestGitHubURLOverrideAcceptsOnlyAnOptedInLoopbackOrigin(t *testing.T) {
	const path = "/repos/acme/porter/releases?per_page=100&page=1"
	for _, tc := range []struct {
		name, override, optIn, want, refusal string
	}{
		{name: "unset", want: "https://api.github.com" + path},
		{name: "blank", override: "  ", want: "https://api.github.com" + path},
		{name: "http loopback", override: "http://127.0.0.1:8080", optIn: "1", want: "http://127.0.0.1:8080" + path},
		{name: "trailing slash", override: "http://127.0.0.1:8080/", optIn: "true", want: "http://127.0.0.1:8080" + path},
		{name: "https loopback", override: "https://127.0.0.2:8443", optIn: "yes", want: "https://127.0.0.2:8443" + path},
		{name: "localhost", override: "http://LOCALHOST:1", optIn: "1", want: "http://LOCALHOST:1" + path},
		{name: "ipv6 loopback", override: "http://[::1]:1", optIn: "1", want: "http://[::1]:1" + path},
		{name: "http without opt-in", override: "http://127.0.0.1:8080", refusal: "requires PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP=1"},
		{name: "https without opt-in", override: "https://127.0.0.1:8443", optIn: "0", refusal: "requires PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP=1"},
		{name: "https public host", override: "https://example.com", optIn: "1", refusal: "must name a loopback host, not example.com"},
		{name: "http public host", override: "http://example.com", optIn: "1", refusal: "must name a loopback host, not example.com"},
		{name: "private address", override: "http://10.0.0.1:8080", optIn: "1", refusal: "must name a loopback host, not 10.0.0.1"},
		{name: "path", override: "http://127.0.0.1:8080/mirror", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
		{name: "query", override: "http://127.0.0.1:8080?x=1", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
		{name: "fragment", override: "http://127.0.0.1:8080#x", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
		{name: "credentials", override: "http://user:secret@127.0.0.1:8080", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
		{name: "scheme", override: "ftp://127.0.0.1:8080", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
		{name: "opaque", override: "http:127.0.0.1", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
		{name: "no scheme", override: "127.0.0.1:8080", optIn: "1", refusal: "must be a bare http(s) loopback origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(GitHubURLEnv, tc.override)
			t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", tc.optIn)
			got, err := githubURL("api.github.com", path)
			if tc.refusal != "" {
				if _, policy := errors.AsType[urlPolicyError](err); !policy || !strings.Contains(err.Error(), tc.refusal) || got != "" {
					t.Fatalf("githubURL() = %q, %v; want urlPolicyError containing %q", got, err, tc.refusal)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("refusal echoes credentials: %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("githubURL() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestGitHubURLOverrideRoutesIndexAndDiscovery checks that github: index URLs and update discovery both take the override, and that a refused override sends no request.
func TestGitHubURLOverrideRoutesIndexAndDiscovery(t *testing.T) {
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	t.Setenv(GitHubURLEnv, "")
	ref, err := parseReleaseReference("github:acme/tools/porter@1.2.3", "")
	if err != nil || ref.url != "https://github.com/acme/tools/releases/download/porter%2Fv1.2.3/piglet-release.json" {
		t.Fatalf("unset index URL = %q, %v", ref.url, err)
	}

	t.Setenv(GitHubURLEnv, "http://127.0.0.1:9")
	ref, err = parseReleaseReference("github:acme/tools/porter@1.2.3", "")
	if err != nil || ref.url != "http://127.0.0.1:9/acme/tools/releases/download/porter%2Fv1.2.3/piglet-release.json" {
		t.Fatalf("override index URL = %q, %v", ref.url, err)
	}
	probe := &requestProbeTransport{}
	if _, err := discoverGitHubVersion(context.Background(), &http.Client{Transport: probe}, GitHubRelease{Repository: "acme/tools", TagPrefix: "porter/"}); err == nil {
		t.Fatal("discovery against the probe succeeded")
	}
	if want := "http://127.0.0.1:9/repos/acme/tools/releases?per_page=100&page=1"; len(probe.urls) != 1 || probe.urls[0] != want {
		t.Fatalf("discovery requests = %v, want [%s]", probe.urls, want)
	}

	t.Setenv(GitHubURLEnv, "https://example.com")
	if _, err := parseReleaseReference("github:acme/tools/porter@1.2.3", ""); err == nil || !strings.Contains(err.Error(), "loopback host") {
		t.Fatalf("non-loopback index override error = %v", err)
	}
	probe = &requestProbeTransport{}
	if _, err := discoverGitHubVersion(context.Background(), &http.Client{Transport: probe}, GitHubRelease{Repository: "acme/tools"}); err == nil || !strings.Contains(err.Error(), "loopback host") || len(probe.urls) != 0 {
		t.Fatalf("non-loopback discovery override error = %v, requests %v", err, probe.urls)
	}
}

type requestProbeTransport struct{ urls []string }

func (p *requestProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	p.urls = append(p.urls, request.URL.String())
	return nil, errors.New("probe transport sends nothing")
}

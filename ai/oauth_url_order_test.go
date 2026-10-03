package ai

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// queryKeys lists the keys of a raw query in the order they appear.
func queryKeys(t *testing.T, rawURL string) []string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for pair := range strings.SplitSeq(parsed.RawQuery, "&") {
		key, _, _ := strings.Cut(pair, "=")
		keys = append(keys, key)
	}
	return keys
}

// placeholderQuery replaces the generated values of the named query keys with
// fixed placeholders so the rest of the URL can be compared byte for byte.
func placeholderQuery(t *testing.T, rawURL string, placeholders map[string]string) string {
	t.Helper()
	base, rawQuery, ok := strings.Cut(rawURL, "?")
	if !ok {
		t.Fatalf("URL has no query: %s", rawURL)
	}
	pairs := strings.Split(rawQuery, "&")
	for i, pair := range pairs {
		key, _, _ := strings.Cut(pair, "=")
		if placeholder, ok := placeholders[key]; ok {
			pairs[i] = key + "=" + placeholder
		}
	}
	return base + "?" + strings.Join(pairs, "&")
}

// anthropic.ts:151-160,193-202 build the authorize URL with URLSearchParams, which keeps insertion order. The expected
// URLs are Node's `${AUTHORIZE_URL}?${new URLSearchParams({...}).toString()}` output with code_challenge=CHALLENGE and
// state=VERIFIER.
func TestAnthropicAuthorizeURLKeepsUpstreamParameterOrder(t *testing.T) {
	want := []string{"code", "client_id", "response_type", "redirect_uri", "scope", "code_challenge", "code_challenge_method", "state"}
	const scope = "&scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainference+user%3Asessions%3Aclaude_code+user%3Amcp_servers+user%3Afile_upload"
	stop := errors.New("stop after the authorize URL")
	cases := []struct {
		name  string
		login func(context.Context, OAuthLoginCallbacks) (OAuthCredentials, error)
		url   string
	}{
		{"callback", LoginAnthropic, "https://claude.ai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A53692%2Fcallback" + scope + "&code_challenge=CHALLENGE&code_challenge_method=S256&state=VERIFIER"},
		{"copy-code", LoginAnthropicCopyCode, "https://claude.ai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback" + scope + "&code_challenge=CHALLENGE&code_challenge_method=S256&state=VERIFIER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_OAUTH_CALLBACK_HOST", "")
			var authorizeURL string
			_, err := tc.login(t.Context(), OAuthLoginCallbacks{
				OnAuth:                   func(info OAuthAuthInfo) { authorizeURL = info.URL },
				OnManualCodeInput:        func() (string, error) { return "", stop },
				OnManualCodeInputContext: func(context.Context) (string, error) { return "", stop },
			})
			if !errors.Is(err, stop) {
				t.Fatalf("login error = %v, want the manual prompt error", err)
			}
			if got := queryKeys(t, authorizeURL); !slices.Equal(got, want) {
				t.Fatalf("authorize URL parameter order = %v, want %v\nURL: %s", got, want, authorizeURL)
			}
			if got := placeholderQuery(t, authorizeURL, map[string]string{"code_challenge": "CHALLENGE", "state": "VERIFIER"}); got != tc.url {
				t.Fatalf("authorize URL =\n%s\nwant\n%s", got, tc.url)
			}
		})
	}
}

// openrouter.ts:127-132 build the authorize search with URLSearchParams in this order. The expected URL is Node's
// output for callback_url=http://127.0.0.1:<port>/oauth/callback/<uuid> and code_challenge=CHALLENGE.
func TestOpenRouterAuthorizeURLKeepsUpstreamParameterOrder(t *testing.T) {
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "")
	auth, ok := OAuthProviderAuth("openrouter")
	if !ok {
		t.Fatal("openrouter OAuth provider is not registered")
	}
	stop := errors.New("stop after the authorize URL")
	var authorizeURL string
	_, err := auth.Login(t.Context(), AuthInteraction{
		Prompt: func(context.Context, AuthPrompt) (string, error) { return "", stop },
		Notify: func(event AuthEvent) {
			if event, ok := event.(AuthURLEvent); ok {
				authorizeURL = event.URL
			}
		},
	}, LoginOptions{})
	if !errors.Is(err, stop) {
		t.Fatalf("login error = %v, want the manual prompt error", err)
	}
	want := []string{"callback_url", "code_challenge", "code_challenge_method"}
	if got := queryKeys(t, authorizeURL); !slices.Equal(got, want) {
		t.Fatalf("authorize URL parameter order = %v, want %v\nURL: %s", got, want, authorizeURL)
	}
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	callbackURL, err := url.Parse(parsed.Query().Get("callback_url"))
	if err != nil {
		t.Fatal(err)
	}
	if callbackURL.Hostname() != "127.0.0.1" || !strings.HasPrefix(callbackURL.Path, "/oauth/callback/") {
		t.Fatalf("callback_url = %s, want http://127.0.0.1:<port>/oauth/callback/<uuid>", callbackURL)
	}
	wantURL := "https://openrouter.ai/auth?callback_url=http%3A%2F%2F127.0.0.1%3A" + callbackURL.Port() + "%2Foauth%2Fcallback%2F" + strings.TrimPrefix(callbackURL.Path, "/oauth/callback/") + "&code_challenge=CHALLENGE&code_challenge_method=S256"
	if got := placeholderQuery(t, authorizeURL, map[string]string{"code_challenge": "CHALLENGE"}); got != wantURL {
		t.Fatalf("authorize URL =\n%s\nwant\n%s", got, wantURL)
	}
}

// orderedQuery must serialize like URLSearchParams.toString (WHATWG application/x-www-form-urlencoded). The expected
// strings are Node's new URLSearchParams([[k, v], ...]).toString() output.
func TestOrderedQueryMatchesURLSearchParamsSerializer(t *testing.T) {
	cases := []struct {
		pairs []string
		want  string
	}{
		{[]string{"b", "1", "a", "2"}, "b=1&a=2"},
		{[]string{"a", "~*-._ !'()/:é"}, "a=%7E*-._+%21%27%28%29%2F%3A%C3%A9"},
		{[]string{"k y", "a&b=c+d%"}, "k+y=a%26b%3Dc%2Bd%25"},
		{[]string{"empty", ""}, "empty="},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := orderedQuery(tc.pairs...); got != tc.want {
			t.Errorf("orderedQuery(%q) = %q, want %q", tc.pairs, got, tc.want)
		}
	}
}

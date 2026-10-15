package oauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// piOAuthURLs runs Pi's own packages/mcp OAuth source from the upstream mirror on the cases and returns, for each, the
// href of the URL Pi builds or the message of the error it throws.
func piOAuthURLs(t *testing.T, cases []urlCase) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".upstream", "current", "packages", "mcp", "src"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `
const { resolve } = require("node:path");
const { pathToFileURL } = require("node:url");
const [root, input] = process.argv.slice(1);
const load = (file) => import(pathToFileURL(resolve(root, file)).href);
(async () => {
  const flow = await load("oauth/flow.ts");
  const discovery = await load("oauth/discovery.ts");
  const out = [];
  for (const c of JSON.parse(input)) {
    try {
      switch (c.op) {
        case "authorize": {
          const metadata = c.endpoint ? { issuer: c.server, authorization_endpoint: c.endpoint, token_endpoint: "https://as.example/token", response_types_supported: ["code"] } : undefined;
          const { authorizationUrl } = await flow.startAuthorization(c.server, { metadata, clientInformation: { client_id: "c" }, redirectUrl: "http://127.0.0.1/cb" });
          out.push(authorizationUrl.href);
          break;
        }
        case "token":
          await flow.exchangeAuthorizationCode(c.server, { metadata: { issuer: c.server, authorization_endpoint: "https://as.example/a", token_endpoint: c.endpoint, response_types_supported: ["code"] }, clientInformation: { client_id: "c" }, code: "x", codeVerifier: "v", redirectUrl: "http://127.0.0.1/cb" });
          out.push("no error");
          break;
        case "challenge":
          out.push(String(discovery.parseWwwAuthenticate(c.header).resourceMetadataUrl?.href));
          break;
        case "resource":
          out.push(discovery.resourceUrlFromServerUrl(c.server).href);
          break;
        case "select":
          out.push(String(discovery.selectResource(c.server, { resource: c.endpoint })));
          break;
      }
    } catch (error) {
      out.push("error: " + error.message);
    }
  }
  process.stdout.write(JSON.stringify(out));
})();`
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, root, string(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var hrefs []string
	if err := json.Unmarshal(out, &hrefs); err != nil {
		t.Fatal(err)
	}
	if len(hrefs) != len(cases) {
		t.Fatalf("Pi oracle answered %d of %d cases", len(hrefs), len(cases))
	}
	return hrefs
}

type urlCase struct {
	Op       string `json:"op"`
	Server   string `json:"server,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Header   string `json:"header,omitempty"`
}

// searchParams is `url.searchParams` of an OAuth URL.
func searchParams(u oauth.URL) url.Values { return nodeurl.SearchParams(u.Query) }

// mustURL is `new URL(text)`.
func mustURL(t *testing.T, text string) oauth.URL {
	t.Helper()
	u, err := nodeurl.ParseURL(text)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// codeChallenge is the PKCE challenge, fresh on every call, in an authorization URL.
var codeChallenge = regexp.MustCompile(`code_challenge=[^&#]*`)

// The URLs the OAuth flow builds are WHATWG URLs, as Pi's are: an empty fragment ("#") and a malformed escape in a
// fragment stay in the href, a URL of any scheme takes the authorization parameters, and the protected resource's
// origin check compares WHATWG origins, which are "null" for every scheme but http, https, ws, wss and ftp.
func TestOAuthURLsMatchPi(t *testing.T) {
	cases := []urlCase{
		{Op: "authorize", Server: "https://as.example", Endpoint: "https://as.example/authorize#"},
		{Op: "authorize", Server: "https://as.example", Endpoint: "https://as.example/authorize#%zz"},
		{Op: "authorize", Server: "https://as.example", Endpoint: "https://as.example/authorize?x=1&y=%zz#f%zz"},
		{Op: "authorize", Server: "https://as.example", Endpoint: "myapp://as/authorize#"},
		{Op: "authorize", Server: "https://as.example", Endpoint: "myapp://as/a%zz?q#"},
		{Op: "authorize", Server: "https://as.example", Endpoint: "x:opaque#"},
		{Op: "authorize", Server: "https://as.example", Endpoint: "https://as.example/authorize?response_type=token&a=+%41&response_type=x&client_id=old#f"},
		{Op: "authorize", Server: "https://as.example/tenant?q=1#f"},
		{Op: "authorize", Server: "myapp://as/tenant"},
		{Op: "authorize", Server: "x:opaque"},
		{Op: "token", Server: "https://as.example", Endpoint: "http://as.example/token#"},
		{Op: "token", Server: "https://as.example", Endpoint: "http://as.example/token#%zz"},
		{Op: "token", Server: "https://as.example", Endpoint: "x://h/t#"},
		{Op: "challenge", Header: `Bearer resource_metadata="https://h/.well-known/r#"`},
		{Op: "challenge", Header: `Bearer resource_metadata="https://h/r#%zz"`},
		{Op: "challenge", Header: `Bearer resource_metadata="myapp://h/r?#"`},
		{Op: "resource", Server: "https://h/mcp#"},
		{Op: "resource", Server: "x://h/p?#%zz"},
		{Op: "select", Server: "x://a/mcp", Endpoint: "x://b/mcp"},
		{Op: "select", Server: "x://a/mcp", Endpoint: "y://a/mcp"},
		{Op: "select", Server: "https://a/mcp#", Endpoint: "https://a/"},
	}
	want := piOAuthURLs(t, cases)
	for i, c := range cases {
		got := goOAuthURL(c)
		if c.Op == "authorize" {
			got = codeChallenge.ReplaceAllString(got, "code_challenge=X")
			want[i] = codeChallenge.ReplaceAllString(want[i], "code_challenge=X")
		}
		if got != want[i] {
			t.Errorf("%s %q %q %q:\n got %s\nwant %s", c.Op, c.Server, c.Endpoint, c.Header, got, want[i])
		}
	}
}

func goOAuthURL(c urlCase) string {
	switch c.Op {
	case "authorize":
		var metadata *oauth.AuthorizationServerMetadata
		if c.Endpoint != "" {
			metadata = &oauth.AuthorizationServerMetadata{Issuer: c.Server, AuthorizationEndpoint: c.Endpoint, TokenEndpoint: "https://as.example/token", ResponseTypesSupported: []string{"code"}}
		}
		u, _, err := oauth.StartAuthorization(c.Server, oauth.StartAuthorizationOptions{Metadata: metadata, ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "c"}, RedirectURL: "http://127.0.0.1/cb"})
		if err != nil {
			return "error: " + err.Error()
		}
		return u.Href()
	case "token":
		metadata := &oauth.AuthorizationServerMetadata{Issuer: c.Server, AuthorizationEndpoint: "https://as.example/a", TokenEndpoint: c.Endpoint, ResponseTypesSupported: []string{"code"}}
		_, err := oauth.ExchangeAuthorizationCode(context.Background(), c.Server, oauth.ExchangeAuthorizationCodeOptions{
			TokenRequestOptions: oauth.TokenRequestOptions{Metadata: metadata, ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "c"}},
			Code:                "x", CodeVerifier: "v", RedirectURL: "http://127.0.0.1/cb",
		})
		if err != nil {
			return "error: " + err.Error()
		}
		return "no error"
	case "challenge":
		if u := oauth.ParseWWWAuthenticate(c.Header).ResourceMetadataURL; u != nil {
			return u.Href()
		}
		return "undefined"
	case "resource":
		u, err := oauth.ResourceURLFromServerURL(c.Server)
		if err != nil {
			return "error: " + err.Error()
		}
		return u.Href()
	case "select":
		resource, err := oauth.SelectResource(c.Server, &oauth.OAuthProtectedResourceMetadata{Resource: c.Endpoint})
		if err != nil {
			return "error: " + err.Error()
		}
		return resource
	}
	return "unknown op " + c.Op
}

// A metadata request is the URL's fetch: the fragment, an empty or malformed one included, is not part of the request.
func TestDiscoveryRequestsTheURLWithoutItsFragment(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"` + "http://" + r.Host + `/mcp","authorization_servers":["https://as.example"]}`))
	}))
	defer server.Close()
	for _, fragment := range []string{"#", "#%zz", "#a%20b"} {
		paths = nil
		metadata, err := oauth.DiscoverProtectedResourceMetadata(t.Context(), server.URL+"/mcp", oauth.DiscoveryOptions{ResourceMetadataURL: server.URL + "/meta?x=%zz" + fragment})
		if err != nil || metadata == nil {
			t.Fatalf("fragment %q: metadata %v, error %v", fragment, metadata, err)
		}
		if want := []string{"/meta?x=%zz"}; !slices.Equal(paths, want) {
			t.Errorf("fragment %q: requested %q, want %q", fragment, paths, want)
		}
	}
}

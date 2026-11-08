//go:build !windows

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	nexthost "github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports test/p2/mcp-sign-in.test.ts: MCP OAuth sign-ins (sign_in.go, routes.go, probe.go) on a bare server host, against a
// fake authorization server whose MCP server takes only the tokens it issued: a 401 waits for a sign-in, the loopback flow
// (the owner view's `signIn` and the device owner's sign-in route), the callback route of relayed and public owners with the
// probe page and the probe, pasting, refusals (wrong state, guests, nobody), refreshing without a browser, signing out.

// brain is the origin a relayed owner reaches p2 through (never contacted: the test brings the browser's redirect back).
const brain = "https://brain.example"

var relayOwner = p2runtime.OwnerPrincipal{Access: p2runtime.AccessRelay}

// authServer is an authorization server with an MCP server at `/mcp` that answers only requests with a token it issued and
// has not revoked: discovery, dynamic client registration, PKCE, rotating refresh tokens.
type authServer struct {
	url           string
	mu            sync.Mutex
	registrations []map[string]any
	authorizing   []url.Values
	grants        []string
	challenges    map[string]string
	accessTokens  map[string]bool
	refreshTokens map[string]bool
	issued        int
}

func newAuthServer(t *testing.T) *authServer {
	p2test.NoLeakedGoroutines(t)
	a := &authServer{challenges: map[string]string{}, accessTokens: map[string]bool{}, refreshTokens: map[string]bool{}}
	var origin string
	issue := func() map[string]any {
		a.issued++
		a.accessTokens[fmt.Sprintf("access-%d", a.issued)] = true
		a.refreshTokens[fmt.Sprintf("refresh-%d", a.issued)] = true
		return map[string]any{"access_token": fmt.Sprintf("access-%d", a.issued), "refresh_token": fmt.Sprintf("refresh-%d", a.issued), "token_type": "Bearer"}
	}
	send := func(response http.ResponseWriter, status int, body any) {
		response.Header().Set("content-type", "application/json")
		response.WriteHeader(status)
		_ = json.NewEncoder(response).Encode(body)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		body := string(raw)
		a.mu.Lock()
		defer a.mu.Unlock()
		switch request.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			send(response, 200, map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}})
		case "/.well-known/oauth-authorization-server":
			send(response, 200, map[string]any{
				"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token",
				"registration_endpoint": origin + "/register", "response_types_supported": []string{"code"},
				"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/register":
			var metadata map[string]any
			_ = json.Unmarshal(raw, &metadata)
			a.registrations = append(a.registrations, metadata)
			metadata["client_id"] = fmt.Sprintf("client-%d", len(a.registrations))
			send(response, 201, metadata)
		case "/authorize":
			query := request.URL.Query()
			a.authorizing = append(a.authorizing, query)
			code := fmt.Sprintf("code-%d", len(a.authorizing))
			a.challenges[code] = query.Get("code_challenge")
			redirect, _ := url.Parse(query.Get("redirect_uri"))
			values := redirect.Query()
			values.Set("code", code)
			values.Set("state", query.Get("state"))
			redirect.RawQuery = values.Encode()
			response.Header().Set("location", redirect.String())
			response.WriteHeader(302)
		case "/token":
			params, _ := url.ParseQuery(body)
			a.grants = append(a.grants, params.Get("grant_type"))
			if params.Get("grant_type") == "authorization_code" {
				code := params.Get("code")
				sum := sha256.Sum256([]byte(params.Get("code_verifier")))
				if a.challenges[code] != base64.RawURLEncoding.EncodeToString(sum[:]) {
					send(response, 400, map[string]any{"error": "invalid_grant"})
					return
				}
				delete(a.challenges, code)
				send(response, 200, issue())
				return
			}
			refresh := params.Get("refresh_token")
			if !a.refreshTokens[refresh] {
				send(response, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(a.refreshTokens, refresh)
			send(response, 200, issue())
		case "/mcp":
			token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
			if !a.accessTokens[token] {
				response.Header().Set("www-authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, origin))
				response.WriteHeader(401)
				return
			}
			if request.Method != http.MethodPost {
				response.WriteHeader(405)
				return
			}
			var message struct {
				ID     *float64 `json:"id"`
				Method string   `json:"method"`
			}
			_ = json.Unmarshal(raw, &message)
			if message.ID == nil {
				response.WriteHeader(202)
				return
			}
			var result any
			switch message.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "oauth-fixture", "version": "1.0.0"}}
			case "tools/list":
				result = map[string]any{"tools": []any{map[string]any{"name": "whoami", "inputSchema": map[string]any{"type": "object"}}}}
			default:
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": token}}}
			}
			send(response, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
		default:
			response.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(closeIdleClients)
	origin = server.URL
	a.url = origin + "/mcp"
	return a
}

// expireAccessTokens revokes every access token issued so far (refresh tokens stay valid).
func (a *authServer) expireAccessTokens() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accessTokens = map[string]bool{}
}

func (a *authServer) snapshot() (grants []string, authorizations int, redirectURIs any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.registrations) > 0 {
		redirectURIs = a.registrations[len(a.registrations)-1]["redirect_uris"]
	}
	return append([]string(nil), a.grants...), len(a.authorizing), redirectURIs
}

func closeIdleClients() {
	noRedirects.CloseIdleConnections()
	http.DefaultClient.CloseIdleConnections()
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// redirectOf follows target like a browser up to the redirect it answers with, and returns where it redirects.
func redirectOf(t *testing.T, target string) *url.URL {
	t.Helper()
	response, err := noRedirects.Get(target)
	noError(t, err)
	_ = response.Body.Close()
	location := response.Header.Get("location")
	if location == "" {
		t.Fatalf("%s answered %d without a redirect", target, response.StatusCode)
	}
	base, _ := url.Parse(target)
	resolved, err := base.Parse(location)
	noError(t, err)
	return resolved
}

// signInHost is a server host with the MCP plugin and mcp.json holding the fake server as `docs`, plus http: requests to its
// routes as an access (any principal and origin) through a local HTTP server.
type signInHost struct {
	*mcpHost
	mu     sync.Mutex
	access p2runtime.RequestAccess
	base   string
}

func newSignInHost(t *testing.T, oauth *authServer) *signInHost {
	t.Helper()
	h := newMcpHost(t, func(string) map[string]any { return map[string]any{"docs": map[string]any{"url": oauth.url}} }, mcpOptions{leaksChecked: true})
	s := &signInHost{mcpHost: h}
	// Session s1's worker leases the servers (shared ones start with their first lease).
	worker := h.worker("s1", 1)
	noError(t, worker.setLocations(locJSON(nil, h.root)))
	routesAny, err := h.f.Root(func(ctx nexthost.Ctx) (any, error) {
		source, err := h.f.Host.Registry(ctx, p2runtime.Routes.Registry, nexthost.GetOptions{})
		if err != nil {
			return nil, err
		}
		return p2runtime.Routes.Table(source)
	})
	noError(t, err)
	routes := routesAny.(*p2runtime.KeyedTable[p2runtime.RouteHandler])
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		route, ok := p2runtime.FindRoute(routes, request.Method, request.URL.Path)
		if !ok {
			response.WriteHeader(404)
			return
		}
		s.mu.Lock()
		access := s.access
		s.mu.Unlock()
		_, _ = h.f.Root(func(ctx nexthost.Ctx) (any, error) { return nil, route(response, request, access, ctx) })
	}))
	t.Cleanup(server.Close)
	t.Cleanup(closeIdleClients)
	s.base = server.URL
	return s
}

// http is a request to the routes as principal reaching p2 through origin (loopback by default).
func (s *signInHost) http(t *testing.T, path string, principal p2runtime.Principal, origin ...string) (*http.Response, string) {
	t.Helper()
	through := "http://127.0.0.1:8788"
	if len(origin) > 0 {
		through = origin[0]
	}
	s.mu.Lock()
	s.access = p2runtime.RequestAccess{Principal: principal, Origin: p2runtime.RequestOrigin{Origin: through, Remote: !strings.HasPrefix(through, "http://127.0.0.1"), Port: 8788}}
	s.mu.Unlock()
	response, err := noRedirects.Get(s.base + path)
	noError(t, err)
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func docsOf(value delta.JsonValue) *delta.JsonObject { return instanceOf(value, "docs") }

func docsStatus(status string) func(delta.JsonValue) bool {
	return func(value delta.JsonValue) bool {
		docs := docsOf(value)
		return docs != nil && docs.Value("status") == status
	}
}

func startURL(t *testing.T, start delta.JsonValue) string {
	t.Helper()
	if objectOf(start).Value("kind") != "redirect" {
		t.Fatalf("the sign-in did not redirect: %v", start)
	}
	return stringOf(objectOf(start).Value("url"))
}

func TestA401WaitsForASignInTheOwnerViewsLoopbackSignInConnectsWithTheTokens(t *testing.T) {
	oauth := newAuthServer(t)
	h := newSignInHost(t, oauth)
	owner := h.owner()
	waiting := docsOf(owner.until("instances", docsStatus("sign-in")))
	expectEqual(t, stringOf(waiting.Value("auth")), "oauth")
	start, err := owner.Call("signIn", delta.JsonObjectOf("server", "docs"), p2test.Background)
	noError(t, err)
	expectEqual(t, stringOf(objectOf(start).Value("kind")), "redirect")
	// The answer follows its state: the flow is listed when the call resolves.
	flow := objectOf(owner.member("signIns").([]any)[0])
	expectEqual(t, stringOf(flow.Value("via")), "loopback")
	expectEqual(t, stringOf(flow.Value("status")), "waiting")
	// The browser goes to the authorization server, which redirects to the loopback callback on this machine.
	back := redirectOf(t, startURL(t, start))
	expectEqual(t, back.Hostname(), "127.0.0.1")
	expectEqual(t, back.Path, "/callback")
	page, err := http.Get(back.String())
	noError(t, err)
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	expectEqual(t, page.StatusCode, 200)
	expectMatch(t, string(body), `Signed in to docs`)
	connected := docsOf(owner.until("instances", docsStatus("connected")))
	expectJSON(t, instanceTools(connected), []string{"whoami"})
	owner.until("signIns", lenIs(0))
	// The credentials are private to the owner of the data directory.
	info, err := os.Stat(filepath.Join(h.dataDir, "mcp-auth.json"))
	noError(t, err)
	expectEqual(t, info.Mode().Perm(), os.FileMode(0o600))
	stored, err := os.ReadFile(filepath.Join(h.dataDir, "mcp-auth.json"))
	noError(t, err)
	expectMatch(t, string(stored), `access-1`)
}

func TestTheSignInRouteLoopbackForTheDeviceOwnerTheProbePageTheProbeAndTheCallbackRouteForARelayedOwner(t *testing.T) {
	oauth := newAuthServer(t)
	h := newSignInHost(t, oauth)
	owner := h.owner()
	owner.until("instances", docsStatus("sign-in"))
	// The device owner goes straight to the authorization server, returning to loopback.
	local, _ := h.http(t, "/mcp/sign-in/docs", ownerPrincipal)
	expectEqual(t, local.StatusCode, 302)
	target, err := url.Parse(local.Header.Get("location"))
	noError(t, err)
	expectMatch(t, target.Query().Get("redirect_uri"), `^http://127\.0\.0\.1:\d+/callback$`)
	// A relayed owner first gets the probe page, with a probe only its origin may read.
	probePage, html := h.http(t, "/mcp/sign-in/docs", relayOwner, brain)
	expectEqual(t, probePage.StatusCode, 200)
	found := regexp.MustCompile(`fetch\("(http://127\.0\.0\.1:\d+/[0-9a-f]+)"`).FindStringSubmatch(html)
	if found == nil {
		t.Fatal("the page does not probe the loopback listener")
	}
	probe := found[1]
	probeURL, _ := url.Parse(probe)
	expectMatch(t, probePage.Header.Get("content-security-policy"), "connect-src "+regexp.QuoteMeta(probeURL.Scheme+"://"+probeURL.Host))
	probeWith := func(target, origin string) *http.Response {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("origin", origin)
		response, err := noRedirects.Do(request)
		noError(t, err)
		_ = response.Body.Close()
		return response
	}
	answer := probeWith(probe, brain)
	expectEqual(t, answer.StatusCode, 204)
	expectEqual(t, answer.Header.Get("access-control-allow-origin"), brain)
	expectEqual(t, probeWith(probe, "https://elsewhere.example").StatusCode, 404)
	expectEqual(t, probeWith(probeURL.Scheme+"://"+probeURL.Host+"/unknown", brain).StatusCode, 404)
	// On another device the page continues with the callback route on the origin p2 is reached through.
	routed, _ := h.http(t, "/mcp/sign-in/docs?via=callback", relayOwner, brain)
	expectEqual(t, routed.StatusCode, 302)
	back := redirectOf(t, routed.Header.Get("location"))
	expectEqual(t, back.Scheme+"://"+back.Host+back.Path, brain+"/mcp/callback")
	owner.until("signIns", func(v delta.JsonValue) bool {
		list, _ := v.([]any)
		return len(list) > 0 && objectOf(list[0]).Value("via") == "route"
	})
	expectEqual(t, stringOf(objectOf(owner.member("signIns").([]any)[0]).Value("status")), "waiting")
	// The registration names p2 and the https callback.
	_, _, redirectURIs := oauth.snapshot()
	expectJSON(t, redirectURIs, []string{brain + "/mcp/callback"})
	// Nobody but the owner completes it: a browser without p2's sign-in is told to paste instead.
	anonymous, anonymousBody := h.http(t, "/mcp/callback?"+back.RawQuery, nil, brain)
	expectEqual(t, anonymous.StatusCode, 401)
	expectMatch(t, anonymousBody, `paste it in p2`)
	guest, _ := h.http(t, "/mcp/callback?"+back.RawQuery, guestPrincipal, brain)
	expectEqual(t, guest.StatusCode, 403)
	// A response of another sign-in is refused and leaves this one waiting.
	wrong, _ := h.http(t, "/mcp/callback?code=code-9&state=other", relayOwner, brain)
	expectEqual(t, wrong.StatusCode, 400)
	waiting := owner.until("signIns", lenIs(1))
	expectEqual(t, stringOf(objectOf(waiting.([]any)[0]).Value("status")), "waiting")
	done, doneBody := h.http(t, "/mcp/callback?"+back.RawQuery, relayOwner, brain)
	expectEqual(t, done.StatusCode, 200)
	expectMatch(t, doneBody, `Signed in to docs`)
	owner.until("instances", docsStatus("connected"))
}

func TestPastingTheFinalRedirectURLCompletesASignInAURLOfAnotherSignInIsRefused(t *testing.T) {
	oauth := newAuthServer(t)
	h := newSignInHost(t, oauth)
	owner := h.owner()
	owner.until("instances", docsStatus("sign-in"))
	start, err := owner.Call("signIn", delta.JsonObjectOf("server", "docs"), p2test.Background)
	noError(t, err)
	back := redirectOf(t, startURL(t, start))
	flow := objectOf(owner.until("signIns", lenIs(1)).([]any)[0])
	other := *back
	query := other.Query()
	query.Set("state", "someone-else")
	other.RawQuery = query.Encode()
	_, err = owner.Call("paste", delta.JsonObjectOf("id", flow.Value("id"), "redirectUrl", other.String()), p2test.Background)
	expectError(t, err, `different sign-in`)
	expectEqual(t, stringOf(objectOf(owner.until("signIns", lenIs(1)).([]any)[0]).Value("status")), "waiting")
	// The loopback page never loaded (the browser runs elsewhere): its address, pasted, completes the sign-in.
	_, err = owner.Call("paste", delta.JsonObjectOf("id", flow.Value("id"), "redirectUrl", back.String()), p2test.Background)
	noError(t, err)
	// The finished flow is gone when the call resolves; the instance reconnects with the tokens.
	expectEqual(t, len(owner.member("signIns").([]any)), 0)
	owner.until("instances", docsStatus("connected"))
}

func TestRefusedTokensRefreshWithoutABrowserSigningOutForgetsThemAndWaitsForASignIn(t *testing.T) {
	oauth := newAuthServer(t)
	h := newSignInHost(t, oauth)
	owner := h.owner()
	waiting := docsOf(owner.until("instances", docsStatus("sign-in")))
	start, err := owner.Call("signIn", delta.JsonObjectOf("server", "docs"), p2test.Background)
	noError(t, err)
	response, err := http.Get(redirectOf(t, startURL(t, start)).String())
	noError(t, err)
	_ = response.Body.Close()
	owner.until("instances", docsStatus("connected"))
	oauth.expireAccessTokens()
	_, err = owner.Call("restart", delta.JsonObjectOf("instance", waiting.Value("id")), p2test.Background)
	noError(t, err)
	// A restart shows the instance starting when the call resolves (a pending state, then its outcome).
	expectEqual(t, stringOf(docsOf(owner.member("instances")).Value("status")), "starting")
	owner.until("instances", docsStatus("connected"))
	grants, authorizations, _ := oauth.snapshot()
	expectJSON(t, grants, []string{"authorization_code", "refresh_token"})
	expectEqual(t, authorizations, 1)
	// A refresh token a stored sign-in still has signs in at once, without the browser.
	again, err := owner.Call("signIn", delta.JsonObjectOf("server", "docs"), p2test.Background)
	noError(t, err)
	expectJSON(t, again, map[string]string{"kind": "authorized"})
	_, err = owner.Call("signOut", delta.JsonObjectOf("server", "docs"), p2test.Background)
	noError(t, err)
	// Pi reads the status the moment the call resolves; here the instances list of the connection a sign-in's own restart
	// finished a moment ago can reach this client just after the answer (the runtime's followed changes are applied in the
	// loop, the answer is sent from the call's goroutine), so the reconnect is waited for within a moment, not read at once.
	owner.until("instances", func(v delta.JsonValue) bool { return docsOf(v).Value("status") != "connected" })
	owner.until("instances", docsStatus("sign-in"))
	stored, err := os.ReadFile(filepath.Join(h.dataDir, "mcp-auth.json"))
	noError(t, err)
	if regexp.MustCompile(`access-`).Match(stored) {
		t.Fatalf("the credentials still hold tokens: %s", stored)
	}
}

func TestGuestsAndAnonymousVisitorsGetNoSignInRouteUnknownServersFail(t *testing.T) {
	oauth := newAuthServer(t)
	h := newSignInHost(t, oauth)
	guest, _ := h.http(t, "/mcp/sign-in/docs", guestPrincipal, brain)
	expectEqual(t, guest.StatusCode, 403)
	anonymous, _ := h.http(t, "/mcp/sign-in/docs", nil, brain)
	expectEqual(t, anonymous.StatusCode, 401)
	unknown, body := h.http(t, "/mcp/sign-in/nope", ownerPrincipal)
	expectEqual(t, unknown.StatusCode, 502)
	expectMatch(t, body, `No MCP server &quot;nope&quot; signs in with OAuth`)
	owner := h.owner()
	_, err := owner.Call("signIn", delta.JsonObjectOf("server", "nope"), p2test.Background)
	expectError(t, err, `No MCP server "nope" signs in with OAuth`)
	_ = context.Background
}

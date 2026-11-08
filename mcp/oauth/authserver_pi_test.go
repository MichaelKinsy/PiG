package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// authServer is startAuthorizationServer of packages/mcp/test/authorization-server.ts (Pi bb266bf35, 797e122cb): the authorization server
// of an MCP server at `<origin>/mcp`: discovery, dynamic client registration, PKCE, and rotating refresh tokens. Token responses name no scope.
type authServer struct {
	t         *testing.T
	serverURL string

	mu             sync.Mutex
	registrations  []map[string]any
	authorizations []url.Values
	tokenRequests  []url.Values
	challenges     map[string]string
	refreshTokens  map[string]bool
	stall          map[string]bool
	failure        *refreshFailure
	issued         int
	options        authServerOptions
}

type authServerOptions struct{ cimd, issParameter bool }

type refreshFailure struct {
	code      string
	onRequest func()
}

func startAuthorizationServer(t *testing.T, options authServerOptions) *authServer {
	t.Helper()
	s := &authServer{t: t, challenges: map[string]string{}, refreshTokens: map[string]bool{}, stall: map[string]bool{}, options: options}
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, origin string) { s.handle(w, r, origin) })
	s.serverURL = origin + "/mcp"
	return s
}

func (s *authServer) issueTokens() map[string]any {
	s.issued++
	refresh := "refresh-" + itoa(s.issued)
	s.refreshTokens[refresh] = true
	return map[string]any{"access_token": "access-" + itoa(s.issued), "refresh_token": refresh, "token_type": "Bearer"}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (s *authServer) handle(w http.ResponseWriter, r *http.Request, origin string) {
	s.mu.Lock()
	stalled := s.stall[r.URL.Path]
	s.mu.Unlock()
	if stalled {
		// The request is read first, so the server notices when the client gives up.
		_ = readAll(r)
		<-r.Context().Done()
		return
	}
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		jsonResponse(w, 200, map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}})
	case "/.well-known/oauth-authorization-server":
		metadata := map[string]any{
			"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token",
			"registration_endpoint": origin + "/register", "response_types_supported": []string{"code"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		}
		if s.options.cimd {
			metadata["client_id_metadata_document_supported"] = true
		}
		if s.options.issParameter {
			metadata["authorization_response_iss_parameter_supported"] = true
		}
		jsonResponse(w, 200, metadata)
	case "/register":
		var metadata map[string]any
		_ = json.Unmarshal([]byte(readAll(r)), &metadata)
		s.mu.Lock()
		s.registrations = append(s.registrations, metadata)
		id := "client-" + itoa(len(s.registrations))
		s.mu.Unlock()
		metadata["client_id"] = id
		jsonResponse(w, 201, metadata)
	case "/authorize":
		query := r.URL.Query()
		s.mu.Lock()
		s.authorizations = append(s.authorizations, query)
		code := "code-" + itoa(len(s.authorizations))
		s.challenges[code] = query.Get("code_challenge")
		s.mu.Unlock()
		redirect, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		values := redirect.Query()
		values.Set("code", code)
		values.Set("state", query.Get("state"))
		if s.options.issParameter {
			values.Set("iss", origin)
		}
		redirect.RawQuery = values.Encode()
		w.Header().Set("Location", redirect.String())
		w.WriteHeader(302)
	case "/token":
		params, _ := url.ParseQuery(readAll(r))
		s.mu.Lock()
		s.tokenRequests = append(s.tokenRequests, params)
		if params.Get("grant_type") == "authorization_code" {
			code := params.Get("code")
			sum := sha256.Sum256([]byte(params.Get("code_verifier")))
			if s.challenges[code] != base64.RawURLEncoding.EncodeToString(sum[:]) {
				s.mu.Unlock()
				jsonResponse(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(s.challenges, code)
			tokens := s.issueTokens()
			s.mu.Unlock()
			jsonResponse(w, 200, tokens)
			return
		}
		failure := s.failure
		s.failure = nil
		s.mu.Unlock()
		if failure != nil {
			if failure.onRequest != nil {
				failure.onRequest()
			}
			jsonResponse(w, 400, map[string]any{"error": failure.code})
			return
		}
		s.mu.Lock()
		known := s.refreshTokens[params.Get("refresh_token")]
		delete(s.refreshTokens, params.Get("refresh_token"))
		var tokens map[string]any
		if known {
			tokens = s.issueTokens()
		}
		s.mu.Unlock()
		if !known {
			jsonResponse(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
		jsonResponse(w, 200, tokens)
	default:
		w.WriteHeader(404)
	}
}

// revokeRefreshTokens makes every refresh fail with `invalid_grant`, like a revoked grant.
func (s *authServer) revokeRefreshTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshTokens = map[string]bool{}
}

// failNextRefresh answers the next refresh with the OAuth error, after running onRequest.
func (s *authServer) failNextRefresh(code string, onRequest func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = &refreshFailure{code: code, onRequest: onRequest}
}

func (s *authServer) stallPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall[path] = true
}

func (s *authServer) registered() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.registrations...)
}

func (s *authServer) authorized() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.authorizations...)
}

func (s *authServer) tokens() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.tokenRequests...)
}

func (s *authServer) grantTypes() []string {
	var out []string
	for _, request := range s.tokens() {
		out = append(out, request.Get("grant_type"))
	}
	return out
}

// memoryStore is an in-memory store whose refresh lock only runs fn: there is one process.
type memoryStore struct{ oauth.MemoryOAuthStateStore }

func (*memoryStore) WithRefreshLock(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// recordingStore is an in-memory store that records whether each save happened while the refresh lock was held. With lockError, taking the lock
// fails with it; hold stands for another process holding the lock until the returned function releases it.
type recordingStore struct {
	memory    oauth.MemoryOAuthStateStore
	lockError error

	mu      sync.Mutex
	saves   []recordedSave
	locked  bool
	held    chan struct{}
	started int
	runs    sync.WaitGroup
}

type recordedSave struct {
	locked      bool
	accessToken string
}

func (s *recordingStore) Load(ctx context.Context) (*oauth.McpOAuthState, error) {
	return s.memory.Load(ctx)
}

func (s *recordingStore) Save(ctx context.Context, state oauth.McpOAuthState) error {
	s.mu.Lock()
	save := recordedSave{locked: s.locked}
	if state.Tokens != nil {
		save.accessToken = state.Tokens.AccessToken
	}
	s.saves = append(s.saves, save)
	s.mu.Unlock()
	return s.memory.Save(ctx, state)
}

func (s *recordingStore) WithRefreshLock(ctx context.Context, fn func(context.Context) error) error {
	s.mu.Lock()
	held := s.held
	// Counted under mu: waitRuns reads the count under mu first, so the first Add is ordered before Wait as sync.WaitGroup requires.
	s.started++
	s.runs.Add(1)
	s.mu.Unlock()
	defer s.runs.Done()
	if held != nil {
		<-held
	}
	if s.lockError != nil {
		return s.lockError
	}
	s.mu.Lock()
	s.locked = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.locked = false
		s.mu.Unlock()
	}()
	return fn(ctx)
}

// waitRuns waits for every WithRefreshLock call that has started, and fails the test if none did.
func (s *recordingStore) waitRuns(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started == 0 {
		t.Fatal("no lock wait started")
	}
	s.runs.Wait()
}

// hold makes the next lock waits stand for another process holding the lock, until the returned function releases it.
func (s *recordingStore) hold() func() {
	release := make(chan struct{})
	s.mu.Lock()
	s.held = release
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(release) }) }
}

func (s *recordingStore) isLocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked
}

func (s *recordingStore) accessTokenSaves(token string) []recordedSave {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedSave
	for _, save := range s.saves {
		if save.accessToken == token {
			out = append(out, save)
		}
	}
	return out
}

// authorizeInBrowser follows the authorization URL like a browser, up to the redirect back to the application.
func authorizeInBrowser(t *testing.T, authorizationURL string) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	return response.Header.Get("Location")
}

// routeResponse is the response a callback route reads from the query of the redirect.
func routeResponse(t *testing.T, location string) oauth.McpAuthorizationResponse {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	return oauth.McpAuthorizationResponse{Code: query.Get("code"), State: query.Get("state"), Iss: query.Get("iss")}
}

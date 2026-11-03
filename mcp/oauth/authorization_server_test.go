package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/authorization-server.ts.

// authorizationServer is the authorization server of an MCP server at `<origin>/mcp`: discovery, dynamic client
// registration, PKCE, and rotating refresh tokens. Token responses name no scope. `cimd` advertises Client ID Metadata
// Documents, `issParameter` the `iss` parameter of authorization responses. Paths in `stall` never answer.
type authorizationServer struct {
	ServerURL string

	cimd, issParameter bool
	httpServer         *httptest.Server
	release            chan struct{}

	mu             sync.Mutex
	registrations  []map[string]any
	authorizations []url.Values
	tokenRequests  []url.Values
	challenges     map[string]string
	refreshTokens  map[string]bool
	stall          map[string]bool
	refreshFailure *refreshFailure
	issued         int
}

type refreshFailure struct {
	code      string
	onRequest func()
}

type authorizationServerOptions struct{ cimd, issParameter bool }

func startAuthorizationServer(t *testing.T, options authorizationServerOptions) *authorizationServer {
	t.Helper()
	s := &authorizationServer{
		cimd: options.cimd, issParameter: options.issParameter, release: make(chan struct{}),
		challenges: map[string]string{}, refreshTokens: map[string]bool{}, stall: map[string]bool{},
	}
	s.httpServer = httptest.NewServer(http.HandlerFunc(s.handle))
	s.ServerURL = s.httpServer.URL + "/mcp"
	t.Cleanup(func() {
		close(s.release)
		s.httpServer.CloseClientConnections()
		s.httpServer.Close()
	})
	return s
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// issueTokens must be called with s.mu held.
func (s *authorizationServer) issueTokens() map[string]any {
	s.issued++
	refresh := "refresh-" + strconv.Itoa(s.issued)
	s.refreshTokens[refresh] = true
	return map[string]any{"access_token": "access-" + strconv.Itoa(s.issued), "refresh_token": refresh, "token_type": "Bearer"}
}

func (s *authorizationServer) handle(w http.ResponseWriter, r *http.Request) {
	origin := s.httpServer.URL
	s.mu.Lock()
	stalled := s.stall[r.URL.Path]
	s.mu.Unlock()
	if stalled {
		select {
		case <-s.release:
		case <-r.Context().Done():
		}
		return
	}
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		writeJSON(w, 200, map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}})
	case "/.well-known/oauth-authorization-server":
		metadata := map[string]any{
			"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token",
			"registration_endpoint": origin + "/register", "response_types_supported": []string{"code"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		}
		if s.cimd {
			metadata["client_id_metadata_document_supported"] = true
		}
		if s.issParameter {
			metadata["authorization_response_iss_parameter_supported"] = true
		}
		writeJSON(w, 200, metadata)
	case "/register":
		body, _ := io.ReadAll(r.Body)
		var metadata map[string]any
		_ = json.Unmarshal(body, &metadata)
		s.mu.Lock()
		s.registrations = append(s.registrations, metadata)
		id := len(s.registrations)
		s.mu.Unlock()
		metadata["client_id"] = "client-" + strconv.Itoa(id)
		writeJSON(w, 201, metadata)
	case "/authorize":
		query := r.URL.Query()
		s.mu.Lock()
		s.authorizations = append(s.authorizations, query)
		code := "code-" + strconv.Itoa(len(s.authorizations))
		s.challenges[code] = query.Get("code_challenge")
		s.mu.Unlock()
		redirect, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		values := redirect.Query()
		values.Set("code", code)
		values.Set("state", query.Get("state"))
		if s.issParameter {
			values.Set("iss", origin)
		}
		redirect.RawQuery = values.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound) //nolint:gosec // the test server redirects to the redirect_uri it was given, like the server it stands for
	case "/token":
		body, _ := io.ReadAll(r.Body)
		params, _ := url.ParseQuery(string(body))
		s.mu.Lock()
		s.tokenRequests = append(s.tokenRequests, params)
		if params.Get("grant_type") == "authorization_code" {
			sum := sha256.Sum256([]byte(params.Get("code_verifier")))
			code := params.Get("code")
			if s.challenges[code] != base64.RawURLEncoding.EncodeToString(sum[:]) {
				s.mu.Unlock()
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(s.challenges, code)
			tokens := s.issueTokens()
			s.mu.Unlock()
			writeJSON(w, 200, tokens)
			return
		}
		failure := s.refreshFailure
		s.refreshFailure = nil
		s.mu.Unlock()
		if failure != nil {
			if failure.onRequest != nil {
				failure.onRequest()
			}
			writeJSON(w, 400, map[string]any{"error": failure.code})
			return
		}
		s.mu.Lock()
		presented := params.Get("refresh_token")
		valid := s.refreshTokens[presented]
		delete(s.refreshTokens, presented)
		var tokens map[string]any
		if valid {
			tokens = s.issueTokens()
		}
		s.mu.Unlock()
		if !valid {
			writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
		writeJSON(w, 200, tokens)
	default:
		w.WriteHeader(404)
	}
}

// Registrations are the client metadata of dynamic client registrations.
func (s *authorizationServer) Registrations() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.registrations)
}

// Authorizations are the query parameters of authorization requests.
func (s *authorizationServer) Authorizations() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.authorizations)
}

// TokenRequests are the parameters of token requests.
func (s *authorizationServer) TokenRequests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tokenRequests)
}

// GrantTypes are the `grant_type` parameters of the token requests.
func (s *authorizationServer) GrantTypes() []string {
	var types []string
	for _, request := range s.TokenRequests() {
		types = append(types, request.Get("grant_type"))
	}
	return types
}

// Stall makes the server accept requests to path and never answer them.
func (s *authorizationServer) Stall(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall[path] = true
}

// RevokeRefreshTokens makes every refresh fail with `invalid_grant`, like a revoked grant.
func (s *authorizationServer) RevokeRefreshTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.refreshTokens)
}

// FailNextRefresh answers the next refresh with the OAuth error code, after running onRequest.
func (s *authorizationServer) FailNextRefresh(code string, onRequest func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshFailure = &refreshFailure{code: code, onRequest: onRequest}
}

// memoryServerStore is an in-memory store whose refresh lock only runs fn: there is one process.
type memoryServerStore struct{ oauth.MemoryOAuthStateStore }

func (*memoryServerStore) WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func memoryStore() oauth.McpOAuthServerStore { return &memoryServerStore{} }

type recordedSave struct {
	locked      bool
	accessToken string
}

// recordingStore is an in-memory store that records whether each save happened while the refresh lock was held. With
// lockFails, taking the lock fails like a lock that timed out.
type recordingStore struct {
	memory    oauth.MemoryOAuthStateStore
	lockFails bool

	mu     sync.Mutex
	saves  []recordedSave
	locked bool
}

func (s *recordingStore) Load(ctx context.Context) (*oauth.McpOAuthState, error) {
	return s.memory.Load(ctx)
}

func (s *recordingStore) Save(ctx context.Context, state oauth.McpOAuthState) error {
	token := ""
	if state.Tokens != nil {
		token = state.Tokens.AccessToken
	}
	s.mu.Lock()
	s.saves = append(s.saves, recordedSave{locked: s.locked, accessToken: token})
	s.mu.Unlock()
	return s.memory.Save(ctx, state)
}

func (s *recordingStore) WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.lockFails {
		return errors.New("Lock file is already being held")
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

func (s *recordingStore) Saves() []recordedSave {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.saves)
}

// authorizeInBrowser follows the authorization URL like a browser, up to the redirect back to the application.
func authorizeInBrowser(t *testing.T, authorizationURL string) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.Header.Get("Location")
}

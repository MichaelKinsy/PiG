//go:build !pig_strip_mcp

package coding

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// mcpOAuthServer is startMcpOAuthServer of test/suite/mcp-oauth-server.ts: an
// MCP server protected by OAuth, with its own authorization server (discovery,
// DCR, PKCE, refresh).
type mcpOAuthServer struct {
	URL    string
	server *httptest.Server

	mu            sync.Mutex
	log           []string
	validTokens   map[string]bool
	refreshTokens map[string]bool
	challenges    map[string]string
	issued        int
	origin        string
	// deletes are the access tokens of session DELETE requests: the MCP endpoint assigns a session, so closing a
	// connection sends one.
	deletes []string
	// stall holds the paths that accept requests and never answer them, like an unresponsive server. stalled are those
	// requests; closed ends once the client gave up on one.
	stall   map[string]bool
	stalled []*stalledRequest
	// registrations are the client metadata of dynamic client registrations (`registrations` of mcp-oauth-server.ts).
	registrations []map[string]any
}

// stalledRequest is a request to a stalled path.
type stalledRequest struct {
	path string
	// closed is closed when the client gave up on the request (the connection ended).
	closed chan struct{}
}

// stallPath makes the path accept requests and never answer them.
func (s *mcpOAuthServer) stallPath(path string) {
	s.mu.Lock()
	s.stall[path] = true
	s.mu.Unlock()
}

// stalledRequests are the requests to stalled paths so far.
func (s *mcpOAuthServer) stalledRequests() []*stalledRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.stalled)
}

// deleteTokens are the access tokens of the session DELETE requests so far.
func (s *mcpOAuthServer) deleteTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.deletes)
}

// registrationClientNames are the `client_name` values of the dynamic client registrations so far.
func (s *mcpOAuthServer) registrationClientNames() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := []any{}
	for _, metadata := range s.registrations {
		names = append(names, metadata["client_name"])
	}
	return names
}

func (s *mcpOAuthServer) logEntries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

func (s *mcpOAuthServer) push(entry string) {
	s.mu.Lock()
	s.log = append(s.log, entry)
	s.mu.Unlock()
}

// expireAccessTokens simulates access token expiry.
func (s *mcpOAuthServer) expireAccessTokens() {
	s.mu.Lock()
	s.validTokens = map[string]bool{}
	s.mu.Unlock()
}

func (s *mcpOAuthServer) issueTokens() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued++
	access, refresh := fmt.Sprintf("access-%d", s.issued), fmt.Sprintf("refresh-%d", s.issued)
	s.validTokens[access] = true
	s.refreshTokens[refresh] = true
	return map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600}
}

func mcpWriteJSON(w http.ResponseWriter, status int, body any, headers map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func mcpReadBody(r *http.Request) string {
	data, _ := io.ReadAll(r.Body)
	return string(data)
}

func (s *mcpOAuthServer) handleMcp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.mu.Lock()
		s.deletes = append(s.deletes, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		s.mu.Unlock()
	}
	if r.Method != http.MethodPost {
		if r.Method == http.MethodGet {
			w.WriteHeader(405)
		} else {
			w.WriteHeader(200)
		}
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	valid := token != "" && s.validTokens[token]
	s.mu.Unlock()
	if !valid {
		shown := token
		if shown == "" {
			shown = "none"
		}
		s.push("401 " + shown)
		w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, s.origin))
		w.WriteHeader(401)
		return
	}
	var message struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
	}
	_ = json.Unmarshal([]byte(mcpReadBody(r)), &message)
	if message.ID == nil {
		w.WriteHeader(202)
		return
	}
	var result any
	switch message.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": mcp.LatestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "issues", "version": "1.0.0"}}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{"name": "whoami", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
	case "tools/call":
		s.push("call " + token)
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "token " + token}}}
	default:
		result = map[string]any{}
	}
	mcpWriteJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result}, map[string]string{"Mcp-Session-Id": "session-1"})
}

func (s *mcpOAuthServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	stalls := s.stall[r.URL.Path]
	s.mu.Unlock()
	if stalls {
		// The server notices a closed connection once the request is read.
		_, _ = io.Copy(io.Discard, r.Body)
		request := &stalledRequest{path: r.URL.Path, closed: make(chan struct{})}
		s.mu.Lock()
		s.stalled = append(s.stalled, request)
		s.mu.Unlock()
		<-r.Context().Done()
		close(request.closed)
		return
	}
	origin := s.origin
	switch r.URL.Path {
	case "/mcp":
		s.handleMcp(w, r)
	case "/.well-known/oauth-protected-resource/mcp":
		mcpWriteJSON(w, 200, map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}}, nil)
	case "/.well-known/oauth-authorization-server":
		mcpWriteJSON(w, 200, map[string]any{
			"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token",
			"registration_endpoint": origin + "/register", "response_types_supported": []string{"code"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		}, nil)
	case "/register":
		var metadata map[string]any
		_ = json.Unmarshal([]byte(mcpReadBody(r)), &metadata)
		s.push("register")
		s.mu.Lock()
		s.registrations = append(s.registrations, metadata)
		s.mu.Unlock()
		metadata["client_id"] = "client-1"
		mcpWriteJSON(w, 201, metadata, nil)
	case "/authorize":
		s.mu.Lock()
		code := fmt.Sprintf("code-%d", len(s.challenges)+1)
		s.challenges[code] = r.URL.Query().Get("code_challenge")
		s.mu.Unlock()
		redirect, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
		query := redirect.Query()
		query.Set("code", code)
		query.Set("state", r.URL.Query().Get("state"))
		redirect.RawQuery = query.Encode()
		w.Header().Set("Location", redirect.String())
		w.WriteHeader(302)
	case "/token":
		params, _ := url.ParseQuery(mcpReadBody(r))
		if params.Get("grant_type") == "authorization_code" {
			s.mu.Lock()
			challenge := s.challenges[params.Get("code")]
			s.mu.Unlock()
			digest := sha256.Sum256([]byte(params.Get("code_verifier")))
			if challenge == "" || challenge != base64.RawURLEncoding.EncodeToString(digest[:]) {
				mcpWriteJSON(w, 400, map[string]any{"error": "invalid_grant"}, nil)
				return
			}
			s.mu.Lock()
			delete(s.challenges, params.Get("code"))
			s.mu.Unlock()
			s.push("token code")
			mcpWriteJSON(w, 200, s.issueTokens(), nil)
			return
		}
		refresh := params.Get("refresh_token")
		s.mu.Lock()
		known := s.refreshTokens[refresh]
		delete(s.refreshTokens, refresh)
		s.mu.Unlock()
		if !known {
			mcpWriteJSON(w, 400, map[string]any{"error": "invalid_grant"}, nil)
			return
		}
		s.push("token refresh")
		mcpWriteJSON(w, 200, s.issueTokens(), nil)
	default:
		w.WriteHeader(404)
	}
}

func startMcpOAuthServer(t *testing.T) *mcpOAuthServer {
	t.Helper()
	s := &mcpOAuthServer{validTokens: map[string]bool{}, refreshTokens: map[string]bool{}, challenges: map[string]string{}, stall: map[string]bool{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	s.origin = s.server.URL
	s.URL = s.origin + "/mcp"
	t.Cleanup(func() {
		s.server.CloseClientConnections()
		s.server.Close()
	})
	return s
}

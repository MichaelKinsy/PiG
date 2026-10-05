package mcpext_test

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

// oauthMcpServer is startOAuthMcpServer of test/suite/mcp-oauth-server.ts: an
// MCP server protected by OAuth, with its own authorization server (discovery,
// DCR, PKCE, refresh).
type oauthMcpServer struct {
	URL     string
	server  *httptest.Server
	options oauthMcpServerOptions

	mu            sync.Mutex
	log           []string
	validTokens   map[string]bool
	refreshTokens map[string]bool
	challenges    map[string]string
	issued        int
	origin        string
	// registrations are the client metadata of dynamic client registrations (`registrations` of mcp-oauth-server.ts).
	registrations []map[string]any
	// authorizations are the query parameters of authorization requests.
	authorizations []url.Values
	// tokenRequests are the parameters of token requests.
	tokenRequests []url.Values
}

func (s *oauthMcpServer) recordedAuthorizations() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.authorizations)
}

func (s *oauthMcpServer) recordedTokenRequests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tokenRequests)
}

func (s *oauthMcpServer) registrationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.registrations)
}

// registrationClientNames are the `client_name` values of the dynamic client registrations so far.
func (s *oauthMcpServer) registrationClientNames() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := []any{}
	for _, metadata := range s.registrations {
		names = append(names, metadata["client_name"])
	}
	return names
}

func (s *oauthMcpServer) logEntries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

func (s *oauthMcpServer) push(entry string) {
	s.mu.Lock()
	s.log = append(s.log, entry)
	s.mu.Unlock()
}

// expireAccessTokens simulates access token expiry.
func (s *oauthMcpServer) expireAccessTokens() {
	s.mu.Lock()
	s.validTokens = map[string]bool{}
	s.mu.Unlock()
}

func (s *oauthMcpServer) issueTokens() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued++
	access, refresh := fmt.Sprintf("access-%d", s.issued), fmt.Sprintf("refresh-%d", s.issued)
	s.validTokens[access] = true
	s.refreshTokens[refresh] = true
	return map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600}
}

func writeJSONStatus(w http.ResponseWriter, status int, body any, headers map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func readRequestBody(r *http.Request) string {
	data, _ := io.ReadAll(r.Body)
	return string(data)
}

func (s *oauthMcpServer) handleMcp(w http.ResponseWriter, r *http.Request) {
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
	_ = json.Unmarshal([]byte(readRequestBody(r)), &message)
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
	writeJSONStatus(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result}, nil)
}

func (s *oauthMcpServer) handle(w http.ResponseWriter, r *http.Request) {
	origin := s.origin
	switch r.URL.Path {
	case "/mcp":
		s.handleMcp(w, r)
	case "/.well-known/oauth-protected-resource/mcp":
		writeJSONStatus(w, 200, map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}}, nil)
	case "/.well-known/oauth-authorization-server":
		metadata := map[string]any{
			"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token",
			"registration_endpoint": origin + "/register", "response_types_supported": []string{"code"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		}
		if s.options.Cimd {
			metadata["client_id_metadata_document_supported"] = true
		}
		if s.options.IssParameter {
			metadata["authorization_response_iss_parameter_supported"] = true
		}
		writeJSONStatus(w, 200, metadata, nil)
	case "/register":
		var metadata map[string]any
		_ = json.Unmarshal([]byte(readRequestBody(r)), &metadata)
		s.push("register")
		s.mu.Lock()
		s.registrations = append(s.registrations, metadata)
		s.mu.Unlock()
		metadata["client_id"] = "client-1"
		writeJSONStatus(w, 201, metadata, nil)
	case "/authorize":
		s.mu.Lock()
		s.authorizations = append(s.authorizations, r.URL.Query())
		code := fmt.Sprintf("code-%d", len(s.challenges)+1)
		s.challenges[code] = r.URL.Query().Get("code_challenge")
		s.mu.Unlock()
		redirect, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
		if s.options.RedirectPath != "" {
			redirect.Path = s.options.RedirectPath
		}
		query := redirect.Query()
		query.Set("code", code)
		query.Set("state", r.URL.Query().Get("state"))
		iss := s.options.Iss
		if iss == "" && s.options.IssParameter {
			iss = origin
		}
		if iss != "" {
			query.Set("iss", iss)
		}
		redirect.RawQuery = query.Encode()
		w.Header().Set("Location", redirect.String())
		w.WriteHeader(302)
	case "/token":
		params, _ := url.ParseQuery(readRequestBody(r))
		s.mu.Lock()
		s.tokenRequests = append(s.tokenRequests, params)
		s.mu.Unlock()
		if params.Get("grant_type") == "authorization_code" {
			s.mu.Lock()
			challenge := s.challenges[params.Get("code")]
			s.mu.Unlock()
			digest := sha256.Sum256([]byte(params.Get("code_verifier")))
			if challenge == "" || challenge != base64.RawURLEncoding.EncodeToString(digest[:]) {
				writeJSONStatus(w, 400, map[string]any{"error": "invalid_grant"}, nil)
				return
			}
			s.mu.Lock()
			delete(s.challenges, params.Get("code"))
			s.mu.Unlock()
			s.push("token code")
			writeJSONStatus(w, 200, s.issueTokens(), nil)
			return
		}
		refresh := params.Get("refresh_token")
		s.mu.Lock()
		known := s.refreshTokens[refresh]
		delete(s.refreshTokens, refresh)
		s.mu.Unlock()
		if !known {
			writeJSONStatus(w, 400, map[string]any{"error": "invalid_grant"}, nil)
			return
		}
		s.push("token refresh")
		writeJSONStatus(w, 200, s.issueTokens(), nil)
	default:
		w.WriteHeader(404)
	}
}

// oauthMcpServerOptions are the options of startOAuthMcpServer.
type oauthMcpServerOptions struct {
	// Iss is sent as the `iss` parameter of authorization responses (RFC 9207).
	Iss string
	// IssParameter advertises that parameter and sends the server's issuer.
	IssParameter bool
	// Cimd advertises Client ID Metadata Documents.
	Cimd bool
	// RedirectPath replaces the path of the redirect URI, like a mixed-up authorization server.
	RedirectPath string
}

func startOAuthMcpServer(t *testing.T, options ...oauthMcpServerOptions) *oauthMcpServer {
	t.Helper()
	s := &oauthMcpServer{validTokens: map[string]bool{}, refreshTokens: map[string]bool{}, challenges: map[string]string{}}
	for _, o := range options {
		s.options = o
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	s.origin = s.server.URL
	s.URL = s.origin + "/mcp"
	t.Cleanup(func() {
		s.server.CloseClientConnections()
		s.server.Close()
	})
	return s
}

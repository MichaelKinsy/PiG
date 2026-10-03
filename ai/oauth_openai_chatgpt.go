package ai

// Ports packages/ai/src/auth/oauth/openai-chatgpt.ts.
//
// OpenAI Responses API token sharing through Sign in with ChatGPT. This public-client flow uses no client secret and
// sends the resulting user access token directly to api.openai.com.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

const (
	// Every login registers a new client with this ID; OpenAI returns the issued client ID in the callback.
	chatgptDynamicClientID  = "dynamic_agent_client"
	chatgptAgentNameHint    = "Pi"
	chatgptAuthorizeURL     = "https://auth.openai.com/api/accounts/authorize"
	chatgptTokenRequestURL  = "https://auth.openai.com/api/accounts/oauth/token"
	chatgptResource         = "https://api.openai.com/v1"
	chatgptCallbackPort     = 1455
	chatgptCallbackPath     = "/auth/callback"
	chatgptRedirectURI      = "http://127.0.0.1:1455/auth/callback"
	chatgptDirectTokenScope = "chatgpt.tokens.use.direct"
	chatgptScope            = "openid profile email offline_access resource.invoke " + chatgptDirectTokenScope
	// chatgptExpiryMargin refreshes this long before the real expiry so a request never starts with a token about to
	// expire.
	chatgptExpiryMargin = 3 * 60 * 1000
	// OpenAIChatGPTOAuthName is the OAuth method name of the openai provider.
	OpenAIChatGPTOAuthName = "OpenAI (ChatGPT subscription)"
)

var chatgptUUIDPattern = lazyregexp.New(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type chatgptAuthorizationResult struct {
	code     string
	clientID string
}

type chatgptTokenBody map[string]json.RawMessage

func chatgptRandomValue() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func chatgptResultFromCallback(query url.Values, expectedState string) (chatgptAuthorizationResult, error) {
	code := query.Get("code")
	if code == "" {
		return chatgptAuthorizationResult{}, errors.New("Missing authorization code")
	}
	state := query.Get("state")
	if state == "" {
		return chatgptAuthorizationResult{}, errors.New("Missing OAuth state")
	}
	if state != expectedState {
		return chatgptAuthorizationResult{}, errors.New("OAuth state mismatch")
	}
	clientID := trimJSWhitespace(query.Get("client_id"))
	if clientID == "" {
		return chatgptAuthorizationResult{}, errors.New("OpenAI OAuth registration callback did not contain an issued client ID")
	}
	return chatgptAuthorizationResult{code: code, clientID: clientID}, nil
}

func chatgptResultFromManualInput(input, expectedState string) (chatgptAuthorizationResult, error) {
	pasted, err := url.Parse(trimJSWhitespace(input))
	// new URL throws for a relative reference and for an http(s), ws(s) or ftp URL without a host. Any other scheme, as in
	// a pasted "localhost:1455/..." without "http://", parses with an opaque origin and then fails the origin check.
	if err != nil || pasted.Scheme == "" || (pasted.Host == "" && pasted.Opaque == "" && slices.Contains([]string{"http", "https", "ws", "wss", "ftp"}, pasted.Scheme)) {
		return chatgptAuthorizationResult{}, errors.New("Paste the full callback URL from the browser")
	}
	expected, _ := url.Parse(chatgptRedirectURI)
	if pasted.Scheme+"://"+pasted.Host != expected.Scheme+"://"+expected.Host || pasted.Path != expected.Path {
		return chatgptAuthorizationResult{}, fmt.Errorf("The pasted callback URL must start with %s", chatgptRedirectURI)
	}
	query := pasted.Query()
	if failure := query.Get("error"); failure != "" {
		return chatgptAuthorizationResult{}, fmt.Errorf("ChatGPT authorization failed: %s", failure)
	}
	return chatgptResultFromCallback(query, expectedState)
}

// chatgptCallbackServer serves the browser redirect. An invalid callback gets an error page and the server keeps
// waiting; the first valid callback or provider error settles it.
type chatgptCallbackServer struct {
	server  *http.Server
	served  chan struct{}
	result  chan chatgptAuthorizationResultOrError
	settled sync.Once
}

type chatgptAuthorizationResultOrError struct {
	result chatgptAuthorizationResult
	err    error
}

func (s *chatgptCallbackServer) settle(result chatgptAuthorizationResult, err error) {
	s.settled.Do(func() { s.result <- chatgptAuthorizationResultOrError{result, err} })
}

func startChatGPTCallbackServer(expectedState string) (*chatgptCallbackServer, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(oauthCallbackHost(), strconv.Itoa(chatgptCallbackPort)))
	if err != nil {
		return nil, err
	}
	s := &chatgptCallbackServer{served: make(chan struct{}), result: make(chan chatgptAuthorizationResultOrError, 1)}
	send := writeOAuthPage
	s.server = &http.Server{ReadHeaderTimeout: oauthCallbackHeaderTimeout, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != chatgptCallbackPath {
			send(w, http.StatusNotFound, OAuthErrorHTML("Callback route not found.", ""))
			return
		}
		query := r.URL.Query()
		if failure := query.Get("error"); failure != "" {
			send(w, http.StatusBadRequest, OAuthErrorHTML("ChatGPT was not connected.", "Error: "+failure))
			s.settle(chatgptAuthorizationResult{}, fmt.Errorf("ChatGPT authorization failed: %s", failure))
			return
		}
		result, err := chatgptResultFromCallback(query, expectedState)
		if err != nil {
			send(w, http.StatusBadRequest, OAuthErrorHTML(err.Error(), ""))
			return
		}
		send(w, http.StatusOK, OAuthSuccessHTML("ChatGPT authentication completed. You can close this window."))
		s.settle(result, nil)
	})}
	go func() {
		defer close(s.served)
		_ = s.server.Serve(listener)
	}()
	return s, nil
}

// close stops the listener and drops every connection. Browsers open spare connections ahead of time, and one that has
// not sent a request yet stays attached to this server. A later login in the same process starts a new server with a new
// state, but the browser may send that login's callback over the spare connection; this server would then handle it and
// reject it with "OAuth state mismatch", and the new login would never see it.
func (s *chatgptCallbackServer) close() {
	_ = s.server.Close()
	<-s.served
}

func requestChatGPTToken(ctx context.Context, form url.Values) (chatgptTokenBody, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptTokenRequestURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("accept", "application/json")
	request.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := string(body)
		if detail == "" {
			detail = http.StatusText(response.StatusCode)
		}
		return nil, fmt.Errorf("OpenAI OAuth token request failed (%d): %s", response.StatusCode, detail)
	}
	token, ok := jsonObjectOf(body)
	if !ok {
		return nil, errors.New("OpenAI OAuth token response must be an object")
	}
	return token, nil
}

func requireChatGPTTokenString(token chatgptTokenBody, field string) (string, error) {
	var value string
	if json.Unmarshal(token[field], &value) != nil || trimJSWhitespace(value) == "" {
		return "", fmt.Errorf("OpenAI OAuth token response has invalid %s", field)
	}
	return value, nil
}

func chatgptCredentialFromTokenResponse(token chatgptTokenBody, clientID string) (OAuthCredentials, error) {
	access, err := requireChatGPTTokenString(token, "access_token")
	if err != nil {
		return OAuthCredentials{}, err
	}
	refresh, err := requireChatGPTTokenString(token, "refresh_token")
	if err != nil {
		return OAuthCredentials{}, err
	}
	scope, err := requireChatGPTTokenString(token, "scope")
	if err != nil {
		return OAuthCredentials{}, err
	}
	expiresIn, ok := finiteNumberOf(token["expires_in"])
	if !ok || expiresIn <= 0 {
		return OAuthCredentials{}, errors.New("OpenAI OAuth token response has invalid expires_in")
	}
	scopes := strings.Fields(scope)
	if !slices.Contains(scopes, chatgptDirectTokenScope) {
		return OAuthCredentials{}, fmt.Errorf("OpenAI OAuth grant did not include %s", chatgptDirectTokenScope)
	}
	clientIDJSON, _ := json.Marshal(clientID)
	scopesJSON, _ := json.Marshal(scopes)
	credentials := OAuthCredentials{Access: access, Refresh: refresh, Extra: map[string]json.RawMessage{"clientId": clientIDJSON, "scopes": scopesJSON}}
	credentials.SetExpiresMillis(float64(nowMillis()) + expiresIn*1000 - chatgptExpiryMargin)
	return credentials, nil
}

func exchangeChatGPTAuthorizationCode(ctx context.Context, code, verifier, clientID string) (OAuthCredentials, error) {
	token, err := requestChatGPTToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {chatgptRedirectURI},
		"resource":      {chatgptResource},
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	// Pi does not use the ID token to identify the user or read profile data. Keep the presence check as part of the
	// token-response contract.
	var idToken string
	if json.Unmarshal(token["id_token"], &idToken) != nil || trimJSWhitespace(idToken) == "" {
		return OAuthCredentials{}, errors.New("OpenAI OAuth token response did not contain an ID token")
	}
	return chatgptCredentialFromTokenResponse(token, clientID)
}

// RefreshOpenAIChatGPTToken refreshes a Sign in with ChatGPT credential with the client ID OpenAI issued at login.
func RefreshOpenAIChatGPTToken(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	var clientID string
	if json.Unmarshal(credentials.Extra["clientId"], &clientID) != nil || trimJSWhitespace(clientID) == "" {
		return OAuthCredentials{}, errors.New("Stored OpenAI OAuth credential does not contain an issued client ID; reconnect ChatGPT")
	}
	token, err := requestChatGPTToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {credentials.Refresh},
		"resource":      {chatgptResource},
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	return chatgptCredentialFromTokenResponse(token, clientID)
}

// chatgptAgentHostID is the stable URI OpenAI identifies each installation ("agent host") by.
func chatgptAgentHostID(deviceID string) (string, error) {
	if !chatgptUUIDPattern.MatchString(deviceID) {
		return "", errors.New("Sign in with ChatGPT requires a device ID (UUID) for this installation")
	}
	return "urn:uuid:" + strings.ToLower(deviceID), nil
}

// LoginOpenAIChatGPT runs the Sign in with ChatGPT flow: the browser callback and the pasted redirect URL race, then the
// authorization code is exchanged for tokens. callbacks.GetDeviceID supplies the installation's device ID.
func LoginOpenAIChatGPT(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	deviceID := ""
	if callbacks.GetDeviceID != nil {
		deviceID = callbacks.GetDeviceID()
	}
	hostID, err := chatgptAgentHostID(deviceID)
	if err != nil {
		return OAuthCredentials{}, err
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := chatgptRandomValue()
	if err != nil {
		return OAuthCredentials{}, err
	}
	nonce, err := chatgptRandomValue()
	if err != nil {
		return OAuthCredentials{}, err
	}
	callback, err := startChatGPTCallbackServer(state)
	if err != nil {
		if callbacks.OnInfo != nil {
			callbacks.OnInfo(fmt.Sprintf("Could not listen on %s; paste the final redirect URL to continue. %s", chatgptRedirectURI, err))
		}
		callback = nil
	} else {
		defer callback.close()
	}

	authorizeURL := chatgptAuthorizeURL + "?" + orderedQuery(
		"client_id", chatgptDynamicClientID, "agent_name_hint", chatgptAgentNameHint, "ext_agent_host_id", hostID,
		"response_type", "code", "redirect_uri", chatgptRedirectURI, "resource", chatgptResource, "scope", chatgptScope,
		"state", state, "code_challenge", pkce.Challenge, "code_challenge_method", "S256", "nonce", nonce,
	)
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{URL: authorizeURL, Instructions: "Complete sign-in in your browser. If the callback does not complete, paste the final redirect URL here."})
	}

	manualCtx, cancelManual := context.WithCancel(ctx)
	manual := make(chan chatgptAuthorizationResultOrError, 1)
	manualDone := make(chan struct{})
	go func() {
		defer close(manualDone)
		input, err := manualCodeInteraction(callbacks).Prompt(manualCtx, AuthManualCodePrompt{Message: "Complete login in your browser, or paste the final redirect URL here:", Placeholder: chatgptRedirectURI})
		if err != nil {
			manual <- chatgptAuthorizationResultOrError{err: err}
			return
		}
		result, err := chatgptResultFromManualInput(input, state)
		manual <- chatgptAuthorizationResultOrError{result, err}
	}()
	defer func() {
		cancelManual()
		<-manualDone
	}()

	var callbackResult <-chan chatgptAuthorizationResultOrError
	if callback != nil {
		callbackResult = callback.result
	}
	var outcome chatgptAuthorizationResultOrError
	select {
	case outcome = <-callbackResult:
	case outcome = <-manual:
	case <-ctx.Done():
		return OAuthCredentials{}, errors.New("Login cancelled")
	}
	if outcome.err != nil {
		if ctx.Err() != nil {
			return OAuthCredentials{}, errors.New("Login cancelled")
		}
		return OAuthCredentials{}, outcome.err
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}
	credentials, err := exchangeChatGPTAuthorizationCode(ctx, outcome.result.code, pkce.Verifier, outcome.result.clientID)
	if err != nil && ctx.Err() != nil {
		return OAuthCredentials{}, errors.New("Login cancelled")
	}
	return credentials, err
}

// OpenAIChatGPTOAuthProvider implements OAuthProviderInterface for Sign in with ChatGPT on the openai provider.
type OpenAIChatGPTOAuthProvider struct{}

func (OpenAIChatGPTOAuthProvider) ID() string                          { return "openai" }
func (OpenAIChatGPTOAuthProvider) IsSubscription() bool                { return true }
func (OpenAIChatGPTOAuthProvider) Name() string                        { return OpenAIChatGPTOAuthName }
func (OpenAIChatGPTOAuthProvider) UsesCallbackServer() bool            { return true }
func (OpenAIChatGPTOAuthProvider) GetAPIKey(c OAuthCredentials) string { return c.Access }

func (p OpenAIChatGPTOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.LoginContext(context.Background(), callbacks)
}

func (OpenAIChatGPTOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return LoginOpenAIChatGPT(ctx, callbacks)
}

func (p OpenAIChatGPTOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), creds)
}

func (OpenAIChatGPTOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return RefreshOpenAIChatGPTToken(ctx, creds)
}

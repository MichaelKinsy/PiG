package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports apps/p2/plugins/mcp/sign-in.ts: MCP OAuth sign-ins, a native of the manager: the auth provider every OAuth HTTP
// connection sends its tokens with, and the sign-in flows, one per server at a time. A flow redirects the browser either to
// a loopback callback server on the p2 host (the browser runs there) or to p2's own `/mcp/callback` route (the browser
// reaches p2 through Radius or a public host); the final redirect URL pasted by the owner always completes it too. Flows live
// in memory: a restart forgets them, the stored tokens stay.

// SignInRedirect is where a sign-in's browser step returns: a loopback callback on the p2 host, or p2's callback route at URL.
type SignInRedirect struct {
	// Route is false for a loopback callback.
	Route bool
	URL   string
}

// SignInStart is a begun sign-in: signed in at once (a stored refresh token worked), or the authorization URL to open.
type SignInStart struct {
	Authorized bool
	URL        string
}

// SignInView is a sign-in as the owner sees it: no URL, state or code.
type SignInView struct {
	ID     string
	Server string
	// Via is "loopback" or "route".
	Via string
	// Status is "waiting" or "failed".
	Status string
	Error  string
}

// AuthorizationResponse is what the callback route carries: an error response fails the sign-in.
type AuthorizationResponse struct {
	State string
	Code  string
	Iss   string
	// Error is the error response's message (empty: a code response).
	Error string
}

// SignInTimeout is how long a sign-in waits for its browser step.
const SignInTimeout = 10 * time.Minute

// piClientMetadataDocuments are pi's Client ID Metadata Documents on pi.dev: they list the loopback redirect URIs
// `http://127.0.0.1/callback` and `http://localhost/callback` (any port), so loopback sign-ins may use them; p2's https
// callback route registers dynamically.
var piClientMetadataDocuments = &oauth.McpClientMetadataDocuments{
	BaseURL: "https://pi.dev/oauth",
	ListsRedirectURL: func(redirectURL string) bool {
		url, err := nodeurl.ParseHTTPURL(redirectURL)
		return err == nil && url.Protocol == "http:" && (url.Hostname == "127.0.0.1" || url.Hostname == "localhost") && url.Pathname == "/callback"
	},
}

type flowStatus int

const (
	flowWaiting flowStatus = iota
	flowCompleting
	flowFailed
)

// flow is one sign-in under way (or failed, kept for the owner to see until replaced or dismissed).
type flow struct {
	id            string
	server        OAuthServer
	options       oauth.McpSignInOptions
	authorization oauth.McpSignInRedirect
	callback      *oauth.OAuthCallbackServer
	timer         *time.Timer
	status        flowStatus
	failure       string
}

// SignIns are the auth providers and sign-in flows; subscribers hear of every change of List.
type SignIns struct {
	credentials *McpCredentials
	clientName  string
	changed     func(server string)
	timeout     time.Duration
	ctx         context.Context
	cancel      context.CancelFunc

	mu sync.Mutex
	// challenges is the last challenge of each server's connections (its resource metadata URL and scope), by server key.
	challenges map[string]oauth.OAuthChallenge
	// flows is the current flow of each server, by name.
	flows     map[string]*flow
	listeners map[int]func()
	next      int
}

// NewSignIns is the sign-ins of credentials; changed hears of every server whose credentials changed (signed in or out):
// its instances reconnect.
func NewSignIns(credentials *McpCredentials, clientName string, changed func(server string), timeout time.Duration) *SignIns {
	//portlint:allow ctxbackground the sign-ins own this context for their life: Close cancels it
	ctx, cancel := context.WithCancel(context.Background())
	return &SignIns{
		credentials: credentials, clientName: clientName, changed: changed, timeout: timeout, ctx: ctx, cancel: cancel,
		challenges: map[string]oauth.OAuthChallenge{}, flows: map[string]*flow{}, listeners: map[int]func(){},
	}
}

// Subscribe has listener called after every change of List; the result stops it.
func (s *SignIns) Subscribe(listener func()) (stop func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	s.listeners[id] = listener
	return func() {
		s.mu.Lock()
		delete(s.listeners, id)
		s.mu.Unlock()
	}
}

// List is the flows, by server.
func (s *SignIns) List() []SignInView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]SignInView, 0, len(s.flows))
	for _, each := range s.flows {
		view := SignInView{ID: each.id, Server: each.server.Name, Via: "loopback", Status: "waiting", Error: each.failure}
		if each.callback == nil {
			view.Via = "route"
		}
		// Completing is still waiting to the owner: a paste meanwhile is refused as for any finished flow.
		if each.status == flowFailed {
			view.Status = "failed"
		}
		views = append(views, view)
	}
	slices.SortFunc(views, func(a, b SignInView) int { return strings.Compare(a.Server, b.Server) })
	return views
}

// AuthProvider is the auth provider of server's connections: stored tokens, refreshed; never a browser step.
func (s *SignIns) AuthProvider(server OAuthServer) (oauth.McpAuthProvider, error) {
	store, err := s.credentials.ForServer(server.Name, server.URL)
	if err != nil {
		return nil, err
	}
	return oauth.CreateMcpAuthProvider(oauth.McpAuthProviderOptions{
		ServerURL: server.URL,
		Store:     store,
		Settings:  func() (oauth.McpOAuthSettings, error) { return settingsOf(server.Config) },
		OnChallenge: func(challenge oauth.OAuthChallenge) {
			s.mu.Lock()
			s.challenges[challengeKey(server)] = challenge
			s.mu.Unlock()
		},
		ClientName:              s.clientName,
		ClientMetadataDocuments: piClientMetadataDocuments,
	}), nil
}

// Begin begins a sign-in to server, replacing its current one. A pre-registered client (`oauth.clientId`) always uses the
// loopback callback: its redirect URIs are registered as configured.
func (s *SignIns) Begin(server OAuthServer, redirect SignInRedirect) (SignInStart, error) {
	settings, err := settingsOf(server.Config)
	if err != nil {
		return SignInStart{}, err
	}
	store, err := s.credentials.ForServer(server.Name, server.URL)
	if err != nil {
		return SignInStart{}, err
	}
	var callback *oauth.OAuthCallbackServer
	redirectURL := redirect.URL
	if !redirect.Route || settings.ClientID != "" {
		listening, err := oauth.ListenForLoopbackSignIn(s.ctx, oauth.ListenForLoopbackSignInOptions{
			ServerURL: server.URL, Store: store, Settings: settings, ClientMetadataDocuments: piClientMetadataDocuments,
			RenderPage: func(page oauth.OAuthCallbackPage) string { return callbackPage(server.Name, page) },
		})
		if err != nil {
			return SignInStart{}, err
		}
		callback, redirectURL = listening.Callback, listening.RedirectURL
	}
	closeCallback := func() {
		if callback != nil {
			_ = callback.Close()
		}
	}
	s.mu.Lock()
	challenge, hasChallenge := s.challenges[challengeKey(server)]
	s.mu.Unlock()
	options := oauth.McpSignInOptions{
		McpOAuthClientOptions: oauth.McpOAuthClientOptions{
			ServerURL: server.URL, Store: store, Settings: settings, ClientName: s.clientName,
			ClientMetadataDocuments: piClientMetadataDocuments,
		},
		RedirectURL: redirectURL,
	}
	if hasChallenge {
		options.Challenge = &challenge
	}
	start, err := oauth.StartMcpSignIn(s.ctx, options)
	if err != nil {
		closeCallback()
		return SignInStart{}, err
	}
	s.mu.Lock()
	if previous := s.flows[server.Name]; previous != nil {
		s.dropLocked(previous)
	}
	if start.Kind == oauth.McpSignInKindAuthorized {
		s.mu.Unlock()
		closeCallback()
		s.notify()
		s.changed(server.Name)
		return SignInStart{Authorized: true}, nil
	}
	created := &flow{id: randomUUID(), server: server, options: options, authorization: start.Redirect(), callback: callback, status: flowWaiting}
	created.timer = time.AfterFunc(s.timeout, func() { s.fail(created, errors.New("The sign-in expired; start it again")) })
	s.flows[server.Name] = created
	s.mu.Unlock()
	if callback != nil {
		registered := make(chan struct{})
		var once sync.Once
		path := "/"
		if url, err := nodeurl.ParseHTTPURL(start.Redirect().RedirectURL); err == nil {
			path = url.Pathname
		}
		go func() {
			response, err := callback.WaitForCallback(oauth.WithCallbackRegistered(s.ctx, func() { once.Do(func() { close(registered) }) }), start.Redirect().State, path)
			once.Do(func() { close(registered) })
			if err != nil {
				s.fail(created, err)
				return
			}
			_ = s.finish(created, response)
		}()
		// The browser may follow the URL the moment it is returned: the wait is registered first.
		select {
		case <-registered:
		case <-s.ctx.Done():
		}
	}
	s.notify()
	return SignInStart{URL: start.Redirect().AuthorizationURL}, nil
}

// Complete completes the route sign-in whose state the authorization response carries (an error response fails it); the
// result names its server.
func (s *SignIns) Complete(response AuthorizationResponse) (string, error) {
	s.mu.Lock()
	var found *flow
	for _, candidate := range s.flows {
		if candidate.callback == nil && candidate.status == flowWaiting && candidate.authorization.State == response.State {
			found = candidate
			break
		}
	}
	s.mu.Unlock()
	if found == nil {
		return "", data.NewPluginError("not_found", "This sign-in is unknown, finished or expired")
	}
	if response.Error != "" {
		s.fail(found, errors.New(response.Error))
		return "", data.NewPluginError("sign_in", response.Error)
	}
	if err := s.finish(found, oauth.McpAuthorizationResponse{Code: response.Code, State: response.State, Iss: response.Iss}); err != nil {
		return "", err
	}
	return found.server.Name, nil
}

// Paste completes sign-in id with the final redirect URL the owner copied from the browser.
func (s *SignIns) Paste(id, redirectURL string) error {
	s.mu.Lock()
	found := s.byIDLocked(id)
	var authorization oauth.McpSignInRedirect
	waiting := found != nil && found.status == flowWaiting
	if waiting {
		authorization = found.authorization
	}
	s.mu.Unlock()
	if !waiting {
		return data.NewPluginError("not_found", "This sign-in is unknown, finished or expired")
	}
	// A URL of another sign-in or no code is refused without failing this one.
	response, err := oauth.ResponseFromRedirectURL(redirectURL, authorization)
	if err != nil {
		return data.NewPluginError("invalid_url", err.Error())
	}
	return s.finish(found, response)
}

// Cancel ends sign-in id, under way or failed.
func (s *SignIns) Cancel(id string) {
	s.mu.Lock()
	found := s.byIDLocked(id)
	if found == nil {
		s.mu.Unlock()
		return
	}
	s.dropLocked(found)
	s.mu.Unlock()
	s.notify()
}

// SignOut forgets server's credentials and ends its sign-in; its instances reconnect, without them.
func (s *SignIns) SignOut(ctx context.Context, server OAuthServer) error {
	s.mu.Lock()
	found := s.flows[server.Name]
	if found != nil {
		s.dropLocked(found)
	}
	s.mu.Unlock()
	if found != nil {
		s.notify()
	}
	if err := s.credentials.Remove(ctx, server.Name, server.URL); err != nil {
		return err
	}
	s.changed(server.Name)
	return nil
}

// Close ends every flow (their loopback servers close).
func (s *SignIns) Close() {
	s.cancel()
	s.mu.Lock()
	flows := make([]*flow, 0, len(s.flows))
	for _, each := range s.flows {
		flows = append(flows, each)
	}
	s.flows = map[string]*flow{}
	s.mu.Unlock()
	for _, each := range flows {
		each.timer.Stop()
		if each.callback != nil {
			_ = each.callback.Close()
		}
	}
	s.credentials.Close()
}

func (s *SignIns) byIDLocked(id string) *flow {
	for _, each := range s.flows {
		if each.id == id {
			return each
		}
	}
	return nil
}

// finish exchanges the code of response; a failure fails the flow and is returned.
func (s *SignIns) finish(current *flow, response oauth.McpAuthorizationResponse) error {
	s.mu.Lock()
	if current.status != flowWaiting || s.flows[current.server.Name] != current {
		s.mu.Unlock()
		return data.NewPluginError("not_found", "This sign-in is unknown, finished or expired")
	}
	current.status = flowCompleting
	s.mu.Unlock()
	if err := oauth.CompleteMcpSignIn(s.ctx, oauth.McpCompleteSignInOptions{McpSignInOptions: current.options, Authorization: current.authorization, Response: response}); err != nil {
		s.fail(current, err)
		return data.NewPluginError("sign_in", err.Error())
	}
	s.mu.Lock()
	dropped := s.flows[current.server.Name] == current
	if dropped {
		s.dropLocked(current)
	}
	s.mu.Unlock()
	if dropped {
		s.notify()
	}
	s.changed(current.server.Name)
	return nil
}

// fail marks current failed (if it is still the server's current one and has not finished).
func (s *SignIns) fail(current *flow, err error) {
	s.mu.Lock()
	if s.flows[current.server.Name] != current || current.status == flowFailed {
		s.mu.Unlock()
		return
	}
	current.status = flowFailed
	current.failure = err.Error()
	current.timer.Stop()
	s.mu.Unlock()
	if current.callback != nil {
		_ = current.callback.Close()
	}
	s.notify()
}

// dropLocked removes current: its timer stops and its loopback server closes.
func (s *SignIns) dropLocked(current *flow) {
	if s.flows[current.server.Name] == current {
		delete(s.flows, current.server.Name)
	}
	current.timer.Stop()
	if current.callback != nil {
		go func(callback *oauth.OAuthCallbackServer) { _ = callback.Close() }(current.callback)
	}
}

func (s *SignIns) notify() {
	s.mu.Lock()
	listeners := make([]func(), 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// settingsOf is the settings of an `oauth` entry: `${NAME}` in the client secret from the p2 host's environment.
func settingsOf(config *extension.McpOAuthConfig) (oauth.McpOAuthSettings, error) {
	if config == nil {
		return oauth.McpOAuthSettings{}, nil
	}
	settings := oauth.McpOAuthSettings{
		ClientID: config.ClientID, CallbackPort: config.CallbackPort, CallbackURL: config.CallbackURL, Scope: config.Scope,
		ClientName: config.ClientName, ClientRegistration: string(config.ClientRegistration),
	}
	if config.ClientSecret != "" {
		secret, err := ExpandHostVariables(config.ClientSecret)
		if err != nil {
			return settings, err
		}
		settings.ClientSecret = secret
	}
	if config.AuthServerMetadataURL != "" {
		parsed, err := oauth.ParseURL(config.AuthServerMetadataURL)
		if err != nil {
			return settings, err
		}
		settings.AuthServerMetadataURL = &parsed
	}
	return settings, nil
}

// challengeKey is a server's key for its challenges: its name and URL, as its credentials'.
func challengeKey(server OAuthServer) string {
	key, _ := json.Marshal([]string{server.Name, server.URL})
	return string(key)
}

// callbackPage is the loopback callback's page after the redirect.
func callbackPage(server string, page oauth.OAuthCallbackPage) string {
	if page.OK {
		return ResultPage("Signed in to "+server, "You may close this page.")
	}
	message := page.Message
	if page.Details != "" {
		message += " " + page.Details
	}
	return ResultPage(fmt.Sprintf("Signing in to %s failed", server), message)
}

// randomUUID is a version 4 UUID.
func randomUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}

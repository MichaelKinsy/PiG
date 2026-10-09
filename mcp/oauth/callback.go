package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ports packages/mcp/src/oauth/callback.ts.

// OAuthCallback is the redirect the browser delivers.
type OAuthCallback struct {
	Code  string
	State string
	Iss   string
}

// OAuthCallbackPage is the outcome shown on the browser page after the redirect.
type OAuthCallbackPage struct {
	OK      bool
	Message string
	Details string
}

// OAuthCallbackServerOptions configure [ListenOAuthCallbackServer].
type OAuthCallbackServerOptions struct {
	// Host is the address to listen on. Default: 127.0.0.1.
	Host string
	// RedirectHost is the host name in RedirectURL, for example `localhost`
	// for a client registered with it while listening on 127.0.0.1. Default: Host.
	RedirectHost string
	Port         int
	Path         string
	// ExtraPaths are more paths that receive the callback, for example a
	// server-specific path of a redirect URI.
	ExtraPaths []string
	// Timeout bounds each wait for a callback. Default: 5 minutes.
	Timeout time.Duration
	// RenderPage renders the browser page as HTML. Default: a plain-text message.
	RenderPage func(page OAuthCallbackPage) string
}

func plainText(page OAuthCallbackPage) string {
	if page.OK {
		return "Authorization complete. You may close this window."
	}
	if page.Details != "" {
		return page.Message + "\n\n" + page.Details
	}
	return page.Message
}

type callbackResult struct {
	callback OAuthCallback
	err      error
}

type pendingCallback struct {
	result chan callbackResult
	timer  *time.Timer
	// path is the only path the response may arrive on, or "" for any.
	path string
}

type callbackRegisteredKey struct{}

// WithCallbackRegistered returns a context that makes [OAuthCallbackServer.WaitForCallback] call registered once the state is pending and before it blocks. Upstream's waitForCallback registers the state when it is called, before the caller's next statement; a Go caller that waits on its own goroutine uses this to act (for example, show the authorization URL) only after the callback can be received.
func WithCallbackRegistered(ctx context.Context, registered func()) context.Context {
	return context.WithValue(ctx, callbackRegisteredKey{}, registered)
}

// OAuthCallbackServer is a loopback HTTP server that receives the OAuth
// redirect.
type OAuthCallbackServer struct {
	// RedirectURL is the redirect URI to register and send.
	RedirectURL string
	server      *http.Server
	listener    net.Listener
	paths       []string
	timeout     time.Duration
	renderPage  func(OAuthCallbackPage) string

	mu      sync.Mutex
	pending map[string]*pendingCallback
	done    chan struct{}
}

// ListenOAuthCallbackServer starts the server.
func ListenOAuthCallbackServer(options OAuthCallbackServerOptions) (*OAuthCallbackServer, error) {
	host := options.Host
	if host == "" {
		host = "127.0.0.1"
	}
	redirectHost := options.RedirectHost
	if redirectHost == "" {
		redirectHost = host
	}
	path := options.Path
	if path == "" {
		path = "/callback"
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(options.Port)))
	if err != nil {
		return nil, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("OAuth callback server did not bind to TCP")
	}
	shownHost := redirectHost
	if strings.Contains(shownHost, ":") {
		shownHost = "[" + shownHost + "]"
	}
	s := &OAuthCallbackServer{
		RedirectURL: fmt.Sprintf("http://%s:%d%s", shownHost, address.Port, path),
		listener:    listener,
		paths:       append([]string{path}, options.ExtraPaths...),
		timeout:     timeout,
		renderPage:  options.RenderPage,
		pending:     map[string]*pendingCallback{},
		done:        make(chan struct{}),
	}
	s.server = &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 30 * time.Second}
	go func() {
		defer close(s.done)
		_ = s.server.Serve(listener)
	}()
	return s, nil
}

// WaitForCallback waits for the authorization response with state and returns its code, state and iss. It fails when state is already
// pending, when the wait times out, when the server closes and when ctx ends. With a non-empty path (upstream's optional `path`), a
// response on another path fails, so a server-specific redirect URI can tell authorization servers apart (RFC 9700 section 4.4.2.2).
// See [WithCallbackRegistered] for acting once the state is pending.
func (s *OAuthCallbackServer) WaitForCallback(ctx context.Context, state string, path ...string) (OAuthCallback, error) {
	only := ""
	if len(path) > 0 {
		only = path[0]
	}
	s.mu.Lock()
	if _, ok := s.pending[state]; ok {
		s.mu.Unlock()
		return OAuthCallback{}, errors.New("OAuth state is already pending")
	}
	entry := &pendingCallback{result: make(chan callbackResult, 1), path: only}
	entry.timer = time.AfterFunc(s.timeout, func() {
		s.mu.Lock()
		current := s.pending[state]
		if current == entry {
			delete(s.pending, state)
		}
		s.mu.Unlock()
		if current == entry {
			entry.result <- callbackResult{err: errors.New("OAuth callback timed out")}
		}
	})
	s.pending[state] = entry
	s.mu.Unlock()
	if registered, ok := ctx.Value(callbackRegisteredKey{}).(func()); ok && registered != nil {
		registered()
	}
	select {
	case r := <-entry.result:
		return r.callback, r.err
	case <-ctx.Done():
		return OAuthCallback{}, context.Cause(ctx)
	}
}

// Close rejects every pending wait and stops the server.
func (s *OAuthCallbackServer) Close() error {
	s.mu.Lock()
	pending := s.pending
	s.pending = map[string]*pendingCallback{}
	s.mu.Unlock()
	for _, entry := range pending {
		entry.timer.Stop()
		entry.result <- callbackResult{err: errors.New("OAuth callback server closed")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.server.Shutdown(ctx)
	if err != nil {
		_ = s.server.Close()
	}
	<-s.done
	return nil
}

func (s *OAuthCallbackServer) reply(w http.ResponseWriter, status int, page OAuthCallbackPage) {
	if s.renderPage != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(s.renderPage(page)))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(plainText(page)))
}

func (s *OAuthCallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	if !slices.Contains(s.paths, r.URL.Path) {
		s.reply(w, http.StatusNotFound, OAuthCallbackPage{Message: "Not found"})
		return
	}
	query := r.URL.Query()
	state := query.Get("state")
	s.mu.Lock()
	entry := s.pending[state]
	if state == "" || entry == nil {
		s.mu.Unlock()
		s.reply(w, http.StatusBadRequest, OAuthCallbackPage{Message: "Invalid or expired OAuth state"})
		return
	}
	entry.timer.Stop()
	delete(s.pending, state)
	s.mu.Unlock()
	if entry.path != "" && r.URL.Path != entry.path {
		entry.result <- callbackResult{err: errors.New("The authorization response arrived on another redirect URI")}
		s.reply(w, http.StatusBadRequest, OAuthCallbackPage{Message: "Unexpected redirect URI"})
		return
	}
	if failure := query.Get("error"); failure != "" {
		description := failure
		if query.Has("error_description") {
			description = query.Get("error_description")
		}
		entry.result <- callbackResult{err: errors.New(description)}
		s.reply(w, http.StatusOK, OAuthCallbackPage{Message: "Authorization failed. You may close this window.", Details: description})
		return
	}
	code := query.Get("code")
	if code == "" {
		entry.result <- callbackResult{err: errors.New("OAuth callback did not include an authorization code")}
		s.reply(w, http.StatusBadRequest, OAuthCallbackPage{Message: "Missing authorization code"})
		return
	}
	entry.result <- callbackResult{callback: OAuthCallback{Code: code, State: state, Iss: query.Get("iss")}}
	s.reply(w, http.StatusOK, OAuthCallbackPage{OK: true})
}

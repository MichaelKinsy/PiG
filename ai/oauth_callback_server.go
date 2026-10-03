package ai

// Ports packages/ai/src/auth/oauth/callback-server.ts.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OAuthCallbackServerOptions configures the loopback redirect handler shared by the browser sign-in flows.
type OAuthCallbackServerOptions[T any] struct {
	// ProviderName is used on the browser page, for example "OpenAI".
	ProviderName string
	// Host is the address to listen on.
	Host string
	// Port is the port to listen on; 0 picks a free port.
	Port int
	Path string
	// RedirectHost is the host in the redirect URI when it differs from Host, for example "localhost".
	RedirectHost string
	// State is the expected state parameter. Nil skips the check for providers that send none.
	State *string
	// Complete finishes the sign-in with the received code before the browser page is sent, so the page can show
	// exchange failures. Its context is the one passed to StartOAuthCallbackServer.
	Complete func(ctx context.Context, code string) (T, error)
	// Timeout ends the wait with an error. Zero means no timeout.
	Timeout time.Duration
}

// oauthCallbackHeaderTimeout is the loopback servers' request-header deadline. Node's http.Server enforces its own
// default; Go's has none, and a silent local client must not hold a login server open.
const oauthCallbackHeaderTimeout = 10 * time.Second

// OAuthCallbackServer waits for one browser redirect.
type OAuthCallbackServer[T any] struct {
	// RedirectURI is the URI the provider redirects the browser to.
	RedirectURI string

	options  OAuthCallbackServerOptions[T]
	ctx      context.Context
	server   *http.Server
	served   chan struct{}
	done     chan struct{}
	stopCtx  func() bool
	timer    *time.Timer
	mu       sync.Mutex
	claimed  bool
	settled  bool
	value    T
	hasValue bool
	err      error
}

// StartOAuthCallbackServer listens for the provider's redirect. Cancelling ctx ends the wait with "Login cancelled".
func StartOAuthCallbackServer[T any](ctx context.Context, options OAuthCallbackServerOptions[T]) (*OAuthCallbackServer[T], error) {
	if ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.Host, strconv.Itoa(options.Port)))
	if err != nil {
		return nil, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("OAuth callback server did not bind to TCP")
	}
	redirectHost := options.RedirectHost
	if redirectHost == "" {
		redirectHost = options.Host
	}
	if strings.Contains(redirectHost, ":") {
		redirectHost = "[" + redirectHost + "]"
	}
	s := &OAuthCallbackServer[T]{
		RedirectURI: fmt.Sprintf("http://%s:%d%s", redirectHost, address.Port, options.Path),
		options:     options,
		ctx:         ctx,
		served:      make(chan struct{}),
		done:        make(chan struct{}),
	}
	s.server = &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: oauthCallbackHeaderTimeout}
	go func() {
		defer close(s.served)
		_ = s.server.Serve(listener) // returns when Close shuts the server down
	}()
	// finish reads both under the lock, so a watcher that fires at once waits for the assignments.
	s.mu.Lock()
	s.stopCtx = context.AfterFunc(ctx, func() { s.finish(nil, false, errors.New("Login cancelled")) })
	if options.Timeout > 0 {
		s.timer = time.AfterFunc(options.Timeout, func() { s.finish(nil, false, fmt.Errorf("%s sign-in timed out", options.ProviderName)) })
	}
	s.mu.Unlock()
	return s, nil
}

// finish settles the wait once. A nil value with hasValue false is the cancelled outcome.
func (s *OAuthCallbackServer[T]) finish(value *T, hasValue bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return
	}
	s.settled = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.stopCtx != nil {
		s.stopCtx()
	}
	if hasValue {
		s.value, s.hasValue = *value, true
	}
	s.err = err
	close(s.done)
}

func (s *OAuthCallbackServer[T]) sendPage(w http.ResponseWriter, status int, html string) {
	writeOAuthPage(w, status, html)
}

// writeOAuthPage sends a complete response before returning. Node's response.end() reaches the socket before the
// server closes its connections; Go buffers until the handler returns, so an immediate Close would drop the page.
func writeOAuthPage(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(html)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *OAuthCallbackServer[T]) handle(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if r.Method != http.MethodGet || r.URL.Path != s.options.Path {
		s.sendPage(w, http.StatusNotFound, OAuthErrorHTML("Callback route not found.", ""))
		return
	}
	if s.options.State != nil && query.Get("state") != *s.options.State {
		s.sendPage(w, http.StatusBadRequest, OAuthErrorHTML("State mismatch.", ""))
		return
	}
	s.mu.Lock()
	unavailable := s.claimed || s.settled
	s.mu.Unlock()
	if unavailable {
		s.sendPage(w, http.StatusConflict, OAuthErrorHTML("This sign-in has already been handled.", ""))
		return
	}
	providerName := s.options.ProviderName
	if failure := query.Get("error"); failure != "" {
		description := failure
		if values, present := query["error_description"]; present {
			description = values[0]
		}
		s.sendPage(w, http.StatusBadRequest, OAuthErrorHTML(providerName+" authorization failed.", description))
		s.finish(nil, false, fmt.Errorf("%s authorization failed: %s", providerName, description))
		return
	}
	code := query.Get("code")
	if code == "" {
		s.sendPage(w, http.StatusBadRequest, OAuthErrorHTML("Missing authorization code.", ""))
		return
	}
	s.mu.Lock()
	if s.claimed || s.settled {
		s.mu.Unlock()
		s.sendPage(w, http.StatusConflict, OAuthErrorHTML("This sign-in has already been handled.", ""))
		return
	}
	s.claimed = true
	s.mu.Unlock()
	value, err := s.options.Complete(s.ctx, code)
	if err != nil {
		s.sendPage(w, http.StatusBadGateway, OAuthErrorHTML(providerName+" sign-in failed.", err.Error()))
		s.finish(nil, false, err)
		return
	}
	s.sendPage(w, http.StatusOK, OAuthSuccessHTML("Signed in to "+providerName+". You may now close this page."))
	s.finish(&value, true, nil)
}

// Wait returns the result of Complete, or ok=false after Cancel. It fails when the provider redirects with an error,
// Complete fails, the context ends or the timeout elapses.
func (s *OAuthCallbackServer[T]) Wait() (value T, ok bool, err error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.hasValue, s.err
}

// Cancel stops waiting for the browser unless a callback is already being completed.
func (s *OAuthCallbackServer[T]) Cancel() {
	s.mu.Lock()
	claimed := s.claimed
	s.mu.Unlock()
	if !claimed {
		s.finish(nil, false, nil)
	}
}

// Close fails a pending wait, stops the listener and drops every connection (callback-server.ts:145-148). Node's
// server.close() also closes idle connections, so a browser's spare connection cannot keep the server alive past its
// login; http.Server.Close additionally ends a connection whose Complete is still running.
func (s *OAuthCallbackServer[T]) Close() {
	s.finish(nil, false, errors.New("OAuth callback server closed"))
	_ = s.server.Close()
	<-s.served
}

// OAuthCallbackOrManualInput is the outcome of WaitForCallbackOrManualInput: the callback result or the pasted input.
type OAuthCallbackOrManualInput[T any] struct {
	// Callback is true when the browser callback completed the sign-in.
	Callback bool
	Value    T
	Input    string
}

// WaitForCallbackOrManualInput waits for the browser callback, or for the user to paste the code or redirect URL when
// the browser cannot reach the loopback server (for example over SSH). Without a callback server only the manual prompt
// is used. The manual prompt's context ends, and the prompt is joined, before it returns.
func WaitForCallbackOrManualInput[T any](ctx context.Context, interaction AuthInteraction, callback *OAuthCallbackServer[T], prompt AuthManualCodePrompt) (OAuthCallbackOrManualInput[T], error) {
	var result OAuthCallbackOrManualInput[T]
	if interaction.Prompt == nil {
		return result, errors.New("manual code input is unavailable")
	}
	// The callback server owns cancellation of the parent context: it ends the wait with "Login cancelled". The manual
	// prompt ends when this wait does, as upstream aborts its own controller in finally. Without a callback server the
	// parent context also ends the prompt, since nothing else would.
	promptParent := ctx
	if callback != nil {
		promptParent = context.WithoutCancel(ctx)
	}
	manualCtx, cancelManual := context.WithCancel(promptParent)
	var mu sync.Mutex
	var manualInput string
	var manualErr error
	manualDone := make(chan struct{})
	go func() {
		defer close(manualDone)
		input, err := interaction.Prompt(manualCtx, prompt)
		mu.Lock()
		manualInput, manualErr = input, err
		mu.Unlock()
		if callback != nil {
			callback.Cancel()
		}
	}()
	defer func() {
		cancelManual()
		<-manualDone
	}()
	manualFailure := func() error {
		mu.Lock()
		defer mu.Unlock()
		return manualErr
	}
	if callback != nil {
		value, ok, err := callback.Wait()
		if err != nil {
			return result, err
		}
		if err := manualFailure(); err != nil {
			return result, err
		}
		if ok {
			return OAuthCallbackOrManualInput[T]{Callback: true, Value: value}, nil
		}
	}
	<-manualDone
	if err := manualFailure(); err != nil {
		return result, err
	}
	mu.Lock()
	defer mu.Unlock()
	return OAuthCallbackOrManualInput[T]{Input: manualInput}, nil
}

// oauthCallbackHost is where the browser sign-in flows listen: PI_OAUTH_CALLBACK_HOST, or the IPv4 loopback address.
func oauthCallbackHost() string {
	return firstNonEmptyString(os.Getenv("PI_OAUTH_CALLBACK_HOST"), "127.0.0.1")
}

// manualCodeInteraction answers a flow's manual_code prompt with the caller's manual-code callbacks. A callback without
// a context cannot be interrupted, so it runs on its own goroutine and its result is dropped when the prompt's context
// ends first. Without any callback the prompt stays open until its context ends.
func manualCodeInteraction(callbacks OAuthLoginCallbacks) AuthInteraction {
	return AuthInteraction{Notify: func(AuthEvent) {}, Prompt: func(ctx context.Context, prompt AuthPrompt) (string, error) {
		if manual, ok := prompt.(AuthManualCodePrompt); ok && callbacks.OnManualCodePromptContext != nil {
			return callbacks.OnManualCodePromptContext(ctx, manual)
		}
		if callbacks.OnManualCodeInputContext != nil {
			return callbacks.OnManualCodeInputContext(ctx)
		}
		if callbacks.OnManualCodeInput == nil {
			<-ctx.Done()
			return "", context.Cause(ctx)
		}
		type manualInput struct {
			value string
			err   error
		}
		input := make(chan manualInput, 1)
		go func() {
			value, err := callbacks.OnManualCodeInput()
			input <- manualInput{value, err}
		}()
		select {
		case result := <-input:
			return result.value, result.err
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}}
}

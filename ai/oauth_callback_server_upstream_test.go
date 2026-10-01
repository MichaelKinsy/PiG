package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

type oauthCallbackPage struct {
	status      int
	contentType string
	body        string
}

var oauthNativeClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func oauthCallbackURL(t *testing.T, redirectURI string, params ...string) string {
	t.Helper()
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for i := 0; i+1 < len(params); i += 2 {
		query.Set(params[i], params[i+1])
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func oauthGet(t *testing.T, target string) oauthCallbackPage {
	t.Helper()
	response, err := oauthNativeClient.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return oauthCallbackPage{response.StatusCode, response.Header.Get("content-type"), string(body)}
}

// oauthPendingPrompt is a manual prompt that stays open until its context ends.
func oauthPendingPrompt(onPrompt func(context.Context, AuthPrompt)) func(context.Context, AuthPrompt) (string, error) {
	return func(ctx context.Context, prompt AuthPrompt) (string, error) {
		if onPrompt != nil {
			onPrompt(ctx, prompt)
		}
		<-ctx.Done()
		return "", errors.New("prompt aborted")
	}
}

func startTestCallbackServer[T any](t *testing.T, ctx context.Context, options OAuthCallbackServerOptions[T]) *OAuthCallbackServer[T] {
	t.Helper()
	server, err := StartOAuthCallbackServer(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func stringCallbackOptions(state *string) OAuthCallbackServerOptions[string] {
	return OAuthCallbackServerOptions[string]{ProviderName: "Example", Host: "127.0.0.1", Port: 0, Path: "/callback", State: state,
		Complete: func(_ context.Context, code string) (string, error) { return "completed:" + code, nil }}
}

// Ports packages/ai/test/oauth-callback-server.test.ts.
func TestOAuthCallbackServerUpstream(t *testing.T) {
	expected := new("expected-state")
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:52
	t.Run("ignores stray requests and resolves with the completed code", func(t *testing.T) {
		server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(expected))
		if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/callback$`).MatchString(server.RedirectURI) {
			t.Fatalf("redirectUri=%s", server.RedirectURI)
		}
		redirect, _ := url.Parse(server.RedirectURI)
		other := *redirect
		other.Path = "/other"
		if page := oauthGet(t, other.String()); page.status != 404 {
			t.Errorf("wrong path=%+v", page)
		}
		wrongState := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "c", "state", "other"))
		if wrongState.status != 400 || wrongState.contentType != "text/html; charset=utf-8" || !strings.Contains(wrongState.body, "State mismatch.") {
			t.Errorf("wrong state=%+v", wrongState)
		}
		post, err := oauthNativeClient.Post(oauthCallbackURL(t, server.RedirectURI, "code", "c", "state", "expected-state"), "text/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = post.Body.Close()
		if post.StatusCode != 404 {
			t.Errorf("post=%d", post.StatusCode)
		}
		if page := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "state", "expected-state")); page.status != 400 {
			t.Errorf("missing code=%+v", page)
		}

		success := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "the-code", "state", "expected-state"))
		if success.status != 200 || success.contentType != "text/html; charset=utf-8" || !strings.Contains(success.body, "Authentication successful") || !strings.Contains(success.body, "Signed in to Example.") {
			t.Errorf("success=%+v", success)
		}
		if value, ok, err := server.Wait(); err != nil || !ok || value != "completed:the-code" {
			t.Errorf("wait=%q %v %v", value, ok, err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:77
	t.Run("uses the redirect host and skips the state check when none is expected", func(t *testing.T) {
		options := stringCallbackOptions(nil)
		options.RedirectHost = "localhost"
		server := startTestCallbackServer(t, t.Context(), options)
		if !regexp.MustCompile(`^http://localhost:\d+/callback$`).MatchString(server.RedirectURI) {
			t.Fatalf("redirectUri=%s", server.RedirectURI)
		}
		if page := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "no-state")); page.status != 200 {
			t.Errorf("page=%+v", page)
		}
		if value, ok, err := server.Wait(); err != nil || !ok || value != "completed:no-state" {
			t.Errorf("wait=%q %v %v", value, ok, err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:85
	t.Run("shows completion failures on the page and rejects the wait", func(t *testing.T) {
		options := stringCallbackOptions(expected)
		options.Complete = func(context.Context, string) (string, error) { return "", errors.New("token exchange failed") }
		server := startTestCallbackServer(t, t.Context(), options)

		failure := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "c", "state", "expected-state"))

		if failure.status != 502 || !strings.Contains(failure.body, "Example sign-in failed.") || !strings.Contains(failure.body, "token exchange failed") {
			t.Errorf("failure=%+v", failure)
		}
		if _, _, err := server.Wait(); err == nil || !strings.Contains(err.Error(), "token exchange failed") {
			t.Errorf("wait err=%v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:100
	t.Run("rejects the wait when the provider redirects with an error", func(t *testing.T) {
		server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(expected))
		failure := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "error", "access_denied", "error_description", "User denied access", "state", "expected-state"))

		if failure.status != 400 || !strings.Contains(failure.body, "User denied access") {
			t.Errorf("failure=%+v", failure)
		}
		if _, _, err := server.Wait(); err == nil || !strings.Contains(err.Error(), "Example authorization failed: User denied access") {
			t.Errorf("wait err=%v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:116
	t.Run("completes only the first callback", func(t *testing.T) {
		started, release := make(chan struct{}), make(chan string)
		options := stringCallbackOptions(expected)
		options.Complete = func(context.Context, string) (string, error) {
			close(started)
			return <-release, nil
		}
		server := startTestCallbackServer(t, t.Context(), options)
		target := oauthCallbackURL(t, server.RedirectURI, "code", "c", "state", "expected-state")
		first := make(chan oauthCallbackPage, 1)
		go func() { first <- oauthGet(t, target) }()
		<-started
		if second := oauthGet(t, target); second.status != 409 {
			t.Errorf("second=%+v", second)
		}
		// A claimed callback keeps completing even when the caller switches to manual input.
		server.Cancel()
		release <- "done"
		if page := <-first; page.status != 200 {
			t.Errorf("first=%+v", page)
		}
		if value, ok, err := server.Wait(); err != nil || !ok || value != "done" {
			t.Errorf("wait=%q %v %v", value, ok, err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:136
	t.Run("resolves with undefined after cancel", func(t *testing.T) {
		server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(expected))
		server.Cancel()
		if _, ok, err := server.Wait(); err != nil || ok {
			t.Errorf("wait ok=%v err=%v", ok, err)
		}
		if late := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "c", "state", "expected-state")); late.status != 409 {
			t.Errorf("late=%+v", late)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:144
	t.Run("rejects the wait on abort and on timeout", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		aborted := startTestCallbackServer(t, ctx, stringCallbackOptions(expected))
		cancel()
		if _, _, err := aborted.Wait(); err == nil || !strings.Contains(err.Error(), "Login cancelled") {
			t.Errorf("abort err=%v", err)
		}

		options := stringCallbackOptions(expected)
		options.Timeout = 10 * time.Millisecond
		timedOut := startTestCallbackServer(t, t.Context(), options)
		if _, _, err := timedOut.Wait(); err == nil || !strings.Contains(err.Error(), "Example sign-in timed out") {
			t.Errorf("timeout err=%v", err)
		}

		alreadyAborted, cancelAborted := context.WithCancel(t.Context())
		cancelAborted()
		if _, err := StartOAuthCallbackServer(alreadyAborted, stringCallbackOptions(expected)); err == nil || !strings.Contains(err.Error(), "Login cancelled") {
			t.Errorf("start err=%v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:158
	t.Run("fails instead of picking another port when the requested port is taken", func(t *testing.T) {
		blocker, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Close() }()
		options := stringCallbackOptions(expected)
		options.Port = blocker.Addr().(*net.TCPAddr).Port

		// Pi: rejects.toMatchObject({ code: "EADDRINUSE" }). nodeerrno maps the platform errno (EADDRINUSE on POSIX, WSAEADDRINUSE on Windows) to Node's error.code.
		if _, err := StartOAuthCallbackServer(t.Context(), options); nodeerrno.ErrorCode(err) != "EADDRINUSE" {
			t.Errorf("err=%v, code=%q, want EADDRINUSE", err, nodeerrno.ErrorCode(err))
		}
	})
}

// Ports the waitForCallbackOrManualInput cases of packages/ai/test/oauth-callback-server.test.ts.
func TestWaitForCallbackOrManualInputUpstream(t *testing.T) {
	newServer := func(t *testing.T) *OAuthCallbackServer[string] {
		options := stringCallbackOptions(nil)
		options.Complete = func(_ context.Context, code string) (string, error) { return code, nil }
		return startTestCallbackServer(t, t.Context(), options)
	}
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:172
	t.Run("returns the browser callback and aborts the manual prompt", func(t *testing.T) {
		server := newServer(t)
		promptCtx := make(chan context.Context, 1)
		type outcome struct {
			result OAuthCallbackOrManualInput[string]
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := WaitForCallbackOrManualInput(t.Context(), AuthInteraction{Notify: func(AuthEvent) {}, Prompt: oauthPendingPrompt(func(ctx context.Context, _ AuthPrompt) { promptCtx <- ctx })}, server, AuthManualCodePrompt{Message: "paste", Placeholder: server.RedirectURI})
			done <- outcome{result, err}
		}()
		manual := <-promptCtx
		oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "from-browser"))

		got := <-done
		if got.err != nil || got.result != (OAuthCallbackOrManualInput[string]{Callback: true, Value: "from-browser"}) {
			t.Errorf("result=%+v err=%v", got.result, got.err)
		}
		if manual.Err() == nil {
			t.Error("manual prompt was not aborted")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:199
	t.Run("returns pasted input and stops waiting for the browser", func(t *testing.T) {
		server := newServer(t)
		result, err := WaitForCallbackOrManualInput(t.Context(), AuthInteraction{Notify: func(AuthEvent) {}, Prompt: func(context.Context, AuthPrompt) (string, error) { return "pasted", nil }}, server, AuthManualCodePrompt{Message: "paste", Placeholder: server.RedirectURI})

		if err != nil || result != (OAuthCallbackOrManualInput[string]{Input: "pasted"}) {
			t.Errorf("result=%+v err=%v", result, err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:222
	t.Run("uses only the manual prompt without a callback server", func(t *testing.T) {
		result, err := WaitForCallbackOrManualInput[string](t.Context(), AuthInteraction{Notify: func(AuthEvent) {}, Prompt: func(context.Context, AuthPrompt) (string, error) { return "pasted", nil }}, nil, AuthManualCodePrompt{Message: "paste", Placeholder: "http://localhost/callback"})

		if err != nil || result != (OAuthCallbackOrManualInput[string]{Input: "pasted"}) {
			t.Errorf("result=%+v err=%v", result, err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/oauth-callback-server.test.ts:234
	t.Run("propagates manual prompt failures", func(t *testing.T) {
		server := newServer(t)
		_, err := WaitForCallbackOrManualInput(t.Context(), AuthInteraction{Notify: func(AuthEvent) {}, Prompt: func(context.Context, AuthPrompt) (string, error) { return "", errors.New("prompt cancelled") }}, server, AuthManualCodePrompt{Message: "paste", Placeholder: server.RedirectURI})

		if err == nil || !strings.Contains(err.Error(), "prompt cancelled") {
			t.Errorf("err=%v", err)
		}
	})
}

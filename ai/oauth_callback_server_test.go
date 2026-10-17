package ai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// The claimed/settled check runs before the provider error is read (.upstream/v0.99.1/packages/ai/src/auth/oauth/callback-server.ts:73-76):
// a redirect that arrives after the sign-in settled is a 409, whatever it carries.
func TestOAuthCallbackServerRejectsLateRedirectsBeforeReadingThem(t *testing.T) {
	server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(nil))
	server.Cancel()
	if _, _, err := server.Wait(); err != nil {
		t.Fatal(err)
	}

	late := oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "error", "access_denied"))

	if late.status != 409 || !strings.Contains(late.body, "This sign-in has already been handled.") {
		t.Fatalf("late error redirect=%+v", late)
	}
}

// The manual prompt is joined: it has returned when the wait does, so no prompt outlives its login.
func TestWaitForCallbackOrManualInputJoinsTheManualPrompt(t *testing.T) {
	server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(nil))
	var exited atomic.Bool
	started := make(chan struct{})
	prompt := func(ctx context.Context, _ AuthPrompt) (string, error) {
		close(started)
		<-ctx.Done()
		exited.Store(true)
		return "", errors.New("prompt aborted")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = WaitForCallbackOrManualInput(t.Context(), AuthInteraction{Prompt: prompt}, server, AuthManualCodePrompt{Message: "paste"})
		if !exited.Load() {
			t.Error("the wait returned before the manual prompt did")
		}
	}()
	<-started
	oauthGet(t, oauthCallbackURL(t, server.RedirectURI, "code", "from-browser"))
	<-done
}

// A cancelled parent context ends the wait with "Login cancelled", not with the manual prompt's cancellation, when a
// callback server owns the cancellation (openrouter-oauth.test.ts:272).
func TestWaitForCallbackOrManualInputReportsLoginCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	server := startTestCallbackServer(t, ctx, stringCallbackOptions(nil))
	started := make(chan struct{})
	prompt := func(ctx context.Context, _ AuthPrompt) (string, error) {
		close(started)
		<-ctx.Done()
		return "", context.Cause(ctx)
	}
	errs := make(chan error, 1)
	go func() {
		_, err := WaitForCallbackOrManualInput(ctx, AuthInteraction{Prompt: prompt}, server, AuthManualCodePrompt{Message: "paste"})
		errs <- err
	}()
	<-started
	cancel()
	if err := <-errs; err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("err=%v", err)
	}
}

// callback-server.ts parses request.url with new URL(request.url, "http://localhost"). The pathname keeps its
// percent-encoding, so "/%63allback" is not "/callback", while "%2e" segments and backslashes resolve as dot segments
// and separators, and URLSearchParams keeps a ";" in the code. The second request claims the sign-in, so the third,
// matched after the claim, gets 409.
func TestOAuthCallbackServerMatchesTheWHATWGPathname(t *testing.T) {
	server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(nil))
	redirect, err := url.Parse(server.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		requestURI string
		status     int
	}{
		{"/%63allback?code=x", 404},
		{"/other/%2e%2e/callback?code=a;b", 200},
		{`/x\..\callback?code=y`, 409},
	} {
		conn, err := net.Dial("tcp", redirect.Host)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n", tc.requestURI)
		line, err := bufio.NewReader(conn).ReadString('\n')
		_ = conn.Close()
		if err != nil || !strings.Contains(line, fmt.Sprintf(" %d ", tc.status)) {
			t.Fatalf("%s: status line %q (err %v), want %d", tc.requestURI, line, err, tc.status)
		}
	}
	if value, ok, err := server.Wait(); err != nil || !ok || value != "completed:a;b" {
		t.Fatalf("wait = %q %v %v", value, ok, err)
	}
}

// callback-server.ts matches new URL(request.url, "http://localhost").pathname exactly (callback-server.ts:80): the path stays percent-encoded and case-sensitive, dot segments
// (also "%2e") resolve, and an absolute-form or "//" target names another host whose path still counts. The raw request target reaches the handler unchanged.
func TestOAuthCallbackServerMatchesTheResolvedRequestTarget(t *testing.T) {
	server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(new("expected-state")))
	redirect, err := url.Parse(server.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		target string
		status int
	}{
		{"/CALLBACK?state=expected-state&code=c", 404},
		{"/%63allback?state=expected-state&code=c", 404},
		{"/callback/?state=expected-state&code=c", 404},
		{"/x/../callback?state=wrong", 400},
		{"/x/%2e%2E/callback?state=wrong", 400},
		{"//elsewhere/callback?state=wrong", 400},
		{"ws://elsewhere/callback?state=wrong", 400},
		{"http://elsewhere/x/./../callback?state=wrong", 400},
		{"ws://elsewhere/CALLBACK?state=wrong", 404},
	} {
		conn, err := net.Dial("tcp", redirect.Host)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", test.target, redirect.Host)
		line, err := bufio.NewReader(conn).ReadString('\n')
		_ = conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("HTTP/1.1 %d ", test.status); !strings.HasPrefix(line, want) {
			t.Errorf("GET %s: %q, want %q", test.target, strings.TrimSpace(line), want)
		}
	}
}

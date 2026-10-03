package ai

import (
	"context"
	"errors"
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

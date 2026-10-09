package codingagent

// pi: packages/coding-agent/src/utils/open-browser.ts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// callbackFirstProvider signs in through a real callback server raced against the dialog's manual code prompt, as
// Anthropic, OpenAI Codex and OpenRouter browser login do, and completes the callback before anyone pastes a code.
type callbackFirstProvider struct{ parityOAuthProvider }

func (callbackFirstProvider) LoginContext(ctx context.Context, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	state := "probe-state"
	server, err := ai.StartOAuthCallbackServer(ctx, ai.OAuthCallbackServerOptions[string]{
		ProviderName: "Callback Probe", Host: "127.0.0.1", Port: 0, Path: "/callback", State: &state,
		Complete: func(_ context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	defer server.Close()
	interaction := ai.AuthInteraction{Notify: func(ai.AuthEvent) {}, Prompt: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
		return callbacks.OnManualCodePromptContext(ctx, prompt.(ai.AuthManualCodePrompt))
	}}
	browser := make(chan error, 1)
	go func() {
		response, err := http.Get(server.RedirectURI + "?code=probe-code&state=" + state)
		if err == nil {
			err = response.Body.Close()
		}
		browser <- err
	}()
	result, err := ai.WaitForCallbackOrManualInput(ctx, interaction, server, ai.AuthManualCodePrompt{Message: "Paste the probe code:"})
	if browserErr := <-browser; browserErr != nil {
		return ai.OAuthCredentials{}, browserErr
	}
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	if !result.Callback || result.Value != "probe-code" {
		return ai.OAuthCredentials{}, fmt.Errorf("result = %+v, want the browser callback", result)
	}
	return ai.OAuthCredentials{Access: "probe-access", Refresh: "probe-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

// Pi's showAuthPrompt races a prompt's answer against prompt.signal and rejects with "Login cancelled" when it aborts
// (.upstream/v1.0.0/packages/coding-agent/src/modes/interactive/interactive-mode.ts:6212-6222);
// waitForCallbackOrManualInput aborts the manual code prompt once the browser callback wins
// (.upstream/v1.0.0/packages/ai/src/auth/oauth/callback-server.ts:157-193). The dialog's manual code prompt must end
// with its context, or the wait that joins it never returns and the login never completes.
func TestBrowserCallbackCompletesLoginWhileTheManualCodePromptIsOpen(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	done := make(chan error, 1)
	go func() {
		done <- m.runLoginRegisteredOAuth(t.Context(), callbackFirstProvider{parityOAuthProvider{id: "callback-probe", name: "Callback Probe"}}, "")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("login did not complete after the browser callback; dialog:\n%s", plainRender(m.editorContainer))
	}
	if got := plainRender(m.chatContainer); !strings.Contains(got, "Logged in to Callback Probe") {
		t.Fatalf("chat = %s, want the login confirmation", got)
	}
}

// Pi's showAuthPrompt shows a manual_code prompt's own message (interactive-mode.ts:6207-6208). OpenAI Codex browser
// login asks "Complete login in your browser, or paste the authorization code / redirect URL here:"
// (.upstream/v1.0.0/packages/ai/src/auth/oauth/openai-codex.ts:379-382).
func TestOpenAICodexLoginDialogShowsTheFlowsManualCodeMessage(t *testing.T) {
	original := openBrowser
	t.Cleanup(func() { openBrowser = original })
	openBrowser = func(string) error { return nil }
	// Keep the fixed Codex callback port free for other test processes; a host that cannot bind leaves the manual
	// prompt as the only path, which shows the same message.
	pid := os.Getpid()
	t.Setenv("PI_OAUTH_CALLBACK_HOST", fmt.Sprintf("127.%d.%d.%d", (pid>>16)&255, (pid>>8)&255, pid&255|1))
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	done := make(chan error, 1)
	go func() { done <- m.runLoginOpenAICodex(t.Context()) }()
	waitForRender(t, m.editorContainer, "Select OpenAI Codex login method:")
	deliverModalInput(t, m, []byte("\r"))
	waitForRender(t, m.editorContainer, "Complete login in your browser, or paste the authorization code / redirect URL here:")
	if got := plainRender(m.editorContainer); strings.Contains(got, "Paste redirect URL below") {
		t.Fatalf("dialog shows the generic manual code message:\n%s", got)
	}
	deliverModalInput(t, m, []byte("\x1b"))
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, errLoginAborted) {
			t.Fatalf("err = %v, want the aborted login", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Codex login did not return after Escape")
	}
}

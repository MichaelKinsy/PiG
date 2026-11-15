package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type rejectedLoginProvider struct{ parityOAuthProvider }

func (rejectedLoginProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("Token exchange failed: 400 invalid_grant")
}

func newLoginFailureMode(t *testing.T) *InteractiveMode {
	t.Helper()
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	m.slashRegistry = NewSlashRegistry()
	return m
}

// runLoginCommand runs a /login line through the command path the input loop uses, which reports a failed login with showError.
func runLoginCommand(t *testing.T, m *InteractiveMode, line string) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.dispatchSlashContext(m.buildSlashContext(t.Context()), line)
	}()
	return done
}

func awaitLoginCommand(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the login dialog stayed open after the login failed")
	}
}

// Pi's showLoginDialog restores the editor when the login rejects (interactive-mode.ts:6564) and reports every message but "Login cancelled" with showError as "Error: Failed to login to <provider name>: <message>" in the transcript (:6573, :4809-4813). The failure is not shown inside the dialog, which is gone, so the next keypress belongs to the editor.
func assertLoginFailureShownAsPi(t *testing.T, m *InteractiveMode, want string) {
	t.Helper()
	chat := plainRender(m.chatContainer)
	if !strings.Contains(chat, want) {
		t.Fatalf("transcript lacks %q:\n%s", want, chat)
	}
	if got, editor := plainRender(m.editorContainer), plainRender(m.editor); got != editor {
		t.Fatalf("the editor slot is not the editor again:\n%s", got)
	}
	if strings.Contains(chat, "Login failed") {
		t.Fatalf("transcript keeps the old dialog wording:\n%s", chat)
	}
}

func TestSlashLoginRejectedOpenAICodexClosesDialogAndReportsError(t *testing.T) {
	original := loginOpenAICodex
	t.Cleanup(func() { loginOpenAICodex = original })
	loginOpenAICodex = func(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
		return ai.OAuthCredentials{}, errors.New("401 Bad credentials")
	}
	m := newLoginFailureMode(t)
	done := runLoginCommand(t, m, "/login openai-codex")
	waitForRender(t, m.editorContainer, "Select OpenAI Codex login method")
	deliverModalInput(t, m, []byte("\r"))
	awaitLoginCommand(t, done)
	assertLoginFailureShownAsPi(t, m, "Error: Failed to login to "+buildAuthProviderName("openai-codex")+": 401 Bad credentials")
}

func TestSlashLoginRejectedGitHubCopilotClosesDialogAndReportsError(t *testing.T) {
	t.Setenv("PIG_PARITY_LOGIN_DIALOG", "unauthorized")
	m := newLoginFailureMode(t)
	done := runLoginCommand(t, m, "/login github-copilot")
	waitForRender(t, m.editorContainer, "Select authentication method for GitHub Copilot")
	deliverModalInput(t, m, []byte("\r"))
	waitForRender(t, m.editorContainer, "GitHub Enterprise URL/domain")
	deliverModalInput(t, m, []byte("\r"))
	awaitLoginCommand(t, done)
	assertLoginFailureShownAsPi(t, m, "Error: Failed to login to "+buildAuthProviderName("github-copilot")+": ")
	if chat := plainRender(m.chatContainer); !strings.Contains(chat, "401") {
		t.Fatalf("transcript lacks the 401 the device endpoint answered:\n%s", chat)
	}
}

func TestRegisteredOAuthLoginRejectedReturnsPiFailure(t *testing.T) {
	m := newLoginFailureMode(t)
	provider := rejectedLoginProvider{parityOAuthProvider{id: "anthropic", name: "Anthropic"}}
	done := make(chan error, 1)
	go func() { done <- m.runLoginRegisteredOAuth(t.Context(), provider, testOAuthLoginName(provider), "") }()
	select {
	case err := <-done:
		if err == nil || err.Error() != "Failed to login to Anthropic: Token exchange failed: 400 invalid_grant" {
			t.Fatalf("rejected login err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the login dialog stayed open after the login failed")
	}
	if got, editor := plainRender(m.editorContainer), plainRender(m.editor); got != editor {
		t.Fatalf("the editor slot is not the editor again:\n%s", got)
	}
}

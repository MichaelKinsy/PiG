package codingagent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactionLoginProvider is a provider object's OAuth login: it takes Pi's AuthInteraction, sends an info event with links and a progress event, then asks one text prompt so the dialog stays open.
type interactionLoginProvider struct{ parityOAuthProvider }

func (p interactionLoginProvider) LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("a provider object's login ran through the login callbacks")
}

func (p interactionLoginProvider) LoginInteraction(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.OAuthCredentials, error) {
	interaction.Notify(ai.AuthInfoEvent{Message: "Use your work account", Links: []ai.AuthInfoLink{{URL: "https://example.test/help", Label: "Help"}, {URL: "https://example.test/terms"}}})
	interaction.Notify(ai.AuthProgressEvent{Message: "Checking"})
	if _, err := interaction.Prompt(ctx, ai.AuthTextPrompt{Message: "Workspace:"}); err != nil {
		return ai.OAuthCredentials{}, err
	}
	return ai.OAuthCredentials{Access: "a", Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

// A provider object's auth.oauth.login takes Pi's AuthInteraction (interactive-mode.ts:6315-6336 loginProvider), and Pi's login dialog shows an info event's message and each link, labelled "<label>: <url>" or as the bare URL (:6308-6309 notifyAuthDialog, login-dialog.ts:190-203 showInfo).
func TestOAuthLoginShowsAnInfoEventWithItsLinks(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	done := make(chan error, 1)
	go func() {
		done <- m.runLoginRegisteredOAuth(t.Context(), interactionLoginProvider{parityOAuthProvider{id: "info-event", name: "Info Event"}}, "Info Event", "")
	}()
	waitForRender(t, m.editorContainer, "Workspace:")
	dialog := plainRender(m.editorContainer)
	order := []string{"Use your work account", "Help: https://example.test/help", "https://example.test/terms", "Checking", "Workspace:"}
	last := -1
	for _, want := range order {
		at := strings.Index(dialog, want)
		if at <= last {
			t.Fatalf("dialog lacks %q after the previous lines %q:\n%s", want, order, dialog)
		}
		last = at
	}
	deliverModalInput(t, m, []byte("\r"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("login did not complete; dialog:\n%s", plainRender(m.editorContainer))
	}
	if got := plainRender(m.chatContainer); !strings.Contains(got, "Logged in to Info Event") {
		t.Fatalf("chat = %s, want the login confirmation", got)
	}
}

// secretInteractionLoginProvider is a provider object's OAuth login that asks one secret prompt and stores the answer as its access token.
type secretInteractionLoginProvider struct{ parityOAuthProvider }

func (p secretInteractionLoginProvider) LoginInteraction(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.OAuthCredentials, error) {
	token, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Workspace token:"})
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	return ai.OAuthCredentials{Access: token, Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

// A secret prompt of a provider object's OAuth login follows maskSecretInput (D80), as the same prompt in its API-key login does: Pi's showAuthPrompt shows it with showPrompt (interactive-mode.ts:6278-6286), and PiG's default masks a prompt the flow marked secret.
func TestOAuthInteractionSecretPromptHonorsMaskSecretInput(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	done := make(chan error, 1)
	go func() {
		done <- m.runLoginRegisteredOAuth(t.Context(), secretInteractionLoginProvider{parityOAuthProvider{id: "secret-event", name: "Secret Event"}}, "Secret Event", "")
	}()
	waitForRender(t, m.editorContainer, "Workspace token:")
	const token = "workspace-secret-9876"
	deliverModalInput(t, m, []byte(token))
	deliverModalInput(t, m, []byte(""))
	frame := plainRender(m.editorContainer)
	if strings.Contains(frame, token) || !strings.Contains(frame, "Input hidden (PiG default)") || !strings.Contains(frame, "9876") {
		t.Fatalf("secret prompt is not masked:\n%s", frame)
	}
	deliverModalInput(t, m, []byte("\r"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("login did not complete; dialog:\n%s", plainRender(m.editorContainer))
	}
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if credential, ok, err := store.Get("secret-event"); err != nil || !ok || credential.Access != token {
		t.Fatalf("stored credential = %+v, %v, %v; want the submitted token", credential, ok, err)
	}
}

// testOAuthLoginName is the provider name /login shows for a test flow: its catalog name, or the flow's own name for a provider outside the catalog.
func testOAuthLoginName(provider ai.OAuthProviderInterface) string {
	if name := ai.ProviderDisplayName(provider.ID()); name != provider.ID() {
		return name
	}
	return provider.Name()
}

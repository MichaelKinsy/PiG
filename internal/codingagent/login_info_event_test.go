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

// interactionLoginProvider is a provider object's OAuth login: it takes Pi's AuthInteraction, sends an info event with links and a progress event, then asks one text prompt so the dialog stays open.
type interactionLoginProvider struct{ parityOAuthProvider }

func (p interactionLoginProvider) LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("a provider object's login ran through the login callbacks")
}

func (p interactionLoginProvider) LoginInteraction(ctx context.Context, interaction ai.AuthInteraction) (ai.OAuthCredentials, error) {
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
		done <- m.runLoginRegisteredOAuth(t.Context(), interactionLoginProvider{parityOAuthProvider{id: "info-event", name: "Info Event"}}, "")
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

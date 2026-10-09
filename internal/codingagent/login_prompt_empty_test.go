package codingagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// emptyAnswerProvider asks one text prompt and signs in with whatever it was answered.
type emptyAnswerProvider struct {
	parityOAuthProvider
	answers chan string
}

func (p emptyAnswerProvider) LoginContext(ctx context.Context, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	answer, err := callbacks.OnPromptContext(ctx, ai.OAuthPrompt{Message: "Workspace (optional):"})
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	p.answers <- answer
	return ai.OAuthCredentials{Access: "workspace-" + answer, Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

// Pi's showPrompt resolves the submitted text, empty or not (login-dialog.ts:58-66, 154-176); no prompt enforces
// allowEmpty (interactive-mode.ts:6278-6300 showAuthPrompt). An empty answer reaches the flow.
func TestOAuthLoginTextPromptAcceptsAnEmptyAnswer(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	provider := emptyAnswerProvider{parityOAuthProvider{id: "empty-answer", name: "Empty Answer"}, make(chan string, 1)}
	done := make(chan error, 1)
	go func() { done <- m.runLoginRegisteredOAuth(t.Context(), provider, "") }()
	waitForRender(t, m.editorContainer, "Workspace (optional):")
	deliverModalInput(t, m, []byte("\r"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("login did not complete; dialog:\n%s", plainRender(m.editorContainer))
	}
	select {
	case answer := <-provider.answers:
		if answer != "" {
			t.Fatalf("the flow received %q, want the empty answer", answer)
		}
	default:
		t.Fatalf("the flow never received the answer; chat:\n%s", plainRender(m.chatContainer))
	}
	if got := plainRender(m.chatContainer); !strings.Contains(got, "Logged in to Empty Answer") {
		t.Fatalf("chat = %s, want the login confirmation", got)
	}
}

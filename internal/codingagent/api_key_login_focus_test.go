package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/login-dialog.ts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// interactive-mode.ts:6181-6190 showApiKeyLoginDialog focuses the mounted LoginDialogComponent, whose focused setter focuses its prompt input
// (login-dialog.ts:25-31), so the API-key prompt emits the hardware-cursor marker; restoreEditor returns focus when the login ends.
func TestAPIKeyLoginPromptHoldsFocus(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	method := ai.EnvAPIKeyAuth("Focus service token")
	method.Login = func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Focus secret"})
		return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
	}
	if err := m.opts.ModelRegistry.RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "focus-proxy", Name: "Focus Proxy", Auth: ai.ProviderAuth{APIKey: method}, GetModels: func() ([]*ai.Model, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	before := m.tuiInst.GetFocusedComponent()
	done := make(chan error, 1)
	go func() {
		done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login Focus Proxy", nil)
	}()
	waitForRender(t, m.editorContainer, "Focus secret")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(strings.Join(m.editorContainer.Render(400), "\n"), widthx.CursorMarker) {
		if time.Now().After(deadline) {
			t.Fatalf("the API-key prompt never rendered the hardware-cursor marker; focused component %T", m.tuiInst.GetFocusedComponent())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := m.tuiInst.GetFocusedComponent().(*tui.LoginDialogComponent); !ok {
		t.Fatalf("focused component during the prompt = %T, want the login dialog", m.tuiInst.GetFocusedComponent())
	}
	deliverModalInput(t, m, []byte("focus-key"))
	deliverModalInput(t, m, []byte("\r"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := m.tuiInst.GetFocusedComponent(); got != before {
		t.Fatalf("focus after the login = %T, want the focus before it (%T)", got, before)
	}
}

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

// Pi 1.0.0 passes onBack to the API key and ambient login dialogs too: showApiKeyLoginDialog calls it when a login
// prompt is cancelled ("Login cancelled", interactive-mode.ts:6160-6162), and showAmbientAuthDialog calls it when the
// setup note closes (interactive-mode.ts:6094-6101). Started from the provider list, both reopen the list.
func TestAPIKeyLoginCancelReturnsToTheProviderList(t *testing.T) {
	for _, tc := range []struct {
		name, id, providerName, prompt string
		login                          func(context.Context, ai.AuthInteraction) (ai.Credential, error)
	}{
		{
			name: "cancelled select prompt", id: "select-probe", providerName: "Select Probe", prompt: "Select probe authentication method:",
			login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
				_, err := interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Select probe authentication method:", Options: []ai.AuthSelectOption{{ID: "key", Label: "API key"}, {ID: "profile", Label: "Profile"}}})
				return ai.Credential{}, err
			},
		},
		{name: "closed ambient setup note", id: "ambient-probe", providerName: "Ambient Probe", prompt: "Ambient token is configured outside pig."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.runCtx = t.Context()
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			method := ai.EnvAPIKeyAuth("Ambient token")
			method.Login = tc.login
			if err := m.opts.ModelRegistry.RegisterNativeModelsProvider(&ai.ModelsProvider{ID: tc.id, Name: tc.providerName, Auth: ai.ProviderAuth{APIKey: method}, GetModels: func() ([]*ai.Model, error) { return nil, nil }}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			// An argument that matches no provider opens the provider list with it as the search (interactive-mode.ts:5794).
			go func() {
				done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login "+strings.ToLower(tc.providerName[:4]), nil)
			}()
			waitForRender(t, m.editorContainer, "Select provider to configure:")
			waitForRender(t, m.editorContainer, tc.providerName)
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, tc.prompt)
			deliverModalInput(t, m, []byte("\x1b"))
			waitForRender(t, m.editorContainer, "Select provider to configure:")
			if got := plainRender(m.chatContainer); strings.Contains(got, "Failed to save API key") {
				t.Fatalf("a cancelled login reported a failure: %s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, errLoginCancelled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("/login did not return")
			}
		})
	}
}

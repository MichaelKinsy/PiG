package codingagent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestLoginVertexServiceAccountUsesProviderPrompts(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.opts.Model = &ai.Model{ID: "existing", ProviderMeta: ai.ProviderMetadata{ProviderID: "existing"}}
	done := make(chan error, 1)
	go func() {
		done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login google-vertex", nil)
	}()
	waitForRender(t, m.editorContainer, "Select Google Vertex AI authentication method:")
	deliverModalInput(t, m, []byte("\x1b[B"))
	deliverModalInput(t, m, []byte("\x1b[B"))
	deliverModalInput(t, m, []byte("\r"))
	for _, step := range []struct{ prompt, reply string }{{"Enter Google Cloud project ID", "project"}, {"Enter Google Cloud location", "us-central1"}, {"Enter service account credentials file path", "/credentials/service.json"}} {
		waitForRender(t, m.editorContainer, step.prompt)
		deliverModalInput(t, m, []byte(step.reply))
		deliverModalInput(t, m, []byte("\r"))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok, err := store.Get("google-vertex")
	if err != nil || !ok || credential.Key != "" || credential.Env["GOOGLE_CLOUD_PROJECT"] != "project" || credential.Env["GOOGLE_CLOUD_LOCATION"] != "us-central1" || credential.Env["GOOGLE_APPLICATION_CREDENTIALS"] != "/credentials/service.json" {
		t.Fatalf("stored=%+v ok=%v err=%v", credential, ok, err)
	}
}

func TestLoginRegisteredProviderUsesDeclaredMethodWithoutDefaultModel(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	method := ai.EnvAPIKeyAuth("Custom service token")
	method.Login = func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Custom secret"})
		if err != nil {
			return ai.Credential{}, err
		}
		region, err := interaction.Prompt(ctx, ai.AuthTextPrompt{Message: "Custom region"})
		return ai.Credential{Type: ai.CredentialAPIKey, Key: key, Env: map[string]string{"REGION": region}}, err
	}
	if err := m.opts.ModelRegistry.RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "custom-proxy", Name: "Custom Proxy", Auth: ai.ProviderAuth{APIKey: method}, GetModels: func() ([]*ai.Model, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login Custom Proxy", nil)
	}()
	for _, step := range []struct{ prompt, reply string }{{"Custom secret", " custom-key "}, {"Custom region", "region-one"}} {
		waitForRender(t, m.editorContainer, step.prompt)
		deliverModalInput(t, m, []byte(step.reply))
		deliverModalInput(t, m, []byte("\r"))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if text := plainRender(m.chatContainer); !strings.Contains(text, `no default model is configured for provider "custom-proxy"`) {
		t.Fatalf("missing no-default guidance: %s", text)
	}
	if m.opts.Model != nil {
		t.Fatal("selected a model for a provider without a default")
	}
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok, err := store.Get("custom-proxy")
	if err != nil || !ok || credential.Key != " custom-key " || credential.Env["REGION"] != "region-one" {
		t.Fatalf("credential=%+v ok=%v err=%v", credential, ok, err)
	}
}

func TestLoginArgumentCompletionWithoutCredentials(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir()})
	// Missing credential storage must not remove provider metadata from completion.
	m.opts.AgentDir = filepath.Join(m.opts.AgentDir, "missing")
	if items := m.loginArgCompletions("openrouter"); len(items) != 1 {
		t.Fatalf("metadata completion depends on credential storage: %v", items)
	}
}

func BenchmarkLoginArgumentCompletions(b *testing.B) {
	m := NewInteractiveMode(InteractiveOptions{AgentDir: b.TempDir()})
	b.ReportAllocs()
	for b.Loop() {
		_ = m.loginArgCompletions("open")
	}
}

func TestLoginSelectCancellationIsSilent(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	done := make(chan error, 1)
	go func() {
		done <- m.runAPIKeyLogin(tui.OAuthProvider{ID: "google-vertex", Name: "Google Vertex AI", AuthType: "api_key"})
	}()
	waitForRender(t, m.editorContainer, "Select Google Vertex AI authentication method:")
	deliverModalInput(t, m, []byte("\x1b"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plainRender(m.chatContainer), "Failed") {
		t.Fatal("select cancellation should reject as Login cancelled, not abort the dialog")
	}
}

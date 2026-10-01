package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func model(id, provider, baseURL string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: id, Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: ai.APIOpenAICompletions, BaseURL: baseURL}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
}
func unused(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return nil, errors.New("unused")
}
func provider(m *ai.Model) *ai.ModelsProvider {
	return &ai.ModelsProvider{ID: m.ProviderMeta.ProviderID, Name: "Extension Native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{m}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Native key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}, Source: "native"}, nil
	}}}, Stream: unused, StreamSimple: unused}
}
func run() (resultErr error) {
	root, err := os.MkdirTemp("", "native-compat-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	result := map[string]any{}
	create := func(name, config string) (*coding.Services, *ai.InMemoryModelsStore, error) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, err
		}
		if config != "" {
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				return nil, nil, err
			}
		}
		services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			return nil, nil, err
		}
		store := ai.NewInMemoryModelsStore()
		services.Registry().SetModelsStore(store)
		return services, store, nil
	}
	services, _, err := create("auth", "")
	if err != nil {
		return err
	}
	runtime := services.ModelRuntime()
	p := provider(model("native", "extension-native", "https://fallback.test/v1"))
	p.Auth.APIKey = &ai.APIKeyAuth{Name: "Native setup", Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "API key"})
		return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
	}, Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		if input.Credential != nil && input.Credential.Key != "" {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored native key"}, nil
		}
		return nil, nil
	}, Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if input.Credential != nil && input.Credential.Key != "" {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key, BaseURL: "https://resolved.test/v1"}, Source: "stored native key"}, nil
		}
		return nil, nil
	}}
	if err := runtime.RegisterNativeProvider(p); err != nil {
		return err
	}
	if services.Registry().GetProvider(p.ID) != p || services.Registry().GetRegisteredNativeProvider(p.ID) != p || services.Registry().Find(p.ID, "native") == nil {
		return errors.New("native identity missing")
	}
	if _, err := runtime.Login(context.Background(), p.ID, ai.CredentialAPIKey, ai.AuthInteraction{Prompt: func(context.Context, ai.AuthPrompt) (string, error) { return "secret", nil }, Notify: func(ai.AuthEvent) {}}); err != nil {
		return err
	}
	auth, err := services.Registry().GetProviderAuth(context.Background(), p.ID)
	if err != nil {
		return err
	}
	result["auth"] = map[string]any{"auth": map[string]any{"apiKey": auth.Auth.APIKey, "baseUrl": auth.Auth.BaseURL}, "source": auth.Source}
	services.Registry().UnregisterProvider(p.ID)
	if services.Registry().GetProvider(p.ID) != nil {
		return errors.New("native provider retained")
	}
	overlaid, _, err := create("overlay", `{"providers":{"extension-native-deferred":{"baseUrl":"https://overlay.test/v1"},"extension-native":{"modelOverrides":{"native":{"contextWindow":4242}}}}}`)
	if err != nil {
		return err
	}
	deferred := provider(model("native-deferred", "extension-native-deferred", "https://native.test/v1"))
	deferred.FetchDeferred = func(_ context.Context, m *ai.Model, handle ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
		result["fetch"] = map[string]any{"baseUrl": m.ProviderMeta.BaseURL, "id": handle.ID, "apiKey": options.APIKey, "wait": options.Wait, "headers": options.Headers, "transformForwarded": options.TransformHeaders != nil}
		message := &ai.AssistantMessage{API: m.ProviderMeta.API, Provider: m.ProviderMeta.ProviderID, Model: m.ID, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		_ = stream.Push(ai.StartEvent{Partial: message})
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		return stream, nil
	}
	deferred.CancelDeferred = func(_ context.Context, _ *ai.Model, handle ai.DeferredHandle, options ai.DeferredCancelOptions) error {
		result["cancel"] = map[string]any{"id": handle.ID, "apiKey": options.APIKey, "timeoutMs": options.TimeoutMs, "headers": options.Headers, "transformForwarded": options.TransformHeaders != nil}
		return nil
	}
	if err := overlaid.ModelRuntime().RegisterNativeProvider(deferred); err != nil {
		return err
	}
	selected := overlaid.ModelRuntime().GetModel(deferred.ID, "native-deferred")
	handle := ai.DeferredHandle{Provider: deferred.ID, ModelID: selected.ID, API: selected.ProviderMeta.API, ID: "fetch-id"}
	transform := func(value string) func(context.Context, ai.ProviderHeaders) (ai.ProviderHeaders, error) {
		return func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
			out := maps.Clone(headers)
			if out == nil {
				out = ai.ProviderHeaders{}
			}
			out["X-Transformed"] = new(value)
			return out, nil
		}
	}
	fetched := overlaid.ModelRuntime().FetchDeferred(context.Background(), selected, handle, ai.DeferredFetchOptions{Wait: new(float64(25)), StreamOptions: ai.StreamOptions{Headers: ai.ProviderHeaders{"X-Fetch": new("fetch")}, TransformHeaders: transform("fetch")}})
	if fetched.StopReason != ai.StopReasonStop {
		return fmt.Errorf("fetch: %s", fetched.ErrorMessage)
	}
	handle.ID = "cancel-id"
	if err := overlaid.ModelRuntime().CancelDeferred(context.Background(), selected, handle, ai.DeferredCancelOptions{TimeoutMs: new(100), TransformHeaders: transform("cancel")}); err != nil {
		return err
	}
	if err := overlaid.ModelRuntime().RegisterNativeProvider(p); err != nil {
		return err
	}
	result["contextWindow"] = overlaid.ModelRuntime().GetModel(p.ID, "native").Capabilities.ContextWindow
	dynamic, store, err := create("dynamic", "")
	if err != nil {
		return err
	}
	if err := dynamic.ModelRuntime().RegisterProvider("extension-dynamic", coding.ProviderConfigInput{BaseURL: "http://localhost:8080/v1", API: ai.APIOpenAICompletions, APIKey: "local", RefreshModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		return []ai.AnyModel{model("live", "extension-dynamic", "http://localhost:8080/v1")}, nil
	}}); err != nil {
		return err
	}
	refresh := dynamic.ModelRuntime().Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if len(refresh.Errors) > 0 {
		return fmt.Errorf("refresh: %v", refresh.Errors)
	}
	if dynamic.ModelRuntime().GetModel("extension-dynamic", "live") == nil {
		return errors.New("dynamic model missing")
	}
	stored, err := store.Read(context.Background(), "extension-dynamic")
	if err != nil {
		return err
	}
	result["persisted"] = stored
	legacy, _, err := create("legacy", "")
	if err != nil {
		return err
	}
	if err := legacy.Auth().Set("extension-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}); err != nil {
		return err
	}
	if err := legacy.ModelRuntime().RegisterProvider("extension-oauth", coding.ProviderConfigInput{BaseURL: "https://example.test/v1", API: ai.APIOpenAICompletions, Models: []ai.AnyModel{model("base", "extension-oauth", "https://example.test/v1")}, OAuth: &coding.ExtensionOAuthConfig{Name: "Extension OAuth", RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil }, GetAPIKey: func(credential ai.Credential) string { return credential.Access }, ModifyModels: func(models []*ai.Model, credential ai.Credential) []*ai.Model {
		if credential.Access == "access" {
			return append(models, model("credential-model", "extension-oauth", "https://example.test/v1"))
		}
		return models
	}}}); err != nil {
		return err
	}
	refresh = legacy.ModelRuntime().Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if len(refresh.Errors) > 0 {
		return fmt.Errorf("refresh: %v", refresh.Errors)
	}
	ids := func() []string {
		var ids []string
		for _, m := range legacy.ModelRuntime().GetModels() {
			if m.ProviderMeta.ProviderID == "extension-oauth" {
				ids = append(ids, m.ID)
			}
		}
		return ids
	}
	result["beforeLogout"] = ids()
	if err := legacy.ModelRuntime().Logout(context.Background(), "extension-oauth"); err != nil {
		return err
	}
	result["afterLogout"] = ids()
	return json.NewEncoder(os.Stdout).Encode(result)
}

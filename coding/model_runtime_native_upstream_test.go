package coding

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func nativeCompatModel(id, provider, baseURL string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: id, Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: ai.APIOpenAICompletions, BaseURL: baseURL}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
}
func nativeCompatServices(t *testing.T, config string, credentials map[string]ai.Credential) (*Services, *ai.InMemoryModelsStore) {
	t.Helper()
	dir := t.TempDir()
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if credentials != nil {
		data, err := json.Marshal(credentials)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	services, err := NewServices(ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	store := ai.NewInMemoryModelsStore()
	services.Registry().SetModelsStore(store)
	return services, store
}
func nativeUnusedStream(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	panic("unused")
}
func nativeCompatProvider(model *ai.Model) *ai.ModelsProvider {
	return &ai.ModelsProvider{ID: model.ProviderMeta.ProviderID, Name: "Extension Native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Native key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}, Source: "native"}, nil
	}}}, Stream: nativeUnusedStream, StreamSimple: nativeUnusedStream}
}

func TestModelRuntimeNativeCompatibilityUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-modify-models-compat.test.ts:33
	t.Run("registers native pi-ai providers with their auth implementation", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		registry := services.Registry()
		model := nativeCompatModel("native", "extension-native", "https://fallback.test/v1")
		provider := nativeCompatProvider(model)
		provider.Auth.APIKey = &ai.APIKeyAuth{Name: "Native setup", Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
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
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if registry.GetProvider(provider.ID) != provider || registry.GetRegisteredNativeProvider(provider.ID) != provider {
			t.Fatal("native identity was replaced")
		}
		if !slices.Contains(registry.GetRegisteredProviderIDs(), provider.ID) || registry.Find(provider.ID, model.ID) == nil {
			t.Fatal("native registration absent from facade")
		}
		_, err := runtime.Login(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{Prompt: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			if !reflect.DeepEqual(prompt, ai.AuthSecretPrompt{Message: "API key"}) {
				t.Errorf("prompt=%+v", prompt)
			}
			return "secret", nil
		}, Notify: func(ai.AuthEvent) {}})
		if err != nil {
			t.Fatal(err)
		}
		auth, err := registry.GetProviderAuth(t.Context(), provider.ID)
		if err != nil {
			t.Fatal(err)
		}
		if auth == nil || auth.Auth.APIKey != "secret" || auth.Auth.BaseURL != "https://resolved.test/v1" {
			t.Fatalf("auth=%+v", auth)
		}
		registry.UnregisterProvider(provider.ID)
		if registry.GetProvider(provider.ID) != nil {
			t.Fatal("unregister retained provider")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-modify-models-compat.test.ts:94
	t.Run("preserves native deferred methods through provider overlays", func(t *testing.T) {
		services, _ := nativeCompatServices(t, `{"providers":{"extension-native-deferred":{"baseUrl":"https://overlay.test/v1"}}}`, nil)
		runtime := services.ModelRuntime()
		model := nativeCompatModel("native-deferred", "extension-native-deferred", "https://native.test/v1")
		provider := nativeCompatProvider(model)
		var fetchedURL, cancelledID string
		var fetched ai.DeferredFetchOptions
		var cancelled ai.DeferredCancelOptions
		provider.FetchDeferred = func(_ context.Context, request *ai.Model, _ ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
			fetchedURL, fetched = request.ProviderMeta.BaseURL, options
			message := &ai.AssistantMessage{API: request.ProviderMeta.API, Provider: request.ProviderMeta.ProviderID, Model: request.ID, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop}
			stream := ai.NewAssistantMessageEventStream()
			_ = stream.Push(ai.StartEvent{Partial: message})
			_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
			return stream, nil
		}
		provider.CancelDeferred = func(_ context.Context, _ *ai.Model, handle ai.DeferredHandle, options ai.DeferredCancelOptions) error {
			cancelledID, cancelled = handle.ID, options
			return nil
		}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		selected := runtime.GetModel(provider.ID, model.ID)
		if selected == nil {
			t.Fatal("composed model missing")
		}
		handle := ai.DeferredHandle{Provider: provider.ID, ModelID: model.ID, API: model.ProviderMeta.API, ID: "fetch-id"}
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
		reply := runtime.FetchDeferred(t.Context(), selected, handle, ai.DeferredFetchOptions{Wait: new(float64(25)), StreamOptions: ai.StreamOptions{Headers: ai.ProviderHeaders{"X-Fetch": new("fetch")}, TransformHeaders: transform("fetch")}})
		if reply.StopReason != ai.StopReasonStop {
			t.Fatalf("fetch=%+v", reply)
		}
		handle.ID = "cancel-id"
		if err := runtime.CancelDeferred(t.Context(), selected, handle, ai.DeferredCancelOptions{TimeoutMs: new(100), TransformHeaders: transform("cancel")}); err != nil {
			t.Fatal(err)
		}
		if fetchedURL != "https://overlay.test/v1" || fetched.APIKey != "key" || fetched.Wait == nil || *fetched.Wait != 25 || !reflect.DeepEqual(fetched.Headers, ai.ProviderHeaders{"X-Fetch": new("fetch"), "X-Transformed": new("fetch")}) {
			t.Fatalf("fetch URL=%s opts=%+v", fetchedURL, fetched)
		}
		if cancelledID != "cancel-id" || cancelled.APIKey != "key" || cancelled.TimeoutMs == nil || *cancelled.TimeoutMs != 100 || !reflect.DeepEqual(cancelled.Headers, ai.ProviderHeaders{"X-Transformed": new("cancel")}) {
			t.Fatalf("cancel=%s %+v", cancelledID, cancelled)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-modify-models-compat.test.ts:218
	t.Run("applies models.json overrides above native providers", func(t *testing.T) {
		services, _ := nativeCompatServices(t, `{"providers":{"extension-native":{"modelOverrides":{"native":{"contextWindow":4242}}}}}`, nil)
		model := nativeCompatModel("native", "extension-native", "https://native.test/v1")
		if err := services.ModelRuntime().RegisterNativeProvider(nativeCompatProvider(model)); err != nil {
			t.Fatal(err)
		}
		got := services.ModelRuntime().GetModel("extension-native", "native")
		if got == nil || got.Capabilities.ContextWindow != 4242 {
			t.Fatalf("model=%+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-modify-models-compat.test.ts:269
	t.Run("publishes refreshModels results without forcing ModelsStore persistence", func(t *testing.T) {
		services, store := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		if err := runtime.RegisterProvider("extension-dynamic", ProviderConfigInput{BaseURL: "http://localhost:8080/v1", API: ai.APIOpenAICompletions, APIKey: "local", RefreshModels: func(ai.RefreshModelsContext) ([]*ai.Model, error) {
			return []*ai.Model{nativeCompatModel("live", "extension-dynamic", "http://localhost:8080/v1")}, nil
		}}); err != nil {
			t.Fatal(err)
		}
		result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
		if result.Aborted || len(result.Errors) > 0 {
			t.Fatalf("refresh=%+v", result)
		}
		if runtime.GetModel("extension-dynamic", "live") == nil {
			t.Fatal("published model missing")
		}
		stored, err := store.Read(t.Context(), "extension-dynamic")
		if err != nil || stored != nil {
			t.Fatalf("stored=%+v err=%v", stored, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-modify-models-compat.test.ts:295
	t.Run("applies legacy OAuth modifyModels after async credential initialization", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", map[string]ai.Credential{"extension-oauth": {Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}})
		runtime := services.ModelRuntime()
		model := nativeCompatModel("base", "extension-oauth", "https://example.test/v1")
		if err := runtime.RegisterProvider("extension-oauth", ProviderConfigInput{BaseURL: model.ProviderMeta.BaseURL, API: model.ProviderMeta.API, Models: []*ai.Model{model}, OAuth: &ExtensionOAuthConfig{Name: "Extension OAuth", RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil }, GetAPIKey: func(credential ai.Credential) string { return credential.Access }, ModifyModels: func(models []*ai.Model, credential ai.Credential) []*ai.Model {
			if credential.Access == "access" {
				return append(models, nativeCompatModel("credential-model", "extension-oauth", "https://example.test/v1"))
			}
			return models
		}}}); err != nil {
			t.Fatal(err)
		}
		result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
		if result.Aborted || len(result.Errors) > 0 {
			t.Fatalf("refresh=%+v", result)
		}
		if runtime.GetModel("extension-oauth", "base") == nil || runtime.GetModel("extension-oauth", "credential-model") == nil {
			t.Fatal("credential projection missing")
		}
		if err := runtime.Logout(t.Context(), "extension-oauth"); err != nil {
			t.Fatal(err)
		}
		if runtime.GetModel("extension-oauth", "credential-model") != nil {
			t.Fatal("logout retained credential projection")
		}
	})
}

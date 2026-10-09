package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func namespaceProvider(id, modelID string, configured bool) *ai.ModelsProvider {
	return &ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) {
		return []*ai.Model{nativeCompatModel(modelID, id, "https://example.invalid/v1")}, nil
	}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "native", Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		if configured {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "native"}, nil
		}
		return nil, nil
	}}}}
}

func namespaceExtensionConfig(modelID string) extension.ProviderConfig {
	return extension.ProviderConfig{API: ai.APIOpenAICompletions, APIKey: "configured", BaseURL: "https://example.invalid/v1", Models: []extension.ProviderModelConfig{{ID: modelID, Name: modelID, Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100}}}
}

func TestRegistryNamespaceReplacementAcrossEntryPoints(t *testing.T) {
	// Pi model-runtime.ts:744-766 keeps one native-or-legacy registration namespace. Catalog lookup must change synchronously; fresh availability is checked separately from RP-011's cached-publication contract.
	for _, from := range []string{"native", "runtime", "extension"} {
		for _, to := range []string{"native", "runtime", "extension"} {
			if from == to {
				continue
			}
			t.Run(from+" to "+to, func(t *testing.T) {
				_, services := newAvailabilitySession(t)
				runtime := services.ModelRuntime()
				const id = "namespace-provider"
				register := func(kind, modelID string) error {
					switch kind {
					case "native":
						return runtime.RegisterNativeProvider(namespaceProvider(id, modelID, true))
					case "runtime":
						return runtime.RegisterProvider(id, ProviderConfigInput{API: ai.APIOpenAICompletions, APIKey: "configured", BaseURL: "https://example.invalid/v1", Models: []ai.AnyModel{nativeCompatModel(modelID, id, "https://example.invalid/v1")}})
					default:
						return services.Registry().RegisterExtensionProvider(id, namespaceExtensionConfig(modelID))
					}
				}
				if err := register(from, "old"); err != nil {
					t.Fatal(err)
				}
				if err := register(to, "new"); err != nil {
					t.Fatal(err)
				}
				if runtime.GetModel(id, "old") != nil || runtime.GetModel(id, "new") == nil {
					t.Fatalf("namespace retained displaced catalog: old=%v new=%v", runtime.GetModel(id, "old"), runtime.GetModel(id, "new"))
				}
				available, err := runtime.GetAvailable(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]string, 0, len(available))
				for _, model := range available {
					ids = append(ids, model.ID)
				}
				if !slices.Equal(ids, []string{"new"}) {
					t.Fatalf("available=%v", ids)
				}
			})
		}
	}
}

func TestRegistryNativeReplacementDropsLegacyConfiguredAuth(t *testing.T) {
	for _, from := range []string{"runtime", "extension"} {
		t.Run(from, func(t *testing.T) {
			_, services := newAvailabilitySession(t)
			runtime := services.ModelRuntime()
			const id = "namespace-auth"
			var err error
			if from == "runtime" {
				err = runtime.RegisterProvider(id, ProviderConfigInput{API: ai.APIOpenAICompletions, APIKey: "old-key", BaseURL: "https://example.invalid/v1", Models: []ai.AnyModel{nativeCompatModel("old", id, "https://example.invalid/v1")}})
			} else {
				err = services.Registry().RegisterExtensionProvider(id, namespaceExtensionConfig("old"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.GetAvailable(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := runtime.RegisterNativeProvider(namespaceProvider(id, "new", false)); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.GetAvailable(t.Context()); err != nil {
				t.Fatal(err)
			}
			if status := runtime.GetProviderAuthStatus(id); status.Configured {
				t.Fatalf("displaced legacy auth remains configured: %+v", status)
			}
		})
	}
}

// Pi: packages/coding-agent/src/core/model-registry.ts:233 (ModelRegistry.getRegisteredProviderConfig).
func TestRegistryLegacyMetadataUpdateRetainsRuntimeCallbacks(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-callbacks"
	var refreshes int
	input := ProviderConfigInput{API: ai.APIOpenAICompletions, APIKey: "configured", BaseURL: "https://example.invalid/v1", Models: []ai.AnyModel{nativeCompatModel("old", id, "https://example.invalid/v1")}, StreamSimple: nativeUnusedStream, RefreshModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		refreshes++
		return []ai.AnyModel{nativeCompatModel("refreshed", id, "https://example.invalid/v1")}, nil
	}}
	if err := runtime.RegisterProvider(id, input); err != nil {
		t.Fatal(err)
	}
	if err := services.Registry().RegisterExtensionProvider(id, extension.ProviderConfig{Headers: map[string]string{"X-New": "new"}}); err != nil {
		t.Fatal(err)
	}
	retained := services.Registry().GetRegisteredProviderConfig(id)
	if retained == nil || retained.StreamSimple == nil || retained.RefreshModels == nil || retained.Headers["X-New"] != "new" {
		t.Fatalf("legacy update did not preserve and merge native callbacks/config: %+v", retained)
	}
	if err := services.Registry().RegisterExtensionProvider(id, extension.ProviderConfig{Headers: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	retained = services.Registry().GetRegisteredProviderConfig(id)
	if retained.Headers == nil || len(retained.Headers) != 0 || retained.StreamSimple == nil || retained.RefreshModels == nil {
		t.Fatalf("defined empty headers did not replace the earlier map while retaining callbacks: %+v", retained)
	}
	result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{id}, AllowNetwork: new(false)})
	if result.Aborted || len(result.Errors) != 0 || refreshes == 0 {
		t.Fatalf("refresh=%+v callback calls=%d", result, refreshes)
	}
	if runtime.GetModel(id, "refreshed") == nil {
		t.Fatal("retained refresh callback did not publish models")
	}
}

type namespaceNoNetwork struct{}

func (namespaceNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected fallback HTTP")
}

func namespaceResult(text string) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop}
	_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
	return stream
}

func TestRegistryCrossEntryCallbackInheritanceAndReplacement(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-stream"
	config := namespaceExtensionConfig("old")
	config.StreamSimple = func(model extension.Model, _ extension.AIContext, options extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		if model.ID != "old" || options.APIKey != "configured" {
			t.Error("inherited callback received stale metadata/auth")
		}
		return namespaceResult("inherited")
	}
	if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider(id, ProviderConfigInput{Headers: map[string]string{"X-New": "new"}}); err != nil {
		t.Fatal(err)
	}
	options := ai.StreamOptions{Fetch: &http.Client{Transport: namespaceNoNetwork{}}}
	model := runtime.GetModel(id, "old")
	if model == nil {
		t.Fatal("legacy update dropped the model")
	}
	if message := runtime.CompleteSimple(t.Context(), model, ai.Context{}, options); message.StopReason != ai.StopReasonStop || ai.ContentText(message.Content) != "inherited" {
		t.Fatalf("inherited callback result=%+v", message)
	}
	config = namespaceExtensionConfig("new")
	config.StreamSimple = func(model extension.Model, _ extension.AIContext, _ extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		if model.ID != "new" {
			t.Error("replacement callback received old model")
		}
		return namespaceResult("replacement")
	}
	if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
		t.Fatal(err)
	}
	if message := runtime.CompleteSimple(t.Context(), runtime.GetModel(id, "new"), ai.Context{}, options); message.StopReason != ai.StopReasonStop || ai.ContentText(message.Content) != "replacement" {
		t.Fatalf("replacement callback result=%+v", message)
	}
}

func TestRegistryCrossEntryOAuthCallbacksRemainCallable(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-oauth"
	config := namespaceExtensionConfig("old")
	config.APIKey = ""
	var calls int
	config.OAuth = &extension.ProviderOAuth{Name: "Fixture OAuth", GetAPIKey: func(value extension.OAuthCredentials) string {
		calls++
		return value.Access
	}}
	if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialOAuth, Access: "oauth-access", Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider(id, ProviderConfigInput{Headers: map[string]string{"X-New": "new"}}); err != nil {
		t.Fatal(err)
	}
	auth, err := services.Registry().GetProviderAuth(t.Context(), id)
	if err != nil || auth == nil || auth.Auth.APIKey != "oauth-access" || calls == 0 {
		t.Fatalf("retained OAuth callback: auth=%+v calls=%d err=%v", auth, calls, err)
	}
}

type refreshContextKey struct{}

func TestRegistryCrossEntryOAuthLifecycleAndModelCallbacks(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-oauth-lifecycle"
	config := namespaceExtensionConfig("old")
	config.APIKey = ""
	var loginCalls, refreshCalls, projectionCalls atomic.Int64
	credential := func(access string) extension.OAuthCredentials {
		return extension.OAuthCredentials{Access: access, Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
	}
	config.OAuth = &extension.ProviderOAuth{Name: "Fixture OAuth",
		Login: func(extension.OAuthLoginCallbacks) (extension.OAuthCredentials, error) {
			loginCalls.Add(1)
			return credential("login-access"), nil
		},
		RefreshToken: func(ctx context.Context, _ extension.OAuthCredentials) (extension.OAuthCredentials, error) {
			refreshCalls.Add(1)
			// types.ts:1944: refreshToken receives the refresh's AbortSignal.
			if ctx.Value(refreshContextKey{}) != "caller" {
				return extension.OAuthCredentials{}, errors.New("refreshToken did not receive the caller's context")
			}
			return credential("refreshed-access"), nil
		},
		GetAPIKey: func(value extension.OAuthCredentials) string { return value.Access },
		ModifyModels: func(_ []extension.Model, _ extension.OAuthCredentials) []extension.Model {
			projectionCalls.Add(1)
			return []extension.Model{nativeCompatModel("oauth-model", id, "https://example.invalid/v1")}
		},
	}
	if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider(id, ProviderConfigInput{Headers: map[string]string{"X-One": "one"}}); err != nil {
		t.Fatal(err)
	}
	if err := services.Registry().RegisterExtensionProvider(id, extension.ProviderConfig{Headers: map[string]string{"X-Two": "two"}}); err != nil {
		t.Fatal(err)
	}
	logged, err := runtime.Login(t.Context(), id, ai.CredentialOAuth, ai.AuthInteraction{})
	if err != nil || logged.Type != ai.CredentialOAuth || logged.Access != "login-access" || loginCalls.Load() != 1 {
		t.Fatalf("login=%+v calls=%d err=%v", logged, loginCalls.Load(), err)
	}
	if runtime.GetModel(id, "oauth-model") == nil || projectionCalls.Load() == 0 {
		t.Fatal("legacy model projection callback was lost")
	}
	logged.Expires = time.Now().Add(-time.Hour).UnixMilli()
	if err := services.Auth().Set(id, logged); err != nil {
		t.Fatal(err)
	}
	auth, err := services.Registry().GetProviderAuth(context.WithValue(t.Context(), refreshContextKey{}, "caller"), id)
	if err != nil || auth == nil || auth.Auth.APIKey != "refreshed-access" || refreshCalls.Load() != 1 {
		t.Fatalf("refresh auth=%+v calls=%d err=%v", auth, refreshCalls.Load(), err)
	}
}

func TestRegistryFailedReplacementLeavesNativeOwnerIntact(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-invalid"
	provider := namespaceProvider(id, "old", true)
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	invalid := namespaceExtensionConfig("bad")
	invalid.API, invalid.BaseURL = "", ""
	if err := services.Registry().RegisterExtensionProvider(id, invalid); err == nil {
		t.Fatal("invalid core registration was accepted")
	}
	if err := runtime.RegisterProvider(id, ProviderConfigInput{StreamSimple: nativeUnusedStream}); err == nil {
		t.Fatal("invalid runtime registration was accepted")
	}
	if runtime.GetModel(id, "old") == nil || runtime.GetModel(id, "bad") != nil || services.Registry().GetRegisteredNativeProvider(id) != provider {
		t.Fatal("failed registration displaced the valid native owner")
	}
}

func TestRegistryNativeReplacementKeepsFileConfiguration(t *testing.T) {
	const id = "namespace-file-config"
	services, _ := nativeCompatServices(t, `{"providers":{"namespace-file-config":{"apiKey":"file-key"}}}`, nil)
	runtime := services.ModelRuntime()
	if err := services.Registry().RegisterExtensionProvider(id, namespaceExtensionConfig("old")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(namespaceProvider(id, "new", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status := runtime.GetProviderAuthStatus(id); !status.Configured || status.Source != ai.AuthSourceModelsJSONKey {
		t.Fatalf("native replacement lost file configuration or retained legacy auth: %+v", status)
	}
}

func BenchmarkRegistryNamespaceSwitch(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(services.Close)
			runtime := services.ModelRuntime()
			const id = "namespace-benchmark"
			provider := namespaceProvider(id, "native", true)
			config := namespaceExtensionConfig("legacy")
			config.Models = make([]extension.ProviderModelConfig, count)
			for i := range config.Models {
				config.Models[i] = extension.ProviderModelConfig{ID: fmt.Sprintf("model-%d", i), Name: "Model", Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100}
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := runtime.RegisterNativeProvider(provider); err != nil {
					b.Fatal(err)
				}
				if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
					b.Fatal(err)
				}
				if err := runtime.RegisterProvider(id, ProviderConfigInput{Headers: map[string]string{"X-New": "new"}}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestRegistryRuntimeMetadataUpdateRetainsExtensionDefinition(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-reverse"
	if err := services.Registry().RegisterExtensionProvider(id, namespaceExtensionConfig("old")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider(id, ProviderConfigInput{Headers: map[string]string{"X-New": "new"}}); err != nil {
		t.Fatal(err)
	}
	if runtime.GetModel(id, "old") == nil {
		t.Fatal("runtime legacy update discarded prior extension model definition")
	}
	if status := runtime.GetProviderAuthStatus(id); !status.Configured {
		t.Fatalf("runtime legacy update discarded prior configured key: %+v", status)
	}
}

// upstream: packages/ai/src/utils/oauth/types.ts OAuthCredentials (`refresh`, `access`, `expires` and `[key: string]: unknown`) is what an extension's ProviderConfig.oauth getApiKey/refreshToken/modifyModels callbacks receive (types.ts:1944-1950).
func TestExtensionOAuthCallbacksReceiveTheStoredOAuthCredentials(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "namespace-oauth-credentials"
	config := namespaceExtensionConfig("old")
	config.APIKey = ""
	var seen extension.OAuthCredentials
	config.OAuth = &extension.ProviderOAuth{Name: "Fixture OAuth", GetAPIKey: func(value extension.OAuthCredentials) string {
		seen = value
		return value.Access
	}}
	if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).UnixMilli()
	stored := ai.Credential{Type: ai.CredentialOAuth, Access: "stored-access", Refresh: "stored-refresh", Expires: expires, Extra: map[string]json.RawMessage{"tenant": json.RawMessage(`"t1"`)}}
	if err := services.Auth().Set(id, stored); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider(id, ProviderConfigInput{Headers: map[string]string{"X-New": "new"}}); err != nil {
		t.Fatal(err)
	}
	if auth, err := services.Registry().GetProviderAuth(t.Context(), id); err != nil || auth == nil || auth.Auth.APIKey != "stored-access" {
		t.Fatalf("provider auth = %+v, err = %v", auth, err)
	}
	if seen.Access != "stored-access" || seen.Refresh != "stored-refresh" || seen.Expires != expires || string(seen.Extra["tenant"]) != `"t1"` {
		t.Fatalf("callback received %+v, want the stored access, refresh, expires and provider-owned extra field", seen)
	}
}

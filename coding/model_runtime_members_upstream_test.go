package coding

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi model-runtime.ts getProviders, getAllAvailable, getAuth, getCompatibilityRequestConfig, getRegistered*, listCredentials, setRuntimeApiKey, removeRuntimeApiKey, unregisterProvider; model-registry.ts isUsingOAuth.

func TestModelRuntimeRegistrationMembersUpstream(t *testing.T) {
	// model-runtime.ts:514-524 getRegisteredProviderConfig, getRegisteredProviderIds, getRegisteredNativeProvider; 942-948 unregisterProvider.
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	if ids := runtime.GetRegisteredProviderIds(); len(ids) != 0 {
		t.Fatalf("ids before registration = %v", ids)
	}
	native := nativeCompatProvider(nativeCompatModel("native", "ext-native", "https://native.test/v1"))
	if err := runtime.RegisterNativeProvider(native); err != nil {
		t.Fatal(err)
	}
	legacy := ProviderConfigInput{BaseURL: "https://legacy.test/v1", APIKey: "legacy-key", API: ai.APIOpenAICompletions, StreamSimple: nativeUnusedStream, Models: ai.AnyModels([]*ai.Model{nativeCompatModel("legacy", "ext-legacy", "https://legacy.test/v1")})}
	if err := runtime.RegisterProvider("ext-legacy", legacy); err != nil {
		t.Fatal(err)
	}
	if got := runtime.GetRegisteredNativeProvider("ext-native"); got != native {
		t.Fatalf("native registration = %p, want the caller's provider %p", got, native)
	}
	if got := runtime.GetRegisteredNativeProvider("ext-legacy"); got != nil {
		t.Fatalf("legacy registration reported as native: %+v", got)
	}
	if got := runtime.GetRegisteredProviderConfig("ext-legacy"); got == nil || got.BaseURL != legacy.BaseURL {
		t.Fatalf("registered config = %+v", got)
	}
	if got := runtime.GetRegisteredProviderConfig("unknown"); got != nil {
		t.Fatalf("unknown config = %+v", got)
	}
	ids := runtime.GetRegisteredProviderIds()
	if !slices.Contains(ids, "ext-native") || !slices.Contains(ids, "ext-legacy") {
		t.Fatalf("ids = %v", ids)
	}
	runtime.UnregisterProvider("ext-native")
	runtime.UnregisterProvider("ext-legacy")
	runtime.UnregisterProvider("never-registered") // Pi runs the same path for an unknown name and throws nothing.
	if ids := runtime.GetRegisteredProviderIds(); len(ids) != 0 {
		t.Fatalf("ids after unregister = %v", ids)
	}
	if runtime.GetRegisteredNativeProvider("ext-native") != nil || runtime.GetRegisteredProviderConfig("ext-legacy") != nil {
		t.Fatal("registration retained after unregister")
	}
}

func TestModelRuntimeGetProvidersUpstream(t *testing.T) {
	// model-runtime.ts:433 getProviders delegates to models.getProviders: built-in providers, then registrations.
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	native := nativeCompatProvider(nativeCompatModel("native", "ext-native", "https://native.test/v1"))
	if err := runtime.RegisterNativeProvider(native); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, provider := range runtime.GetProviders() {
		order = append(order, provider.ID)
	}
	for _, id := range ai.ListProviders()[:3] {
		if !slices.Contains(order, id) {
			t.Fatalf("built-in provider %q missing from %v", id, order)
		}
	}
	index := slices.Index(order, "ext-native")
	if index < 0 || index < slices.Index(order, ai.ListProviders()[0]) {
		t.Fatalf("registered provider order = %v", order)
	}
	if got := runtime.GetProviders()[index]; got.ID != "ext-native" || got.Name != "Extension Native" {
		t.Fatalf("composed provider = %+v", got)
	}
	runtime.UnregisterProvider("ext-native")
	for _, provider := range runtime.GetProviders() {
		if provider.ID == "ext-native" {
			t.Fatal("unregistered provider still listed")
		}
	}
}

func TestModelRuntimeRuntimeApiKeyUpstream(t *testing.T) {
	// model-runtime.ts:613-640 setRuntimeApiKey, removeRuntimeApiKey, listCredentials; getAuth(providerId); getAllAvailable.
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	provider := nativeCompatProvider(nativeCompatModel("native", "ext-native", "https://native.test/v1"))
	provider.Auth.APIKey = &ai.APIKeyAuth{Name: "Native key", Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		if input.Credential != nil && input.Credential.Key != "" {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "native key"}, nil
		}
		return nil, nil
	}, Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if input.Credential == nil || input.Credential.Key == "" {
			return nil, nil
		}
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "native key"}, nil
	}}
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.GetAuth(t.Context(), provider.ID); err != nil || got != nil {
		t.Fatalf("auth before key = %+v, %v", got, err)
	}
	if err := runtime.SetRuntimeApiKey(t.Context(), provider.ID, "runtime-key"); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.GetAuth(t.Context(), provider.ID)
	if err != nil || got == nil || got.Auth.APIKey != "runtime-key" {
		t.Fatalf("auth after key = %+v, %v", got, err)
	}
	credentials, err := runtime.ListCredentials(t.Context())
	if err != nil || !slices.Contains(credentials, ai.CredentialInfo{ProviderID: provider.ID, Type: ai.CredentialAPIKey}) {
		t.Fatalf("credentials = %+v, %v", credentials, err)
	}
	if status := runtime.GetProviderAuthStatus(provider.ID); !status.Configured || status.Source != ai.AuthSourceRuntime {
		t.Fatalf("status = %+v", status)
	}
	available, err := runtime.GetAllAvailable(t.Context(), provider.ID)
	if err != nil || len(available) != 1 || available[0].ModelID() != "native" {
		t.Fatalf("available after key = %v, %v", available, err)
	}
	if err := runtime.RemoveRuntimeApiKey(t.Context(), provider.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.GetAuth(t.Context(), provider.ID); err != nil || got != nil {
		t.Fatalf("auth after removal = %+v, %v", got, err)
	}
	if credentials, err := runtime.ListCredentials(t.Context()); err != nil || slices.ContainsFunc(credentials, func(info ai.CredentialInfo) bool { return info.ProviderID == provider.ID }) {
		t.Fatalf("credentials after removal = %+v, %v", credentials, err)
	}
	if available, err := runtime.GetAllAvailable(t.Context(), provider.ID); err != nil || len(available) != 0 {
		t.Fatalf("available after removal = %v, %v", available, err)
	}
}

func TestModelRuntimeRuntimeApiKeyOperationErrorsUpstream(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	provider := nativeCompatProvider(nativeCompatModel("native", "ext-native", "https://native.test/v1"))
	cause := errors.New("catalog unavailable")
	provider.RefreshModels = func(ai.RefreshModelsContext) error { return cause }
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	// model-runtime.ts:734-751: a committed key whose local synchronization fails reports the operation, the provider and the credential.
	err := runtime.SetRuntimeApiKey(t.Context(), provider.ID, "kept")
	sync, ok := errors.AsType[*CredentialSynchronizationError](err)
	if !ok || sync.Operation != CredentialSynchronizationSetRuntimeAPIKey || sync.ProviderID != provider.ID || sync.Credential == nil || sync.Credential.Key != "kept" || !errors.Is(err, cause) {
		t.Fatalf("set error = %#v", err)
	}
	if key, ok := services.Registry().RuntimeAPIKey(provider.ID); !ok || key != "kept" {
		t.Fatalf("runtime key = %q, %v: the committed key must stay installed", key, ok)
	}
	err = runtime.RemoveRuntimeApiKey(t.Context(), provider.ID)
	sync, ok = errors.AsType[*CredentialSynchronizationError](err)
	if !ok || sync.Operation != CredentialSynchronizationRemoveRuntimeAPIKey || sync.Credential != nil || !errors.Is(err, cause) {
		t.Fatalf("remove error = %#v", err)
	}
	if _, ok := services.Registry().RuntimeAPIKey(provider.ID); ok {
		t.Fatal("runtime key retained after removal")
	}
	// operationSignal(options.signal).throwIfAborted: a cancelled operation installs nothing.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runtime.SetRuntimeApiKey(ctx, provider.ID, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled set = %v", err)
	}
	if _, ok := services.Registry().RuntimeAPIKey(provider.ID); ok {
		t.Fatal("a cancelled SetRuntimeApiKey installed its key")
	}
	if err := runtime.RemoveRuntimeApiKey(ctx, provider.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled remove = %v", err)
	}
}

func TestModelRuntimeCompatibilityRequestConfigUpstream(t *testing.T) {
	// model-runtime.ts:527 getCompatibilityRequestConfig → provider-composer.ts:703 resolveCompatibilityRequestConfig.
	config := `{"providers":{"custom-provider":{"baseUrl":"https://example.test/v1","api":"openai-completions","apiKey":"configured","authHeader":true,"headers":{"X-Provider":"provider"},"models":[{"id":"test-model","headers":{"X-Model":"model"}},{"id":"bare"}]},"plain":{"baseUrl":"https://plain.test/v1","api":"openai-completions","apiKey":"k","models":[{"id":"bare"}]}}}`
	services, _ := nativeCompatServices(t, config, nil)
	runtime := services.ModelRuntime()
	model := runtime.GetModel("custom-provider", "test-model")
	if model == nil {
		t.Fatal("configured model not found")
	}
	got, err := runtime.GetCompatibilityRequestConfig(model)
	if err != nil || !got.AuthHeader || got.Headers == nil || *got.Headers["X-Provider"] != "provider" || *got.Headers["X-Model"] != "model" {
		t.Fatalf("config = %+v, %v", got, err)
	}
	plain := runtime.GetModel("plain", "bare")
	if plain == nil {
		t.Fatal("plain model not found")
	}
	got, err = runtime.GetCompatibilityRequestConfig(plain)
	if err != nil || got.AuthHeader || got.Headers != nil {
		t.Fatalf("plain config = %+v, %v", got, err)
	}
}

// Pi: packages/coding-agent/src/core/model-registry.ts:206 (ModelRegistry.isUsingOAuth).
func TestModelRegistryIsUsingOAuthUpstream(t *testing.T) {
	// model-registry.ts isUsingOAuth(model) → runtime.isUsingOAuth(model.provider).
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	model := nativeCompatModel("native", "ext-native", "https://native.test/v1")
	provider := nativeCompatProvider(model)
	credential := ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 4102444800000}
	provider.Auth.OAuth = &ai.OAuthAuth{Login: func(context.Context, ai.AuthInteraction, ai.LoginOptions) (ai.Credential, error) {
		return credential, nil
	}, Refresh: func(_ context.Context, current ai.Credential) (ai.Credential, error) { return current, nil }, ToAuth: func(current ai.Credential) (ai.ModelAuth, error) {
		return ai.ModelAuth{APIKey: current.Access}, nil
	}}
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	registry := services.Registry()
	if registry.IsUsingOAuth(model) {
		t.Fatal("OAuth reported before login")
	}
	if _, err := runtime.Login(t.Context(), provider.ID, ai.CredentialOAuth, ai.AuthInteraction{}); err != nil {
		t.Fatal(err)
	}
	if !registry.IsUsingOAuth(model) {
		t.Fatal("OAuth not reported after login")
	}
	if registry.IsUsingOAuth(nativeCompatModel("other", "other-provider", "https://other.test/v1")) {
		t.Fatal("OAuth reported for another provider")
	}
}

func TestModelRuntimeGetRegisteredProviderIdsOrderUpstream(t *testing.T) {
	// model-runtime.ts:518-520 lists extensionProviders keys, then nativeExtensionProviders keys, each in Map insertion order. registerNativeProvider deletes the ID from extensionProviders (884-885), registerProvider deletes it from nativeExtensionProviders (923-931), and re-setting an existing key keeps its position. Expectations are the output of the same sequence against Pi's ModelRuntime.
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	register := func(id string) {
		input := ProviderConfigInput{BaseURL: "https://" + id + ".test/v1", APIKey: "k", API: ai.APIOpenAICompletions, StreamSimple: nativeUnusedStream, Models: ai.AnyModels([]*ai.Model{nativeCompatModel("m", id, "https://"+id+".test/v1")})}
		if err := runtime.RegisterProvider(id, input); err != nil {
			t.Fatal(err)
		}
	}
	registerNative := func(id string) {
		if err := runtime.RegisterNativeProvider(nativeCompatProvider(nativeCompatModel("n", id, "https://"+id+".test/v1"))); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(step string, want ...string) {
		t.Helper()
		if got := runtime.GetRegisteredProviderIds(); !slices.Equal(got, want) {
			t.Fatalf("%s: ids = %q, want %q", step, got, want)
		}
	}
	registerNative("zz-native")
	register("zz-legacy")
	register("aa-legacy")
	expect("config IDs precede native IDs", "zz-legacy", "aa-legacy", "zz-native")
	registerNative("bb-native")
	register("zz-legacy")
	expect("re-registration keeps its position", "zz-legacy", "aa-legacy", "zz-native", "bb-native")
	register("zz-native")
	expect("native to config moves the ID to the end of the config IDs", "zz-legacy", "aa-legacy", "zz-native", "bb-native")
	registerNative("zz-legacy")
	expect("config to native moves the ID to the end of the native IDs", "aa-legacy", "zz-native", "bb-native", "zz-legacy")
	runtime.UnregisterProvider("aa-legacy")
	expect("unregister", "zz-native", "bb-native", "zz-legacy")
}

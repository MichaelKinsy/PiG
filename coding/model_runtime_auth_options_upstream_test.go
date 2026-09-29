package coding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/test/model-runtime-auth-options.test.ts. Cases already covered elsewhere:
// :73 projects provider-owned methods (internal/codingagent TestRuntimeProjectsProviderOwnedMethodsUpstream),
// :126 subscription OAuth (TestModelRuntimeDistinguishesSubscriptionOAuthUpstream),
// :266 extension OAuth refresh cancellation (TestRuntimeForwardsExtensionRefreshCancellationUpstream).
// :36 and :44 run on CreateModelRuntimeOptions.Credentials.

func authOptionsTestModel(id string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: id, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
}

// countingCredentialStore records every provider read, as the :44 upstream CredentialStore wrapper does.
type countingCredentialStore struct {
	ai.CredentialStore
	mu        sync.Mutex
	reads     []string
	failReads bool
}

func (store *countingCredentialStore) Read(ctx context.Context, providerID string) (*ai.Credential, error) {
	store.mu.Lock()
	store.reads = append(store.reads, providerID)
	fail := store.failReads
	store.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("read failed for %s", providerID)
	}
	return store.CredentialStore.Read(ctx, providerID)
}

func (store *countingCredentialStore) reset(fail bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads = nil
	store.failReads = fail
}

func (store *countingCredentialStore) readSet() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return slices.Compact(slices.Sorted(slices.Values(store.reads)))
}

func createRuntimeOnCredentials(t *testing.T, credentials ai.CredentialStore, agentDir string) *ModelRuntime {
	t.Helper()
	t.Setenv(icodingagent.ENV_AGENT_DIR, agentDir)
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{Credentials: credentials, ModelsPath: new((*string)(nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

// model-runtime-auth-options.test.ts:36 "accepts a pi-ai CredentialStore": ModelRuntime.create({credentials}) resolves the stored key from the injected store, and no auth.json is opened or created (model-runtime.ts:174).
func TestCreateModelRuntimeAcceptsCredentialStoreUpstream(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	credentials := ai.NewInMemoryCredentialStore()
	if _, err := credentials.Modify(t.Context(), "anthropic", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	runtime := createRuntimeOnCredentials(t, credentials, agentDir)

	check, err := runtime.CheckAuth(t.Context(), "anthropic")
	if err != nil || check == nil || check.Type != ai.CredentialAPIKey {
		t.Fatalf("CheckAuth = %+v, %v", check, err)
	}
	var model *ai.Model
	for _, candidate := range runtime.GetModels() {
		if candidate.ProviderMeta.ProviderID == "anthropic" {
			model = candidate
			break
		}
	}
	if model == nil {
		t.Fatal("anthropic catalog is empty")
	}
	_, _, options, err := runtime.prepareRequest(t.Context(), model, ai.StreamOptions{})
	if err != nil || options.APIKey != "stored-key" {
		t.Fatalf("prepareRequest apiKey = %q, %v", options.APIKey, err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("auth.json was opened despite an injected store: %v", err)
	}
}

// model-runtime-auth-options.test.ts:44 "scopes provider availability reads and records refresh failures" on a wrapping CredentialStore passed as CreateModelRuntimeOptions.Credentials: a provider-scoped availability read touches only that provider, a failing read surfaces "Credential store read failed for <id>" and the Availability refresh error record, and a later unscoped refresh clears it.
func TestCreateModelRuntimeScopesAvailabilityReadsUpstream(t *testing.T) {
	credentials := &countingCredentialStore{CredentialStore: ai.NewInMemoryCredentialStore()}
	runtime := createRuntimeOnCredentials(t, credentials, t.TempDir())

	credentials.reset(false)
	if _, err := runtime.GetAvailable(t.Context(), "anthropic"); err != nil {
		t.Fatal(err)
	}
	if got := credentials.readSet(); !slices.Equal(got, []string{"anthropic"}) {
		t.Fatalf("scoped availability read providers = %v, want [anthropic]", got)
	}

	credentials.reset(true)
	_, err := runtime.GetAvailable(t.Context(), "anthropic")
	if err == nil || !strings.Contains(err.Error(), "Credential store read failed for anthropic") {
		t.Fatalf("GetAvailable error = %v", err)
	}
	if got := runtime.GetError(); !strings.Contains(got, "Availability refresh: Credential store read failed for anthropic") {
		t.Fatalf("GetError = %q", got)
	}

	credentials.reset(false)
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := runtime.GetError(); got != "" {
		t.Fatalf("GetError after recovery = %q", got)
	}
}

// TestModelRuntimeRecordsRealStoreReadFailureUpstream keeps the :44 failure record on the default auth.json store: a malformed file fails the scoped read with the same message.
func TestModelRuntimeRecordsRealStoreReadFailureUpstream(t *testing.T) {
	session, services := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	if _, err := runtime.GetAvailable(t.Context(), "anthropic"); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	path := filepath.Join(services.AgentDir(), "auth.json")
	writeAuth := func(content string, offset time.Duration) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(offset)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	writeAuth("{invalid-json", 2*time.Second)
	_, err := runtime.GetAvailable(t.Context(), "anthropic")
	if err == nil || !strings.Contains(err.Error(), "Credential store read failed for anthropic") {
		t.Fatalf("GetAvailable error = %v", err)
	}
	if got := runtime.GetError(); !strings.Contains(got, "Availability refresh: Credential store read failed for anthropic") {
		t.Fatalf("GetError = %q", got)
	}
	writeAuth("{}", 4*time.Second)
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := runtime.GetError(); got != "" {
		t.Fatalf("GetError after recovery = %q", got)
	}
}

// model-runtime-auth-options.test.ts:108 "attaches the provider's active auth status to every method option".
func TestModelRuntimeAuthStatusForProviderWithBothMethodsUpstream(t *testing.T) {
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"anthropic": {Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}})
	runtime, err := icodingagent.NewRequestAuthRuntime(t.Context(), icodingagent.RequestAuthRuntimeOptions{Credentials: credentials})
	if err != nil {
		t.Fatal(err)
	}
	provider := runtime.GetProvider("anthropic")
	if provider == nil || provider.Auth.OAuth == nil || provider.Auth.APIKey == nil {
		t.Fatalf("anthropic must offer both methods: %+v", provider)
	}
	check, err := runtime.CheckAuth(t.Context(), "anthropic")
	if err != nil || check == nil || check.Type != ai.CredentialOAuth {
		t.Fatalf("CheckAuth = %+v, %v", check, err)
	}
}

// model-runtime-auth-options.test.ts:108 on ModelRuntime.create({credentials: AuthStorage.inMemory(...)}): the injected OAuth credential is the active anthropic auth. coding.ModelRuntime has no getProviders, so the method projection runs on the request-auth runtime above.
func TestCreateModelRuntimeInjectedOAuthIsActiveAuthUpstream(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"anthropic": {Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}})
	runtime := createRuntimeOnCredentials(t, credentials, t.TempDir())
	check, err := runtime.CheckAuth(t.Context(), "anthropic")
	if err != nil || check == nil || check.Type != ai.CredentialOAuth {
		t.Fatalf("CheckAuth = %+v, %v", check, err)
	}
}

// model-runtime-auth-options.test.ts:126 on ModelRuntime.create({credentials}): the create-time refresh publishes OAuth usage from the injected store without a later availability call, and only anthropic is a subscription.
func TestCreateModelRuntimeInjectedSubscriptionOAuthUpstream(t *testing.T) {
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{
		"anthropic":  {Type: ai.CredentialOAuth, Access: "anthropic-access", Refresh: "anthropic-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
		"openrouter": {Type: ai.CredentialOAuth, Access: "openrouter-key", Refresh: "", Expires: 9007199254740991},
		"radius":     {Type: ai.CredentialOAuth, Access: "radius-access", Refresh: "radius-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	runtime := createRuntimeOnCredentials(t, credentials, t.TempDir())
	for _, id := range []string{"anthropic", "openrouter", "radius"} {
		if !runtime.IsUsingOAuth(id) || runtime.IsUsingSubscription(id) != (id == "anthropic") {
			t.Fatalf("%s OAuth=%v subscription=%v", id, runtime.IsUsingOAuth(id), runtime.IsUsingSubscription(id))
		}
	}
}

// model-runtime-auth-options.test.ts:159 "constructs an API key method for an extension API-key provider".
func TestExtensionAPIKeyProviderGetsAPIKeyMethodUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	registerRefreshSignalProvider(t, services, "extension-api-key", ProviderConfigInput{Name: "Extension API Key", BaseURL: "https://example.test/v1", APIKey: "$EXTENSION_TEST_API_KEY", API: ai.APIOpenAICompletions, Models: []*ai.Model{authOptionsTestModel("extension-model")}})
	provider := services.Registry().NativeModels().GetProvider("extension-api-key")
	if provider == nil || provider.Name != "Extension API Key" {
		t.Fatalf("provider = %+v", provider)
	}
	if provider.Auth.OAuth != nil || provider.Auth.APIKey == nil || provider.Auth.APIKey.Name != "API key" || provider.Auth.APIKey.Login == nil {
		t.Fatalf("auth = %+v", provider.Auth)
	}
}

// model-runtime-auth-options.test.ts:179 "resolves configured auth from request-scoped environment overrides".
func TestExtensionAuthResolvesRequestScopedEnvironmentUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	registerRefreshSignalProvider(t, services, "request-env-provider", ProviderConfigInput{BaseURL: "https://example.test/v1", APIKey: "$REQUEST_SCOPED_API_KEY", Headers: map[string]string{"x-request-value": "$REQUEST_SCOPED_HEADER"}, API: ai.APIOpenAICompletions, Models: []*ai.Model{authOptionsTestModel("request-env-model")}})
	result, err := services.Registry().NativeModels().GetAuth(t.Context(), "request-env-provider", ai.AuthResolutionOverrides{Env: map[string]string{"REQUEST_SCOPED_API_KEY": "request-key", "REQUEST_SCOPED_HEADER": "request-header"}})
	if err != nil || result == nil {
		t.Fatalf("GetAuth = %+v, %v", result, err)
	}
	want := ai.ModelAuth{APIKey: "request-key", Headers: ai.ProviderHeaders{"x-request-value": new("request-header")}}
	if !reflect.DeepEqual(result.Auth, want) {
		t.Fatalf("auth = %+v, want %+v", result.Auth, want)
	}
}

// model-runtime-auth-options.test.ts:196 "lets an explicit Authorization header override authHeader case-insensitively".
func TestExplicitAuthorizationHeaderOverridesAuthHeaderUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	var captured ai.StreamOptions
	if err := services.ModelRuntime().RegisterProvider("auth-header-provider", ProviderConfigInput{
		BaseURL: "https://example.test/v1", APIKey: "generated-key", AuthHeader: new(true), API: ai.APIOpenAICompletions,
		StreamSimple: func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			captured = options
			return nil, errors.New("captured")
		},
		Models: []*ai.Model{authOptionsTestModel("auth-header-model")},
	}); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("auth-header-provider", "auth-header-model")
	if model == nil {
		t.Fatal("registered model is absent")
	}
	services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{Headers: ai.ProviderHeaders{"authorization": new("Explicit token")}})
	want := ai.ProviderHeaders{"authorization": new("Explicit token")}
	if !reflect.DeepEqual(captured.Headers, want) {
		t.Fatalf("headers = %s, want %s", formatProviderHeaders(captured.Headers), formatProviderHeaders(want))
	}
}

// model-runtime-auth-options.test.ts:218 "transforms fully assembled headers once without forwarding the transform".
func TestExtensionHeadersAreTransformedOnceUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	var captured ai.StreamOptions
	if err := services.ModelRuntime().RegisterProvider("header-provider", ProviderConfigInput{
		BaseURL: "https://example.test/v1", APIKey: "generated-key", AuthHeader: new(true), Headers: map[string]string{"x-provider": "provider"}, API: ai.APIOpenAICompletions,
		StreamSimple: func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			captured = options
			return nil, errors.New("captured")
		},
		Models: []*ai.Model{func() *ai.Model {
			model := authOptionsTestModel("header-model")
			model.ProviderMeta.Headers = map[string]string{"x-model": "model"}
			return model
		}()},
	}); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("header-provider", "header-model")
	if model == nil {
		t.Fatal("registered model is absent")
	}
	transforms := 0
	services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{
		Headers: ai.ProviderHeaders{"x-explicit": new("explicit")},
		TransformHeaders: func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
			transforms++
			want := ai.ProviderHeaders{"Authorization": new("Bearer generated-key"), "x-provider": new("provider"), "x-model": new("model"), "x-explicit": new("explicit")}
			if !reflect.DeepEqual(headers, want) {
				t.Errorf("transform input = %s, want %s", formatProviderHeaders(headers), formatProviderHeaders(want))
			}
			out := ai.ProviderHeaders{}
			maps.Copy(out, headers)
			out["x-transformed"] = new("yes")
			return out, nil
		},
	})
	if transforms != 1 {
		t.Fatalf("transforms = %d, want 1", transforms)
	}
	if captured.TransformHeaders != nil {
		t.Fatal("transformHeaders was forwarded to the provider")
	}
	want := ai.ProviderHeaders{"Authorization": new("Bearer generated-key"), "x-provider": new("provider"), "x-model": new("model"), "x-explicit": new("explicit"), "x-transformed": new("yes")}
	if !reflect.DeepEqual(captured.Headers, want) {
		t.Fatalf("headers = %s, want %s", formatProviderHeaders(captured.Headers), formatProviderHeaders(want))
	}
}

func formatProviderHeaders(headers ai.ProviderHeaders) string {
	names := slices.Sorted(maps.Keys(headers))
	var b strings.Builder
	for _, name := range names {
		value := "<nil>"
		if headers[name] != nil {
			value = *headers[name]
		}
		b.WriteString(name + "=" + value + ";")
	}
	return b.String()
}

// model-runtime-auth-options.test.ts:302 "does not fabricate an API key method for an extension OAuth-only provider".
func TestExtensionOAuthOnlyProviderHasNoAPIKeyMethodUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	registerRefreshSignalProvider(t, services, "extension-oauth", ProviderConfigInput{Name: "Extension OAuth", BaseURL: "https://example.test/v1", API: ai.APIOpenAICompletions, Models: []*ai.Model{authOptionsTestModel("extension-model")}, OAuth: &ExtensionOAuthConfig{
		Name: "Extension subscription", IsSubscription: true,
		Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
			return ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}, nil
		},
		RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil },
		GetAPIKey:    func(credential ai.Credential) string { return credential.Access },
	}})
	provider := services.Registry().NativeModels().GetProvider("extension-oauth")
	if provider == nil || provider.Name != "Extension OAuth" {
		t.Fatalf("provider = %+v", provider)
	}
	if provider.Auth.APIKey != nil {
		t.Fatalf("fabricated API-key method: %+v", provider.Auth.APIKey)
	}
	if oauth := provider.Auth.OAuth; oauth == nil || oauth.Name != "Extension subscription" || !oauth.IsSubscription {
		t.Fatalf("oauth = %+v", provider.Auth.OAuth)
	}
}

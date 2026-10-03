package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// availabilityGate holds every full and provider availability pass at its credential read, so a snapshot change observed while it is closed cannot come from the scheduled refresh.
type availabilityGate struct {
	open   chan struct{}
	once   sync.Once
	passes atomic.Int32
}

func gateAvailability(t *testing.T, runtime *ModelRuntime) *availabilityGate {
	t.Helper()
	gate := &availabilityGate{open: make(chan struct{})}
	wait := func(ctx context.Context) error {
		gate.passes.Add(1)
		select {
		case <-gate.open:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	list, read := runtime.availability.listCredentials, runtime.availability.readCredential
	runtime.availability.listCredentials = func(ctx context.Context) ([]ai.CredentialInfo, error) {
		if err := wait(ctx); err != nil {
			return nil, err
		}
		return list(ctx)
	}
	runtime.availability.readCredential = func(ctx context.Context, id string) (*ai.Credential, error) {
		if err := wait(ctx); err != nil {
			return nil, err
		}
		return read(ctx, id)
	}
	t.Cleanup(gate.release)
	return gate
}

func (gate *availabilityGate) release() { gate.once.Do(func() { close(gate.open) }) }

func registrationServices(t *testing.T, credentials map[string]ai.Credential) *Services {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PIG_HOME", filepath.Join(dir, "home"))
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
	return services
}

func providerModelIDs(models []*ai.Model, providerID string) []string {
	var ids []string
	for _, model := range models {
		if model.ProviderMeta.ProviderID == providerID {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func snapshotRegistration(apiKey string) extension.ProviderConfig {
	return extension.ProviderConfig{API: "openai-completions", BaseURL: "https://registered.invalid/v1", APIKey: apiKey, Models: []extension.ProviderModelConfig{{ID: "registered", Name: "Registered", Input: []string{"text"}, ContextWindow: 4096, MaxTokens: 1024}}}
}

func coreRegistration(id, apiKey string) ProviderConfigInput {
	return ProviderConfigInput{API: ai.APIOpenAICompletions, BaseURL: "https://registered.invalid/v1", APIKey: apiKey, Models: []ai.AnyModel{nativeCompatModel("registered", id, "https://registered.invalid/v1")}}
}

// Pi model-runtime.ts:753-797 updates the shared snapshot synchronously in registerProvider and unregisterProvider; model-registry.ts:54-56 and every selector read that snapshot. AgentSession (agent-session.ts:3128-3139) and interactive listeners run after the update, so Pig's change observers must see it too. The gate proves the scheduled refresh is not the source.
func TestProviderRegistrationUpdatesAvailabilityBeforeReturn(t *testing.T) {
	const id = "registered-provider"
	for _, entry := range []string{"extension-config", "extension-runtime", "core-input"} {
		t.Run(entry, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := registrationServices(t, nil)
				runtime := services.ModelRuntime()
				synctest.Wait()
				gate := gateAvailability(t, runtime)
				var observed []bool
				detach := services.Registry().ObserveChanges(func() {
					observed = append(observed, slices.Contains(providerModelIDs(runtime.GetAvailableSnapshot(), id), "registered"))
				})
				t.Cleanup(detach)
				var err error
				switch entry {
				case "extension-config":
					err = services.Registry().RegisterProvider(id, snapshotRegistration("literal-key"))
				case "extension-runtime":
					shared := extension.CreateExtensionRuntime()
					inproc.NewRunner(nil, t.TempDir(), shared).BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
					err = shared.RegisterProvider(id, snapshotRegistration("literal-key"), "registered-extension")
				case "core-input":
					err = runtime.RegisterProvider(id, coreRegistration(id, "literal-key"))
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); !slices.Equal(got, []string{"registered"}) {
					t.Fatalf("available snapshot after registration=%v, want [registered]", got)
				}
				if got := providerModelIDs(services.Registry().GetAvailableModelData(), id); !slices.Equal(got, []string{"registered"}) {
					t.Fatalf("registry availability after registration=%v, want [registered]", got)
				}
				if len(observed) == 0 || !observed[0] {
					t.Fatalf("change observers saw availability %v before the snapshot update", observed)
				}
				observed = nil
				services.Registry().UnregisterProvider(id)
				if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); len(got) != 0 {
					t.Fatalf("available snapshot after unregister=%v", got)
				}
				if got := providerModelIDs(runtime.GetModels(), id); len(got) != 0 {
					t.Fatalf("catalog after unregister=%v", got)
				}
				if len(observed) == 0 || observed[0] {
					t.Fatalf("change observers saw availability %v after unregister", observed)
				}
				// The local refresh is queued and runs on the caller's next awaited model-runtime call (D84).
				services.Registry().StartRegistrationRefresh(t.Context())
				synctest.Wait()
				if gate.passes.Load() == 0 {
					t.Fatal("registration did not queue its availability refresh")
				}
			})
		})
	}
}

// Pi model-runtime.ts:766-787 marks a registration available before its refresh only when auth.json already stores the provider or its request auth is configured, without replacing a real check. Otherwise only the catalog changes. The provisional type is "oauth" only when the effective registration has oauth and no apiKey (model-runtime.ts:777-779).
func TestProviderRegistrationProvisionalAvailabilityUpstream(t *testing.T) {
	const id = "provisional-provider"
	storedOAuth := map[string]ai.Credential{id: {Type: ai.CredentialOAuth, Access: "stored-access", Refresh: "stored-refresh", Expires: 1 << 50}}
	cases := []struct {
		name        string
		credentials map[string]ai.Credential
		env         string
		apiKey      string
		oauth       bool
		available   bool
		authType    ai.CredentialType
	}{
		{name: "literal key", apiKey: "literal-key", available: true, authType: ai.CredentialAPIKey},
		{name: "stored credential", credentials: map[string]ai.Credential{id: {Type: ai.CredentialAPIKey, Key: "stored-key"}}, available: true, authType: ai.CredentialAPIKey},
		{name: "configured environment", env: "configured", apiKey: "$PIG_REGISTRATION_TEST_KEY", available: true, authType: ai.CredentialAPIKey},
		{name: "unset environment", apiKey: "$PIG_REGISTRATION_TEST_KEY"},
		{name: "unconfigured"},
		{name: "stored oauth without key", credentials: storedOAuth, oauth: true, available: true, authType: ai.CredentialOAuth},
		{name: "stored oauth with key", credentials: storedOAuth, apiKey: "literal-key", oauth: true, available: true, authType: ai.CredentialAPIKey},
		{name: "unstored oauth", oauth: true},
	}
	for _, entry := range []string{"extension-config", "core-input"} {
		for _, tc := range cases {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				t.Setenv("PIG_REGISTRATION_TEST_KEY", tc.env)
				synctest.Test(t, func(t *testing.T) {
					services := registrationServices(t, tc.credentials)
					runtime := services.ModelRuntime()
					synctest.Wait()
					gateAvailability(t, runtime)
					var err error
					if entry == "extension-config" {
						config := snapshotRegistration(tc.apiKey)
						if tc.oauth {
							config.OAuth = &extension.ProviderOAuth{Name: "Registered OAuth", Login: func(extension.OAuthLoginCallbacks) (extension.OAuthCredentials, error) {
								return map[string]any{"access": "unused"}, nil
							}, RefreshToken: func(credentials extension.OAuthCredentials) (extension.OAuthCredentials, error) {
								return credentials, nil
							}, GetAPIKey: func(extension.OAuthCredentials) string { return "" }}
						}
						err = services.Registry().RegisterProvider(id, config)
					} else {
						input := coreRegistration(id, tc.apiKey)
						if tc.oauth {
							input.OAuth = &ExtensionOAuthConfig{Name: "Registered OAuth", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
								return ai.Credential{}, nil
							}, RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil }, GetAPIKey: func(credential ai.Credential) string { return credential.Access }}
						}
						err = runtime.RegisterProvider(id, input)
					}
					if err != nil {
						t.Fatal(err)
					}
					if got := providerModelIDs(runtime.GetModels(), id); !slices.Equal(got, []string{"registered"}) {
						t.Fatalf("catalog=%v", got)
					}
					var want []string
					if tc.available {
						want = []string{"registered"}
					}
					if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); !slices.Equal(got, want) {
						t.Fatalf("available snapshot=%v, want %v", got, want)
					}
					if got := providerModelIDs(services.Registry().GetAvailableModelData(), id); !slices.Equal(got, want) {
						t.Fatalf("registry availability=%v, want %v", got, want)
					}
					runtime.availability.mu.RLock()
					check := runtime.availability.snapshot.auth[id]
					runtime.availability.mu.RUnlock()
					if !tc.available {
						if check != nil {
							t.Fatalf("unconfigured registration published auth %+v", check)
						}
						return
					}
					if check == nil || check.Type != tc.authType {
						t.Fatalf("provisional auth=%+v, want type %q", check, tc.authType)
					}
				})
			})
		}
	}
}

// Pi model-runtime.ts:776-782 never replaces a real check with the provisional one when a provider re-registers.
func TestProviderReRegistrationKeepsRealAuthCheck(t *testing.T) {
	const id = "reregistered-provider"
	synctest.Test(t, func(t *testing.T) {
		services := registrationServices(t, nil)
		runtime := services.ModelRuntime()
		if err := services.Registry().RegisterProvider(id, snapshotRegistration("literal-key")); err != nil {
			t.Fatal(err)
		}
		if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("refresh=%+v", result)
		}
		synctest.Wait()
		runtime.availability.mu.RLock()
		real := runtime.availability.snapshot.auth[id]
		runtime.availability.mu.RUnlock()
		if real == nil || real.Source == "configured provider" {
			t.Fatalf("refresh did not publish a real check: %+v", real)
		}
		gateAvailability(t, runtime)
		if err := services.Registry().RegisterProvider(id, extension.ProviderConfig{Name: "Renamed"}); err != nil {
			t.Fatal(err)
		}
		runtime.availability.mu.RLock()
		current := runtime.availability.snapshot.auth[id]
		runtime.availability.mu.RUnlock()
		if current != real {
			t.Fatalf("re-registration replaced real check %+v with %+v", real, current)
		}
	})
}

// Pi model-runtime.ts:744-750 publishes a native provider's catalog synchronously and leaves availability to the unawaited local refresh, which PiG queues and starts on the caller's next awaited model-runtime call or an extension host callback (D84).
func TestNativeProviderRegistrationSchedulesLocalRefresh(t *testing.T) {
	const id = "native-registered"
	synctest.Test(t, func(t *testing.T) {
		services := registrationServices(t, nil)
		runtime := services.ModelRuntime()
		synctest.Wait()
		gate := gateAvailability(t, runtime)
		model := nativeCompatModel("native", id, "https://native.invalid/v1")
		provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "native check"}, nil
		}}}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if got := providerModelIDs(runtime.GetModels(), id); !slices.Equal(got, []string{"native"}) {
			t.Fatalf("catalog=%v", got)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); len(got) != 0 {
			t.Fatalf("native registration was available before its refresh: %v", got)
		}
		services.Registry().StartRegistrationRefresh(t.Context())
		gate.release()
		synctest.Wait()
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); !slices.Equal(got, []string{"native"}) {
			t.Fatalf("available snapshot after scheduled refresh=%v", got)
		}
		if got := providerModelIDs(services.Registry().GetAvailableModelData(), id); !slices.Equal(got, []string{"native"}) {
			t.Fatalf("registry availability after scheduled refresh=%v", got)
		}
		services.Registry().UnregisterProvider(id)
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); len(got) != 0 {
			t.Fatalf("available snapshot after unregister=%v", got)
		}
	})
}

// A registration made through the Session-bound extension runtime is immediately cycleable, as Pi's cycleModel reads getAvailableSnapshot (agent-session.ts:2175,2215).
func TestSessionCycleModelSeesExtensionProviderRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := registrationServices(t, nil)
		runtime := services.ModelRuntime()
		const baseID, extensionID = "cycle-base", "cycle-extension"
		if err := services.Registry().RegisterProvider(baseID, snapshotRegistration("base-key")); err != nil {
			t.Fatal(err)
		}
		if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("refresh=%+v", result)
		}
		synctest.Wait()
		shared := extension.CreateExtensionRuntime()
		runner := inproc.NewRunner(nil, t.TempDir(), shared)
		session, err := NewSession(services, SessionOptions{NoSession: true, Model: runtime.GetModel(baseID, "registered"), Runner: runner})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
			t.Fatal(err)
		}
		gateAvailability(t, runtime)
		if err := shared.RegisterProvider(extensionID, snapshotRegistration("extension-key"), "cycle-extension"); err != nil {
			t.Fatal(err)
		}
		result, err := session.CycleModel("forward")
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || providerID(result.Model) != extensionID {
			t.Fatalf("cycle after registration=%+v, want %s/registered", result, extensionID)
		}
		shared.UnregisterProvider(extensionID)
		if result, err := session.CycleModel("forward"); err != nil || result != nil {
			t.Fatalf("cycle after unregister=%+v err=%v, want no other available model", result, err)
		}
	})
}

// Pi's updateModelSnapshot (model-runtime.ts:277-284) widens every configured provider to its unfiltered catalog on registration or removal, and the unawaited local refresh queued by that call recomputes the account filter (model-runtime.ts:797, models.ts getAvailable filterModels). Pig queues that refresh (D84), so the widening lasts until the caller's next awaited model-runtime call.
func TestProviderRegistrationWidensOtherProvidersUntilTheQueuedRefresh(t *testing.T) {
	const filteredID, registeredID = "filtered-provider", "registered-provider"
	synctest.Test(t, func(t *testing.T) {
		services := registrationServices(t, nil)
		runtime := services.ModelRuntime()
		provider := &ai.ModelsProvider{ID: filteredID, GetModels: func() ([]*ai.Model, error) {
			return []*ai.Model{nativeCompatModel("allowed", filteredID, "https://filtered.invalid/v1"), nativeCompatModel("hidden", filteredID, "https://filtered.invalid/v1")}, nil
		}, FilterModels: func(models []*ai.Model, _ *ai.Credential) []*ai.Model {
			return slices.DeleteFunc(slices.Clone(models), func(model *ai.Model) bool { return model.ID == "hidden" })
		}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "filtered check"}, nil
		}}}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), filteredID); !slices.Equal(got, []string{"allowed"}) {
			t.Fatalf("filtered availability before registration=%v", got)
		}
		if err := services.Registry().RegisterProvider(registeredID, snapshotRegistration("literal-key")); err != nil {
			t.Fatal(err)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), filteredID); !slices.Equal(got, []string{"allowed", "hidden"}) {
			t.Fatalf("registration left another provider's availability at %v, want Pi's widened catalog", got)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), registeredID); !slices.Equal(got, []string{"registered"}) {
			t.Fatalf("registered availability=%v", got)
		}
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), filteredID); !slices.Equal(got, []string{"allowed"}) {
			t.Fatalf("queued refresh left availability at %v", got)
		}
		services.Registry().UnregisterProvider(registeredID)
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), filteredID); !slices.Equal(got, []string{"allowed", "hidden"}) {
			t.Fatalf("removal left another provider's availability at %v, want Pi's widened catalog", got)
		}
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), filteredID); !slices.Equal(got, []string{"allowed"}) {
			t.Fatalf("queued refresh after removal left availability at %v", got)
		}
	})
}

// Pi registers providers on one event loop, so every registerProvider call that has returned is in the snapshot (model-runtime.ts:753-789). Pig's extension hosts register concurrently; the latest projection must read the provider order after it takes its sequence, or it can commit an order that lacks a provider registered just before it. The gate keeps the scheduled refreshes from repairing the snapshot.
func TestConcurrentProviderRegistrationsAllReachSnapshot(t *testing.T) {
	const providers = 16
	for iteration := range 40 {
		services := registrationServices(t, nil)
		runtime := services.ModelRuntime()
		gateAvailability(t, runtime)
		var registrations sync.WaitGroup
		for i := range providers {
			registrations.Go(func() {
				if err := services.Registry().RegisterProvider(fmt.Sprintf("concurrent-%02d", i), snapshotRegistration("literal-key")); err != nil {
					t.Error(err)
				}
			})
		}
		registrations.Wait()
		snapshot := runtime.GetAvailableSnapshot()
		for i := range providers {
			if id := fmt.Sprintf("concurrent-%02d", i); !slices.Equal(providerModelIDs(snapshot, id), []string{"registered"}) {
				t.Fatalf("iteration %d: %s missing from the available snapshot after every registration returned", iteration, id)
			}
		}
	}
}

// BenchmarkProviderRegistration measures the synchronous registration path, including Pi's catalog projection into the available snapshot.
func BenchmarkProviderRegistration(b *testing.B) {
	dir := b.TempDir()
	services, err := NewServices(ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	config := snapshotRegistration("literal-key")
	b.ReportAllocs()
	for b.Loop() {
		if err := services.Registry().RegisterProvider("benchmark-provider", config); err != nil {
			b.Fatal(err)
		}
	}
}

// requireConfiguredMatchesAuthChecks asserts the invariant that makes the projection condition unobservable: every published snapshot holds the same providers in the configured set and among the non-nil auth checks (model-runtime.ts:302-310, 346-355, 772-784).
func requireConfiguredMatchesAuthChecks(t *testing.T, runtime *ModelRuntime, when string) {
	t.Helper()
	state := &runtime.availability
	state.mu.Lock()
	defer state.mu.Unlock()
	for id, check := range state.snapshot.auth {
		if (check != nil) != state.snapshot.configured[id] {
			t.Fatalf("%s: provider %s has auth check %v but configured=%v", when, id, check != nil, state.snapshot.configured[id])
		}
	}
	for id := range state.snapshot.configured {
		if state.snapshot.auth[id] == nil {
			t.Fatalf("%s: provider %s is configured without an auth check", when, id)
		}
	}
}

// Pi's refreshProviderAvailability removes a provider from configuredProviders when its check no longer returns auth (model-runtime.ts:346-355), so hasConfiguredAuth turns false and a later catalog projection (updateModelSnapshot, model-runtime.ts:277-284) does not make its models available again. This is a removal guard: because the runtime keeps the configured set and the auth checks in step, it does not distinguish the projection reading the configured set from it reading the auth checks (TestConfiguredInSnapshotReadsConfiguredSetNotAuthChecks pins the getter with an unreachable seeded state).
func TestProviderRefreshWithoutAuthLeavesConfiguredProviders(t *testing.T) {
	const id = "native-deauthorized"
	synctest.Test(t, func(t *testing.T) {
		services := registrationServices(t, nil)
		runtime := services.ModelRuntime()
		synctest.Wait()
		var authorized atomic.Bool
		authorized.Store(true)
		model := nativeCompatModel("native", id, "https://native.invalid/v1")
		provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if !authorized.Load() {
				return nil, nil
			}
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "native check"}, nil
		}}}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		services.Registry().StartRegistrationRefresh(t.Context())
		synctest.Wait()
		if !services.Registry().HasConfiguredAuth(id) {
			t.Fatal("provider with an auth check is not configured")
		}
		requireConfiguredMatchesAuthChecks(t, runtime, "after registration")
		authorized.Store(false)
		if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{id}, AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("refresh=%+v", result)
		}
		synctest.Wait()
		if services.Registry().HasConfiguredAuth(id) {
			t.Fatal("provider stayed configured after its check stopped returning auth")
		}
		requireConfiguredMatchesAuthChecks(t, runtime, "after deauthorization")
		gateAvailability(t, runtime)
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if got := providerModelIDs(runtime.GetAvailableSnapshot(), id); len(got) != 0 {
			t.Fatalf("catalog projection made an unconfigured provider available: %v", got)
		}
	})
}

// Pi's hasConfiguredAuth reads snapshot.configuredProviders, not the auth map (model-runtime.ts:467-469). The two hold the same providers whenever the runtime publishes them, so this seeds a snapshot where they differ to pin the getter to the configured set.
func TestConfiguredInSnapshotReadsConfiguredSetNotAuthChecks(t *testing.T) {
	runtime := registrationServices(t, nil).ModelRuntime()
	state := &runtime.availability
	state.mu.Lock()
	state.snapshot.configured = map[string]bool{"only-configured": true}
	state.snapshot.auth = map[string]*ai.AuthCheck{"only-auth": {Type: ai.CredentialAPIKey, Source: "check"}}
	state.mu.Unlock()
	if !runtime.configuredInSnapshot("only-configured") {
		t.Fatal("provider in the configured set is not configured")
	}
	if runtime.configuredInSnapshot("only-auth") {
		t.Fatal("provider with only an auth check is configured")
	}
}

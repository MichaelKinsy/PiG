package coding

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi model-runtime.ts:744-797: registerNativeProvider, registerProvider and unregisterProvider finish synchronously (updateModelSnapshot, 277-284; provisional auth, 768-787) and then start an unawaited `void this.refresh({ allowNetwork: false })` (750, 788, 796). That refresh is a continuation on the single JavaScript thread: it cannot pass its first await (ModelConfig.load, 702) before the registering caller yields, and it never runs concurrently with provider callbacks. PiG runs it inside the first awaited model-runtime call (Refresh, GetAvailable, CheckAuth, Login, Logout), or when an extension host registration returns (cmd/pig), and never on a free goroutine started by the registration itself or in a provider request.

func newRegistrationServices(t *testing.T) *AgentSessionServices {
	t.Helper()
	dir := t.TempDir()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().SetModelsStore(ai.NewInMemoryModelsStore())
	t.Cleanup(services.Close)
	return services
}

func snapshotAuth(runtime *ModelRuntime, providerID string) *ai.AuthCheck {
	state := &runtime.availability
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.snapshot.auth[providerID]
}

func availableIDs(runtime *ModelRuntime, providerID string) []string {
	var ids []string
	for _, model := range runtime.GetAvailableSnapshot() {
		if model.ProviderMeta.ProviderID == providerID {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func requireAvailableIDs(t *testing.T, runtime *ModelRuntime, providerID string, want ...string) {
	t.Helper()
	if got := availableIDs(runtime, providerID); !slices.Equal(got, want) {
		t.Fatalf("available %s models=%v, want %v", providerID, got, want)
	}
}

func extensionRegistration(baseURL, apiKey string, ids ...string) extension.ProviderConfig {
	config := extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: baseURL, APIKey: apiKey}
	for _, id := range ids {
		config.Models = append(config.Models, extension.ProviderModelConfig{ID: id, Name: id, Input: []string{"text"}, ContextWindow: 128000, MaxTokens: 4096})
	}
	return config
}

func endOnlyStream(_ context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	stream.End(&ai.AssistantMessage{Model: model.ID, Provider: model.ProviderMeta.ProviderID, API: model.ProviderMeta.API, StopReason: ai.StopReasonStop})
	return stream, nil
}

// countedNativeProvider is a stored-key provider whose Check and Resolve callbacks are counted.
func countedNativeProvider(id string, checks, resolves *atomic.Int32, models ...string) *ai.ModelsProvider {
	var catalog []*ai.Model
	for _, model := range models {
		catalog = append(catalog, nativeCompatModel(model, id, "https://native.invalid/v1"))
	}
	provider := nativeCompatProvider(nativeCompatModel("unused", id, "https://native.invalid/v1"))
	provider.GetModels = func() ([]*ai.Model, error) { return catalog, nil }
	provider.Auth.APIKey.Check = func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		checks.Add(1)
		if input.Credential == nil {
			return nil, nil
		}
		return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored native key"}, nil
	}
	provider.Stream, provider.StreamSimple = endOnlyStream, endOnlyStream
	resolve := provider.Auth.APIKey.Resolve
	provider.Auth.APIKey.Resolve = func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		resolves.Add(1)
		return resolve(ctx, input)
	}
	return provider
}

// REVIEW-PR-5-4 / REVIEW-PR-0-7: a native registration calls no provider callback and starts no goroutine; the caller's next awaited call runs the queued refresh, which checks auth and publishes availability without an explicit Refresh. A provider request never runs it (Pi's request auth precedes the refresh's callbacks: scenario 23 prints 0,2).
func TestNativeRegistrationRefreshRunsOnTheCallersYield(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		const id = "registration-native"
		if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
			t.Fatal(err)
		}
		var checks, resolves, refreshes atomic.Int32
		provider := countedNativeProvider(id, &checks, &resolves, "native")
		provider.RefreshModels = func(ai.RefreshModelsContext) error {
			refreshes.Add(1)
			return nil
		}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if checks.Load() != 0 || resolves.Load() != 0 || refreshes.Load() != 0 {
			t.Fatalf("registration ran callbacks: checks=%d resolves=%d refreshes=%d", checks.Load(), resolves.Load(), refreshes.Load())
		}
		requireAvailableIDs(t, runtime, id)
		synctest.Wait()
		if checks.Load() != 0 || resolves.Load() != 0 || refreshes.Load() != 0 {
			t.Fatalf("a background refresh ran callbacks: checks=%d resolves=%d refreshes=%d", checks.Load(), resolves.Load(), refreshes.Load())
		}
		for _, result := range []*ai.AssistantMessage{runtime.Stream(t.Context(), runtime.GetModel(id, "native"), ai.Context{}, ai.StreamOptions{}).Result(), runtime.StreamSimple(t.Context(), runtime.GetModel(id, "native"), ai.Context{}, ai.StreamOptions{}).Result()} {
			if result == nil {
				t.Fatal("no result")
			}
		}
		if checks.Load() != 0 || resolves.Load() != 2 || refreshes.Load() != 0 {
			t.Fatalf("provider requests ran the registration refresh: checks=%d resolves=%d refreshes=%d", checks.Load(), resolves.Load(), refreshes.Load())
		}
		requireAvailableIDs(t, runtime, id)
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		// Availability alone never calls RefreshModels, so only the registration refresh (Pi's `void this.refresh`, model-runtime.ts:750) accounts for it. The unscoped call waits for that pass, so no synctest.Wait separates the call from the count.
		if refreshes.Load() == 0 {
			t.Fatal("the awaited call did not run the queued registration refresh")
		}
		requireAvailableIDs(t, runtime, id, "native")
		if got := snapshotAuth(runtime, id); !reflect.DeepEqual(got, &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored native key"}) {
			t.Fatalf("auth=%+v", got)
		}
		if got := runtime.GetError(); got != "" {
			t.Fatalf("unexpected error: %s", got)
		}
	})
}

// Registrations that queue before a yield share one refresh: Pi's earlier passes are superseded by the newest and publish nothing.
func TestRegistrationsBeforeAYieldShareOneRefresh(t *testing.T) {
	passChecks := func(t *testing.T, registrations int) int32 {
		var checks atomic.Int32
		synctest.Test(t, func(t *testing.T) {
			services := newRegistrationServices(t)
			runtime := services.ModelRuntime()
			const id = "shared-refresh"
			var resolves atomic.Int32
			for range registrations {
				if err := runtime.RegisterNativeProvider(countedNativeProvider(id, &checks, &resolves, "native")); err != nil {
					t.Fatal(err)
				}
			}
			services.Registry().YieldToRegistrationRefresh(t.Context())
			settled := checks.Load()
			services.Registry().YieldToRegistrationRefresh(t.Context())
			if checks.Load() != settled {
				t.Fatalf("a settled queue ran another pass: %d -> %d", settled, checks.Load())
			}
		})
		return checks.Load()
	}
	one := passChecks(t, 1)
	if one == 0 {
		t.Fatal("the pass ran no auth check")
	}
	if three := passChecks(t, 3); three != one {
		t.Fatalf("three registrations ran %d auth checks before the yield, one registration ran %d", three, one)
	}
}

// REVIEW-PR-5-3: a first registerProvider publishes Pi's provisional configured auth, so its models are available before the queued refresh runs (model-runtime.ts:768-787). Nothing replaces the provisional entry until a yield.
func TestRegisterProviderPublishesProvisionalConfiguredAuth(t *testing.T) {
	const url = "https://provisional.invalid/v1"
	cases := []struct {
		name        string
		stored      *ai.Credential
		register    func(*AgentSessionServices, string) error
		provisional *ai.AuthCheck
		available   []string
	}{
		{
			name: "runtime input configured by key",
			register: func(services *AgentSessionServices, id string) error {
				return services.ModelRuntime().RegisterProvider(id, registryInput(url, "openai-completions", "one", "two"))
			},
			provisional: &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured provider"},
			available:   []string{"one", "two"},
		},
		{
			name: "extension config configured by key",
			register: func(services *AgentSessionServices, id string) error {
				return services.Registry().RegisterExtensionProvider(id, extensionRegistration(url, "test-key", "one", "two"))
			},
			provisional: &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured provider"},
			available:   []string{"one", "two"},
		},
		{
			// storedProviders.has(providerId) admits an OAuth-only registration; its provisional type is oauth because effective.apiKey is absent (model-runtime.ts:769,777).
			name:   "stored OAuth credential",
			stored: &ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 1 << 50},
			register: func(services *AgentSessionServices, id string) error {
				input := registryInput(url, "openai-completions", "one")
				input.APIKey = ""
				input.OAuth = &ExtensionOAuthConfig{Name: "Provisional OAuth", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
					return ai.Credential{}, errors.New("unused")
				}, RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil }, GetAPIKey: func(credential ai.Credential) string { return credential.Access }}
				return services.ModelRuntime().RegisterProvider(id, input)
			},
			provisional: &ai.AuthCheck{Type: ai.CredentialOAuth, Source: "configured provider"},
			available:   []string{"one"},
		},
		{
			name: "unconfigured registration stays unavailable",
			register: func(services *AgentSessionServices, id string) error {
				input := registryInput(url, "openai-completions", "one")
				input.APIKey = ""
				return services.ModelRuntime().RegisterProvider(id, input)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := newRegistrationServices(t)
				runtime := services.ModelRuntime()
				const id = "provisional-provider"
				if tc.stored != nil {
					if err := services.Auth().Set(id, *tc.stored); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := runtime.GetAvailable(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := tc.register(services, id); err != nil {
					t.Fatal(err)
				}
				if got := snapshotAuth(runtime, id); !reflect.DeepEqual(got, tc.provisional) {
					t.Fatalf("auth after registration=%+v, want provisional %+v", got, tc.provisional)
				}
				requireAvailableIDs(t, runtime, id, tc.available...)
				if got, want := runtime.IsUsingOAuth(id), tc.provisional != nil && tc.provisional.Type == ai.CredentialOAuth; got != want {
					t.Fatalf("IsUsingOAuth=%v, want %v", got, want)
				}
				synctest.Wait()
				if got := snapshotAuth(runtime, id); !reflect.DeepEqual(got, tc.provisional) {
					t.Fatalf("provisional auth changed without a yield: %+v", got)
				}
				check, err := runtime.CheckAuth(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				// A provider-scoped CheckAuth starts the queued refresh and does not wait for it: Pi's checkAuth awaits only the provider's check (model-runtime.ts:401-403), while the registration refresh runs unawaited (768-788).
				synctest.Wait()
				if got := snapshotAuth(runtime, id); !reflect.DeepEqual(got, check) {
					t.Fatalf("queued refresh auth=%+v, want current check %+v", got, check)
				}
				if tc.provisional != nil && (check == nil || check.Source == "configured provider") {
					t.Fatalf("queued refresh kept the provisional entry: %+v", check)
				}
				requireAvailableIDs(t, runtime, id, tc.available...)
				if got := runtime.GetError(); got != "" {
					t.Fatalf("unexpected availability error: %s", got)
				}
			})
		})
	}
}

// REVIEW-RP-011: replacing or removing a configured provider republishes the snapshot before any yield (model-runtime.ts:277-284,791-797), and a registration reads every native catalog's current models.
func TestRegistrationAndRemovalProjectTheSnapshotSynchronously(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		const id = "projected"
		if err := runtime.RegisterProvider(id, registryInput("https://projected.invalid/v1", "openai-completions", "old")); err != nil {
			t.Fatal(err)
		}
		requireAvailableIDs(t, runtime, id, "old")
		if err := runtime.RegisterProvider(id, registryInput("https://projected.invalid/v1", "openai-completions", "new")); err != nil {
			t.Fatal(err)
		}
		requireAvailableIDs(t, runtime, id, "new")
		services.Registry().UnregisterProvider(id)
		requireAvailableIDs(t, runtime, id)

		const catalogID = "mutable-catalog"
		if err := services.Auth().Set(catalogID, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
			t.Fatal(err)
		}
		var checks, resolves atomic.Int32
		catalog := []*ai.Model{nativeCompatModel("old", catalogID, "https://native.invalid/v1")}
		provider := countedNativeProvider(catalogID, &checks, &resolves)
		provider.GetModels = func() ([]*ai.Model, error) { return catalog, nil }
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		requireAvailableIDs(t, runtime, catalogID, "old")
		catalog = []*ai.Model{nativeCompatModel("new", catalogID, "https://native.invalid/v1")}
		if err := runtime.RegisterProvider("other", registryInput("https://other.invalid/v1", "openai-completions", "other")); err != nil {
			t.Fatal(err)
		}
		requireAvailableIDs(t, runtime, catalogID, "new")
	})
}

// A failed queued refresh records its availability error for getError and never reaches the registering caller (model-runtime.ts:323-329,735-739).
func TestRegistrationRefreshFailureSurfacesThroughGetError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		failure := errors.New("registration auth check unavailable")
		var checks, resolves atomic.Int32
		provider := countedNativeProvider("registration-failure", &checks, &resolves, "native")
		provider.Auth.APIKey.Check = func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) { return nil, failure }
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatalf("registration returned the refresh failure: %v", err)
		}
		if got := runtime.GetError(); got != "" {
			t.Fatalf("registration recorded an error before its refresh ran: %s", got)
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		if got := runtime.GetError(); !strings.Contains(got, failure.Error()) {
			t.Fatalf("GetError=%q, want the recorded availability failure", got)
		}
	})
}

// Services.Close cancels a registration refresh that is blocked in a provider callback and drains it. A yielding caller that gave up waiting leaves no goroutine behind.
func TestServicesCloseCancelsARunningRegistrationRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		const id = "registration-cancel"
		if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
			t.Fatal(err)
		}
		var checks, resolves, cancelled atomic.Int32
		provider := countedNativeProvider(id, &checks, &resolves, "native")
		provider.Auth.APIKey.Check = func(ctx context.Context, _ ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			checks.Add(1)
			<-ctx.Done()
			cancelled.Add(1)
			return nil, ctx.Err()
		}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		returned := make(chan struct{})
		go func() {
			defer close(returned)
			services.Registry().YieldToRegistrationRefresh(ctx)
		}()
		synctest.Wait()
		if checks.Load() == 0 {
			t.Fatal("the yield did not run the queued refresh")
		}
		cancel()
		<-returned
		if cancelled.Load() != 0 {
			t.Fatal("the yielding caller's cancellation cancelled the refresh, which has no signal in Pi")
		}
		services.Close()
		synctest.Wait()
		if got, want := cancelled.Load(), checks.Load(); got != want {
			t.Fatalf("%d of %d auth checks did not observe Services.Close", want-got, want)
		}
		requireAvailableIDs(t, runtime, id)
		if got := runtime.GetError(); got != "" {
			t.Fatalf("a cancelled registration refresh published an error: %s", got)
		}
	})
}

// Each registration gets its own refresh: a registration made while another pass runs is never dropped and needs no third call (model-runtime.ts:750 starts a separate `void this.refresh` per registration). Pass one stays blocked in its provider callback throughout.
func TestRegistrationDuringARunningPassGetsItsOwnRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		started, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		first := nativeCompatProvider(nativeCompatModel("first-model", "first-running", "https://native.invalid/v1"))
		var firstCalls atomic.Int32
		first.RefreshModels = func(ai.RefreshModelsContext) error {
			if firstCalls.Add(1) == 1 {
				close(started)
				<-release
			}
			return nil
		}
		if err := runtime.RegisterNativeProvider(first); err != nil {
			t.Fatal(err)
		}
		go services.Registry().YieldToRegistrationRefresh(t.Context())
		<-started
		second := nativeCompatProvider(nativeCompatModel("second-model", "second-registered", "https://native.invalid/v1"))
		var secondCalls atomic.Int32
		second.RefreshModels = func(ai.RefreshModelsContext) error {
			secondCalls.Add(1)
			return nil
		}
		if err := runtime.RegisterNativeProvider(second); err != nil {
			t.Fatal(err)
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		if secondCalls.Load() == 0 {
			t.Fatal("the registration made during a running pass was not refreshed")
		}
	})
}

// A full refresh always recomposes and supersedes an in-flight provider refresh, even when models.json is unchanged and even when its own signal is already aborted (model-runtime.ts:270-275,701-712).
func TestFullRefreshSupersedesInFlightProviderRefresh(t *testing.T) {
	for _, aborted := range []bool{false, true} {
		name := "live signal"
		if aborted {
			name = "aborted signal"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := newRegistrationServices(t)
				runtime := services.ModelRuntime()
				started, release := make(chan struct{}), make(chan struct{})
				published := make(chan error, 1)
				provider := nativeCompatProvider(nativeCompatModel("base", "superseded", "https://native.invalid/v1"))
				var calls atomic.Int32
				provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
					if calls.Add(1) > 1 {
						return nil
					}
					close(started)
					<-release
					_, err := refresh.Publish(ai.ModelsPublication{Update: func() { t.Error("superseded refresh published") }})
					published <- err
					return err
				}
				if err := runtime.RegisterNativeProvider(provider); err != nil {
					t.Fatal(err)
				}
				go services.Registry().YieldToRegistrationRefresh(t.Context())
				<-started
				ctx, cancel := context.WithCancel(t.Context())
				if aborted {
					cancel()
				}
				defer cancel()
				runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{"superseded"}})
				close(release)
				if err := <-published; err == nil {
					t.Fatal("the superseded refresh's publication was accepted")
				}
			})
		})
	}
}

// Extension provider registration through the extension host runs no authentication before its refresh (model-runtime.ts:744-750).
func TestExtensionNativeRegistrationAuthenticatesInItsRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		registry := services.Registry()
		var checks atomic.Int32
		provider := &extension.NativeProvider{ID: "ext-native", Name: "Ext", Models: []extension.ProviderModelConfig{{ID: "model", API: ai.APIOpenAICompletions, BaseURL: "https://ext.invalid"}},
			CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
				checks.Add(1)
				return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
			},
			ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
			},
			ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
			Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return ai.NewAssistantMessageEventStream(), nil
			},
		}
		if err := registry.RegisterNativeProvider(t.Context(), provider); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if checks.Load() != 0 {
			t.Fatalf("registration authenticated %d times before its refresh", checks.Load())
		}
		if registry.NativeProvider("ext-native") != provider {
			t.Fatal("registration is not visible")
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		if checks.Load() == 0 {
			t.Fatal("the queued refresh did not authenticate")
		}
	})
}

// The registration refresh notifies listeners on goroutines the listener owns, so Services.Close never waits for listener code (a listener blocked on the caller's lock would otherwise hang shutdown).
func TestServicesCloseDoesNotWaitForRegistrationRefreshListener(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		const id = "listener-native"
		if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
			t.Fatal(err)
		}
		var checks, resolves, notifications atomic.Int32
		entered, release := make(chan struct{}), make(chan struct{})
		detach := runtime.SetChangeListener(func() {
			if notifications.Add(1) == 2 {
				close(entered)
				<-release
			}
		})
		if err := runtime.RegisterNativeProvider(countedNativeProvider(id, &checks, &resolves, "native")); err != nil {
			t.Fatal(err)
		}
		if got := notifications.Load(); got != 1 {
			t.Fatalf("registration notified %d times, want 1", got)
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		<-entered
		requireAvailableIDs(t, runtime, id, "native")
		services.Close()
		close(release)
		detach()
		if got := notifications.Load(); got != 2 {
			t.Fatalf("listener notified %d times, want the registration plus one refresh", got)
		}
	})
}

// Like any refresh, the registration refresh notifies listeners once when it ends, on a goroutine the listener owns. Several registrations before one yield share that pass and that notification.
func TestRegistrationRefreshNotifiesListenersOncePerPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		var notifications atomic.Int32
		detach := runtime.SetChangeListener(func() { notifications.Add(1) })
		defer detach()
		for _, id := range []string{"first", "second"} {
			if err := services.Registry().RegisterExtensionProvider(id, extensionRegistration("https://"+id+".invalid/v1", "test-key", "one")); err != nil {
				t.Fatal(err)
			}
		}
		if got := notifications.Load(); got != 2 {
			t.Fatalf("registrations notified %d times, want 2", got)
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		synctest.Wait()
		if got := notifications.Load(); got != 3 {
			t.Fatalf("notifications=%d after the shared refresh, want 3", got)
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		synctest.Wait()
		if got := notifications.Load(); got != 3 {
			t.Fatalf("a yield with nothing queued notified: %d", got)
		}
	})
}

// A provider-scoped awaited call never waits for the queued registration refresh (model-runtime.ts:701-739 launches it unawaited; models.ts:398-449 refreshes only the requested providers). A registered provider whose auth check blocks must not hold an unrelated provider's Refresh, GetAvailable, CheckAuth or Login until the caller's deadline.
func TestProviderScopedCallDoesNotWaitForTheRegistrationRefresh(t *testing.T) {
	calls := map[string]func(*testing.T, *ModelRuntime, context.Context) bool{
		"Refresh": func(_ *testing.T, runtime *ModelRuntime, ctx context.Context) bool {
			return runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{"unrelated"}}).Aborted
		},
		"GetAvailable": func(t *testing.T, runtime *ModelRuntime, ctx context.Context) bool {
			_, err := runtime.GetAvailable(ctx, "unrelated")
			return err != nil
		},
		"CheckAuth": func(t *testing.T, runtime *ModelRuntime, ctx context.Context) bool {
			_, err := runtime.CheckAuth(ctx, "unrelated")
			return err != nil
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := newRegistrationServices(t)
				runtime := services.ModelRuntime()
				const blocked = "blocked-registration"
				if err := services.Auth().Set(blocked, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
					t.Fatal(err)
				}
				release := make(chan struct{})
				defer close(release)
				var checks, resolves atomic.Int32
				provider := countedNativeProvider(blocked, &checks, &resolves, "native")
				provider.Auth.APIKey.Check = func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
					checks.Add(1)
					<-release
					return nil, nil
				}
				if err := runtime.RegisterNativeProvider(provider); err != nil {
					t.Fatal(err)
				}
				unrelated := nativeCompatProvider(nativeCompatModel("model", "unrelated", "https://native.invalid/v1"))
				if err := runtime.RegisterNativeProvider(unrelated); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
				defer cancel()
				start := time.Now()
				failed := call(t, runtime, ctx)
				elapsed := time.Since(start)
				if failed || elapsed != 0 {
					t.Errorf("scoped %s waited for the registration refresh: failed=%v after %v", name, failed, elapsed)
				}
				synctest.Wait()
				if checks.Load() == 0 {
					t.Errorf("scoped %s did not start the queued registration refresh", name)
				}
			})
		})
	}
}

// An aborted provider-scoped refresh must not swallow the queued registration refresh of the providers it covered. Pi's registration refresh takes no signal and runs to completion regardless of a later caller's abort (model-runtime.ts:744-750), so the registered provider still becomes available without another call.
func TestAbortedScopedRefreshKeepsTheCoveredRegistrationRefresh(t *testing.T) {
	calls := map[string]func(context.Context, *AgentSessionServices, string) bool{
		"Refresh": func(ctx context.Context, services *AgentSessionServices, id string) bool {
			return services.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{id}}).Aborted
		},
		"ExtensionRefresh": func(ctx context.Context, services *AgentSessionServices, id string) bool {
			return services.Registry().ExtensionRefresh(ctx, new(false), []string{id}, nil).Aborted
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := newRegistrationServices(t)
				runtime := services.ModelRuntime()
				const id = "aborted-scope"
				if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
					t.Fatal(err)
				}
				var checks, resolves atomic.Int32
				if err := runtime.RegisterNativeProvider(countedNativeProvider(id, &checks, &resolves, "native")); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if !call(ctx, services, id) {
					t.Fatalf("%s with an ended context did not report aborted", name)
				}
				synctest.Wait()
				requireAvailableIDs(t, runtime, id, "native")
			})
		})
	}
}

// A login or logout recomposes and refreshes its own provider during local synchronization (model-runtime.ts:515-535), and Pi's earlier-queued registration refresh begins before it, so the operation's own refresh is never superseded by the registration pass (models.ts beginProviderRefresh). PiG covers the provider's queued pass instead of starting it beside the operation: a pass started beside the login began the same provider's refresh after the login's and dropped the login's CredentialSynchronizationError (TestModelRuntimeCredentialSynchronizationErrorRetainsCommit failed 15 of 40 runs). A login that never reaches synchronization returns the provider to the queue, because Pi's registration refresh still runs. A registration pass checks every provider, so an already refreshed witness provider shows whether one ran.
func TestCredentialOperationCoversItsProvidersRegistrationRefresh(t *testing.T) {
	for _, loginFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "synchronized", true: "login failed"}[loginFails], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := newRegistrationServices(t)
				runtime := services.ModelRuntime()
				var witnessChecks, resolves atomic.Int32
				if err := runtime.RegisterNativeProvider(countedNativeProvider("witness", &witnessChecks, &resolves, "model")); err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.GetAvailable(t.Context()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				provider := nativeCompatProvider(nativeCompatModel("model", "covered-login", "https://native.invalid/v1"))
				provider.Auth.APIKey.Login = func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
					if loginFails {
						return ai.Credential{}, errors.New("login cancelled")
					}
					return ai.Credential{Type: ai.CredentialAPIKey, Key: "key"}, nil
				}
				if err := runtime.RegisterNativeProvider(provider); err != nil {
					t.Fatal(err)
				}
				witnessChecks.Store(0)
				_, err := runtime.Login(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{})
				if (err != nil) != loginFails {
					t.Fatalf("login error = %v", err)
				}
				synctest.Wait()
				if ran := witnessChecks.Load() > 0; ran != loginFails {
					t.Fatalf("registration pass ran = %v (witness checks %d), want %v", ran, witnessChecks.Load(), loginFails)
				}
			})
		})
	}
}

// A provider-scoped GetAvailable starts the queued registration refresh and does not wait for it (model-runtime.ts:750 launches it unawaited; the scoped query selects only its own provider). Availability alone never calls RefreshModels, so a RefreshModels call proves the registration pass ran, which a scoped availability check does not do by itself. Without the start nothing else would run it.
func TestScopedGetAvailableStartsTheRegistrationRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		const id = "scoped-availability"
		var checks, resolves, refreshes atomic.Int32
		provider := countedNativeProvider(id, &checks, &resolves, "native")
		provider.RefreshModels = func(ai.RefreshModelsContext) error {
			refreshes.Add(1)
			return nil
		}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.GetAvailable(t.Context(), "unrelated"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if refreshes.Load() == 0 {
			t.Fatal("a scoped GetAvailable did not start the queued registration refresh")
		}
	})
}

// upstream: model-runtime.ts:884-897 registerNativeProvider marks the provider provisionally configured with the type its auth supports (`provider.auth.oauth && !provider.auth.apiKey ? "oauth" : "api_key"`), so a provider with a stored credential
// is available before its queued refresh runs. The provider object's Auth member (Provider.auth) decides the type; a provider with no stored credential or configured key stays unavailable.
func TestExtensionNativeRegistrationPublishesProvisionalConfiguredAuth(t *testing.T) {
	cases := []struct {
		name        string
		auth        ai.ProviderAuth
		stored      *ai.Credential
		provisional *ai.AuthCheck
	}{
		{"api key", ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "key"}}, &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}, &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured provider"}},
		{"oauth only", ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "oauth"}}, &ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 1 << 50}, &ai.AuthCheck{Type: ai.CredentialOAuth, Source: "configured provider"}},
		{"oauth and api key", ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "key"}, OAuth: &ai.OAuthAuth{Name: "oauth"}}, &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}, &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured provider"}},
		{"no stored credential", ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "key"}}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := newRegistrationServices(t)
				const id = "provisional-ext-native"
				if tc.stored != nil {
					if err := services.Auth().Set(id, *tc.stored); err != nil {
						t.Fatal(err)
					}
				}
				runtime := services.ModelRuntime()
				if _, err := runtime.GetAvailable(t.Context()); err != nil {
					t.Fatal(err)
				}
				provider := &extension.NativeProvider{ID: id, Name: "Ext", Auth: tc.auth, Models: []extension.ProviderModelConfig{{ID: "model", API: ai.APIOpenAICompletions, BaseURL: "https://ext.invalid"}},
					CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
						return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
					},
					ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
						return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
					},
					ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
					Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
						return ai.NewAssistantMessageEventStream(), nil
					},
				}
				if err := services.Registry().RegisterNativeProvider(t.Context(), provider); err != nil {
					t.Fatal(err)
				}
				if got := snapshotAuth(runtime, id); !reflect.DeepEqual(got, tc.provisional) {
					t.Fatalf("auth after registration=%+v, want provisional %+v", got, tc.provisional)
				}
				if tc.provisional == nil {
					requireAvailableIDs(t, runtime, id)
				} else {
					requireAvailableIDs(t, runtime, id, "model")
				}
			})
		})
	}
}

package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream 7027:40 uses in-memory credentials and a network refresh that never observes cancellation. Cleanup releases the Go callback only after every source assertion has completed.
func TestLoginSupersedesOlderStalledCatalogRefreshUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		provider := credentialTestProvider("stalled-login")
		provider.Name = "Stalled Login"
		provider.Auth.APIKey.Login = func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
			return ai.Credential{Type: ai.CredentialAPIKey, Key: "secret"}, nil
		}
		provider.Auth.APIKey.Check = func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if input.Credential != nil && input.Credential.Key != "" {
				return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored key"}, nil
			}
			return nil, nil
		}
		provider.Auth.APIKey.Resolve = func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			key, source := "ambient-key", "ambient key"
			if input.Credential != nil && input.Credential.Key != "" {
				key, source = input.Credential.Key, "stored key"
			}
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: key}, Source: source}, nil
		}
		provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			if refresh.AllowNetwork {
				close(started)
				<-release
			}
			return nil
		}
		store := ai.NewInMemoryAuthStorage(nil)
		registry := credentialTestRuntime(t, store, provider)
		var callers sync.WaitGroup
		defer func() { close(release); callers.Wait(); registry.NativeModels().Close() }()
		refreshed := make(chan ai.ModelsRefreshResult, 1)
		callers.Go(func() {
			refreshed <- registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(true), Providers: []string{provider.ID}})
		})
		<-started
		type loginResult struct {
			credential ai.Credential
			err        error
		}
		login := make(chan loginResult, 1)
		callers.Go(func() {
			credential, err := registry.LoginNativeProvider(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{
				Prompt: func(context.Context, ai.AuthPrompt) (string, error) { return "unused", nil }, Notify: func(ai.AuthEvent) {},
			})
			login <- loginResult{credential, err}
		})
		synctest.Wait()
		want := ai.Credential{Type: ai.CredentialAPIKey, Key: "secret"}
		select {
		case got := <-login:
			if got.err != nil || !reflect.DeepEqual(got.credential, want) {
				t.Fatalf("login=%+v", got)
			}
		default:
			t.Fatal("login waited for the older network callback")
		}
		if !slices.ContainsFunc(registry.GetAvailable(), func(model ModelEntry) bool { return model.ModelID == "dynamic" }) {
			t.Fatal("dynamic model unavailable after login")
		}
		if got, err := store.Read(t.Context(), provider.ID); err != nil || !reflect.DeepEqual(got, &want) {
			t.Fatalf("stored=%+v err=%v", got, err)
		}
		select {
		case result := <-refreshed:
			if result.Aborted {
				t.Fatal("supersession reported caller cancellation")
			}
		default:
			t.Fatal("older refresh did not settle while its provider callback remained blocked")
		}
	})
}

// Upstream packages/coding-agent/test/suite/regressions/7027-credential-refresh-hang.test.ts:89. The native provider blocks its catalog until the actual 15-second cancellation, while login's owner loop remains responsive.
func TestInteractiveLoginCompletesBeforeBoundedRefreshUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil))
		mode := newPostLoginTestMode(t)
		mode.opts.ModelRegistry = registry
		mode.opts.Model = &ai.Model{ID: "faux-1", ProviderMeta: ai.ProviderMetadata{ProviderID: "faux"}}
		started := make(chan context.Context, 1)
		provider := credentialTestProvider("stalled-login")
		provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			started <- refresh.Signal
			<-refresh.Signal.Done()
			return context.Cause(refresh.Signal)
		}
		if err := registry.RegisterNativeModelsProvider(provider); err != nil {
			t.Fatal(err)
		}
		before := time.Now()
		mode.completeProviderAuthentication(provider.ID, "Stalled Login", ai.CredentialAPIKey, mode.opts.Model, "", nil)
		signal := <-started
		if time.Since(before) != 0 {
			t.Fatal("login waited for catalog refresh")
		}
		if got := plainRender(mode.chatContainer); strings.Contains(got, "Warning:") || strings.Contains(got, "timed out") {
			t.Fatalf("early warning: %s", got)
		}
		<-signal.Done()
		if elapsed := time.Since(before); elapsed != 15*time.Second || !errors.Is(context.Cause(signal), context.DeadlineExceeded) {
			t.Fatalf("deadline = %s, want 15s", elapsed)
		}
		drainPostLoginTasks(mode)
		if got := plainRender(mode.chatContainer); !strings.Contains(got, "Saved API key for Stalled Login, but its model catalog refresh timed out; using cached models.") {
			t.Fatalf("warning missing: %s", got)
		}
	})
}

type credentialDiscoveryHandle struct {
	*postLoginModelHandle
	selected []*ai.Model
}

func (h *credentialDiscoveryHandle) SetModelOnMain(model *ai.Model, options ModelMutationOptions, dispatch func(func() error) error) error {
	h.selected = append(h.selected, model)
	return h.postLoginModelHandle.SetModelOnMain(model, options, dispatch)
}

func TestPostLoginModelDiscoveryUpstream(t *testing.T) {
	for _, tc := range []struct {
		name             string
		ids              []string
		selected         string
		changed, timeout bool
	}{
		// Upstream packages/coding-agent/test/suite/regressions/7027-credential-refresh-hang.test.ts:184, both table rows.
		{"selects balanced from fast balanced", []string{"fast", "balanced"}, "balanced", false, false},
		{"selects fast from fast powerful", []string{"fast", "powerful"}, "fast", false, false},
		// Upstream packages/coding-agent/test/suite/regressions/7027-credential-refresh-hang.test.ts:197,204,212.
		{"reports an empty catalog only after refresh", nil, "", false, false},
		{"preserves a model selected during refresh", []string{"fast", "balanced"}, "", true, false},
		{"bounds refresh to 15 seconds", nil, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_OFFLINE", "1")
			registry, _, store := radiusTestRegistry(t, `{"providers":{"radius":{"oauth":"radius","baseUrl":"https://local.invalid"}}}`, map[string]ai.Credential{"radius": {Type: ai.CredentialAPIKey, Key: "test-key"}})
			synctest.Test(t, func(t *testing.T) {
				mode := newPostLoginTestMode(t)
				mode.opts.ModelRegistry = registry
				handle := &credentialDiscoveryHandle{postLoginModelHandle: &postLoginModelHandle{recordingCompactHandle: &recordingCompactHandle{agent: mode.agent}, settings: mode.opts.SettingsManager}}
				mode.opts.SessionHandle = handle
				unknown := &ai.Model{ID: "unknown", ProviderMeta: ai.ProviderMetadata{ProviderID: "unknown", API: "unknown"}}
				mode.opts.Model = unknown
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
				registry.SetModelsStore(blocked)
				if DefaultModelPerProvider()["radius"] != "balanced" {
					t.Fatal("wrong Radius default")
				}
				before := time.Now()
				mode.completeProviderAuthentication("radius", "Radius", ai.CredentialOAuth, unknown, "", nil)
				signal := <-blocked.started
				synctest.Wait()
				if text := plainRender(mode.chatContainer); !strings.Contains(text, "Credentials saved") || strings.Contains(text, "Error:") || mode.opts.Model != unknown || len(handle.selected) != 0 {
					t.Fatalf("premature selection: %s", text)
				}
				if tc.changed {
					mode.opts.Model = &ai.Model{ID: "faux-1", ProviderMeta: ai.ProviderMetadata{ProviderID: "faux"}}
				}
				if tc.timeout {
					<-signal.Done()
					if elapsed := time.Since(before); elapsed != 15*time.Second || !errors.Is(context.Cause(signal), context.DeadlineExceeded) {
						t.Fatalf("deadline = %s", elapsed)
					}
				}
				var models []json.RawMessage
				for _, id := range tc.ids {
					models = append(models, json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q,"provider":"radius","api":"pi-messages","baseUrl":"https://local.invalid/v1","input":["text"],"contextWindow":1000,"maxTokens":100}`, id, id)))
				}
				if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: mustStoredModels(models)}); err != nil {
					t.Fatal(err)
				}
				close(blocked.release)
				drainPostLoginTasks(mode)
				text := plainRender(mode.chatContainer)
				if tc.selected != "" {
					if len(handle.selected) != 1 || modelSpec(handle.selected[0]) != "radius/"+tc.selected || !handle.persisted {
						t.Fatalf("Session selection calls=%v, persist=%v", handle.selected, handle.persisted)
					}
					if modelSpec(mode.opts.Model) != "radius/"+tc.selected || mode.opts.SettingsManager.GetDefaultModel() != tc.selected || strings.Contains(text, "Error:") {
						t.Fatalf("selection = %s; output = %s", modelSpec(mode.opts.Model), text)
					}
				} else if tc.changed {
					if mode.opts.Model.ID != "faux-1" || len(handle.selected) != 0 || strings.Contains(text, "Error:") || strings.Contains(text, "Selected") {
						t.Fatalf("changed selection = %+v; %s", mode.opts.Model, text)
					}
				} else if len(handle.selected) != 0 || mode.opts.Model != unknown || !strings.Contains(text, "no models are available") {
					t.Fatalf("empty catalog = %+v; %s", mode.opts.Model, text)
				}
				if tc.timeout && !strings.Contains(text, "timed out") {
					t.Fatalf("missing timeout warning: %s", text)
				}
			})
		})
	}
}

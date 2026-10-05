package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type modelsRuntimeCatalogStore struct {
	read   func(context.Context, string) (*ModelsStoreEntry, error)
	write  func(context.Context, string, ModelsStoreEntry) error
	delete func(context.Context, string) error
}

func (s modelsRuntimeCatalogStore) Read(ctx context.Context, id string) (*ModelsStoreEntry, error) {
	return s.read(ctx, id)
}
func (s modelsRuntimeCatalogStore) Write(ctx context.Context, id string, entry ModelsStoreEntry) error {
	return s.write(ctx, id, entry)
}
func (s modelsRuntimeCatalogStore) Delete(ctx context.Context, id string) error {
	return s.delete(ctx, id)
}

func modelsRuntimeStored(t *testing.T, models ...*Model) ModelsStoreEntry {
	t.Helper()
	raw, err := encodeModelsCatalog(AnyModels(models))
	if err != nil {
		t.Fatal(err)
	}
	return ModelsStoreEntry{Models: raw}
}
func modelsRuntimeStoredID(t *testing.T, entry *ModelsStoreEntry) string {
	t.Helper()
	if entry == nil || len(entry.Models) == 0 {
		t.Fatal("missing stored model")
	}
	var value struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(entry.Models[0], &value); err != nil {
		t.Fatal(err)
	}
	return value.ID
}
func modelsRuntimeFactory(id string, auth ProviderAuth, fetch func(RefreshModelsContext) ([]AnyModel, error)) *ModelsProvider {
	return CreateProvider(CreateProviderOptions{ID: id, Auth: auth, Models: []AnyModel{}, FetchModels: fetch, API: &ProviderStreams{Stream: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
		return NewAssistantMessageEventStream(), nil
	}, StreamSimple: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
		return NewAssistantMessageEventStream(), nil
	}}})
}

func TestModelsRuntimeRefreshUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:219
	t.Run("refresh() updates every configured dynamic provider and reports failures", func(t *testing.T) {
		list := []*Model{modelsRuntimeModel("dyn", "before")}
		refreshes := 0
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dyn", getModels: func() ([]*Model, error) { return list, nil }, refreshModels: func(refresh RefreshModelsContext) error {
			if !refresh.AllowNetwork {
				return nil
			}
			refreshes++
			_, err := refresh.Publish(ModelsPublication{Update: func() { list = []*Model{modelsRuntimeModel("dyn", "after")} }})
			return err
		}}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "static", models: []*Model{modelsRuntimeModel("static", "s1")}}))
		if models.GetModel("dyn", "before") == nil {
			t.Fatal("missing baseline")
		}
		first := models.Refresh(t.Context())
		if len(first.Errors) != 0 || refreshes != 1 || models.GetModel("dyn", "after") == nil || models.GetModel("dyn", "before") != nil {
			t.Fatalf("first=%+v refreshes=%d", first, refreshes)
		}
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "flaky", refreshModels: func(refresh RefreshModelsContext) error {
			if refresh.AllowNetwork {
				return errors.New("fetch failed")
			}
			return nil
		}}))
		second := models.Refresh(t.Context())
		if refreshes != 2 || second.Errors["flaky"] == nil || second.Errors["flaky"].Error() != "fetch failed" {
			t.Fatalf("second=%+v refreshes=%d", second, refreshes)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:260
	t.Run("restricts refresh work to selected providers", func(t *testing.T) {
		calls := []string{}
		models := CreateModels()
		for _, id := range []string{"one", "two"} {
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: id, refreshModels: func(refresh RefreshModelsContext) error {
				phase := "cache"
				if refresh.AllowNetwork {
					phase = "network"
				}
				calls = append(calls, id+":"+phase)
				return nil
			}}))
		}
		result := models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"two", "unknown"}})
		if len(result.Errors) != 0 || !reflect.DeepEqual(calls, []string{"two:cache", "two:network"}) {
			t.Fatalf("result=%+v calls=%v", result, calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:280
	t.Run("restores cached models before waiting for network auth", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := NewInMemoryModelsStore()
			if err := store.Write(t.Context(), "dynamic", modelsRuntimeStored(t, modelsRuntimeModel("dynamic", "cached"))); err != nil {
				t.Fatal(err)
			}
			started, finish := make(chan struct{}), make(chan struct{})
			defer close(finish)
			provider := modelsRuntimeFactory("dynamic", ProviderAuth{APIKey: &APIKeyAuth{Name: "Blocked auth", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
				close(started)
				<-finish
				return &AuthResult{Auth: ModelAuth{APIKey: "key"}}, nil
			}}}, func(RefreshModelsContext) ([]AnyModel, error) {
				t.Error("must not fetch")
				return nil, errors.New("must not fetch")
			})
			models := CreateModels(CreateModelsOptions{ModelsStore: store})
			models.SetProvider(provider)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan ModelsRefreshResult, 1)
			go func() { done <- models.Refresh(ctx, ModelsRefreshOptions{Providers: []string{"dynamic"}}) }()
			<-started
			if models.GetModel("dynamic", "cached") == nil {
				t.Fatal("cache was not restored before auth")
			}
			cancel()
			if result := <-done; !result.Aborted {
				t.Fatalf("result=%+v", result)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:324
	t.Run("lets providers choose persistent deletion and ephemeral publication atomically", func(t *testing.T) {
		entry := new(modelsRuntimeStored(t, modelsRuntimeModel("dynamic", "stored")))
		state := "initial"
		store := modelsRuntimeCatalogStore{read: func(context.Context, string) (*ModelsStoreEntry, error) { return entry, nil }, write: func(_ context.Context, _ string, next ModelsStoreEntry) error { entry = &next; return nil }, delete: func(context.Context, string) error { entry = nil; return nil }}
		models := CreateModels(CreateModelsOptions{ModelsStore: store})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error {
			if modelsRuntimeStoredID(t, refresh.Stored) != "stored" {
				t.Error("wrong stored snapshot")
			}
			if _, err := refresh.Publish(ModelsPublication{PersistSet: true, Update: func() {
				if entry != nil {
					t.Error("update ran before deletion")
				}
				state = "deleted"
			}}); err != nil {
				return err
			}
			_, err := refresh.Publish(ModelsPublication{Update: func() { state = "ephemeral" }})
			return err
		}}))
		result := models.Refresh(t.Context(), ModelsRefreshOptions{AllowNetwork: new(false)})
		if len(result.Errors) != 0 || entry != nil || state != "ephemeral" {
			t.Fatalf("result=%+v entry=%v state=%s", result, entry, state)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:365
	t.Run("persists dynamic catalogs and restores them without network access", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		store := NewInMemoryModelsStore()
		modelsRuntimePut(t, credentials, "dynamic", Credential{Type: CredentialAPIKey, Key: "key"})
		online := CreateModels(CreateModelsOptions{Credentials: credentials, ModelsStore: store})
		online.SetProvider(modelsRuntimeFactory("dynamic", ProviderAuth{APIKey: modelsRuntimeEnvKey("")}, func(RefreshModelsContext) ([]AnyModel, error) {
			return []AnyModel{modelsRuntimeModel("dynamic", "fetched")}, nil
		}))
		if result := online.Refresh(t.Context()); len(result.Errors) != 0 || online.GetModel("dynamic", "fetched") == nil {
			t.Fatalf("online=%+v", result)
		}
		offline := CreateModels(CreateModelsOptions{Credentials: credentials, ModelsStore: store})
		offline.SetProvider(modelsRuntimeFactory("dynamic", ProviderAuth{APIKey: modelsRuntimeEnvKey("")}, func(RefreshModelsContext) ([]AnyModel, error) {
			t.Error("must not fetch")
			return nil, errors.New("must not fetch")
		}))
		if result := offline.Refresh(t.Context(), ModelsRefreshOptions{AllowNetwork: new(false)}); len(result.Errors) != 0 || offline.GetModel("dynamic", "fetched") == nil {
			t.Fatalf("offline=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:396
	t.Run("passes effective API-key credentials and refresh options while skipping unconfigured providers", func(t *testing.T) {
		var effective *Credential
		var force *bool
		unconfigured := 0
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "configured", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("ambient-key")}, refreshModels: func(refresh RefreshModelsContext) error {
			if refresh.AllowNetwork {
				effective = refresh.Credential
				force = refresh.Force
			}
			return nil
		}}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "unconfigured", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("")}, refreshModels: func(refresh RefreshModelsContext) error {
			if refresh.AllowNetwork {
				unconfigured++
			}
			return nil
		}}))
		models.Refresh(t.Context(), ModelsRefreshOptions{Force: new(true)})
		if !reflect.DeepEqual(effective, &Credential{Type: CredentialAPIKey, Key: "ambient-key"}) || force == nil || !*force || unconfigured != 0 {
			t.Fatalf("credential=%+v force=%v unconfigured=%d", effective, force, unconfigured)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:428
	t.Run("refreshes expired OAuth before refreshing models", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		modelsRuntimePut(t, credentials, "oauth-dynamic", Credential{Type: CredentialOAuth, Access: "expired", Refresh: "refresh", Expires: 0})
		var effective *Credential
		oauth := modelsRuntimeOAuth()
		oauth.Refresh = func(context.Context, Credential) (Credential, error) {
			return Credential{Type: CredentialOAuth, Access: "fresh", Refresh: "rotated", Expires: time.Now().Add(time.Minute).UnixMilli()}, nil
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "oauth-dynamic", auth: &ProviderAuth{OAuth: oauth}, refreshModels: func(refresh RefreshModelsContext) error {
			if refresh.AllowNetwork {
				effective = refresh.Credential
			}
			return nil
		}}))
		result := models.Refresh(t.Context())
		if len(result.Errors) != 0 || effective == nil || effective.Type != CredentialOAuth || effective.Access != "fresh" || effective.Refresh != "rotated" {
			t.Fatalf("result=%+v credential=%+v", result, effective)
		}
		stored, err := credentials.Read(t.Context(), "oauth-dynamic")
		if err != nil || stored.Access != "fresh" || stored.Refresh != "rotated" {
			t.Fatalf("stored=%+v err=%v", stored, err)
		}
	})
	// .upstream/v1.0.3/packages/ai/test/models-runtime.test.ts:476
	t.Run("persists an OAuth refresh that started before the model refresh was cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			credentials := NewInMemoryCredentialStore()
			modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old", Refresh: "old-refresh", Expires: 0})
			ctx, abort := context.WithCancel(t.Context())
			defer abort()
			oauth := modelsRuntimeOAuth()
			oauth.Refresh = func(_ context.Context, credential Credential) (Credential, error) {
				abort()
				credential.Access, credential.Refresh = "new", "new-refresh"
				credential.Expires = time.Now().Add(time.Minute).UnixMilli()
				return credential, nil
			}
			models := CreateModels(CreateModelsOptions{Credentials: credentials})
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}, refreshModels: func(RefreshModelsContext) error { return nil }}))
			if result := models.Refresh(ctx); !result.Aborted {
				t.Fatalf("result=%+v, want aborted", result)
			}
			models.operations.Wait()
			oauthRefreshWork.Wait()
			credentials.operations.Wait()
			got, err := credentials.Read(t.Context(), "p1")
			if err != nil || got == nil || got.Refresh != "new-refresh" {
				t.Fatalf("credential=%+v err=%v; want the rotated refresh token", got, err)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:462
	t.Run("always gives providers a concrete signal", func(t *testing.T) {
		var signal context.Context
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error { signal = refresh.Signal; return nil }}))
		result := models.Refresh(t.Context())
		if result.Aborted || signal == nil || signal.Done() == nil || signal.Err() != nil {
			t.Fatalf("result=%+v signal=%v", result, signal)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:480
	t.Run("binds model-store waits to the provider refresh signal", func(t *testing.T) {
		var signals []context.Context
		var providerSignal context.Context
		store := modelsRuntimeCatalogStore{read: func(ctx context.Context, _ string) (*ModelsStoreEntry, error) {
			signals = append(signals, ctx)
			return nil, nil
		}, write: func(ctx context.Context, _ string, _ ModelsStoreEntry) error {
			signals = append(signals, ctx)
			return nil
		}, delete: func(ctx context.Context, _ string) error { signals = append(signals, ctx); return nil }}
		models := CreateModels(CreateModelsOptions{ModelsStore: store})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("key")}, refreshModels: func(refresh RefreshModelsContext) error {
			providerSignal = refresh.Signal
			if !refresh.AllowNetwork {
				return nil
			}
			_, err := refresh.Publish(ModelsPublication{Persist: new(modelsRuntimeStored(t, modelsRuntimeModel("dynamic", "fresh")))})
			return err
		}}))
		result := models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dynamic"}})
		if len(result.Errors) != 0 || len(signals) != 3 {
			t.Fatalf("result=%+v signals=%v", result, signals)
		}
		for _, signal := range signals {
			if signal != providerSignal {
				t.Fatal("storage used a different signal")
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:515
	t.Run("returns aborted state without reporting cancellation as a provider error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error {
			cancel()
			if refresh.Signal.Err() != nil {
				return nil
			}
			return nil
		}}))
		result := models.Refresh(ctx)
		if !result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:533
	t.Run("stops waiting on abort when a provider ignores its signal", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			started, reject := make(chan struct{}), make(chan error, 1)
			defer close(reject)
			var calls atomic.Int32
			models := CreateModels()
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(RefreshModelsContext) error {
				if calls.Add(1) != 1 {
					return nil
				}
				close(started)
				return <-reject
			}}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan ModelsRefreshResult, 1)
			go func() { done <- models.Refresh(ctx) }()
			<-started
			cancel()
			result := <-done
			if !result.Aborted || len(result.Errors) != 0 {
				t.Fatalf("result=%+v", result)
			}
			reject <- errors.New("late provider failure")
			models.operations.Wait()
			if len(result.Errors) != 0 {
				t.Fatal("late failure mutated the returned snapshot")
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:570
	t.Run("rejects late publication from a superseded non-cooperative provider", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := NewInMemoryModelsStore()
			state := "initial"
			calls := 0
			started, release := make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			var mu sync.Mutex
			models := CreateModels(CreateModelsOptions{ModelsStore: store})
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error {
				if !refresh.AllowNetwork {
					return nil
				}
				mu.Lock()
				calls++
				current := calls
				mu.Unlock()
				if current == 1 {
					close(started)
					<-release
				}
				value := fmt.Sprintf("generation-%d", current)
				_, err := refresh.Publish(ModelsPublication{Persist: new(modelsRuntimeStored(t, modelsRuntimeModel("dynamic", value))), Update: func() { mu.Lock(); state = value; mu.Unlock() }})
				return err
			}}))
			first := make(chan ModelsRefreshResult, 1)
			go func() { first <- models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dynamic"}}) }()
			<-started
			models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dynamic"}})
			<-first
			released = true
			close(release)
			models.operations.Wait()
			entry, err := store.Read(t.Context(), "dynamic")
			mu.Lock()
			got := state
			mu.Unlock()
			if err != nil || got != "generation-2" || modelsRuntimeStoredID(t, entry) != "generation-2" {
				t.Fatalf("state=%s entry=%v err=%v", got, entry, err)
			}
		})
	})
}

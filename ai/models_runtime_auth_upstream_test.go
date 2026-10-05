package ai

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type modelsRuntimeStore struct {
	base   *InMemoryCredentialStore
	read   func(context.Context, string) (*Credential, error)
	modify func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error)
}

func (s modelsRuntimeStore) Read(ctx context.Context, id string) (*Credential, error) {
	if s.read != nil {
		return s.read(ctx, id)
	}
	return s.base.Read(ctx, id)
}
func (s modelsRuntimeStore) List(ctx context.Context) ([]CredentialInfo, error) {
	if s.base == nil {
		return []CredentialInfo{}, nil
	}
	return s.base.List(ctx)
}
func (s modelsRuntimeStore) Modify(ctx context.Context, id string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
	if s.modify != nil {
		return s.modify(ctx, id, fn)
	}
	if s.base == nil {
		return nil, nil
	}
	return s.base.Modify(ctx, id, fn)
}
func (s modelsRuntimeStore) Delete(ctx context.Context, id string) error {
	if s.base == nil {
		return nil
	}
	return s.base.Delete(ctx, id)
}

func TestModelsRuntimeAuthUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:617
	t.Run("passes caller signals to provider auth callbacks", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var received []context.Context
		apiKey := &APIKeyAuth{Name: "Signal auth", Login: func(ctx context.Context, _ AuthInteraction) (Credential, error) {
			received = append(received, ctx)
			return Credential{Type: CredentialAPIKey, Key: "saved"}, nil
		}, Check: func(ctx context.Context, _ APIKeyAuthInput) (*AuthCheck, error) {
			received = append(received, ctx)
			return &AuthCheck{Type: CredentialAPIKey}, nil
		}, Resolve: func(ctx context.Context, _ APIKeyAuthInput) (*AuthResult, error) {
			received = append(received, ctx)
			return &AuthResult{Auth: ModelAuth{APIKey: "resolved"}}, nil
		}}
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: apiKey}}))
		if _, err := models.CheckAuth(ctx, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := models.GetAuth(ctx, "p1"); err != nil {
			t.Fatal(err)
		}
		if _, err := models.Login(ctx, "p1", CredentialAPIKey, AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { return "unused", nil }, Notify: func(AuthEvent) {}}); err != nil {
			t.Fatal(err)
		}
		if len(received) != 3 || received[0] != ctx || received[1] != ctx || received[2] != ctx {
			t.Fatalf("received contexts=%v", received)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:649
	t.Run("stops waiting for non-cooperative auth callbacks", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			checkStarted, finishCheck := make(chan struct{}), make(chan struct{})
			resolveStarted, finishResolve := make(chan struct{}), make(chan struct{})
			defer close(finishCheck)
			defer close(finishResolve)
			models := CreateModels()
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: &APIKeyAuth{Name: "Blocked auth", Check: func(context.Context, APIKeyAuthInput) (*AuthCheck, error) {
				close(checkStarted)
				<-finishCheck
				return &AuthCheck{Type: CredentialAPIKey}, nil
			}, Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
				close(resolveStarted)
				<-finishResolve
				return &AuthResult{Auth: ModelAuth{APIKey: "key"}}, nil
			}}}}))
			availableCtx, abortAvailable := context.WithCancel(t.Context())
			defer abortAvailable()
			available := make(chan error, 1)
			go func() { _, err := models.GetAvailable(availableCtx); available <- err }()
			<-checkStarted
			abortAvailable()
			if err := <-available; !errors.Is(err, context.Canceled) {
				t.Fatalf("available error=%v", err)
			}
			authCtx, abortAuth := context.WithCancel(t.Context())
			defer abortAuth()
			auth := make(chan error, 1)
			go func() { _, err := models.GetAuth(authCtx, "p1"); auth <- err }()
			<-resolveStarted
			abortAuth()
			if err := <-auth; !errors.Is(err, context.Canceled) {
				t.Fatalf("auth error=%v", err)
			}
		})
	})
	// .upstream/v1.0.3/packages/ai/test/models-runtime.test.ts:780 (Radius bug report 01a10855-43d8-7447-a710-2acf5ee0b2ee: refresh_token_invalidated after a cancelled refresh)
	t.Run("persists an OAuth refresh that started before the request was cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			credentials := NewInMemoryCredentialStore()
			modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old", Refresh: "old-refresh", Expires: 0})
			ctx, abort := context.WithCancelCause(t.Context())
			defer abort(context.Canceled)
			oauth := modelsRuntimeOAuth()
			oauth.Refresh = func(_ context.Context, credential Credential) (Credential, error) {
				// The provider has rotated old-refresh by the time the request is cancelled.
				abort(context.Canceled)
				credential.Access, credential.Refresh = "new", "new-refresh"
				credential.Expires = time.Now().Add(time.Minute).UnixMilli()
				return credential, nil
			}
			models := CreateModels(CreateModelsOptions{Credentials: credentials})
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
			if _, err := models.GetAuth(ctx, "p1"); !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v, want the cancellation", err)
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
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:777
	t.Run("resolves auth: stored credential owns the provider, ambient only when nothing stored", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("env-key"), OAuth: modelsRuntimeOAuth()}}))
		model := modelsRuntimeModel("p1", "model-a")
		byModel, err := models.GetModelAuth(t.Context(), model)
		if err != nil || byModel == nil || byModel.Auth.APIKey != "env-key" {
			t.Fatalf("model auth=%v err=%v", byModel, err)
		}
		byID, err := models.GetAuth(t.Context(), "p1")
		if err != nil || byID == nil || byID.Auth.APIKey != "env-key" {
			t.Fatalf("id auth=%v err=%v", byID, err)
		}
		explicit, err := models.GetModelAuth(t.Context(), model, AuthResolutionOverrides{APIKey: new("explicit-key")})
		if err != nil || explicit == nil || explicit.Auth.APIKey != "explicit-key" {
			t.Fatalf("explicit=%v err=%v", explicit, err)
		}
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "oauth-token", Refresh: "r", Expires: time.Now().Add(10 * time.Minute).UnixMilli()})
		stored, err := models.GetAuth(t.Context(), "p1")
		if err != nil || stored == nil || stored.Auth.APIKey != "oauth-token" || stored.Source != "OAuth" {
			t.Fatalf("oauth=%v err=%v", stored, err)
		}
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialAPIKey, Key: "stored-key"})
		stored, err = models.GetAuth(t.Context(), "p1")
		if err != nil || stored == nil || stored.Auth.APIKey != "stored-key" || stored.Source != "stored" {
			t.Fatalf("stored=%v err=%v", stored, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:806
	t.Run("checks provider auth without refreshing OAuth and filters available models", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		refreshes := 0
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "ambient", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("env-key")}}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "missing", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("")}}))
		oauth := modelsRuntimeOAuth()
		oauth.Refresh = func(_ context.Context, c Credential) (Credential, error) { refreshes++; return c, nil }
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "oauth", auth: &ProviderAuth{OAuth: oauth}}))
		modelsRuntimePut(t, credentials, "oauth", Credential{Type: CredentialOAuth, Access: "expired", Refresh: "refresh", Expires: 0})
		ambient, err := models.CheckAuth(t.Context(), "ambient")
		if err != nil || !reflect.DeepEqual(ambient, &AuthCheck{Source: "env", Type: CredentialAPIKey}) {
			t.Fatalf("ambient=%v err=%v", ambient, err)
		}
		missing, err := models.CheckAuth(t.Context(), "missing")
		if err != nil || missing != nil {
			t.Fatalf("missing=%v err=%v", missing, err)
		}
		check, err := models.CheckAuth(t.Context(), "oauth")
		if err != nil || !reflect.DeepEqual(check, &AuthCheck{Source: "OAuth", Type: CredentialOAuth}) || refreshes != 0 {
			t.Fatalf("oauth=%v refreshes=%d err=%v", check, refreshes, err)
		}
		available, err := models.GetAvailable(t.Context())
		if err != nil || len(available) != 2 || available[0].ProviderMeta.ProviderID != "ambient" || available[1].ProviderMeta.ProviderID != "oauth" {
			t.Fatalf("available=%v err=%v", available, err)
		}
		available, err = models.GetAvailable(t.Context(), "ambient")
		if err != nil || len(available) != 1 || available[0].ProviderMeta.ProviderID != "ambient" {
			t.Fatalf("ambient models=%v err=%v", available, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:840
	t.Run("runs provider login and logout through the credential store", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		apiKey := modelsRuntimeEnvKey("")
		apiKey.Login = func(context.Context, AuthInteraction) (Credential, error) {
			return Credential{Type: CredentialAPIKey, Key: "logged-in"}, nil
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: apiKey}}))
		credential, err := models.Login(t.Context(), "p1", CredentialAPIKey, AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { return "unused", nil }, Notify: func(AuthEvent) {}})
		if err != nil || !reflect.DeepEqual(credential, Credential{Type: CredentialAPIKey, Key: "logged-in"}) {
			t.Fatalf("credential=%+v err=%v", credential, err)
		}
		stored, err := credentials.Read(t.Context(), "p1")
		if err != nil || !reflect.DeepEqual(stored, &credential) {
			t.Fatalf("stored=%v err=%v", stored, err)
		}
		if err := models.Logout(t.Context(), "p1"); err != nil {
			t.Fatal(err)
		}
		stored, err = credentials.Read(t.Context(), "p1")
		if err != nil || stored != nil {
			t.Fatalf("after logout=%v err=%v", stored, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:858
	t.Run("a stored credential without a matching handler blocks ambient fallback", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("env-key")}}))
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "a", Refresh: "r", Expires: 0})
		if auth, err := models.GetAuth(t.Context(), "p1"); auth != nil || err != nil {
			t.Fatalf("auth=%v err=%v", auth, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:868
	t.Run("refreshes expired oauth credentials and persists the rotated credential", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		oauth := modelsRuntimeOAuth()
		oauth.Refresh = func(_ context.Context, c Credential) (Credential, error) {
			c.Access = "new-token"
			c.Expires = time.Now().Add(time.Hour).UnixMilli()
			return c, nil
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old-token", Refresh: "r", Expires: 0})
		auth, err := models.GetAuth(t.Context(), "p1")
		stored, readErr := credentials.Read(t.Context(), "p1")
		if err != nil || auth == nil || auth.Auth.APIKey != "new-token" || readErr != nil || stored.Access != "new-token" {
			t.Fatalf("auth=%v stored=%v err=%v/%v", auth, stored, err, readErr)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:887
	t.Run("refreshes oauth credentials with less than five minutes remaining", func(t *testing.T) { modelsRuntimeMinimumValidity(t, time.Minute, nil) })
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:907
	t.Run("honors a caller's longer OAuth minimum validity", func(t *testing.T) {
		modelsRuntimeMinimumValidity(t, 10*time.Minute, new(float64(30*time.Minute/time.Millisecond)))
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:927
	t.Run("rejects with code oauth when refresh fails, preserving the stored credential", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		oauth := modelsRuntimeOAuth()
		oauth.Refresh = func(context.Context, Credential) (Credential, error) {
			return Credential{}, errors.New("invalid_grant")
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0})
		_, err := models.GetAuth(t.Context(), "p1")
		requireModelsError(t, err, ModelsErrorOAuth)
		stored, err := credentials.Read(t.Context(), "p1")
		if err != nil || stored.Access != "old" {
			t.Fatalf("stored=%v err=%v", stored, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:943
	t.Run("serializes concurrent OAuth refreshes through store.modify (no double refresh)", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			credentials := NewInMemoryCredentialStore()
			modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old", Refresh: "r1", Expires: 0})
			var refreshes atomic.Int32
			oauth := modelsRuntimeOAuth()
			oauth.Refresh = func(context.Context, Credential) (Credential, error) {
				count := refreshes.Add(1)
				time.Sleep(10 * time.Millisecond)
				return Credential{Type: CredentialOAuth, Access: fmt.Sprintf("new-%d", count), Refresh: "r2", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
			}
			models := CreateModels(CreateModelsOptions{Credentials: credentials})
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
			var wg sync.WaitGroup
			auths := make([]*AuthResult, 2)
			errs := make([]error, 2)
			for i := range auths {
				wg.Go(func() { auths[i], errs[i] = models.GetAuth(t.Context(), "p1") })
			}
			wg.Wait()
			if refreshes.Load() != 1 || errs[0] != nil || errs[1] != nil || auths[0] == nil || auths[1] == nil || auths[0].Auth.APIKey != "new-1" || auths[1].Auth.APIKey != "new-1" {
				t.Fatalf("refreshes=%d auth=%v errors=%v", refreshes.Load(), auths, errs)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:965
	t.Run("valid oauth tokens resolve without touching modify", func(t *testing.T) {
		base := NewInMemoryCredentialStore()
		modifies := 0
		credentials := modelsRuntimeStore{base: base, modify: func(ctx context.Context, id string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
			modifies++
			return base.Modify(ctx, id, fn)
		}}
		modelsRuntimePut(t, base, "p1", Credential{Type: CredentialOAuth, Access: "valid", Refresh: "r", Expires: time.Now().Add(10 * time.Minute).UnixMilli()})
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: modelsRuntimeOAuth()}}))
		auth, err := models.GetAuth(t.Context(), "p1")
		if err != nil || auth == nil || auth.Auth.APIKey != "valid" || modifies != 0 {
			t.Fatalf("auth=%v modifies=%d err=%v", auth, modifies, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:990
	t.Run("wraps credential store failures in ModelsError", func(t *testing.T) {
		readFailing := modelsRuntimeStore{read: func(context.Context, string) (*Credential, error) { return nil, errors.New("disk on fire") }}
		models := CreateModels(CreateModelsOptions{Credentials: readFailing})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("env-key")}}))
		_, err := models.GetAuth(t.Context(), "p1")
		requireModelsError(t, err, ModelsErrorAuth)
		modifyFailing := modelsRuntimeStore{read: func(context.Context, string) (*Credential, error) {
			return &Credential{Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0}, nil
		}, modify: func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
			return nil, errors.New("disk on fire")
		}}
		models = CreateModels(CreateModelsOptions{Credentials: modifyFailing})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: modelsRuntimeOAuth()}}))
		_, err = models.GetAuth(t.Context(), "p1")
		requireModelsError(t, err, ModelsErrorAuth)
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1018
	t.Run("keeps the underlying reason in wrapped oauth refresh errors", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0})
		oauth := modelsRuntimeOAuth()
		oauth.Refresh = func(context.Context, Credential) (Credential, error) {
			return Credential{}, errors.New("token refresh failed (400): invalid_grant")
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
		_, err := models.GetAuth(t.Context(), "p1")
		if err == nil || err.Error() != "OAuth refresh failed for p1: token refresh failed (400): invalid_grant" {
			t.Fatalf("error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1040
	t.Run("wraps api-key auth failures in ModelsError", func(t *testing.T) {
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: &APIKeyAuth{Name: "Failing", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return nil, errors.New("nope") }}}}))
		_, err := models.GetAuth(t.Context(), "p1")
		requireModelsError(t, err, ModelsErrorAuth)
	})
}

func modelsRuntimeMinimumValidity(t *testing.T, remaining time.Duration, minimum *float64) {
	t.Helper()
	credentials := NewInMemoryCredentialStore()
	refreshes := 0
	oauth := modelsRuntimeOAuth()
	oauth.Refresh = func(_ context.Context, c Credential) (Credential, error) {
		refreshes++
		c.Access = "new-token"
		c.Expires = time.Now().Add(time.Hour).UnixMilli()
		return c, nil
	}
	models := CreateModels(CreateModelsOptions{Credentials: credentials})
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
	modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old-token", Refresh: "r", Expires: time.Now().Add(remaining).UnixMilli()})
	auth, err := models.GetAuth(t.Context(), "p1", AuthResolutionOverrides{MinOAuthValidityMs: minimum})
	if err != nil || auth == nil || auth.Auth.APIKey != "new-token" || refreshes != 1 {
		t.Fatalf("auth=%v refreshes=%d err=%v", auth, refreshes, err)
	}
}

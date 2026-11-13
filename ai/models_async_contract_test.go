package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// scriptedCredentialStore wraps the in-memory store so a test decides what Modify and Delete do and sees what they were given.
type scriptedCredentialStore struct {
	*InMemoryCredentialStore
	modify func(ctx context.Context, providerID string, fn func(*Credential) (*Credential, error)) (*Credential, error)
	remove func(ctx context.Context, providerID string) error
}

func (s *scriptedCredentialStore) Modify(ctx context.Context, providerID string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
	if s.modify != nil {
		return s.modify(ctx, providerID, fn)
	}
	return s.InMemoryCredentialStore.Modify(ctx, providerID, fn)
}

func (s *scriptedCredentialStore) Delete(ctx context.Context, providerID string) error {
	if s.remove != nil {
		return s.remove(ctx, providerID)
	}
	return s.InMemoryCredentialStore.Delete(ctx, providerID)
}

// oauthModels is a Models whose provider "p" signs in through login, over a scripted credential store.
func oauthModels(login func(context.Context, AuthInteraction, LoginOptions) (Credential, error)) (*Models, *scriptedCredentialStore, *ModelsProvider) {
	store := &scriptedCredentialStore{InMemoryCredentialStore: NewInMemoryCredentialStore()}
	models := CreateModels(CreateModelsOptions{Credentials: store})
	provider := modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p", auth: &ProviderAuth{OAuth: &OAuthAuth{Name: "P", Login: login}}})
	models.SetProvider(provider)
	return models, store, provider
}

var errNeverLogin = errors.New("the login ran")

func loginNeverRuns(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
	return Credential{}, errNeverLogin
}

func oauthCredential() Credential {
	return Credential{Type: CredentialOAuth, Access: "a", Refresh: "r"}
}

// models.ts login (:803-812) takes `signal.throwIfAborted()` before the provider lookup and before the flow, with the signal's own reason.
func TestModelsLoginWithAnAbortedSignalSettlesWithItsReasonBeforeAnyOtherCheck(t *testing.T) {
	reason := errors.New("operator interrupt")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(reason)
	models, store, provider := oauthModels(loginNeverRuns)
	store.modify = func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
		t.Error("the credential store was touched")
		return nil, nil
	}
	if _, err := models.Login(ctx, "no-such-provider", CredentialOAuth, AuthInteraction{}); !errors.Is(err, reason) {
		t.Fatalf("Login(unknown provider) = %v, want the signal's reason before the provider lookup", err)
	}
	if _, err := models.LoginProvider(ctx, provider, CredentialOAuth, AuthInteraction{}); !errors.Is(err, reason) {
		t.Fatalf("LoginProvider = %v, want the signal's reason before the flow", err)
	}
}

// models.ts login (:810-816): `signal.throwIfAborted()` precedes the `does not support <type> login` check, so an aborted login of a provider that has no such
// login settles with the signal's reason. LoginProvider is that body for a provider the caller composed itself, without the lookup.
func TestModelsLoginProviderWithAnAbortedSignalSettlesWithItsReasonBeforeTheLoginMethodCheck(t *testing.T) {
	reason := errors.New("operator interrupt")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(reason)
	models, _, provider := oauthModels(loginNeverRuns)
	if _, err := models.LoginProvider(ctx, provider, CredentialAPIKey, AuthInteraction{}); !errors.Is(err, reason) {
		t.Fatalf("LoginProvider(api key login the provider lacks) = %v, want the signal's reason", err)
	}
	if _, err := models.LoginProvider(t.Context(), provider, CredentialAPIKey, AuthInteraction{}); err == nil || !strings.Contains(err.Error(), " does not support ") {
		t.Fatalf("LoginProvider with a live signal = %v, want the unsupported login error", err)
	}
}

// models.ts :812: an unknown provider is `Unknown provider: <id>`.
func TestModelsLoginNamesAnUnknownProvider(t *testing.T) {
	models, _, _ := oauthModels(loginNeverRuns)
	_, err := models.Login(t.Context(), "nope", CredentialOAuth, AuthInteraction{})
	var modelsErr *ModelsError
	if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorProvider || err.Error() != "Unknown provider: nope" {
		t.Fatalf("error = %v, want a provider ModelsError %q", err, "Unknown provider: nope")
	}
}

// models.ts :817-818 races the flow with the signal (raceWithAbortSignal): an abort settles the login with its reason while the flow is still running, and the credential
// is not stored. A flow that returns after the abort does not turn it into a success (the race already lost).
func TestModelsLoginAbortWhileTheFlowRunsSettlesWithTheReasonAndStoresNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reason := errors.New("operator interrupt")
		ctx, cancel := context.WithCancelCause(t.Context())
		release := make(chan struct{})
		models, store, _ := oauthModels(func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
			<-release
			return oauthCredential(), nil
		})
		store.modify = func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
			t.Error("a login aborted before it finished stored its credential")
			return nil, nil
		}
		done := make(chan error, 1)
		go func() { _, err := models.Login(ctx, "p", CredentialOAuth, AuthInteraction{}); done <- err }()
		synctest.Wait()
		cancel(reason)
		select {
		case err := <-done:
			if !errors.Is(err, reason) {
				t.Fatalf("Login = %v, want the signal's reason while the flow is still running", err)
			}
		case <-time.After(time.Minute):
			t.Fatal("Login did not settle on the abort while the flow was running")
		}
		close(release)
		models.operations.Wait()
	})
}

// A flow that finishes in the same instant as the abort never wins over it: whichever the race sees first, the login settles with the reason (raceWithAbortSignal).
func TestModelsLoginFinishingAfterTheAbortSettlesWithTheReason(t *testing.T) {
	reason := errors.New("operator interrupt")
	for round := range 200 {
		ctx, cancel := context.WithCancelCause(t.Context())
		release := make(chan struct{})
		models, store, _ := oauthModels(func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
			<-release
			return oauthCredential(), nil
		})
		store.modify = func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
			t.Error("a login aborted before it finished stored its credential")
			return nil, nil
		}
		done := make(chan error, 1)
		go func() { _, err := models.Login(ctx, "p", CredentialOAuth, AuthInteraction{}); done <- err }()
		cancel(reason)
		close(release)
		if err := <-done; !errors.Is(err, reason) {
			t.Fatalf("round %d: Login = %v, want the signal's reason", round, err)
		}
		models.operations.Wait()
	}
}

// models.ts :849-860: a store failure is `Credential store modify failed for <id>` with the failure as its cause, unless the signal aborted, which wins.
func TestModelsLoginStoreFailureIsWrappedAndAnAbortDuringTheModifyWins(t *testing.T) {
	diskFull := errors.New("disk full")
	t.Run("wrapped", func(t *testing.T) {
		models, store, _ := oauthModels(func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
			return oauthCredential(), nil
		})
		store.modify = func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
			return nil, diskFull
		}
		_, err := models.Login(t.Context(), "p", CredentialOAuth, AuthInteraction{})
		var modelsErr *ModelsError
		if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorAuth || !errors.Is(err, diskFull) || modelsErr.Message != "Credential store modify failed for p: disk full" {
			t.Fatalf("error = %v, want an auth ModelsError %q caused by the store failure", err, "Credential store modify failed for p: disk full")
		}
	})
	t.Run("abort after the modify started", func(t *testing.T) {
		reason := errors.New("operator interrupt")
		ctx, cancel := context.WithCancelCause(t.Context())
		models, store, _ := oauthModels(func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
			return oauthCredential(), nil
		})
		store.modify = func(ctx context.Context, _ string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
			if _, err := fn(nil); err != nil {
				return nil, err
			}
			cancel(reason)
			return nil, diskFull
		}
		_, err := models.Login(ctx, "p", CredentialOAuth, AuthInteraction{})
		if !errors.Is(err, reason) || errors.Is(err, diskFull) {
			t.Fatalf("error = %v, want the signal's reason, not the store failure", err)
		}
	})
}

// models.ts :838-839: an abort before the store's modify started rejects at once, without waiting for the store.
func TestModelsLoginAbortBeforeTheModifyStartedDoesNotWaitForTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reason := errors.New("operator interrupt")
		ctx, cancel := context.WithCancelCause(t.Context())
		gate := make(chan struct{})
		entered := make(chan struct{})
		models, store, _ := oauthModels(func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
			return oauthCredential(), nil
		})
		store.modify = func(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
			close(entered)
			<-gate
			return nil, errors.New("store gave up")
		}
		done := make(chan error, 1)
		go func() { _, err := models.Login(ctx, "p", CredentialOAuth, AuthInteraction{}); done <- err }()
		<-entered
		cancel(reason)
		select {
		case err := <-done:
			if !errors.Is(err, reason) {
				t.Fatalf("Login = %v, want the signal's reason", err)
			}
		case <-time.After(time.Minute):
			t.Fatal("Login waited for a store modify that had not started")
		}
		close(gate)
		models.operations.Wait()
	})
}

// models.ts logout (:866-875): `signal.throwIfAborted()` first, the signal reaches the store, a failure is `Credential store delete failed for <id>` with the failure as
// its cause, and an abort wins over that wrap.
func TestModelsLogoutAbortStoreSignalAndFailureWrap(t *testing.T) {
	reason := errors.New("operator interrupt")
	diskFull := errors.New("disk full")
	t.Run("aborted first", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(reason)
		models, store, _ := oauthModels(loginNeverRuns)
		store.remove = func(context.Context, string) error { t.Error("the store was asked after the abort"); return nil }
		if err := models.Logout(ctx, "p"); !errors.Is(err, reason) {
			t.Fatalf("Logout = %v, want the signal's reason", err)
		}
	})
	t.Run("the store receives the signal", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		models, store, _ := oauthModels(loginNeverRuns)
		var given context.Context
		store.remove = func(ctx context.Context, _ string) error { given = ctx; return nil }
		if err := models.Logout(ctx, "p"); err != nil {
			t.Fatal(err)
		}
		cancel(reason)
		if given == nil || given.Err() == nil {
			t.Fatal("the store was not handed the caller's signal")
		}
	})
	t.Run("wrapped", func(t *testing.T) {
		models, store, _ := oauthModels(loginNeverRuns)
		store.remove = func(context.Context, string) error { return diskFull }
		err := models.Logout(t.Context(), "p")
		var modelsErr *ModelsError
		if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorAuth || !errors.Is(err, diskFull) || modelsErr.Message != "Credential store delete failed for p: disk full" {
			t.Fatalf("error = %v, want an auth ModelsError %q caused by the store failure", err, "Credential store delete failed for p: disk full")
		}
	})
	t.Run("abort wins over the wrap", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		models, store, _ := oauthModels(loginNeverRuns)
		store.remove = func(context.Context, string) error { cancel(reason); return diskFull }
		if err := models.Logout(ctx, "p"); !errors.Is(err, reason) || errors.Is(err, diskFull) {
			t.Fatalf("Logout = %v, want the signal's reason, not the store failure", err)
		}
	})
}

// models.ts generateImages (:1021-1038) is one try/catch around auth and generation: the signal reaches the provider, and the failure that follows an abort is an
// `aborted` result, any other failure an `error` result carrying its message. transformHeaders (:914) is awaited with its rejection propagated the same way.
func TestModelsGenerateImagesAbortedSignalReachesTheProviderAndEndsAborted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		model := typedImageModel("p1", "m")
		models := CreateModels()
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p1", Models: []AnyModel{model}, Auth: typedNoAuth(), Images: ProviderImageAPIMap{"test-images": {
			GenerateImages: func(ctx context.Context, _ *ImageModel, _ ImagesContext, _ ImagesOptions) (AssistantImages, error) {
				<-ctx.Done()
				return AssistantImages{}, errors.New("the request failed")
			}}}}))
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan AssistantImages, 1)
		go func() { done <- models.GenerateImages(ctx, model, ImagesContext{}) }()
		synctest.Wait()
		cancel()
		select {
		case result := <-done:
			if result.StopReason != ImagesStopReasonAborted {
				t.Fatalf("result = %+v, want an aborted result", result)
			}
		case <-time.After(time.Minute):
			t.Fatal("the provider never saw the abort")
		}
	})
}

func TestModelsGenerateImagesTransformHeadersIsAwaitedWithTheSignalAndItsRejectionIsTheResult(t *testing.T) {
	model := typedImageModel("p1", "m")
	newModels := func() *Models {
		models := CreateModels()
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p1", Models: []AnyModel{model}, Auth: typedNoAuth(), Images: ProviderImageAPIMap{"test-images": {
			GenerateImages: func(context.Context, *ImageModel, ImagesContext, ImagesOptions) (AssistantImages, error) {
				t.Error("the provider ran after transformHeaders rejected")
				return AssistantImages{}, nil
			}}}}))
		return models
	}
	t.Run("rejection", func(t *testing.T) {
		result := newModels().GenerateImages(t.Context(), model, ImagesContext{}, ModelsImagesOptions{TransformHeaders: func(context.Context, ProviderHeaders) (ProviderHeaders, error) {
			return nil, errors.New("transform refused")
		}})
		if result.StopReason != ImagesStopReasonError || result.ErrorMessage != "transform refused" {
			t.Fatalf("result = %+v, want an error result carrying the rejection", result)
		}
	})
	t.Run("signal", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			entered := make(chan struct{})
			done := make(chan AssistantImages, 1)
			models := newModels()
			go func() {
				done <- models.GenerateImages(ctx, model, ImagesContext{}, ModelsImagesOptions{TransformHeaders: func(ctx context.Context, _ ProviderHeaders) (ProviderHeaders, error) {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				}})
			}()
			<-entered
			cancel()
			select {
			case result := <-done:
				if result.StopReason != ImagesStopReasonAborted {
					t.Fatalf("result = %+v, want an aborted result", result)
				}
			case <-time.After(time.Minute):
				t.Fatal("transformHeaders never saw the abort")
			}
		})
	})
}

// models.ts classify (:1039-1058) is the try/catch of generateImages around auth and classification: the signal reaches the provider, the failure that follows an
// abort is an `aborted` result and any other failure an `error` result, each carrying the failure's message.
func TestModelsClassifyAbortedSignalReachesTheProviderAndEndsAborted(t *testing.T) {
	model := classifierTestModel("p1", "m")
	newModels := func(classify func(context.Context) error) *Models {
		models := CreateModels()
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p1", Models: []AnyModel{model}, Auth: typedNoAuth(), Classifiers: ProviderClassifierMap{"test-classifier": {
			Classify: func(ctx context.Context, _ *ClassifierModel, _ ClassifierContext, _ ClassifierOptions) (ClassifierResult, error) {
				return ClassifierResult{}, classify(ctx)
			}}}}))
		return models
	}
	t.Run("abort", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			models := newModels(func(ctx context.Context) error {
				<-ctx.Done()
				return errors.New("the request failed")
			})
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan ClassifierResult, 1)
			go func() { done <- models.Classify(ctx, model, classifierTestContext()) }()
			synctest.Wait()
			cancel()
			select {
			case result := <-done:
				if result.StopReason != ClassifierStopReasonAborted || result.ErrorMessage != "the request failed" {
					t.Fatalf("result = %+v, want an aborted result carrying the failure", result)
				}
			case <-time.After(time.Minute):
				t.Fatal("the provider never saw the abort")
			}
		})
	})
	t.Run("failure under a live signal", func(t *testing.T) {
		result := newModels(func(context.Context) error { return errors.New("the request failed") }).Classify(t.Context(), model, classifierTestContext())
		if result.StopReason != ClassifierStopReasonError || result.ErrorMessage != "the request failed" {
			t.Fatalf("result = %+v, want an error result carrying the failure", result)
		}
	})
}

// models.ts login (:817): `method.login({ ...interaction, signal }, options)`: the flow gets the caller's options and a signal that follows the caller's.
func TestModelsLoginHandsTheFlowTheCallersOptionsAndSignal(t *testing.T) {
	type flowKey struct{}
	var got LoginOptions
	var value any
	models, _, _ := oauthModels(func(ctx context.Context, _ AuthInteraction, options LoginOptions) (Credential, error) {
		got, value = options, ctx.Value(flowKey{})
		return oauthCredential(), nil
	})
	ctx := context.WithValue(t.Context(), flowKey{}, "the caller's")
	if _, err := models.Login(ctx, "p", CredentialOAuth, AuthInteraction{}, LoginOptions{AgentName: new("agent"), GetDeviceID: func() string { return "device" }}); err != nil {
		t.Fatal(err)
	}
	if got.AgentName == nil || *got.AgentName != "agent" || got.GetDeviceID == nil || got.GetDeviceID() != "device" || value != "the caller's" {
		t.Fatalf("the flow got options %+v and context value %v, want the caller's options and context", got, value)
	}
}

// models.ts getAllAvailable: the operation is raced with the signal, so an abort settles it while a provider's auth check is still pending.
func TestModelsGetAllAvailableAbortSettlesWhileAnAuthCheckIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reason := errors.New("operator interrupt")
		ctx, cancel := context.WithCancelCause(t.Context())
		entered, release := make(chan struct{}), make(chan struct{})
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "slow", auth: &ProviderAuth{APIKey: &APIKeyAuth{Name: "Slow", Check: func(context.Context, APIKeyAuthInput) (*AuthCheck, error) {
			close(entered)
			<-release
			return &AuthCheck{Type: CredentialAPIKey}, nil
		}}}}))
		done := make(chan error, 1)
		go func() { _, err := models.GetAllAvailable(ctx); done <- err }()
		<-entered
		cancel(reason)
		select {
		case err := <-done:
			if !errors.Is(err, reason) {
				t.Fatalf("GetAllAvailable = %v, want the signal's reason", err)
			}
		case <-time.After(time.Minute):
			t.Fatal("GetAllAvailable did not settle on the abort while an auth check was pending")
		}
		close(release)
		models.operations.Wait()
	})
}

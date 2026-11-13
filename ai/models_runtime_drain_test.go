package ai

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Pi's Models.refresh stops waiting when its signal aborts and leaves provider.refreshModels running (models.ts raceWithAbortSignal). Drain is the
// Go owner's end of that work: it cancels the refresh and returns only once the provider's refresh has returned, while a login the caller cancelled
// but whose provider callback is still blocked does not hold it.
func TestModelsDrainJoinsAbandonedRefreshButNotLogin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var refreshSettled atomic.Bool
		refreshStarted, loginStarted, releaseLogin := make(chan struct{}), make(chan struct{}), make(chan struct{})
		models := CreateModels()
		models.SetProvider(&ModelsProvider{
			ID:        "drain",
			Name:      "drain",
			GetModels: func() ([]*Model, error) { return nil, nil },
			RefreshModels: func(refresh RefreshModelsContext) error {
				close(refreshStarted)
				<-refresh.Signal.Done()
				time.Sleep(time.Second) // the write the provider finishes after the abort
				refreshSettled.Store(true)
				return context.Cause(refresh.Signal)
			},
			Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "API key", Login: func(context.Context, AuthInteraction) (Credential, error) {
				close(loginStarted)
				<-releaseLogin
				return Credential{Type: CredentialAPIKey, Key: "k"}, nil
			}}},
		})
		refreshCtx, cancelRefresh := context.WithCancel(t.Context())
		refreshed := make(chan ModelsRefreshResult, 1)
		go func() { refreshed <- models.Refresh(refreshCtx) }()
		<-refreshStarted
		loginCtx, cancelLogin := context.WithCancel(t.Context())
		logged := make(chan error, 1)
		go func() {
			_, err := models.Login(loginCtx, "drain", CredentialAPIKey, AuthInteraction{})
			logged <- err
		}()
		<-loginStarted
		cancelRefresh()
		cancelLogin()
		if result := <-refreshed; !result.Aborted {
			t.Fatalf("refresh after cancel = %+v, want aborted", result)
		}
		if err := <-logged; err == nil {
			t.Fatal("cancelled login succeeded")
		}

		models.Drain()
		if !refreshSettled.Load() {
			t.Error("Drain returned while the abandoned provider refresh was still running")
		}
		close(releaseLogin)
		models.Close()
	})
}

func drainStarted(models *Models) <-chan struct{} {
	drained := make(chan struct{})
	go func() { models.Drain(); close(drained) }()
	return drained
}

func isClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// A stream's request setup resolves auth under the stream's context and its callbacks end with that context, like a login's, so Drain does not wait for them.
func TestModelsDrainDoesNotWaitForAStreamsAuthCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: &APIKeyAuth{Name: "blocking", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
			close(started)
			<-release
			return &AuthResult{Auth: ModelAuth{APIKey: "key"}}, nil
		}}}}))
		streamCtx, cancelStream := context.WithCancel(t.Context())
		streamed := make(chan struct{})
		go func() {
			models.CompleteSimple(streamCtx, modelsRuntimeModel("p1", "model-a"), modelsRuntimeContext())
			close(streamed)
		}()
		<-started

		drained := drainStarted(models)
		synctest.Wait()
		if !isClosed(drained) {
			t.Error("Drain waited for a stream's blocked auth callback")
		}
		cancelStream()
		close(release)
		<-streamed
		models.Close()
		<-drained
	})
}

// The credential a cancelled stream's auth resolution was refreshing is persisted by work that outlives the stream; the owner that removes the credential
// store's directory after Drain needs that write joined.
func TestModelsDrainJoinsTheCredentialWriteOfACancelledStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		modelsRuntimePut(t, credentials, "p1", Credential{Type: CredentialOAuth, Access: "old", Refresh: "old-refresh", Expires: 0})
		refreshStarted, releaseRefresh := make(chan struct{}), make(chan struct{})
		oauth := modelsRuntimeOAuth()
		oauth.Refresh = func(_ context.Context, credential Credential) (Credential, error) {
			close(refreshStarted)
			<-releaseRefresh
			credential.Access, credential.Refresh = "new", "new-refresh"
			credential.Expires = time.Now().Add(time.Minute).UnixMilli()
			return credential, nil
		}
		models := CreateModels(CreateModelsOptions{Credentials: credentials})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{OAuth: oauth}}))
		streamCtx, cancelStream := context.WithCancel(t.Context())
		streamed := make(chan struct{})
		go func() {
			models.CompleteSimple(streamCtx, modelsRuntimeModel("p1", "model-a"), modelsRuntimeContext())
			close(streamed)
		}()
		<-refreshStarted
		cancelStream()
		<-streamed

		drained := drainStarted(models)
		synctest.Wait()
		if isClosed(drained) {
			t.Error("Drain returned while the credential write a cancelled stream started was still running")
		}
		close(releaseRefresh)
		synctest.Wait()
		if !isClosed(drained) {
			t.Error("Drain did not return once the credential write finished")
		}
		got, err := credentials.Read(t.Context(), "p1")
		if err != nil || got == nil || got.Refresh != "new-refresh" {
			t.Errorf("credential=%+v err=%v; want the rotated refresh token written before Drain returned", got, err)
		}
		<-drained
		models.Close()
	})
}

// Drain has begun when it starts waiting: a refresh that starts while it waits for another provider's work starts no provider work, so the wait cannot grow
// and no worker is added to the group Drain is waiting on.
func TestModelsRefreshStartsNoProviderWorkOnceDrainBegan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lateCalls atomic.Int32
		firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "first", refreshModels: func(RefreshModelsContext) error {
			close(firstStarted)
			<-releaseFirst // provider work that ignores its aborted signal
			return nil
		}}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "late", refreshModels: func(RefreshModelsContext) error { lateCalls.Add(1); return nil }}))
		first := make(chan ModelsRefreshResult, 1)
		go func() { first <- models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"first"}}) }()
		<-firstStarted

		drained := drainStarted(models)
		synctest.Wait()
		models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"late"}})
		synctest.Wait()
		if n := lateCalls.Load(); n != 0 {
			t.Errorf("a refresh that began after Drain started ran %d provider refreshes", n)
		}
		if isClosed(drained) {
			t.Error("Drain returned while the first provider's work was still running")
		}
		close(releaseFirst)
		<-first
		<-drained
		models.Close()
	})
}

// A refresh loop with providers queued behind the one it is running: Drain begins between them, so the queued providers are never started.
func TestModelsRefreshLoopStartsNoQueuedProviderAfterDrainBegan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var queuedCalls atomic.Int32
		firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "first", refreshModels: func(RefreshModelsContext) error {
			close(firstStarted)
			<-releaseFirst
			return nil
		}}))
		for _, id := range []string{"second", "third"} {
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: id, refreshModels: func(RefreshModelsContext) error { queuedCalls.Add(1); return nil }}))
		}
		// An offline refresh on the caller runs its providers one after another on the loop, so the first provider holds the loop while the others are queued.
		ctx := WithFileReadSnapshotOnCaller(t.Context())
		refreshed := make(chan ModelsRefreshResult, 1)
		go func() { refreshed <- models.Refresh(ctx, ModelsRefreshOptions{AllowNetwork: new(false)}) }()
		<-firstStarted

		drained := drainStarted(models)
		synctest.Wait()
		if !isClosed(drained) {
			t.Fatal("Drain waited for a provider the collection runs on its caller")
		}
		close(releaseFirst)
		<-refreshed
		if n := queuedCalls.Load(); n != 0 {
			t.Errorf("the refresh loop started %d queued providers after Drain began", n)
		}
		models.Close()
	})
}

// A publication from a refresh that already settled, made after Drain began, writes nothing: its write would outlive the Drain that joined the collection's work.
func TestModelsPublicationAfterDrainWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var publish func(ModelsPublication) (bool, error)
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error { publish = refresh.Publish; return nil }}))
		if result := models.Refresh(t.Context(), ModelsRefreshOptions{AllowNetwork: new(false)}); len(result.Errors) != 0 || publish == nil {
			t.Fatalf("result=%+v", result)
		}
		models.Drain()
		accepted, _ := publish(ModelsPublication{Persist: &ModelsStoreEntry{}})
		models.Close()
		if stored, err := models.modelsStore.Read(t.Context(), "dynamic"); accepted || err != nil || stored != nil {
			t.Errorf("accepted=%v stored=%+v err=%v; want no publication after Drain", accepted, stored, err)
		}
	})
}

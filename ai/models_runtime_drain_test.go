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

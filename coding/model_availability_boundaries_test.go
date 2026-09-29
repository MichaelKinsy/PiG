package coding

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func requireAvailabilityErrorIdentity(t *testing.T, got, want error) {
	t.Helper()
	//nolint:errorlint // Upstream rethrows the same rejection; accepting wrappers would weaken this identity guard.
	if got != want {
		t.Fatalf("error identity=%v want %v", got, want)
	}
}

func TestAvailabilityCancellationPreservesLastErrorAndJoins(t *testing.T) {
	session, _ := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	original := runtime.availability.getAvailable
	failure := errors.New("current availability failure")
	runtime.availability.getAvailable = func(context.Context, string) ([]*ai.Model, error) { return nil, failure }
	_, err := runtime.GetAvailable(t.Context(), "")
	requireAvailabilityErrorIdentity(t, err, failure)
	if got := runtime.GetError(); got != "Availability refresh: "+failure.Error() {
		t.Fatalf("error=%q", got)
	}
	runtime.availability.getAvailable = original
	stalled := stallAvailabilityCredentials(t, runtime)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := runtime.GetAvailable(ctx, ""); done <- err }()
	waitAvailabilityStarted(t, stalled.started)
	// This read is the synchronous registry/UI boundary: it must never join pending credential I/O.
	_ = runtime.GetAvailableSnapshot()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled availability work did not drain")
	}
	if got := runtime.GetError(); got != "Availability refresh: "+failure.Error() {
		t.Fatalf("cancellation replaced error: %q", got)
	}
	if _, err := runtime.GetAvailable(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if runtime.GetError() != "" {
		t.Fatalf("recovery did not clear error: %q", runtime.GetError())
	}
}

func TestAvailabilityProviderRefreshRetainsOtherCredentialState(t *testing.T) {
	session, services := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	if err := services.Auth().Set("other", ai.Credential{Type: ai.CredentialAPIKey, Key: "other-key"}); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if err := services.Auth().Set("selected", ai.Credential{Type: ai.CredentialAPIKey, Key: "selected-key"}); err != nil {
		t.Fatal(err)
	}
	original := runtime.availability.getAvailable
	var calls atomic.Int32
	runtime.availability.getAvailable = func(ctx context.Context, provider string) ([]*ai.Model, error) {
		calls.Add(1)
		if provider != "selected" {
			t.Errorf("provider=%q", provider)
		}
		return original(ctx, provider)
	}
	result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{"selected", "selected"}})
	if result.Aborted || len(result.Errors) != 0 || calls.Load() != 1 {
		t.Fatalf("refresh=%+v calls=%d", result, calls.Load())
	}
	requireAvailabilityAuth(t, runtime, "other", ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
	requireAvailabilityAuth(t, runtime, "selected", ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
	runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{}})
	if calls.Load() != 1 {
		t.Fatal("empty provider selection became full refresh")
	}
}

func TestAvailabilityNewProviderErrorSurvivesOlderFullSuccess(t *testing.T) {
	session, _ := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	failure := errors.New("new provider failure")
	original := runtime.availability.getAvailable
	runtime.availability.getAvailable = func(ctx context.Context, provider string) ([]*ai.Model, error) {
		if provider != "" {
			return nil, failure
		}
		return original(ctx, provider)
	}
	stalled := stallAvailabilityCredentials(t, runtime)
	old := startAvailability(t, runtime, "")
	defer func() { stalled.release(nil); <-old }()
	waitAvailabilityStarted(t, stalled.started)
	_, err := runtime.GetAvailable(t.Context(), "selected")
	requireAvailabilityErrorIdentity(t, err, failure)
	stalled.release(nil)
	if err := <-old; err != nil {
		t.Fatal(err)
	}
	if runtime.GetError() != "Availability refresh: "+failure.Error() {
		t.Fatalf("old success cleared new error: %q", runtime.GetError())
	}
}

func TestAvailabilityLatestProviderFailureIsVisible(t *testing.T) {
	session, _ := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	failure := errors.New("provider availability failed")
	runtime.availability.getAvailable = func(context.Context, string) ([]*ai.Model, error) { return nil, failure }
	result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{"selected"}})
	requireAvailabilityErrorIdentity(t, result.Errors["selected"], failure)
	if !strings.Contains(runtime.GetError(), failure.Error()) {
		t.Fatalf("result=%+v error=%q", result, runtime.GetError())
	}
}

package ai

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Pi auth/resolve.ts:refreshStoredOAuthCredential gives the provider AbortSignal.timeout(15000) alone: the signal outlives the callback, on success or rejection, and the caller's cancellation never reaches it.
func TestOAuthRefreshSignalSurvivesSettlement(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				failure := errors.New("refresh rejected")
				var signal context.Context
				oauth := &OAuthAuth{Refresh: func(ctx context.Context, c Credential) (Credential, error) {
					signal = ctx
					if fail {
						return Credential{}, failure
					}
					return c, nil
				}}
				_, err := refreshOAuthWithTimeout(ctx, oauth, Credential{})
				if fail && !errors.Is(err, failure) || !fail && err != nil {
					t.Fatalf("refresh error = %v", err)
				}
				if signal.Err() != nil {
					t.Errorf("settlement canceled retained signal: %v", context.Cause(signal))
				}
				cancel(errors.New("later caller cancellation"))
				if signal.Err() != nil {
					t.Fatalf("caller cancellation reached the retained signal: %v", context.Cause(signal))
				}
			})
		})
	}
}

func TestOAuthRefreshSignalKeepsOriginalTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var signal context.Context
		start := time.Now()
		oauth := &OAuthAuth{Refresh: func(ctx context.Context, c Credential) (Credential, error) {
			signal = ctx
			return c, nil
		}}
		if _, err := refreshOAuthWithTimeout(t.Context(), oauth, Credential{}); err != nil {
			t.Fatal(err)
		}
		if deadline, ok := signal.Deadline(); !ok || !deadline.Equal(start.Add(defaultOAuthRefreshTimeout)) {
			t.Errorf("deadline = %v, %t", deadline, ok)
		}
		if signal.Err() != nil {
			t.Fatalf("completed refresh signal already canceled: %v", signal.Err())
		}
		<-signal.Done()
		if elapsed := time.Since(start); elapsed != defaultOAuthRefreshTimeout {
			t.Errorf("timeout after %s, want %s", elapsed, defaultOAuthRefreshTimeout)
		}
		if !errors.Is(signal.Err(), context.DeadlineExceeded) || !errors.Is(context.Cause(signal), context.DeadlineExceeded) {
			t.Errorf("timeout = %v, cause = %v", signal.Err(), context.Cause(signal))
		}
	})
}

// The caller's deadline bounds the wait for the lock, not the refresh: the callback's signal ends at the fifteen-second timeout.
func TestOAuthRefreshSignalIgnoresCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		start := time.Now()
		oauth := &OAuthAuth{Refresh: func(ctx context.Context, c Credential) (Credential, error) {
			if got, ok := ctx.Deadline(); !ok || !got.Equal(start.Add(defaultOAuthRefreshTimeout)) {
				t.Errorf("callback deadline = %v, %t; want the %s timeout", got, ok, defaultOAuthRefreshTimeout)
			}
			time.Sleep(2 * time.Second)
			if ctx.Err() != nil {
				t.Errorf("callback signal ended with the caller's deadline: %v", ctx.Err())
			}
			return c, nil
		}}
		if _, err := refreshOAuthWithTimeout(ctx, oauth, Credential{}); err != nil {
			t.Fatalf("refresh outliving the caller's deadline = %v", err)
		}
	})
}

func TestOAuthRefreshSignalCancelsActiveCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		oauth := &OAuthAuth{Refresh: func(ctx context.Context, _ Credential) (Credential, error) {
			<-ctx.Done()
			return Credential{}, context.Cause(ctx)
		}}
		start := time.Now()
		_, err := refreshOAuthWithTimeout(t.Context(), oauth, Credential{})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != defaultOAuthRefreshTimeout {
			t.Fatalf("active timeout = %v after %s", err, time.Since(start))
		}
	})
}

// Pi builds the refresh signal as AbortSignal.timeout(15_000) alone for a stored credential and for Models.refresh (auth/resolve.ts:refreshStoredOAuthCredential, models.ts). A provider that hands its callback a signal reads that from the refresh context.
func TestOAuthRefreshTimeoutMarksStoredResolutionAndModelsRefresh(t *testing.T) {
	stored := Credential{Type: CredentialOAuth, Access: "old", Refresh: "r"}
	stored.SetExpiresMillis(1)
	type observed struct {
		called  bool
		value   any
		timeout time.Duration
		marked  bool
	}
	type key struct{}
	observe := func(seen *observed) *ModelsProvider {
		return &ModelsProvider{
			ID:   "p",
			Name: "p",
			Auth: ProviderAuth{OAuth: &OAuthAuth{
				Name: "o",
				Refresh: func(ctx context.Context, credential Credential) (Credential, error) {
					seen.called = true
					seen.value = ctx.Value(key{})
					seen.timeout, seen.marked = OAuthRefreshTimeout(ctx)
					next := cloneCredential(credential)
					next.SetExpiresMillis(float64(nowMillis()) + 3_600_000)
					return next, nil
				},
				ToAuth: func(credential Credential) (ModelAuth, error) { return ModelAuth{APIKey: credential.Access}, nil },
			}},
			GetModels:     func() ([]*Model, error) { return nil, nil },
			RefreshModels: func(RefreshModelsContext) error { return nil },
		}
	}
	caller := context.WithValue(t.Context(), key{}, "caller")

	var resolved observed
	if _, err := expiryModels(t, stored, observe(&resolved)).GetAuth(caller, "p", AuthResolutionOverrides{}); err != nil {
		t.Fatal(err)
	}
	if !resolved.called || !resolved.marked || resolved.timeout != 15*time.Second || resolved.value != "caller" {
		t.Errorf("stored resolution refresh context: timeout %v, marked %v, caller value %v", resolved.timeout, resolved.marked, resolved.value)
	}

	var refreshed observed
	if result := expiryModels(t, stored, observe(&refreshed)).Refresh(caller); len(result.Errors) != 0 {
		t.Fatalf("Refresh errors: %v", result.Errors)
	}
	if !refreshed.called || !refreshed.marked || refreshed.timeout != 15*time.Second {
		t.Errorf("Models.refresh refresh context: called %v, timeout %v, marked %v", refreshed.called, refreshed.timeout, refreshed.marked)
	}
}

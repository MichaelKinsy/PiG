package ai

import (
	"context"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

// RP-007: a provider may retain only Done, not its Context wrapper. Pi auth/resolve.ts:149-153 leaves that observer pending until caller cancellation or the original 15-second timeout.
func TestOAuthRefreshDoneSurvivesGC(t *testing.T) {
	for _, viaModels := range []bool{false, true} {
		name := "helper"
		if viaModels {
			name = "Models.GetAuth"
		}
		t.Run(name, func(t *testing.T) {
			var done <-chan struct{}
			oauth := &OAuthAuth{Name: "review", Refresh: func(ctx context.Context, credential Credential) (Credential, error) {
				done = ctx.Done()
				credential.Access = "new"
				credential.Expires = time.Now().Add(10 * time.Minute).UnixMilli()
				return credential, nil
			}, ToAuth: func(credential Credential) (ModelAuth, error) {
				return ModelAuth{APIKey: credential.Access}, nil
			}}
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			if viaModels {
				store := NewInMemoryAuthStorage(map[string]Credential{"review-oauth": {Type: CredentialOAuth, Access: "old", Refresh: "refresh"}})
				models := CreateModels(CreateModelsOptions{Credentials: store})
				defer models.Close()
				models.SetProvider(&ModelsProvider{ID: "review-oauth", Name: "review", Auth: ProviderAuth{OAuth: oauth}})
				result, err := models.GetAuth(parent, "review-oauth")
				if err != nil || result == nil || result.Auth.APIKey != "new" {
					t.Fatalf("GetAuth=%+v, %v", result, err)
				}
			} else if _, err := refreshOAuthWithTimeout(parent, oauth, Credential{}); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			runtime.GC()
			// This is a negative observation window for asynchronous GC cleanup, not a retry or a wait for refresh completion. It must remain shorter than Pi's original timeout.
			select {
			case <-done:
				t.Fatalf("retained Done closed after %v; caller error=%v, refresh timeout=%v", time.Since(start), parent.Err(), defaultOAuthRefreshTimeout)
			case <-time.After(200 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			default:
				t.Fatal("retained Done did not follow caller cancellation")
			}
		})
	}
}

func TestOAuthRefreshDoneKeepsOriginalTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var done <-chan struct{}
		start := time.Now()
		oauth := &OAuthAuth{Refresh: func(ctx context.Context, credential Credential) (Credential, error) {
			done = ctx.Done()
			return credential, nil
		}}
		if _, err := refreshOAuthWithTimeout(t.Context(), oauth, Credential{}); err != nil {
			t.Fatal(err)
		}
		<-done
		if elapsed := time.Since(start); elapsed != defaultOAuthRefreshTimeout {
			t.Fatalf("Done closed after %s; want %s", elapsed, defaultOAuthRefreshTimeout)
		}
	})
}

func BenchmarkOAuthRefreshSignalLifetime(b *testing.B) {
	oauth := &OAuthAuth{Refresh: func(_ context.Context, credential Credential) (Credential, error) {
		return credential, nil
	}}
	b.ReportAllocs()
	for b.Loop() {
		parent, cancel := context.WithCancel(b.Context())
		_, err := refreshOAuthWithTimeout(parent, oauth, Credential{})
		cancel()
		if err != nil {
			b.Fatal(err)
		}
	}
}

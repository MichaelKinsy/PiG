package ai

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestModelsRuntimeCredentialsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:110
	t.Run("enumerates credential metadata without exposing secrets", func(t *testing.T) {
		credentials := NewInMemoryCredentialStore()
		if _, err := credentials.Modify(t.Context(), "api-provider", func(*Credential) (*Credential, error) { return &Credential{Type: CredentialAPIKey, Key: "secret"}, nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := credentials.Modify(t.Context(), "oauth-provider", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}, nil
		}); err != nil {
			t.Fatal(err)
		}
		got, err := credentials.List(t.Context())
		want := []CredentialInfo{{ProviderID: "api-provider", Type: CredentialAPIKey}, {ProviderID: "oauth-provider", Type: CredentialOAuth}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("metadata=%v, err=%v", got, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:704
	t.Run("cancels queued credential mutations without running them later", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			credentials := NewInMemoryCredentialStore()
			started, release := make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			first := make(chan error, 1)
			go func() {
				_, err := credentials.Modify(t.Context(), "p1", func(*Credential) (*Credential, error) {
					close(started)
					<-release
					return &Credential{Type: CredentialAPIKey, Key: "first"}, nil
				})
				first <- err
			}()
			<-started
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var secondRan atomic.Bool
			second := make(chan error, 1)
			go func() {
				_, err := credentials.Modify(ctx, "p1", func(*Credential) (*Credential, error) {
					secondRan.Store(true)
					return &Credential{Type: CredentialAPIKey, Key: "second"}, nil
				})
				second <- err
			}()
			synctest.Wait()
			cancel()
			if err := <-second; !errors.Is(err, context.Canceled) {
				t.Fatalf("second error=%v", err)
			}
			released = true
			close(release)
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			credentials.operations.Wait()
			got, err := credentials.Read(t.Context(), "p1")
			if secondRan.Load() || err != nil || !reflect.DeepEqual(got, &Credential{Type: CredentialAPIKey, Key: "first"}) {
				t.Fatalf("second ran=%v credential=%+v err=%v", secondRan.Load(), got, err)
			}
		})
	})
}

func TestInMemoryCredentialStoreLiteralOrderAndActiveCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewInMemoryCredentialStore()
		for _, id := range []string{"z", "a"} {
			if _, err := store.Modify(t.Context(), id, func(*Credential) (*Credential, error) {
				return &Credential{Type: CredentialAPIKey, Key: "!printf should-not-run"}, nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		list, err := store.List(t.Context())
		if err != nil || len(list) != 2 || list[0].ProviderID != "z" || list[1].ProviderID != "a" {
			t.Fatalf("list=%v err=%v", list, err)
		}
		old, err := store.Read(t.Context(), "z")
		if err != nil || old.Key != "!printf should-not-run" {
			t.Fatalf("literal=%v err=%v", old, err)
		}
		started, release := make(chan struct{}), make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := store.Modify(ctx, "z", func(*Credential) (*Credential, error) {
				close(started)
				<-release
				return &Credential{Type: CredentialAPIKey, Key: "late"}, nil
			})
			done <- err
		}()
		<-started
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
		released = true
		close(release)
		store.operations.Wait()
		got, err := store.Read(t.Context(), "z")
		if err != nil || !reflect.DeepEqual(got, old) {
			t.Fatalf("late write=%v err=%v", got, err)
		}
	})
}

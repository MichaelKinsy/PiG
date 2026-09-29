package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

// Pi's auth-storage.ts:309-326 chains every asynchronous operation, including reads and deletes, while each caller independently races cancellation.
func TestInMemoryAuthStorageCancelledQueueOperations(t *testing.T) {
	for _, kind := range []string{"read", "list", "modify", "delete"} {
		for _, preAborted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preAborted=%t", kind, preAborted), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					stored := Credential{Type: CredentialAPIKey, Key: "stored"}
					store := NewInMemoryAuthStorage(map[string]Credential{"provider": stored})
					started, release := make(chan struct{}), make(chan struct{})
					first := make(chan error, 1)
					go func() {
						_, err := store.Modify(t.Context(), "other", func(*Credential) (*Credential, error) {
							close(started)
							<-release
							return nil, nil
						})
						first <- err
					}()
					<-started
					ctx, cancel := context.WithCancelCause(t.Context())
					defer cancel(nil)
					cause := errors.New("request canceled")
					if preAborted {
						cancel(cause)
					}
					called := false
					result := make(chan error, 1)
					go func() {
						var err error
						switch kind {
						case "read":
							_, err = store.Read(ctx, "provider")
						case "list":
							_, err = store.List(ctx)
						case "modify":
							_, err = store.Modify(ctx, "provider", func(*Credential) (*Credential, error) {
								called = true
								return &Credential{Type: CredentialAPIKey, Key: "canceled"}, nil
							})
						case "delete":
							err = store.Delete(ctx, "provider")
						}
						result <- err
					}()
					synctest.Wait()
					cancel(cause)
					synctest.Wait()
					select {
					case err := <-result:
						if !errors.Is(err, cause) {
							t.Errorf("cancellation = %v, want %v", err, cause)
						}
					default:
						t.Error("canceled caller remained blocked behind the active mutation")
					}
					close(release)
					if err := <-first; err != nil {
						t.Fatal(err)
					}
					memoryAuthRead(t, store, "provider", &stored)
					synctest.Wait()
					if called {
						t.Error("canceled queued callback ran")
					}
					store.mu.Lock()
					retained := store.pending != nil
					store.mu.Unlock()
					if retained {
						t.Error("idle store retained completed operation closures")
					}
				})
			})
		}
	}
}

func TestInMemoryAuthStorageQueueOrderAfterFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewInMemoryAuthStorage(nil)
		started, release := make(chan struct{}), make(chan struct{})
		failure := errors.New("callback failed")
		first := make(chan error, 1)
		go func() {
			_, err := store.Modify(t.Context(), "blocked", func(*Credential) (*Credential, error) {
				close(started)
				<-release
				return nil, failure
			})
			first <- err
		}()
		<-started
		// A representative FIFO backlog across provider keys. Expectations derive from these supplied operations, not a generated count.
		const requests = 128
		results := make([]chan error, requests)
		var calls, want []int
		for i := range requests {
			want = append(want, i)
			results[i] = make(chan error, 1)
			go func() {
				_, err := store.Modify(t.Context(), fmt.Sprintf("provider-%d", i), func(*Credential) (*Credential, error) {
					calls = append(calls, i)
					return &Credential{Type: CredentialAPIKey, Key: fmt.Sprint(i)}, nil
				})
				results[i] <- err
			}()
			synctest.Wait()
		}
		close(release)
		if err := <-first; !errors.Is(err, failure) {
			t.Errorf("callback failure = %v", err)
		}
		for _, result := range results {
			if err := <-result; err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("callback order = %v, want %v", calls, want)
		}
		if err := store.Delete(t.Context(), "provider-0"); err != nil {
			t.Fatal(err)
		}
		memoryAuthRead(t, store, "provider-0", nil)
		memoryAuthRead(t, store, "provider-127", &Credential{Type: CredentialAPIKey, Key: "127"})
	})
}

// The real request-auth boundary must stop waiting on a canceled refresh without allowing the still-active callback to overwrite the stored credential.
func TestResolveProviderAuthCancelledMemoryRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		previous := Credential{Type: CredentialOAuth, Access: "expired", Refresh: "refresh-token"}
		store := NewInMemoryAuthStorage(map[string]Credential{"oauth": previous})
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		started, release := make(chan struct{}), make(chan struct{})
		auth := ProviderAuth{OAuth: &OAuthAuth{
			Name: "OAuth",
			Refresh: func(context.Context, Credential) (Credential, error) {
				close(started)
				<-release
				return Credential{Type: CredentialOAuth, Access: "refreshed", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
			},
			ToAuth: func(c Credential) (ModelAuth, error) { return ModelAuth{APIKey: c.Access}, nil },
		}}
		result := make(chan error, 1)
		go func() {
			_, err := ResolveProviderAuth(ctx, "oauth", auth, store, testAuthContext(nil), AuthResolutionOverrides{})
			result <- err
		}()
		<-started
		cause := errors.New("request replaced")
		cancel(cause)
		synctest.Wait()
		select {
		case err := <-result:
			if !errors.Is(err, cause) {
				t.Errorf("request auth error = %v, want cancellation cause", err)
			}
		default:
			t.Error("request auth retained its canceled caller")
		}
		close(release)
		memoryAuthRead(t, store, "oauth", &previous)
	})
}

// Upstream packages/coding-agent/test/auth-storage.test.ts:448-529 requires an unsuccessful refresh to preserve the stored credential and permit retry. The provider may mutate nested metadata before rejecting.
func TestResolveProviderAuthRejectedRefreshPreservesCredentialMetadata(t *testing.T) {
	previous := isolationCredential()
	store := NewInMemoryAuthStorage(map[string]Credential{"oauth-provider": previous})
	failure := errors.New("refresh failed after updating metadata")
	refreshes, derivations := 0, 0
	auth := ProviderAuth{OAuth: &OAuthAuth{
		Name: "OAuth",
		Refresh: func(_ context.Context, credential Credential) (Credential, error) {
			refreshes++
			if !reflect.DeepEqual(credential, previous) {
				t.Errorf("refresh input = %#v, want original metadata %#v", credential, previous)
			}
			credential.GatewayConfig[11] = '9'
			credential.Extra["custom"][9] = '9'
			if refreshes == 1 {
				return credential, failure
			}
			credential.Access = "refreshed-access"
			credential.Expires = time.Now().Add(time.Hour).UnixMilli()
			return credential, nil
		},
		ToAuth: func(credential Credential) (ModelAuth, error) {
			derivations++
			return ModelAuth{APIKey: credential.Access}, nil
		},
	}}
	result, err := ResolveProviderAuth(t.Context(), "oauth-provider", auth, store, testAuthContext(nil), AuthResolutionOverrides{})
	modelErr, ok := errors.AsType[*ModelsError](err)
	if result != nil || !ok || modelErr.Code != ModelsErrorOAuth || !errors.Is(err, failure) {
		t.Fatalf("failed auth = %#v, %v; want OAuth error with refresh cause", result, err)
	}
	if derivations != 0 {
		t.Fatal("derived auth from a rejected refresh")
	}
	memoryAuthRead(t, store, "oauth-provider", new(isolationCredential()))
	result, err = ResolveProviderAuth(t.Context(), "oauth-provider", auth, store, testAuthContext(nil), AuthResolutionOverrides{})
	if err != nil || result == nil || result.Auth.APIKey != "refreshed-access" || result.Source != "OAuth" {
		t.Fatalf("retried auth = %#v, %v", result, err)
	}
	stored, err := store.Read(t.Context(), "oauth-provider")
	if err != nil || stored == nil {
		t.Fatalf("Read = %#v, %v", stored, err)
	}
	if refreshes != 2 || derivations != 1 || stored.Access != "refreshed-access" || string(stored.GatewayConfig) != `{"version":9,"models":[]}` || string(stored.Extra["custom"]) != `{"value":9}` {
		t.Fatalf("committed auth = %#v; refreshes=%d derivations=%d", stored, refreshes, derivations)
	}
}

func BenchmarkInMemoryAuthStorageCredentialMetadata(b *testing.B) {
	for _, count := range []int{1, 256} {
		b.Run(fmt.Sprintf("models=%d", count), func(b *testing.B) {
			models := make([]map[string]string, count)
			for i := range models {
				models[i] = map[string]string{"id": fmt.Sprintf("model-%d", i), "name": fmt.Sprintf("Model %d", i)}
			}
			gateway, err := json.Marshal(map[string]any{"models": models})
			if err != nil {
				b.Fatal(err)
			}
			credential := isolationCredential()
			credential.GatewayConfig = gateway
			store := NewInMemoryAuthStorage(map[string]Credential{"oauth": credential})
			b.ReportAllocs()
			for b.Loop() {
				if _, err := store.Read(b.Context(), "oauth"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInMemoryAuthStorageRequestAuth(b *testing.B) {
	store := NewInMemoryAuthStorage(map[string]Credential{
		"oauth": {Type: CredentialOAuth, Access: "expired", Refresh: "refresh-token"},
	})
	auth := ProviderAuth{OAuth: &OAuthAuth{
		Name: "OAuth",
		Refresh: func(_ context.Context, credential Credential) (Credential, error) {
			credential.Access = "refreshed"
			return credential, nil
		},
		ToAuth: func(c Credential) (ModelAuth, error) { return ModelAuth{APIKey: c.Access}, nil },
	}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ResolveProviderAuth(b.Context(), "oauth", auth, store, testAuthContext(nil), AuthResolutionOverrides{}); err != nil {
			b.Fatal(err)
		}
	}
}

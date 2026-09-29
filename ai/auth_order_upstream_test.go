package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

// Ports packages/coding-agent/test/auth-storage.test.ts:195. The external Google credential keeps its position after deletion, as Object.entries does in auth-storage.ts:485-488.
func TestAuthStorageDeletePreservesExternalCredentialOrderUpstream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuthFixture(t, path, `{"anthropic":{"type":"api_key","key":"anthropic-key"},"openai":{"type":"api_key","key":"openai-key"}}`)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	writeAuthFixture(t, path, `{"anthropic":{"type":"api_key","key":"anthropic-key"},"openai":{"type":"api_key","key":"openai-key"},"google":{"type":"api_key","key":"external-key"}}`)
	if err := store.Delete(t.Context(), "anthropic"); err != nil {
		t.Fatal(err)
	}
	infos, err := store.List(t.Context())
	want := []CredentialInfo{{ProviderID: "openai", Type: CredentialAPIKey}, {ProviderID: "google", Type: CredentialAPIKey}}
	if err != nil || !reflect.DeepEqual(infos, want) {
		t.Fatalf("list=%v,%v want=%v", infos, err, want)
	}
	for _, tc := range []struct {
		id   string
		want *Credential
	}{
		{"anthropic", nil},
		{"openai", &Credential{Type: CredentialAPIKey, Key: "openai-key"}},
		{"google", &Credential{Type: CredentialAPIKey, Key: "external-key"}},
	} {
		got, err := store.Read(t.Context(), tc.id)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("read(%s)=%#v,%v want=%#v", tc.id, got, err, tc.want)
		}
	}
}

func requireCredentialOrder(t *testing.T, store CredentialStore, want ...string) {
	t.Helper()
	infos, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(infos))
	for _, info := range infos {
		got = append(got, info.ProviderID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("credential order=%q want=%q", got, want)
	}
}

func setOrderedCredential(t *testing.T, store CredentialStore, id string) {
	t.Helper()
	if _, err := store.Modify(t.Context(), id, func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: id + "-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialStorageObjectEnumeration(t *testing.T) {
	// Pi uses JSON.parse and Object.entries: integer indices sort numerically, other keys retain first insertion, and duplicate keys replace the value without moving the key.
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", `{}`, nil},
		{"ordinary", `{"z":{"type":"api_key"},"a":{"type":"oauth","access":"a","refresh":"r","expires":1}}`, []string{"z", "a"}},
		{"duplicate", `{"z":{"type":"api_key","key":"old"},"a":{"type":"api_key"},"z":{"type":"api_key","key":"new"}}`, []string{"z", "a"}},
		{"indices", `{"z":{"type":"api_key"},"10":{"type":"api_key"},"2":{"type":"api_key"},"01":{"type":"api_key"},"4294967295":{"type":"api_key"},"0":{"type":"api_key"},"4294967294":{"type":"api_key"},"a":{"type":"api_key"}}`, []string{"0", "2", "10", "4294967294", "z", "01", "4294967295", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			writeAuthFixture(t, path, tc.raw)
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range []struct {
				name  string
				store CredentialStore
			}{{"file", store}, {"readonly", NewReadOnlyAuthStorage(path)}} {
				t.Run(entry.name, func(t *testing.T) {
					storage := entry.store
					requireCredentialOrder(t, storage, tc.want...)
					requireCredentialOrder(t, storage, tc.want...) // cached reads retain order too
					if tc.name == "duplicate" {
						cred, err := storage.Read(t.Context(), "z")
						if err != nil || cred == nil || cred.Key != "new" {
							t.Fatalf("duplicate value=%+v,%v", cred, err)
						}
					}
				})
			}
			if err := store.Delete(t.Context(), "missing"); err != nil {
				t.Fatal(err)
			}
			// A fresh reader proves the persisted order, not only the writer's cache.
			requireCredentialOrder(t, NewReadOnlyAuthStorage(path), tc.want...)
		})
	}
}

func TestCredentialStorageMutationOrder(t *testing.T) {
	for _, kind := range []string{"file", "memory"} {
		t.Run(kind, func(t *testing.T) {
			var store CredentialStore = NewInMemoryAuthStorage(nil)
			path := filepath.Join(t.TempDir(), "auth.json")
			if kind == "file" {
				var err error
				store, err = NewAuthStorage(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"z", "a", "10", "2", "01"} {
				setOrderedCredential(t, store, id)
			}
			want := []string{"2", "10", "z", "a", "01"}
			requireCredentialOrder(t, store, want...)
			setOrderedCredential(t, store, "z")
			requireCredentialOrder(t, store, want...)
			failure := errors.New("mutation failed")
			if _, err := store.Modify(t.Context(), "rejected", func(*Credential) (*Credential, error) { return nil, failure }); !errors.Is(err, failure) {
				t.Fatalf("modify error=%v", err)
			}
			if _, err := store.Modify(t.Context(), "unchanged", func(*Credential) (*Credential, error) { return nil, nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := store.Modify(ctx, "cancelled", func(*Credential) (*Credential, error) {
				t.Fatal("cancelled callback ran")
				return nil, nil
			}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel error=%v", err)
			}
			requireCredentialOrder(t, store, want...)
			if file, ok := store.(*AuthStorage); ok {
				if err := file.Update("z", func(cur Credential, _ bool) (Credential, error) { cur.Key = "updated"; return cur, nil }); err != nil {
					t.Fatal(err)
				}
				if err := file.Set("last", Credential{Type: CredentialAPIKey}); err != nil {
					t.Fatal(err)
				}
				if err := file.Delete(t.Context(), "z"); err != nil {
					t.Fatal(err)
				}
				setOrderedCredential(t, store, "z")
				requireCredentialOrder(t, NewReadOnlyAuthStorage(path), "2", "10", "a", "01", "last", "z")
			}
		})
	}
}

func TestRuntimeCredentialsPreservesReloadedStorageOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuthFixture(t, path, `{"z":{"type":"api_key"},"a":{"type":"api_key"}}`)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	overlay := NewRuntimeCredentials(store)
	overlay.SetRuntimeAPIKey("a", "override")
	overlay.SetRuntimeAPIKey("runtime", "runtime-key")
	requireCredentialOrder(t, overlay, "z", "a", "runtime")
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeAuthFixture(t, path, `{"a":{"type":"api_key"},"z":{"type":"api_key"}}`)
	stamp := stat.ModTime().Add(time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	requireCredentialOrder(t, overlay, "a", "z", "runtime")
}

func TestCredentialStorageConcurrentMutationOrder(t *testing.T) {
	for _, kind := range []string{"file", "memory"} {
		t.Run(kind, func(t *testing.T) {
			var store CredentialStore = NewInMemoryAuthStorage(nil)
			if kind == "file" {
				var err error
				store, err = NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
				if err != nil {
					t.Fatal(err)
				}
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() {
				_, err := store.Modify(t.Context(), "z", func(*Credential) (*Credential, error) {
					close(entered)
					<-release
					return &Credential{Type: CredentialAPIKey, Key: "first"}, nil
				})
				if err != nil {
					t.Error(err)
				}
			})
			<-entered
			wg.Go(func() {
				_, err := store.Modify(t.Context(), "a", func(*Credential) (*Credential, error) {
					return &Credential{Type: CredentialAPIKey, Key: "second"}, nil
				})
				if err != nil {
					t.Error(err)
				}
			})
			close(release)
			wg.Wait()
			requireCredentialOrder(t, store, "z", "a")
		})
	}
}

func TestCredentialStorageQueueDeleteIntegration(t *testing.T) {
	store := NewInMemoryAuthStorage(nil)
	for _, id := range []string{"z", "a", "10", "2", "01"} {
		setOrderedCredential(t, store, id)
	}
	if err := store.Delete(t.Context(), "z"); err != nil {
		t.Fatal(err)
	}
	setOrderedCredential(t, store, "z")
	requireCredentialOrder(t, store, "2", "10", "a", "01", "z")
}

func BenchmarkRuntimeCredentialEnumeration(b *testing.B) {
	for _, size := range []int{3, 256} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "auth.json")
			store, err := NewAuthStorage(path)
			if err != nil {
				b.Fatal(err)
			}
			for i := size; i > 0; i-- {
				if err := store.Set(fmt.Sprintf("provider-%d", i), Credential{Type: CredentialAPIKey, Key: "key"}); err != nil {
					b.Fatal(err)
				}
			}
			overlay := NewRuntimeCredentials(store)
			overlay.SetRuntimeAPIKey("runtime", "key")
			for b.Loop() {
				infos, err := overlay.List(b.Context())
				if err != nil || len(infos) != size+1 {
					b.Fatalf("list=%v err=%v", infos, err)
				}
			}
		})
	}
}

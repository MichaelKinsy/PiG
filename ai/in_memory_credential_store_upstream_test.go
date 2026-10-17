package ai

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

func credentialKinds(infos []CredentialInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		out = append(out, info.ProviderID+":"+string(info.Type))
	}
	return out
}

func storeCredential(t *testing.T, store *InMemoryCredentialStore, id string, credential Credential) {
	t.Helper()
	if _, err := store.Modify(context.Background(), id, func(*Credential) (*Credential, error) { return &credential, nil }); err != nil {
		t.Fatal(err)
	}
}

// credential-store.ts InMemoryCredentialStore.list: one entry per provider in Map insertion order (providerId and the credential's type);
// setting an existing provider keeps its position, deleting and setting again moves it to the end.
func TestInMemoryCredentialStoreListsProvidersInInsertionOrder(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryCredentialStore()
	if infos, err := store.List(ctx); err != nil || len(infos) != 0 {
		t.Fatalf("empty store lists %v, %v", infos, err)
	}
	storeCredential(t, store, "b", Credential{Type: CredentialAPIKey, Key: "1"})
	storeCredential(t, store, "a", Credential{Type: CredentialOAuth, Access: "x"})
	storeCredential(t, store, "c", Credential{Type: CredentialAPIKey, Key: "2"})
	storeCredential(t, store, "b", Credential{Type: CredentialOAuth, Access: "y"}) // an existing key keeps its place
	infos, err := store.List(ctx)
	if want := []string{"b:oauth", "a:oauth", "c:api_key"}; err != nil || !slices.Equal(credentialKinds(infos), want) {
		t.Fatalf("list = %v, %v, want %v", credentialKinds(infos), err, want)
	}
	if err := store.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	storeCredential(t, store, "b", Credential{Type: CredentialAPIKey, Key: "3"})
	infos, _ = store.List(ctx)
	if want := []string{"a:oauth", "c:api_key", "b:api_key"}; !slices.Equal(credentialKinds(infos), want) {
		t.Fatalf("after delete and set = %v, want %v", credentialKinds(infos), want)
	}
	if err := store.Delete(ctx, "missing"); err != nil {
		t.Fatalf("deleting a missing provider is not an error: %v", err)
	}
}

// read and list throw when the signal is already aborted (`options?.signal?.throwIfAborted()`), before touching the map.
func TestInMemoryCredentialStoreReadAndListFailOnAnAbortedContext(t *testing.T) {
	store := NewInMemoryCredentialStore()
	storeCredential(t, store, "a", Credential{Type: CredentialAPIKey, Key: "1"})
	cause := errors.New("stop")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if credential, err := store.Read(ctx, "a"); credential != nil || !errors.Is(err, cause) {
		t.Fatalf("read = %v, %v", credential, err)
	}
	if infos, err := store.List(ctx); infos != nil || !errors.Is(err, cause) {
		t.Fatalf("list = %v, %v", infos, err)
	}
}

// modify passes the current credential (nil when absent), stores a returned credential, and keeps the current one when fn returns
// nothing; a failing fn stores nothing; an abort after fn settled discards the result (`options?.signal?.throwIfAborted()`).
func TestInMemoryCredentialStoreModifySemantics(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryCredentialStore()

	seen := &Credential{}
	got, err := store.Modify(ctx, "p", func(current *Credential) (*Credential, error) { seen = current; return nil, nil })
	if err != nil || got != nil || seen != nil {
		t.Fatalf("modify on an empty store: got %v err %v seen %v", got, err, seen)
	}
	if infos, _ := store.List(ctx); len(infos) != 0 {
		t.Fatalf("a nil result must not create an entry: %v", infos)
	}

	storeCredential(t, store, "p", Credential{Type: CredentialAPIKey, Key: "k1"})
	got, err = store.Modify(ctx, "p", func(current *Credential) (*Credential, error) {
		if current == nil || current.Key != "k1" {
			t.Fatalf("fn saw %+v", current)
		}
		return nil, nil
	})
	if err != nil || got == nil || got.Key != "k1" {
		t.Fatalf("a nil result returns the current credential: %+v, %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := store.Modify(ctx, "p", func(*Credential) (*Credential, error) { return &Credential{Type: CredentialAPIKey, Key: "lost"}, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if current, _ := store.Read(ctx, "p"); current == nil || current.Key != "k1" {
		t.Fatalf("a failing fn must not store: %+v", current)
	}

	abortCtx, cancel := context.WithCancelCause(ctx)
	cause := errors.New("aborted mid-modify")
	_, err = store.Modify(abortCtx, "p", func(*Credential) (*Credential, error) {
		cancel(cause)
		return &Credential{Type: CredentialAPIKey, Key: "late"}, nil
	})
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the abort cause", err)
	}
	if current, _ := store.Read(ctx, "p"); current == nil || current.Key != "k1" {
		t.Fatalf("an abort after fn discards the result: %+v", current)
	}
}

// Writes are serialized per provider: a second modify of the same provider starts after the first settled and sees its result, while a
// different provider's modify is not queued behind it.
func TestInMemoryCredentialStoreSerializesWritesPerProvider(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryCredentialStore()
	storeCredential(t, store, "p", Credential{Type: CredentialAPIKey, Key: "0"})

	release := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	var firstDone, secondSaw string
	wg.Go(func() {
		_, _ = store.Modify(ctx, "p", func(*Credential) (*Credential, error) {
			close(started)
			<-release
			return &Credential{Type: CredentialAPIKey, Key: "first"}, nil
		})
		firstDone = "yes"
	})
	<-started
	wg.Go(func() {
		_, _ = store.Modify(ctx, "p", func(current *Credential) (*Credential, error) {
			secondSaw = current.Key
			return current, nil
		})
	})

	// A different provider is not blocked by p's running modify.
	other := make(chan error, 1)
	go func() {
		_, err := store.Modify(ctx, "q", func(*Credential) (*Credential, error) { return &Credential{Type: CredentialAPIKey, Key: "q"}, nil })
		other <- err
	}()
	select {
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a modify of another provider queued behind p")
	}
	time.Sleep(20 * time.Millisecond)
	if secondSaw != "" {
		t.Fatal("the second modify of p ran before the first settled")
	}
	close(release)
	wg.Wait()
	if firstDone != "yes" || secondSaw != "first" {
		t.Fatalf("second modify saw %q after the first returned %q", secondSaw, firstDone)
	}
}

package ai

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An operation's initial cancellation check acknowledges admission without assuming that a blocked sync.Mutex is durably blocked to testing/synctest.
type credentialAdmissionContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (ctx *credentialAdmissionContext) Err() error {
	err := ctx.Context.Err()
	ctx.once.Do(func() { close(ctx.checked) })
	return err
}

func memoryAuthRead(t *testing.T, s CredentialStore, id string, want *Credential) {
	t.Helper()
	got, err := s.Read(t.Context(), id)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read(%s)=%#v,%v want=%#v", id, got, err, want)
	}
}

func TestAuthStorageInMemoryCancellationAndOrderingUpstream(t *testing.T) {
	for _, mode := range []string{"serialize", "queued", "active"} {
		name := map[string]string{"serialize": "serializes in-memory mutations across providers", "queued": "cancels a queued in-memory mutation without running it later", "active": "preserves the stored credential after cancelling an active refresh mutation"}[mode]
		t.Run(name, func(t *testing.T) {
			// Upstream packages/coding-agent/test/auth-storage.test.ts:391,418,448.
			previous := Credential{Type: CredentialOAuth, Access: "expired", Refresh: "refresh-token"}
			storage := NewInMemoryAuthStorage(nil)
			firstID := "anthropic"
			if mode == "active" {
				storage = NewInMemoryAuthStorage(map[string]Credential{"oauth": previous})
				firstID = "oauth"
			}
			started, finish := make(chan struct{}), make(chan struct{})
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			defer cancelFirst()
			first := make(chan error, 1)
			var firstSettled atomic.Bool
			go func() {
				_, err := storage.Modify(firstCtx, firstID, func(*Credential) (*Credential, error) {
					close(started)
					<-finish
					firstSettled.Store(true)
					if mode == "active" {
						next := previous
						next.Access = "refreshed"
						next.Expires = time.Now().Add(time.Minute).UnixMilli()
						return &next, nil
					}
					return &Credential{Type: CredentialAPIKey, Key: "anthropic-key"}, nil
				})
				first <- err
			}()
			<-started
			firstReturned := false
			if mode == "active" {
				cancelFirst()
				select {
				case err := <-first:
					firstReturned = true
					if !errors.Is(err, context.Canceled) {
						t.Errorf("active cancellation = %v", err)
					}
				case <-time.After(time.Second): // Deadlock watchdog: the callback remains deliberately unresolved.
					t.Error("active cancellation waited for the blocked callback instead of rejecting its caller")
				}
			}
			secondBase, cancelSecond := context.WithCancel(t.Context())
			defer cancelSecond()
			secondCtx := &credentialAdmissionContext{Context: secondBase, checked: make(chan struct{})}
			var secondCalls atomic.Int32
			second := make(chan error, 1)
			secondID, secondKey := "openai", "openai-key"
			if mode == "active" {
				secondID, secondKey = "other", "other"
			}
			go func() {
				_, err := storage.Modify(secondCtx, secondID, func(*Credential) (*Credential, error) {
					secondCalls.Add(1)
					if !firstSettled.Load() {
						t.Error("second callback overlapped the first callback")
					}
					return &Credential{Type: CredentialAPIKey, Key: secondKey}, nil
				})
				second <- err
			}()
			<-secondCtx.checked
			secondReturned := false
			if mode == "queued" {
				cancelSecond()
				select {
				case err := <-second:
					secondReturned = true
					if !errors.Is(err, context.Canceled) {
						t.Errorf("queued cancellation = %v", err)
					}
				case <-time.After(time.Second):
					t.Error("queued cancellation waited for the blocked callback instead of rejecting its caller")
				}
			}
			if secondCalls.Load() != 0 {
				t.Error("queued callback ran before first settled")
			}
			close(finish)
			if !firstReturned {
				err := <-first
				if mode == "active" {
					if !errors.Is(err, context.Canceled) {
						t.Error(err)
					}
				} else if err != nil {
					t.Error(err)
				}
			}
			if !secondReturned {
				err := <-second
				if mode == "queued" {
					if !errors.Is(err, context.Canceled) {
						t.Error(err)
					}
				} else if err != nil {
					t.Error(err)
				}
			}
			if mode == "queued" {
				if secondCalls.Load() != 0 {
					t.Error("cancelled queued callback ran after first settled")
				}
				memoryAuthRead(t, storage, "openai", nil)
			} else {
				if secondCalls.Load() != 1 {
					t.Errorf("second callbacks = %d, want 1", secondCalls.Load())
				}
				memoryAuthRead(t, storage, secondID, &Credential{Type: CredentialAPIKey, Key: secondKey})
			}
			if mode == "active" {
				memoryAuthRead(t, storage, "oauth", &previous)
			} else {
				memoryAuthRead(t, storage, "anthropic", &Credential{Type: CredentialAPIKey, Key: "anthropic-key"})
			}
		})
	}
}

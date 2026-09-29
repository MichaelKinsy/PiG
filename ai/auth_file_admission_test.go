package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type authAdmissionContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (ctx *authAdmissionContext) Err() error {
	err := ctx.Context.Err()
	ctx.once.Do(func() { close(ctx.checked) })
	return err
}

// Pi auth-storage.ts:118-195 lets a second file operation cancel its lock acquisition without releasing the first operation's active callback.
func TestFileAuthStorageQueuedMutationCancellation(t *testing.T) {
	for _, operation := range []string{"modify", "delete"} {
		t.Run(operation, func(t *testing.T) {
			store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set("queued", Credential{Type: CredentialAPIKey, Key: "keep"}); err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			first := make(chan error, 1)
			go func() {
				_, err := store.Modify(t.Context(), "first", func(*Credential) (*Credential, error) {
					close(started)
					<-release
					return &Credential{Type: CredentialAPIKey, Key: "committed"}, nil
				})
				first <- err
			}()
			<-started
			parent, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			ctx := &authAdmissionContext{Context: parent, checked: make(chan struct{})}
			second := make(chan error, 1)
			callbackRan := false
			go func() {
				if operation == "delete" {
					second <- store.Delete(ctx, "queued")
					return
				}
				_, err := store.Modify(ctx, "queued", func(*Credential) (*Credential, error) {
					callbackRan = true
					return &Credential{Type: CredentialAPIKey, Key: "wrong"}, nil
				})
				second <- err
			}()
			// The initial context check reads nil before this barrier. The first callback still owns the store.
			<-ctx.checked
			reason := errors.New("queued file operation cancelled")
			cancel(reason)
			completed := false
			select {
			case err := <-second:
				completed = true
				if !errors.Is(err, reason) {
					t.Errorf("queued result=%v, want cause %v", err, reason)
				}
			case <-time.After(time.Second): // Deadlock watchdog: release is deliberately withheld until the queued caller returns.
				t.Error("queued cancellation waited for the unrelated active file callback")
			}
			close(release)
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			if !completed {
				<-second
			}
			if callbackRan {
				t.Fatal("cancelled queued callback ran")
			}
			credential, err := store.Read(t.Context(), "queued")
			if err != nil || credential == nil || credential.Key != "keep" {
				t.Fatalf("queued credential=%+v, %v", credential, err)
			}
			queuedKey := credential.Key
			credential, err = store.Read(t.Context(), "first")
			if err != nil || credential == nil || credential.Key != "committed" {
				t.Fatalf("active callback lost ownership: %+v, %v", credential, err)
			}
			if _, err := os.Stat(store.Path() + ".lock"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("file lock remains: %v", err)
			}
			data, err := json.Marshal([]any{operation, completed, callbackRan, queuedKey, credential.Key})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Println("FILE_AUTH_ADMISSION " + string(data))
		})
	}
}

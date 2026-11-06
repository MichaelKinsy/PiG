package ai

// pi: packages/coding-agent/src/core/runtime-credentials.ts (b2cb62923, #11692)

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Pi b2cb62923 test/runtime-credentials.test.ts "settled waits until pending modifications are written": shutdown waits for an OAuth refresh in progress so the rotated token is not lost.
func TestRuntimeCredentialsSettledWaitsUntilPendingModificationsAreWritten(t *testing.T) {
	storage := NewInMemoryAuthStorage(map[string]Credential{"anthropic": {Type: CredentialAPIKey, Key: "old"}})
	credentials := NewRuntimeCredentials(storage)
	respond := make(chan struct{})
	started := make(chan struct{})
	modified := make(chan error, 1)
	go func() {
		_, err := credentials.Modify(t.Context(), "anthropic", func(*Credential) (*Credential, error) {
			close(started)
			<-respond
			return &Credential{Type: CredentialAPIKey, Key: "rotated"}, nil
		})
		modified <- err
	}()
	<-started

	settled := make(chan error, 1)
	go func() { settled <- credentials.Settled(t.Context()) }()
	select {
	case err := <-settled:
		t.Fatalf("Settled returned %v while a modification was pending", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(respond)
	if err := <-settled; err != nil {
		t.Fatalf("Settled = %v", err)
	}
	// Settled returns only after the write: the stored credential is already the rotated one.
	got, err := storage.Read(t.Context(), "anthropic")
	if err != nil || got == nil || got.Key != "rotated" {
		t.Fatalf("stored credential after Settled = %+v, %v; want the rotated key", got, err)
	}
	if err := <-modified; err != nil {
		t.Fatal(err)
	}
}

// Pi b2cb62923 "settled resolves after a pending modification fails": the failure is the caller's, not settled's.
func TestRuntimeCredentialsSettledResolvesAfterAPendingModificationFails(t *testing.T) {
	credentials := NewRuntimeCredentials(NewInMemoryAuthStorage(nil))
	failure := errors.New("invalid_grant")
	release := make(chan struct{})
	started := make(chan struct{})
	failed := make(chan error, 1)
	go func() {
		_, err := credentials.Modify(t.Context(), "anthropic", func(*Credential) (*Credential, error) {
			close(started)
			<-release
			return nil, failure
		})
		failed <- err
	}()
	<-started
	settled := make(chan error, 1)
	go func() { settled <- credentials.Settled(t.Context()) }()
	close(release)
	if err := <-settled; err != nil {
		t.Fatalf("Settled after a failed modification = %v, want nil", err)
	}
	if err := <-failed; !errors.Is(err, failure) {
		t.Fatalf("Modify = %v, want the callback's error", err)
	}
}

// Settled waits only for modifications that began before it, as Promise.all over the set at call time does, and returns the caller's cancellation cause when its context ends first; the modification itself goes on.
func TestRuntimeCredentialsSettledIgnoresLaterModificationsAndHonoursCancellation(t *testing.T) {
	credentials := NewRuntimeCredentials(NewInMemoryAuthStorage(nil))
	if err := credentials.Settled(t.Context()); err != nil {
		t.Fatalf("Settled with nothing pending = %v", err)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = credentials.Modify(t.Context(), "anthropic", func(*Credential) (*Credential, error) {
			close(started)
			<-release
			return nil, nil
		})
	}()
	<-started
	cause := errors.New("second signal")
	ctx, cancel := context.WithCancelCause(t.Context())
	settled := make(chan error, 1)
	go func() { settled <- credentials.Settled(ctx) }()
	select {
	case err := <-settled:
		t.Fatalf("Settled returned %v before cancellation", err)
	case <-time.After(30 * time.Millisecond):
	}
	cancel(cause)
	if err := <-settled; !errors.Is(err, cause) {
		t.Fatalf("Settled after cancellation = %v, want its cause", err)
	}
	select {
	case <-done:
		t.Fatal("cancelling Settled ended the modification")
	default:
	}
	close(release)
	<-done
	if err := credentials.Settled(t.Context()); err != nil {
		t.Fatalf("Settled after the modification returned = %v", err)
	}
}

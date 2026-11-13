package ai

// pi: packages/coding-agent/src/core/auth-storage.ts

import (
	"context"
	"errors"
	"testing"
)

// auth-storage.ts:177-181: after the callback returns, withLockAsync rejects an aborted signal before it looks at `next`, so a Modify whose callback was
// cancelled and reports no change fails with the cancellation cause instead of returning the unchanged credential; nothing is written.
func TestAuthStorageModifyCancelledDuringTheCallbackRejectsAnUnchangedResult(t *testing.T) {
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	storage, path := authPortFile(t, stored)
	cause := errors.New("request cancelled")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	got, err := storage.Modify(ctx, "anthropic", func(*Credential) (*Credential, error) {
		cancel(cause)
		return nil, nil
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Modify = (%v, %v), want the cancellation cause", got, err)
	}
	authPortDisk(t, path, stored)
}

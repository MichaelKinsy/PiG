package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/session"
)

func TestWait(t *testing.T) {
	cancelled, cancel := context.WithCancelCause(ctx)
	cause := errors.New("the caller gave up")
	cancel(cause)
	t.Run("returns nil for a closed channel even when the context ended", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if err := session.Wait(cancelled, done); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("returns the cause of a context that ended before the channel closed", func(t *testing.T) {
		if err := session.Wait(cancelled, make(chan struct{})); !errors.Is(err, cause) {
			t.Fatalf("Wait = %v, want %v", err, cause)
		}
	})
}

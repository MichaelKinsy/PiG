package experimental

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A wait for a removed path must not depend on filesystem notifications: macOS reports none for a Unix socket file.
func TestAwaitExperimentalPathRemoved(t *testing.T) {
	t.Run("missing path returns at once", func(t *testing.T) {
		if err := awaitExperimentalPathRemoved(t.Context(), filepath.Join(t.TempDir(), "gone"), time.Hour); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("removal during the wait", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "present")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		time.AfterFunc(20*time.Millisecond, func() { _ = os.Remove(path) })
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		if err := awaitExperimentalPathRemoved(ctx, path, time.Millisecond); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a path that stays fails at the deadline", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "present")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		if err := awaitExperimentalPathRemoved(ctx, path, time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want the deadline", err)
		}
	})
}

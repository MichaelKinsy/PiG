package coding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Upstream SessionManager.appendMessage throws when the session file cannot
// be written; the exception fails the run instead of losing every later
// message while the run looks healthy.
func TestPersistFailureFailsRun(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel(), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	go func() {
		for range sess.Events() { //nolint:revive // drain
		}
	}()
	if _, err := sess.Send(context.Background(), "first"); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	path := sess.Path()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not written: %v", err)
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(path, 0o644)
	})
	if _, err := sess.Send(context.Background(), "second"); err == nil {
		t.Fatal("Send succeeded although the session file is not writable")
	}
}

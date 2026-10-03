package pilock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectoryLockReleaseStaleAndRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	lease, err := AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + ".lock"); err != nil || !info.IsDir() {
		t.Fatalf("lock = %v, %v", info, err)
	}
	if _, err := AcquireSync(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("contended lock = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock remains: %v", err)
	}
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	lease, err = AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireSync(path); err == nil {
		t.Fatal("regular lockfile was treated as a removable directory")
	}
	if got, err := os.ReadFile(path + ".lock"); err != nil || string(got) != "not a directory" {
		t.Fatalf("regular file changed: %q, %v", got, err)
	}
}

func TestHeartbeatCompromiseAndCallerCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	// Exercise the timer state machine with a short private test interval.
	lease, err := tryAcquire(t.Context(), path, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	changed := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path+".lock", changed, changed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.Context().Done():
	case <-t.Context().Done():
		t.Fatal("compromise did not cancel the operation")
	}
	if err := lease.Release(); !errors.Is(err, errCompromised) {
		t.Fatalf("release = %v", err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("compromised lock was removed: %v", err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	lease, err = Acquire(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("caller stopped")
	cancel(cause)
	if !errors.Is(context.Cause(lease.Context()), cause) {
		t.Fatalf("lock context cause = %v, want the caller's", context.Cause(lease.Context()))
	}
	// proper-lockfile's release knows nothing of the caller's abort; it reports only the removal.
	if err := lease.Release(); err != nil {
		t.Fatalf("release = %v", err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled owner's lock remains: %v", err)
	}
}

// The reference backend comes from the locked npm Pi, not the Go adapter.
// Both writers must serialize their read-modify-write and release all locks.
func TestConcurrentStoreWritesWithPiBackend(t *testing.T) {
	backend, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "core", "auth-storage.js"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "store.json")
	if err := os.WriteFile(path, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	const updates = 12
	script := fmt.Sprintf(`
const { FileAuthStorageBackend } = await import(require('node:url').pathToFileURL(%q));
const storage = new FileAuthStorageBackend(%q);
for (let i = 0; i < %d; i++) await storage.withLockAsync(async content => {
 await Promise.resolve();
 return { result: undefined, next: String(JSON.parse(content) + 1) };
});
`, backend, path, updates)
	cmd := exec.CommandContext(t.Context(), "node", "--eval", "(async()=>{"+script+"})().catch(e=>{console.error(e);process.exitCode=1})")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for range updates {
		lease, err := Acquire(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := os.ReadFile(path)
		var count int
		decodeErr := json.Unmarshal(data, &count)
		writeErr := os.WriteFile(path, []byte(fmt.Sprint(count+1)), 0o600)
		if err := errors.Join(readErr, decodeErr, writeErr, lease.Release()); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != fmt.Sprint(2*updates) {
		t.Fatalf("lost a writer's updates: %s", data)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock remains: %v", err)
	}
}

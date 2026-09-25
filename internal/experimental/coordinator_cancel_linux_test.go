package experimental

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestCoordinatorSignalCleansOwnedSockets(t *testing.T) {
	dir := t.TempDir()
	public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
	child, err := SpawnInternalProcess("coordinator", []string{public, control}, InternalProcessSpawnOptions{Env: map[string]string{"PIG_TEST_COORDINATOR": "1", "HOME": dir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := TerminateInternalProcess(child); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := tryConnect(t.Context(), control)
		if err != nil {
			t.Fatal(err)
		}
		if conn != nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("coordinator did not listen")
		}
		time.Sleep(time.Millisecond)
	}
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator ignored SIGTERM")
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{public, control} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("signal cleanup %s: %v", path, err)
		}
	}
}

func TestEnsureCoordinatorCancellationJoinsChild(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := EnsureCoordinator(ctx, filepath.Join(dir, "p"), filepath.Join(dir, "c"), InternalProcessSpawnOptions{Env: map[string]string{"PIG_TEST_COORDINATOR": "paused", "PIG_TEST_CHILD_READY": ready, "HOME": dir}})
		result <- err
	}()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(ready)
		if err == nil {
			pid, err = strconv.Atoi(string(data))
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not announce startup")
		}
		time.Sleep(time.Millisecond)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Kill(); _ = child.Release() })
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ensure ignored cancellation")
	}
	if err := child.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("cancellation returned with a child still able to take ownership")
	}
}

//go:build !windows

package experimental

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUpstreamInternalProcess(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/experimental-internal-process.test.ts:27
	t.Run("starts the coordinator through the current runtime", func(t *testing.T) {
		directory := socketDir(t)
		public, control := filepath.Join(directory, "p.sock"), filepath.Join(directory, "c.sock")
		child, err := SpawnInternalProcess("coordinator", []string{public, control}, InternalProcessSpawnOptions{Env: map[string]string{"PIG_TEST_COORDINATOR": "1"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := TerminateInternalProcess(child); err != nil {
				t.Error(err)
			}
		})
		// Pi polls canConnect for ten seconds; connection readiness, not elapsed time, releases this assertion.
		deadline := time.Now().Add(10 * time.Second)
		for {
			conn, err := net.Dial("unix", control)
			if err == nil {
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
				break
			}
			select {
			case <-child.Done():
				t.Fatalf("coordinator exited before listening: %v", child.ProcessState())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("coordinator did not listen: %v", err)
			}
			time.Sleep(time.Millisecond)
		}
		if child.PID() == os.Getpid() {
			t.Fatal("coordinator did not run in a child process")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/experimental-internal-process.test.ts:40
	t.Run("waits for a failed activation child to terminate", func(t *testing.T) {
		directory := socketDir(t)
		child, err := SpawnInternalProcess("coordinator", []string{filepath.Join(directory, "p.sock"), filepath.Join(directory, "c.sock")}, InternalProcessSpawnOptions{Env: map[string]string{"PIG_TEST_COORDINATOR": "1"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := TerminateInternalProcess(child); err != nil {
				t.Error(err)
			}
		})
		if child.PID() <= 0 {
			t.Fatal("child has no PID")
		}
		if err := TerminateInternalProcess(child); err != nil {
			t.Fatal(err)
		}
		state := child.ProcessState()
		if state == nil {
			t.Fatal("termination returned before child exit")
		}
		status, ok := state.Sys().(syscall.WaitStatus)
		if !ok || status.Signal() != syscall.SIGKILL {
			t.Fatalf("signal = %v, want SIGKILL", state)
		}
	})
}

//go:build unix

package experimental

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// waitExperimentalWorkerExit waits on the spawned child's own exit authority. Manager removal follows control-socket disconnection, which can precede process exit and reaping.
func waitExperimentalWorkerExit(t *testing.T, pid int) {
	t.Helper()
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	var exited *InternalProcess
	for _, child := range resources.children {
		if child.PID() == pid {
			exited = child
			break
		}
	}
	resources.mu.Unlock()
	if exited == nil {
		t.Fatalf("worker PID %d was not spawned through the tracked server", pid)
	}
	select {
	case <-exited.Done():
	case <-t.Context().Done():
		t.Fatalf("wait for worker %d to exit: %v", pid, context.Cause(t.Context()))
	}
}

// upstream: packages/coding-agent/src/experimental/session-worker.ts:579-599,747-748. A running worker closes its Session resources, including the ownership lock, before exiting on SIGTERM.
func TestSessionWorkerSigtermReleasesSessionOwnership(t *testing.T) {
	setupExperimentalRemoteTest(t)
	_, runtime := makeExperimentalServer(t)
	attachExperimentalClient(t, runtime, "demo-1")
	pid, exists := runtime.WorkerPids()["demo-1"]
	if !exists {
		t.Fatal("demo-1 has no worker PID")
	}
	manager := runtime.workers
	manager.mu.Lock()
	var sessionPath string
	for _, worker := range manager.workerOrder {
		if worker.metadata.ID == "demo-1" {
			sessionPath = worker.metadata.Path
		}
	}
	manager.mu.Unlock()
	if sessionPath == "" {
		t.Fatal("demo-1 has no worker record")
	}
	resolved, err := filepath.EvalSymlinks(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resolved + ".lock"); err != nil {
		t.Fatalf("running worker does not hold the Session lock: %v", err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitExperimentalWorkerRetired(t, runtime, "demo-1")
	waitExperimentalWorkerExit(t, pid)
	if processExists(t, pid) {
		t.Fatalf("worker %d still exists after SIGTERM", pid)
	}
	if _, err := os.Stat(resolved + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("SIGTERM left the Session lock: %v", err)
	}
}

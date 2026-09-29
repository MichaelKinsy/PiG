package experimental

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

type launchedExitWorker struct {
	manager     *SessionWorkerManager
	coordinator *workerManagerCoordinator
	handle      *RoutedSessionHandle
	command     *exec.Cmd
	exited      chan struct{}
	peer, token string
	metadata    session.SessionMetadata
}

// launchExitWorker registers a real child process as a ready Session worker behind the fake coordinator.
func launchExitWorker(t *testing.T) *launchedExitWorker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the child is killed with SIGKILL and reported by its Unix signal name")
	}
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
	t.Cleanup(func() { manager.Detach(); coordinator.callbacks.Wait(); manager.work.Wait(); manager.background.Wait() })
	command := exec.Command("sleep", "60")
	if err := command.Start(); err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	exited := make(chan struct{})
	child := &InternalProcess{cmd: command, done: exited}
	go func() { child.err = command.Wait(); close(exited) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-exited })
	launched := make(chan [2]string, 1)
	manager.spawn = func(_ InternalProcessRole, _ []string, options InternalProcessSpawnOptions) (*InternalProcess, error) {
		launched <- [2]string{options.Env["PI_SESSION_WORKER_PEER_ID"], options.Env["PI_SESSION_WORKER_CONTROL_TOKEN"]}
		return child, nil
	}
	ready := make(chan error, 1)
	var handle *RoutedSessionHandle
	go func() {
		var err error
		handle, err = manager.OpenSession(context.Background(), metadata, []string{})
		ready <- err
	}()
	identity := <-launched
	worker := &launchedExitWorker{manager: manager, coordinator: coordinator, command: command, exited: exited, peer: identity[0], token: identity[1], metadata: metadata}
	coordinator.emit(worker.peer, map[string]any{"type": "worker_ready", "token": worker.token, "sessionKey": metadata.Path, "sessionId": metadata.ID, "pid": command.Process.Pid, "metadata": metadata, "pluginManifestPaths": []string{}})
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	worker.handle = handle
	return worker
}

func (w *launchedExitWorker) disconnect() {
	w.coordinator.mu.Lock()
	listeners := append([]*CoordinatorConnectionListener(nil), w.coordinator.listeners...)
	w.coordinator.mu.Unlock()
	for _, l := range listeners {
		l.listen(CoordinatorConnectionEvent{Type: "peer_disconnected", PeerID: w.peer})
	}
}

func (w *launchedExitWorker) terminalError(t *testing.T) string {
	t.Helper()
	select {
	case <-w.handle.Terminated():
	case <-time.After(5 * time.Second):
		t.Fatal("worker was not removed after peer_disconnected")
	}
	if err := w.handle.TerminalError(); err != nil {
		return err.Error()
	}
	return ""
}

// upstream: session-worker-manager.ts:#handleCoordinatorEvent (:518-528) and #childExited (:706-722) both call #removeWorker, and upstream reaches either first. A worker's control-socket data reaches the manager before its peer_disconnected, so a child that exits right after answering session_demand must not reject the demand it already answered. When the exit is observed first, the removal keeps #childExited's "exited unexpectedly (<signal>)" error.
func TestSessionWorkerExitDoesNotOvertakeQueuedDemandApplied(t *testing.T) {
	w := launchExitWorker(t)
	demand := make(chan map[string]any, 1)
	w.coordinator.setOnSend(func(_ string, payload map[string]any) {
		if payload["type"] == "session_demand" {
			demand <- payload
		}
	})
	attached := make(chan error, 1)
	go func() {
		_, err := w.handle.AttachClient(context.Background())
		attached <- err
	}()
	payload := <-demand
	_ = w.command.Process.Kill()
	<-w.exited
	deadline := time.After(5 * time.Second)
	for recorded := false; !recorded; {
		select {
		case err := <-attached:
			t.Fatalf("AttachClient settled with %v after the worker exited but before its queued demand_applied was delivered", err)
		case <-deadline:
			t.Fatal("the exit watcher never recorded the worker's exit")
		case <-time.After(time.Millisecond):
		}
		w.manager.mu.Lock()
		recorded = w.handle.worker.exitReason != ""
		w.manager.mu.Unlock()
	}
	select {
	case err := <-attached:
		t.Fatalf("AttachClient settled with %v after the worker exited but before its queued demand_applied was delivered", err)
	case <-time.After(50 * time.Millisecond):
	}
	w.coordinator.emit(w.peer, map[string]any{"type": "demand_applied", "token": w.token, "sessionKey": w.metadata.Path, "requestId": payload["requestId"], "attachmentId": payload["attachmentId"], "attached": payload["attached"]})
	w.disconnect()
	if err := <-attached; err != nil {
		t.Fatalf("AttachClient = %v, want the answered demand to settle successfully", err)
	}
	if got, want := w.terminalError(t), "Session worker session-1 exited unexpectedly (SIGKILL)"; got != want {
		t.Fatalf("terminal error = %q, want %q", got, want)
	}
}

// upstream: session-worker-manager.ts:518-528 removes a worker whose peer disconnects before its exit is observed with "disconnected unexpectedly".
func TestSessionWorkerDisconnectBeforeExitReportsDisconnection(t *testing.T) {
	w := launchExitWorker(t)
	w.disconnect()
	if got, want := w.terminalError(t), "Session worker session-1 disconnected unexpectedly"; got != want {
		t.Fatalf("terminal error = %q, want %q", got, want)
	}
	_ = w.command.Process.Kill()
	<-w.exited
	if got, want := w.handle.TerminalError().Error(), "Session worker session-1 disconnected unexpectedly"; got != want {
		t.Fatalf("terminal error after exit = %q, want %q", got, want)
	}
}

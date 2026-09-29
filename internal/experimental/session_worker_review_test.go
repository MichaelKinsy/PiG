package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// upstream: session-worker-manager.ts:699-703 and :810-812 notify the count synchronously from #recordReadyWorker on the event loop that also runs the server.ts:313-317 idle timer, so a registered worker is always visible to that timer. A delayed count delivery in Go must not let the timer retire a generation that already holds a worker.
func TestServerLifetimeDoesNotRetireWhileWorkerCountDeliveryIsBlocked(t *testing.T) {
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	lifetime := NewServerLifetime(false)
	manager := newLifetimeSessionWorkerManager(coordinator, directory, nil, lifetime)
	defer manager.Detach()
	var retired atomic.Int32
	lifetime.Start(func() { retired.Add(1) })
	lifetime.SetConnectionCount(1)
	lifetime.SetConnectionCount(0)
	lifetime.mu.Lock()
	armed := lifetime.retirementTimer != nil
	lifetime.mu.Unlock()
	if !armed {
		t.Fatal("idle retirement timer was not armed")
	}

	manager.countMu.Lock()
	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		coordinator.emit("worker-1", map[string]any{"type": "worker_ready", "token": "worker-token", "sessionKey": metadata.Path, "sessionId": metadata.ID, "pid": 123, "metadata": metadata, "pluginManifestPaths": []string{}})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(manager.WorkerPids()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("worker was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	// Join the idle timer callback (server.ts AUTO_SERVER_IDLE_GRACE_MS) instead of sleeping past it: the callback clears retirementTimer and, when it retires, commits stopped in the same critical section.
	for {
		lifetime.mu.Lock()
		fired, stopped := lifetime.retirementTimer == nil, lifetime.stopped
		lifetime.mu.Unlock()
		if fired {
			if stopped {
				t.Fatal("idle timer retired the generation while a worker is registered")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle retirement timer did not fire")
		}
		time.Sleep(time.Millisecond)
	}
	if got := retired.Load(); got != 0 {
		t.Fatalf("retire calls = %d while a worker is registered, want 0", got)
	}
	manager.countMu.Unlock()
	<-delivered
}

// upstream: session-worker.ts:73-81 declares modifiedAt as Type.Number (a fractional fs mtimeMs is valid, agent jsonl/repo.ts:85,254-256) and createdAt/storageVersion as Type.Integer (Number.isInteger, so 1e3 is valid).
func TestSessionWorkerMetadataWireNumbers(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "session-1.jsonl")
	metadataJSON := func(createdAt, storageVersion, modifiedAt string) json.RawMessage {
		encodedPath, _ := json.Marshal(path)
		encodedCwd, _ := json.Marshal(directory)
		return json.RawMessage(`{"id":"session-1","createdAt":` + createdAt + `,"storageVersion":` + storageVersion + `,"cwd":` + string(encodedCwd) + `,"path":` + string(encodedPath) + `,"modifiedAt":` + modifiedAt + `}`)
	}
	tests := []struct {
		name                                  string
		createdAt, storageVersion, modifiedAt string
		valid                                 bool
		wantModifiedAt                        int64
	}{
		{"fractional modification time", "1", "1", "1712345678901.5", true, 1712345678901},
		{"exponent integer", "1e3", "1.0", "0", true, 0},
		{"fractional createdAt", "1.5", "1", "1", false, 0},
		{"fractional storageVersion", "1", "1.5", "1", false, 0},
		{"string modifiedAt", "1", "1", `"1"`, false, 0},
		{"null modifiedAt", "1", "1", "null", false, 0},
		{"beyond int64", "1e300", "1", "1", false, 0},
	}
	for _, test := range tests {
		t.Run("worker_ready "+test.name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"type": "worker_ready", "token": "t", "sessionKey": path, "sessionId": "session-1", "pid": 1, "metadata": metadataJSON(test.createdAt, test.storageVersion, test.modifiedAt), "pluginManifestPaths": []string{}})
			event, err := decodeSessionWorkerEvent(payload)
			if (err == nil) != test.valid {
				t.Fatalf("decode error = %v, want valid=%t", err, test.valid)
			}
			if test.valid && event.Metadata.ModifiedAt != test.wantModifiedAt {
				t.Fatalf("modifiedAt = %d, want %d", event.Metadata.ModifiedAt, test.wantModifiedAt)
			}
		})
		t.Run("options "+test.name, func(t *testing.T) {
			encodedDir, _ := json.Marshal(directory)
			options, err := parseSessionWorkerOptions([]string{`{"sessionDir":` + string(encodedDir) + `,"metadata":` + string(metadataJSON(test.createdAt, test.storageVersion, test.modifiedAt)) + `,"pluginManifestPaths":[]}`})
			if (err == nil) != test.valid {
				t.Fatalf("parse error = %v, want valid=%t", err, test.valid)
			}
			if test.valid && options.Metadata.ModifiedAt != test.wantModifiedAt {
				t.Fatalf("modifiedAt = %d, want %d", options.Metadata.ModifiedAt, test.wantModifiedAt)
			}
		})
	}
}

// upstream: session-worker-manager.ts:643-647 parses a service_update with parseServiceProviderUpdate and drops an invalid update instead of delivering it to the subscription listener.
func TestSessionWorkerInvalidServiceUpdateIsNotDelivered(t *testing.T) {
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
	defer func() { manager.Detach(); manager.work.Wait(); manager.background.Wait() }()
	if err := manager.Discover([]string{"worker-1"}); err != nil {
		t.Fatal(err)
	}
	scope := WorkerOperationScope{ServerConnectionID: "server-generation-1", AttachmentID: "attachment-1"}
	delivered := make(chan chord.ServiceProviderUpdate, 4)
	manager.mu.Lock()
	worker := manager.workersByPeer["worker-1"]
	tail := make(chan struct{})
	close(tail)
	manager.subscriptions[subscriptionKey(scope, "sub-1")] = &workerServiceSubscription{worker: worker, scope: scope, subscriptionID: "sub-1", tail: tail, publish: func(_ context.Context, _ string, update chord.ServiceProviderUpdate) error {
		delivered <- update
		return nil
	}}
	manager.mu.Unlock()
	send := func(update map[string]any) {
		coordinator.emit("worker-1", map[string]any{"type": "service_update", "token": "worker-token", "sessionKey": metadata.Path, "scope": scope, "subscriptionId": "sub-1", "update": update})
	}
	send(map[string]any{"type": "unavailable", "forged": true})
	send(map[string]any{"type": "state", "member": "state", "sequence": 0, "ops": []any{}})
	send(map[string]any{"type": "state", "member": "state", "sequence": 1, "ops": []any{[]any{"s", []any{"revision"}, 2}}})
	manager.background.Wait()
	close(delivered)
	var got []chord.ServiceProviderUpdate
	for update := range delivered {
		got = append(got, update)
	}
	if len(got) != 1 || got[0].Sequence != 1 {
		t.Fatalf("delivered updates = %+v, want only the valid sequence-1 update", got)
	}
}

// upstream: a Pi worker forwards the fractional fs mtimeMs (jsonl/repo.ts:85,254-256) in worker_ready; server replacement adopts that worker (session-worker-manager.ts:699-703).
func TestSessionWorkerAdoptsWorkerWithFractionalModifiedAt(t *testing.T) {
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl")}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
	defer manager.Detach()
	coordinator.emit("worker-1", map[string]any{"type": "worker_ready", "token": "worker-token", "sessionKey": metadata.Path, "sessionId": metadata.ID, "pid": 123, "metadata": map[string]any{"id": metadata.ID, "createdAt": 1, "storageVersion": 1, "cwd": metadata.Cwd, "path": metadata.Path, "modifiedAt": 1712345678901.5}, "pluginManifestPaths": []string{}})
	tracked := manager.TrackedSessions()
	if len(manager.WorkerPids()) != 1 || len(tracked) != 1 || tracked[0].ModifiedAt != 1712345678901 {
		t.Fatalf("pids=%v tracked=%+v, want the fractional-mtime worker adopted", manager.WorkerPids(), tracked)
	}
}

// upstream: session-worker-manager.ts:431-435 awaits Promise.all of the pending stop and every #stopWorker. The first rejection rejects shutdown() while the other stops continue, and #detachState (:436) is skipped.
func TestSessionWorkerShutdownRejectsOnFirstStopFailureWhileOthersContinue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		directory := t.TempDir()
		metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
		coordinator := &workerManagerCoordinator{metadata: metadata}
		manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
		failure := errors.New("kill failed")
		release := make(chan struct{})
		var slowKilled atomic.Bool
		manager.kill = func(pid int) error {
			if pid == 1 {
				return failure
			}
			<-release
			slowKilled.Store(true)
			return os.ErrProcessDone
		}
		second := metadata
		second.ID, second.Path = "session-2", filepath.Join(directory, "session-2.jsonl")
		for i, m := range []session.SessionMetadata{metadata, second} {
			coordinator.emit("worker-"+string(rune('1'+i)), map[string]any{"type": "worker_ready", "token": "token", "sessionKey": m.Path, "sessionId": m.ID, "pid": i + 1, "metadata": m, "pluginManifestPaths": []string{}})
		}
		result := make(chan error, 1)
		go func() { result <- manager.Shutdown() }()
		var err error
		select {
		case err = <-result:
		case <-time.After(30 * time.Second):
			t.Fatal("Shutdown waited for the still-running stop instead of rejecting on the first failure")
		}
		if !errors.Is(err, failure) {
			t.Fatalf("Shutdown error = %v, want the first stop failure", err)
		}
		if slowKilled.Load() {
			t.Fatal("the other stop finished before release; the test did not exercise a continuing stop")
		}
		if got := len(manager.WorkerPids()); got != 2 {
			t.Fatalf("workers after failed Shutdown = %d, want both still tracked (detachState skipped)", got)
		}
		close(release)
		manager.work.Wait()
		if !slowKilled.Load() {
			t.Fatal("the remaining stop did not continue after the first failure")
		}
		manager.Detach()
	})
}

// upstream: session-worker-manager.ts:426-427 sends SIGKILL to every pending child and only then awaits pendingFinished, and child.kill returns false instead of throwing. A pending child whose reap never completes must not keep a later pending child from being killed.
func TestSessionWorkerShutdownKillsEveryPendingChildBeforeJoiningReaps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		directory := t.TempDir()
		metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
		coordinator := &workerManagerCoordinator{metadata: metadata}
		manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
		// Shutdown snapshots m.pending, a map, so which child is signaled first is unspecified. The child signaled first is the one that stays stuck after SIGKILL, so the dangerous order (a stuck child ahead of an unkilled one) is exercised whatever the iteration order.
		var kills atomic.Int32
		var killed [2]atomic.Bool
		var stuck atomic.Pointer[InternalProcess]
		var children [2]*InternalProcess
		for i := range children {
			child := &InternalProcess{done: make(chan struct{})}
			child.signal = func() error {
				killed[i].Store(true)
				if kills.Add(1) == 1 {
					stuck.Store(child)
				} else {
					close(child.done)
				}
				return nil
			}
			children[i] = child
			key := filepath.Join(directory, "pending-"+string(rune('1'+i))+".jsonl")
			manager.pending[key] = &workerLaunch{sessionKey: key, peerID: "pending-" + string(rune('1'+i)), child: child, done: make(chan struct{})}
		}
		result := make(chan error, 1)
		go func() { result <- manager.Shutdown() }()
		time.Sleep(10*time.Second + time.Millisecond)
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("Shutdown returned %v before the stuck child was reaped", err)
		default:
		}
		if !killed[0].Load() || !killed[1].Load() {
			t.Fatalf("SIGKILL sent to first=%v second=%v before any reap completed, want both", killed[0].Load(), killed[1].Load())
		}
		close(stuck.Load().done)
		if err := <-result; err != nil {
			t.Fatalf("Shutdown = %v, want nil once every pending child is reaped", err)
		}
	})
}

// upstream: session-worker.ts:148 declares worker_ready pid as Type.Integer({ minimum: 1 }); TypeBox checks Number.isInteger on the JSON.parse value, so 1e3 and 1.0 are the integers 1000 and 1.
func TestSessionWorkerReadyPIDWireNumbers(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "session-1.jsonl")
	tests := []struct {
		name, pid string
		want      int
	}{
		{"plain", "7", 7},
		{"exponent integer", "1e3", 1000},
		{"trailing zero fraction", "1.0", 1},
		{"zero", "0", 0},
		{"negative", "-1", 0},
		{"fraction", "1.5", 0},
		{"string", `"1"`, 0},
		{"null", "null", 0},
		{"beyond int64", "1e300", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encodedPath, _ := json.Marshal(path)
			encodedCwd, _ := json.Marshal(directory)
			payload := `{"type":"worker_ready","token":"t","sessionKey":` + string(encodedPath) + `,"sessionId":"session-1","pid":` + test.pid + `,"metadata":{"id":"session-1","createdAt":1,"storageVersion":1,"cwd":` + string(encodedCwd) + `,"path":` + string(encodedPath) + `,"modifiedAt":1},"pluginManifestPaths":[]}`
			event, err := decodeSessionWorkerEvent(json.RawMessage(payload))
			if (err == nil) != (test.want != 0) {
				t.Fatalf("decode error = %v, want valid=%t", err, test.want != 0)
			}
			if err == nil && event.PID != test.want {
				t.Fatalf("pid = %d, want %d", event.PID, test.want)
			}
		})
	}
}

// upstream: session-worker.ts:75-79 accepts any finite integer for createdAt and storageVersion and any finite number for modifiedAt. session.SessionMetadata stores int64, so 2^62 (a valid Number.isInteger value) is representable and must be accepted; only values that overflow int64 are rejected.
func TestSessionWorkerMetadataWireNumbersUpToInt64(t *testing.T) {
	for _, test := range []struct {
		name, value string
		valid       bool
	}{
		{"2^62", "4611686018427387904", true},
		{"-2^62", "-4611686018427387904", true},
		{"-2^63", "-9223372036854775808", true},
		{"2^63", "9223372036854775808", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := `{"id":"session-1","createdAt":` + test.value + `,"storageVersion":1,"cwd":"/","path":"/s.jsonl","modifiedAt":` + test.value + `}`
			decoded, ok := decodeSessionWorkerMetadata(json.RawMessage(metadata))
			if ok != test.valid {
				t.Fatalf("valid = %t, want %t", ok, test.valid)
			}
			if ok && (decoded.CreatedAt != int64(float64FromString(t, test.value)) || decoded.ModifiedAt != decoded.CreatedAt) {
				t.Fatalf("createdAt = %d for %s", decoded.CreatedAt, test.value)
			}
		})
	}
}

func float64FromString(t *testing.T, value string) float64 {
	t.Helper()
	var parsed float64
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

// upstream: session-worker.ts:781 throws Error("Session worker received invalid options", { cause }) for unparseable JSON; the message excludes the parse error.
func TestSessionWorkerInvalidOptionsJSONMessageExcludesCause(t *testing.T) {
	_, err := parseSessionWorkerOptions([]string{"{"})
	if err == nil || err.Error() != "Session worker received invalid options" || errors.Unwrap(err) == nil {
		t.Fatalf("parse = %#v, want message-only error with the JSON cause", err)
	}
}

package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

type workerManagerSend struct {
	peerID  string
	payload map[string]any
}
type workerManagerCoordinator struct {
	mu        sync.Mutex
	listeners []*CoordinatorConnectionListener
	sent      []workerManagerSend
	onSend    func(string, map[string]any)
	callbacks sync.WaitGroup
	metadata  session.SessionMetadata
}

func (c *workerManagerCoordinator) ControlPath() string        { return "/tmp/control.sock" }
func (c *workerManagerCoordinator) ServerConnectionID() string { return "server-generation-1" }
func (c *workerManagerCoordinator) WasReplaced() bool          { return false }
func (c *workerManagerCoordinator) OnEvent(listener *CoordinatorConnectionListener) func() {
	c.mu.Lock()
	c.listeners = append(c.listeners, listener)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, l := range c.listeners {
			if l == listener {
				c.listeners = append(c.listeners[:i], c.listeners[i+1:]...)
				return
			}
		}
	}
}
func (c *workerManagerCoordinator) Send(peer string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	c.mu.Lock()
	c.sent = append(c.sent, workerManagerSend{peer, fields})
	callback := c.onSend
	c.mu.Unlock()
	if callback != nil {
		callback(peer, fields)
	}
	return nil
}
func (c *workerManagerCoordinator) Broadcast(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields["type"] == "discover_workers" {
		c.emit("worker-1", map[string]any{"type": "worker_ready", "token": "worker-token", "sessionKey": c.metadata.Path, "sessionId": c.metadata.ID, "pid": 123, "metadata": c.metadata, "pluginManifestPaths": []string{}})
	}
	return nil
}
func (c *workerManagerCoordinator) emit(peer string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	listeners := append([]*CoordinatorConnectionListener(nil), c.listeners...)
	c.mu.Unlock()
	for _, l := range listeners {
		l.listen(CoordinatorConnectionEvent{Type: "message", From: peer, Payload: raw})
	}
}
func (c *workerManagerCoordinator) queue(peer string, payload any) {
	c.callbacks.Go(func() { c.emit(peer, payload) })
}
func (c *workerManagerCoordinator) setOnSend(callback func(string, map[string]any)) {
	c.mu.Lock()
	c.onSend = callback
	c.mu.Unlock()
}
func (c *workerManagerCoordinator) sends() []workerManagerSend {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]workerManagerSend(nil), c.sent...)
}

func TestPortWave08WorkerLaunchMetadataProjection(t *testing.T) {
	// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:#launch copies only id, createdAt, storageVersion, cwd, path, modifiedAt and a present parentSessionId into the StrictObject SessionWorkerMetadataSchema (session-worker.ts:73-81). Other JsonlSessionMetadata fields, such as legacyParentSessionPath from a migrated v3 header, never reach the worker, and zero-valued required numbers remain present.
	for _, test := range []struct {
		name   string
		mutate func(*session.SessionMetadata)
		want   map[string]any
	}{
		{name: "legacy parent path is not forwarded", mutate: func(m *session.SessionMetadata) { m.LegacyParentSessionPath = "/old/parent.jsonl" }},
		{name: "parent session id is forwarded", mutate: func(m *session.SessionMetadata) { m.ParentSessionID = "parent-1" }, want: map[string]any{"parentSessionId": "parent-1"}},
		{name: "zero modification time stays present", mutate: func(m *session.SessionMetadata) { m.ModifiedAt = 0 }, want: map[string]any{"modifiedAt": float64(0)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
			test.mutate(&metadata)
			coordinator := &workerManagerCoordinator{metadata: metadata}
			manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
			defer manager.Detach()
			failure := errors.New("intercepted process spawn")
			var arguments []string
			manager.spawn = func(_ InternalProcessRole, args []string, _ InternalProcessSpawnOptions) (*InternalProcess, error) {
				arguments = append([]string(nil), args...)
				return nil, failure
			}
			if _, err := manager.OpenSession(context.Background(), metadata, []string{}); !errors.Is(err, failure) {
				t.Fatalf("launch error = %v, want %v", err, failure)
			}
			var options struct {
				Metadata map[string]any `json:"metadata"`
			}
			if err := json.Unmarshal([]byte(arguments[0]), &options); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"id": "session-1", "createdAt": float64(1), "storageVersion": float64(1), "cwd": metadata.Cwd, "path": metadata.Path, "modifiedAt": float64(1)}
			maps.Copy(want, test.want)
			if !reflect.DeepEqual(options.Metadata, want) {
				t.Fatalf("worker metadata = %v, want %v", options.Metadata, want)
			}
			if _, err := parseSessionWorkerOptions(arguments); err != nil {
				t.Fatalf("worker rejected launch options: %v", err)
			}
		})
	}
}

func TestPortWave08WorkerCountDeliveryOrder(t *testing.T) {
	// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:#notifyWorkerCountChanged runs synchronously after each change, so server.ts setWorkerCount always ends with the current count. A slower notification that read an older count must not be delivered after a newer one.
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	var mu sync.Mutex
	var delivered []int
	var gate chan struct{}
	entered := make(chan struct{})
	manager := NewSessionWorkerManager(coordinator, directory, nil, func(count int) {
		mu.Lock()
		wait := gate
		gate = nil
		mu.Unlock()
		if wait != nil {
			close(entered)
			<-wait
		}
		mu.Lock()
		delivered = append(delivered, count)
		mu.Unlock()
	})
	defer manager.Detach()
	if err := manager.Discover([]string{"worker-1"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	mu.Lock()
	gate = release
	mu.Unlock()
	slow := make(chan struct{})
	go func() { defer close(slow); manager.notifyWorkerCountChanged() }()
	<-entered
	removed := make(chan struct{})
	go func() {
		defer close(removed)
		manager.handleCoordinatorEvent(CoordinatorConnectionEvent{Type: "peer_disconnected", PeerID: "worker-1"})
	}()
	// An unordered delivery completes here while the older one is still held; an ordered delivery waits for it.
	select {
	case <-removed:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-slow
	<-removed
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) == 0 || delivered[len(delivered)-1] != 0 {
		t.Fatalf("delivered counts = %v, want the final delivery to be the current count 0", delivered)
	}
}

func TestPortWave08WorkerLaunchModelPresence(t *testing.T) {
	// upstream: packages/coding-agent/src/experimental/server.ts:525-528; session-worker-manager.ts:454-480; session-worker.ts:82-90.
	for _, test := range []struct {
		name    string
		model   *SessionWorkerModel
		want    map[string]any
		invalid bool
	}{
		{name: "omitted selection", want: map[string]any{}},
		{name: "explicit empty model", model: &SessionWorkerModel{Model: ""}, want: map[string]any{"model": ""}, invalid: true},
		{name: "explicit empty provider", model: &SessionWorkerModel{Provider: new(""), Model: "model-1"}, want: map[string]any{"provider": "", "model": "model-1"}, invalid: true},
		{name: "omitted provider", model: &SessionWorkerModel{Model: "model-1"}, want: map[string]any{"model": "model-1"}},
		{name: "explicit provider", model: &SessionWorkerModel{Provider: new("test-provider"), Model: "model-1"}, want: map[string]any{"provider": "test-provider", "model": "model-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
			coordinator := &workerManagerCoordinator{metadata: metadata}
			manager := NewSessionWorkerManager(coordinator, directory, test.model, nil)
			defer manager.Detach()
			failure := errors.New("intercepted process spawn")
			var arguments []string
			var role InternalProcessRole
			manager.spawn = func(nextRole InternalProcessRole, args []string, _ InternalProcessSpawnOptions) (*InternalProcess, error) {
				role = nextRole
				arguments = append([]string(nil), args...)
				return nil, failure
			}
			if _, err := manager.OpenSession(context.Background(), metadata, []string{}); !errors.Is(err, failure) {
				t.Fatalf("launch error = %v, want %v", err, failure)
			}
			if role != "session-worker" || len(arguments) != 1 {
				t.Fatalf("spawn = %q %v", role, arguments)
			}
			var options map[string]any
			if err := json.Unmarshal([]byte(arguments[0]), &options); err != nil {
				t.Fatal(err)
			}
			selected := make(map[string]any)
			for _, name := range []string{"provider", "model"} {
				if value, present := options[name]; present {
					selected[name] = value
				}
			}
			if !reflect.DeepEqual(selected, test.want) {
				t.Fatalf("serialized selection = %v, want %v", selected, test.want)
			}
			_, err := parseSessionWorkerOptions(arguments)
			if (err != nil) != test.invalid {
				t.Fatalf("worker options error = %v, want invalid=%v", err, test.invalid)
			}
		})
	}
}

func TestPortWave08WorkerPIDRemoval(t *testing.T) {
	// Production bookkeeping guard: packages/coding-agent/src/experimental/session-worker-manager.ts:691,781-793.
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
	defer manager.Detach()
	if err := manager.Discover([]string{"worker-1"}); err != nil {
		t.Fatal(err)
	}
	metadata.Path = filepath.Join(directory, "second-session-1.jsonl")
	coordinator.emit("worker-2", map[string]any{"type": "worker_ready", "token": "second-token", "sessionKey": metadata.Path, "sessionId": metadata.ID, "pid": 456, "metadata": metadata, "pluginManifestPaths": []string{}})
	if got := manager.WorkerPids(); !reflect.DeepEqual(got, map[string]int{"session-1": 456}) {
		t.Fatalf("workerPids = %v", got)
	}
	manager.handleCoordinatorEvent(CoordinatorConnectionEvent{Type: "peer_disconnected", PeerID: "worker-2"})
	if got := manager.WorkerPids(); len(got) != 0 {
		t.Fatalf("removed newest PID was resurrected: %v", got)
	}
	if got := manager.TrackedSessions(); len(got) != 1 || got[0].Path != filepath.Join(directory, "session-1.jsonl") {
		t.Fatalf("surviving tracked Session = %v", got)
	}
}

func TestPortWave08WorkerManager(t *testing.T) {
	directory := t.TempDir()
	metadata := session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
	create := func(t *testing.T) (*workerManagerCoordinator, *SessionWorkerManager) {
		t.Helper()
		coordinator := &workerManagerCoordinator{metadata: metadata}
		manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
		t.Cleanup(func() { manager.Detach(); coordinator.callbacks.Wait(); manager.work.Wait(); manager.background.Wait() })
		return coordinator, manager
	}
	open := func(t *testing.T, manager *SessionWorkerManager) *RoutedSessionHandle {
		t.Helper()
		if err := manager.Discover([]string{"worker-1"}); err != nil {
			t.Fatal(err)
		}
		handle, err := manager.OpenSession(context.Background(), metadata, []string{})
		if err != nil {
			t.Fatal(err)
		}
		return handle
	}
	attached := func(t *testing.T) (*workerManagerCoordinator, *SessionWorkerManager, *RoutedSessionHandle, *RoutedSessionAttachment) {
		t.Helper()
		coordinator, manager := create(t)
		handle := open(t, manager)
		coordinator.setOnSend(func(peer string, payload map[string]any) {
			if payload["type"] != "session_demand" {
				return
			}
			coordinator.queue(peer, map[string]any{"type": "demand_applied", "token": "worker-token", "sessionKey": metadata.Path, "requestId": payload["requestId"], "attachmentId": payload["attachmentId"], "attached": payload["attached"]})
		})
		attachment, err := handle.AttachClient(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return coordinator, manager, handle, attachment
	}
	requireError := func(t *testing.T, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
	noShutdown := func(t *testing.T, c *workerManagerCoordinator) {
		t.Helper()
		for _, sent := range c.sends() {
			if sent.payload["type"] == "shutdown" {
				t.Fatalf("unexpected shutdown: %+v", sent)
			}
		}
	}
	// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:100
	t.Run("adopts a discovered worker with its existing Session plugin selection", func(t *testing.T) {
		coordinator, manager := create(t)
		if err := manager.Discover([]string{"worker-1"}); err != nil {
			t.Fatal(err)
		}
		noShutdown(t, coordinator)
		if got := len(manager.WorkerPids()); got != 1 {
			t.Fatalf("workers = %d, want 1", got)
		}
		if err := manager.AssertSessionPluginManifestPaths(metadata, []string{}); err != nil {
			t.Fatal(err)
		}
		manager.Detach()
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:110
	t.Run("rejects a different plugin selection for an active Session without stopping it", func(t *testing.T) {
		_, manager, _, _ := attached(t)
		requireError(t, manager.AssertSessionPluginManifestPaths(metadata, []string{filepath.Join(directory, "plugin", "chord-facets.json")}), "active with a different plugin selection")
		if got := len(manager.WorkerPids()); got != 1 {
			t.Fatalf("workers = %d, want 1", got)
		}
		manager.Detach()
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:119
	t.Run("compensates a timed-out attachment before rejecting it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			coordinator, manager := create(t)
			handle := open(t, manager)
			var demandsMu sync.Mutex
			var demands []map[string]any
			coordinator.setOnSend(func(peer string, payload map[string]any) {
				if payload["type"] != "session_demand" {
					return
				}
				attachmentID, ok := payload["attachmentId"].(string)
				if !ok {
					return
				}
				attached, ok := payload["attached"].(bool)
				if !ok {
					return
				}
				demandsMu.Lock()
				demands = append(demands, map[string]any{"attachmentId": attachmentID, "attached": attached})
				demandsMu.Unlock()
				if attached {
					return
				}
				coordinator.queue(peer, map[string]any{"type": "demand_applied", "token": "worker-token", "sessionKey": metadata.Path, "requestId": payload["requestId"], "attachmentId": payload["attachmentId"], "attached": false})
			})
			finished := make(chan error, 1)
			go func() { _, err := handle.AttachClient(context.Background()); finished <- err }()
			synctest.Wait()
			time.Sleep(5_000 * time.Millisecond)
			synctest.Wait()
			requireError(t, <-finished, "timed out")
			demandsMu.Lock()
			defer demandsMu.Unlock()
			if len(demands) != 2 {
				t.Fatalf("demands = %v, want two", demands)
			}
			if _, ok := demands[0]["attachmentId"].(string); !ok || demands[0]["attached"] != true {
				t.Fatalf("initial demand = %v", demands[0])
			}
			want := map[string]any{"attachmentId": demands[0]["attachmentId"], "attached": false}
			if !reflect.DeepEqual(demands[1], want) {
				t.Fatalf("compensation = %v, want %v", demands[1], want)
			}
			manager.Detach()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:161
	t.Run("kills a worker when timed-out demand cannot be reconciled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			coordinator, manager := create(t)
			var killed []int
			// The instance-owned Kill boundary is os.Process.Kill, i.e. SIGKILL, never a graceful signal.
			manager.kill = func(pid int) error { killed = append(killed, pid); return nil }
			handle := open(t, manager)
			coordinator.setOnSend(func(string, map[string]any) {})
			finished := make(chan error, 1)
			go func() { _, err := handle.AttachClient(context.Background()); finished <- err }()
			synctest.Wait()
			time.Sleep(5_000 * time.Millisecond)
			synctest.Wait()
			time.Sleep(5_000 * time.Millisecond)
			synctest.Wait()
			time.Sleep(10_000 * time.Millisecond)
			synctest.Wait()
			requireError(t, <-finished, "worker was terminated")
			if !reflect.DeepEqual(killed, []int{123}) {
				t.Fatalf("SIGKILL pids = %v, want [123]", killed)
			}
			if got := len(manager.WorkerPids()); got != 0 {
				t.Fatalf("workers = %d, want 0", got)
			}
			manager.Detach()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:180
	t.Run("bounds Harness-driven worker shutdown", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			coordinator, manager, handle, attachment := attached(t)
			var killed []int
			manager.kill = func(pid int) error { killed = append(killed, pid); return nil }
			if err := attachment.Release(context.Background()); err != nil {
				t.Fatal(err)
			}
			coordinator.setOnSend(func(string, map[string]any) {})
			finished := make(chan error, 1)
			go func() { finished <- handle.Close(context.Background()) }()
			synctest.Wait()
			time.Sleep(10_000 * time.Millisecond)
			synctest.Wait()
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(killed, []int{123}) {
				t.Fatalf("SIGKILL pids = %v, want [123]", killed)
			}
			if got := len(manager.WorkerPids()); got != 0 {
				t.Fatalf("workers = %d, want 0", got)
			}
			manager.Detach()
		})
	})
	serviceCall := chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []json.RawMessage{json.RawMessage(`"Hello"`)}}
	// Each row preserves its separate original case and its single injected response.
	for _, test := range []struct {
		name, token, expectedError string
		nullScope                  bool
	}{
		// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:199
		{name: "correlates service results to the worker generation and attachment", token: "worker-token"},
		// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:236
		{name: "rejects a correlated response with mismatched worker identity", token: "wrong-token", expectedError: "mismatched operation response"},
		// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:265
		{name: "rejects a null request scope", token: "worker-token", nullScope: true, expectedError: "invalid operation response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator, manager, _, attachment := attached(t)
			coordinator.setOnSend(func(peer string, payload map[string]any) {
				if payload["type"] != "operation" {
					return
				}
				scope := payload["scope"]
				if test.nullScope {
					scope = nil
				}
				coordinator.queue(peer, map[string]any{"type": "operation_response", "token": test.token, "sessionKey": metadata.Path, "response": map[string]any{"type": "operation_result", "requestId": payload["requestId"], "scope": scope, "result": map[string]any{"accepted": true}}})
			})
			result, err := attachment.InvokeService(context.Background(), serviceCall, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
			if test.expectedError != "" {
				requireError(t, err, test.expectedError)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var got any
				if err := json.Unmarshal(result, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, map[string]any{"accepted": true}) {
					t.Fatalf("service result = %s", result)
				}
				var operation map[string]any
				for _, sent := range coordinator.sends() {
					if sent.payload["type"] == "operation" {
						operation = sent.payload
						break
					}
				}
				scope, ok := operation["scope"].(map[string]any)
				if !ok {
					t.Fatalf("operation scope = %v", operation)
				}
				if scope["serverConnectionId"] != "server-generation-1" {
					t.Fatalf("server generation = %v", scope)
				}
				if _, ok := scope["attachmentId"].(string); !ok {
					t.Fatalf("attachment ID = %v", scope)
				}
				wantCall := map[string]any{"serviceId": "test.session", "member": "run", "args": []any{"Hello"}}
				if !reflect.DeepEqual(operation["call"], wantCall) {
					t.Fatalf("call = %v, want %v", operation["call"], wantCall)
				}
			}
			manager.Detach()
			if err := attachment.Release(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
	// upstream: packages/coding-agent/test/experimental-session-worker-manager.test.ts:294
	t.Run("rejects pending service calls on replacement without stopping the worker", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			coordinator, manager, _, attachment := attached(t)
			coordinator.setOnSend(func(string, map[string]any) {})
			finished := make(chan error, 1)
			go func() {
				_, err := attachment.InvokeService(context.Background(), serviceCall, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
				finished <- err
			}()
			synctest.Wait()
			manager.Detach()
			requireError(t, <-finished, "replaced during a worker operation")
			noShutdown(t, coordinator)
			if got := len(manager.WorkerPids()); got != 0 {
				t.Fatalf("workers = %d, want 0", got)
			}
		})
	})
}

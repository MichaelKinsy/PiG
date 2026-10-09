package experimental

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:328-357 invokeService registers the operation and
// queues its worker write in one synchronous step, so calls the router admits in sequence reach the worker in that sequence even
// when their outcomes are awaited concurrently.
func TestRoutedSessionAttachmentQueuesWorkerOperationsInAdmissionOrder(t *testing.T) {
	t.Parallel()
	const calls = 200
	directory := t.TempDir()
	metadata := SessionCatalogMetadata{ID: "session-1", CreatedAt: 1, Cwd: directory, Path: filepath.Join(directory, "session-1")}
	coordinator := &workerManagerCoordinator{metadata: metadata}
	manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
	t.Cleanup(func() { manager.Detach(); coordinator.callbacks.Wait(); manager.work.Wait(); manager.background.Wait() })
	if err := manager.Discover([]string{"worker-1"}); err != nil {
		t.Fatal(err)
	}
	handle, err := manager.OpenSession(context.Background(), metadata, []string{})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.setOnSend(func(peer string, payload map[string]any) {
		switch payload["type"] {
		case "session_demand":
			coordinator.queue(peer, map[string]any{"type": "demand_applied", "token": "worker-token", "sessionKey": metadata.Path, "requestId": payload["requestId"], "attachmentId": payload["attachmentId"], "attached": payload["attached"]})
		case "operation":
			coordinator.queue(peer, map[string]any{"type": "operation_response", "token": "worker-token", "sessionKey": metadata.Path, "response": map[string]any{"type": "operation_result", "requestId": payload["requestId"], "scope": payload["scope"], "result": nil}})
		}
	})
	attachment, err := handle.AttachClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = attachment.Release(context.Background()) })
	publish := func(context.Context, string, chord.ServiceProviderUpdate) error { return nil }
	var waits sync.WaitGroup
	for i := range calls {
		invocation, err := attachment.BeginInvokeService(context.Background(), chord.ServiceCall{ServiceId: "test.session", Member: "run", Args: []json.RawMessage{json.RawMessage(strconv.Itoa(i))}}, publish)
		if err != nil {
			t.Fatal(err)
		}
		// The router awaits each admitted call on its own goroutine.
		waits.Go(func() {
			if _, err := invocation.Wait(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	waits.Wait()
	var order []int
	for _, sent := range coordinator.sends() {
		if sent.payload["type"] != "operation" {
			continue
		}
		call, _ := sent.payload["call"].(map[string]any)
		args, _ := call["args"].([]any)
		value, _ := args[0].(float64)
		order = append(order, int(value))
	}
	if len(order) != calls {
		t.Fatalf("worker received %d operations, want %d", len(order), calls)
	}
	for i, value := range order {
		if value != i {
			t.Fatalf("worker received operation %d at position %d; order %v", value, i, order[max(0, i-3):min(len(order), i+4)])
		}
	}
}

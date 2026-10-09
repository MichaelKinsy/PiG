package experimental

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:205-218 (openSession attach) and :544-559
// (demand_rejected): an attachment whose demand the worker rejects fails with "Session worker rejected demand: <message>"
// and its id leaves worker.attachmentIds, so the worker is not left holding an attachment nobody owns; a later attach
// that the worker accepts is the only id the worker keeps.
func TestRoutedSessionAttachRejectedDemandReleasesTheAttachmentID(t *testing.T) {
	t.Parallel()
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
	var reject atomic.Bool
	reject.Store(true)
	coordinator.setOnSend(func(peer string, payload map[string]any) {
		if payload["type"] != "session_demand" {
			return
		}
		if reject.Load() {
			coordinator.queue(peer, map[string]any{"type": "demand_rejected", "token": "worker-token", "sessionKey": metadata.Path, "requestId": payload["requestId"], "message": "nope"})
			return
		}
		coordinator.queue(peer, map[string]any{"type": "demand_applied", "token": "worker-token", "sessionKey": metadata.Path, "requestId": payload["requestId"], "attachmentId": payload["attachmentId"], "attached": payload["attached"]})
	})
	attachmentIDs := func() int {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return len(manager.workersByPeer["worker-1"].attachmentIDs)
	}
	if _, err := handle.AttachClient(context.Background()); err == nil || err.Error() != "Session worker rejected demand: nope" {
		t.Fatalf("AttachClient error = %v, want the worker's rejection", err)
	}
	if got := attachmentIDs(); got != 0 {
		t.Fatalf("worker holds %d attachment ids after the rejected attach, want 0", got)
	}
	reject.Store(false)
	attachment, err := handle.AttachClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = attachment.Release(context.Background()) })
	if got := attachmentIDs(); got != 1 {
		t.Fatalf("worker holds %d attachment ids after the accepted attach, want 1", got)
	}
}

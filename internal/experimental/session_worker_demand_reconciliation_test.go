package experimental

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:#reconcileDemandTimeout rejects with AggregateError. Its message is the aggregate title only; the timeout, cleanup and stop failures stay in its errors list.
func TestSessionWorkerDemandReconciliationAggregateError(t *testing.T) {
	for _, row := range []struct {
		name, message string
		killError     error
		causes        int
	}{
		{name: "worker terminated", message: "Session worker demand reconciliation failed; worker was terminated", causes: 2},
		{name: "termination failed", message: "Session worker demand reconciliation and termination failed", killError: errors.New("kill denied"), causes: 3},
	} {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				directory := t.TempDir()
				metadata := SessionCatalogMetadata{ID: "session-1", CreatedAt: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl")}
				coordinator := &workerManagerCoordinator{metadata: metadata}
				manager := NewSessionWorkerManager(coordinator, directory, nil, nil)
				manager.kill = func(int) error { return row.killError }
				if err := manager.Discover([]string{"worker-1"}); err != nil {
					t.Fatal(err)
				}
				handle, err := manager.OpenSession(context.Background(), metadata, []string{})
				if err != nil {
					t.Fatal(err)
				}
				coordinator.setOnSend(func(string, map[string]any) {})
				finished := make(chan error, 1)
				go func() { _, err := handle.AttachClient(context.Background()); finished <- err }()
				for range 3 {
					synctest.Wait()
					time.Sleep(10_000 * time.Millisecond)
				}
				synctest.Wait()
				err = <-finished
				var aggregate *services.AggregateError
				if !errors.As(err, &aggregate) || err.Error() != row.message || len(aggregate.Errors) != row.causes {
					t.Fatalf("error = %#v, want AggregateError %q with %d causes", err, row.message, row.causes)
				}
				if timeout, ok := aggregate.Errors[0].(error); !ok || timeout.Error() != "Session worker demand update timed out" {
					t.Fatalf("first cause = %v, want the demand timeout", aggregate.Errors[0])
				}
				if stop, _ := aggregate.Errors[len(aggregate.Errors)-1].(error); row.killError != nil && !errors.Is(stop, row.killError) {
					t.Fatalf("stop cause = %v, want %v", aggregate.Errors[2], row.killError)
				}
				manager.Detach()
				coordinator.callbacks.Wait()
				manager.work.Wait()
				manager.background.Wait()
			})
		})
	}
}

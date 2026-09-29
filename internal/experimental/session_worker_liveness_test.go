package experimental

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// gatedCoordinator models a peer that stopped reading: a Send of a gated payload type blocks until release closes, as a socket write does under backpressure. A write is recorded only after it completes, so sends() reports wire order.
type gatedCoordinator struct {
	*workerManagerCoordinator
	gated   map[string]bool
	release chan struct{}
	once    sync.Once
	// failure, when set, is the result of every gated write once it is released, as a socket that errors after backpressure.
	failure error
}

// unblock releases every gated write; failing tests defer it so a red run reports the assertion instead of a bubble deadlock.
func (c *gatedCoordinator) unblock() { c.once.Do(func() { close(c.release) }) }

func (c *gatedCoordinator) Send(peer string, payload any) error {
	c.mu.Lock()
	blocked := false
	if fields, ok := payload.(map[string]any); ok {
		blocked = c.gated[fields["type"].(string)]
	}
	c.mu.Unlock()
	if blocked {
		<-c.release
		if c.failure != nil {
			return c.failure
		}
	}
	return c.workerManagerCoordinator.Send(peer, payload)
}

func (c *gatedCoordinator) disconnect(peer string) {
	c.mu.Lock()
	listeners := slices.Clone(c.listeners)
	c.mu.Unlock()
	for _, l := range listeners {
		l.listen(CoordinatorConnectionEvent{Type: "peer_disconnected", PeerID: peer})
	}
}

func (c *gatedCoordinator) sentTypes() []string {
	var types []string
	for _, sent := range c.sends() {
		types = append(types, sent.payload["type"].(string))
	}
	return types
}

func livenessMetadata(directory string) session.SessionMetadata {
	return session.SessionMetadata{ID: "session-1", CreatedAt: 1, StorageVersion: 1, Cwd: directory, Path: filepath.Join(directory, "session-1.jsonl"), ModifiedAt: 1}
}

func newGatedManager(t *testing.T, gated ...string) (*gatedCoordinator, *SessionWorkerManager, session.SessionMetadata) {
	t.Helper()
	directory := t.TempDir()
	metadata := livenessMetadata(directory)
	coordinator := &gatedCoordinator{workerManagerCoordinator: &workerManagerCoordinator{metadata: metadata}, gated: map[string]bool{}, release: make(chan struct{})}
	for _, kind := range gated {
		coordinator.gated[kind] = true
	}
	return coordinator, NewSessionWorkerManager(coordinator, directory, nil, nil), metadata
}

func attachedWorker(t *testing.T, coordinator *gatedCoordinator, manager *SessionWorkerManager, metadata session.SessionMetadata) *RoutedSessionAttachment {
	t.Helper()
	if err := manager.Discover([]string{"worker-1"}); err != nil {
		t.Fatal(err)
	}
	handle, err := manager.OpenSession(context.Background(), metadata, []string{})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.setOnSend(func(peer string, payload map[string]any) {
		if payload["type"] == "session_demand" {
			coordinator.queue(peer, map[string]any{"type": "demand_applied", "token": "worker-token", "sessionKey": metadata.Path, "requestId": payload["requestId"], "attachmentId": payload["attachmentId"], "attached": payload["attached"]})
		}
	})
	attachment, err := handle.AttachClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return attachment
}

// upstream: session-worker-manager.ts:#invoke installs an abort handler that sends operation_cancel with `void send().catch()` (:343) and rejects the pending operation, and sends the operation itself without awaiting (:353-357). Canceling the context settles the call while the coordinator socket write is blocked, and operation_cancel is written after the operation it cancels.
func TestSessionWorkerInvokeCancelDoesNotWaitForSocketWrite(t *testing.T) {
	for _, test := range []struct {
		name  string
		gated []string
	}{
		{"operation write blocked", []string{"operation"}},
		{"operation_cancel write blocked", []string{"operation_cancel"}},
		{"both writes blocked", []string{"operation", "operation_cancel"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				coordinator, manager, metadata := newGatedManager(t, test.gated...)
				defer coordinator.unblock()
				attachment := attachedWorker(t, coordinator, manager, metadata)
				ctx, cancel := context.WithCancelCause(context.Background())
				cause := errors.New("aborted")
				result := make(chan error, 1)
				go func() {
					_, err := attachment.InvokeService(ctx, chord.ServiceCall{}, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
					result <- err
				}()
				synctest.Wait()
				cancel(cause)
				synctest.Wait()
				select {
				case err := <-result:
					if !errors.Is(err, cause) {
						t.Fatalf("InvokeService error = %v, want the cancellation cause", err)
					}
				default:
					t.Fatal("InvokeService did not settle after cancellation while a coordinator write was blocked")
				}
				coordinator.unblock()
				manager.background.Wait()
				types := coordinator.sentTypes()
				operation, cancelIndex := slices.Index(types, "operation"), slices.Index(types, "operation_cancel")
				if operation < 0 || cancelIndex < operation {
					t.Fatalf("wire order = %v, want operation before operation_cancel", types)
				}
				manager.Detach()
				manager.work.Wait()
				coordinator.callbacks.Wait()
			})
		})
	}
}

// upstream: session-worker-manager.ts:#stopWorkerInternal sends shutdown with `void send().catch()` (:385) and starts the WORKER_SHUTDOWN_TIMEOUT_MS timer regardless, so a peer that stopped reading still gets SIGKILL after the grace period.
func TestSessionWorkerStopKillsAfterGraceWhileShutdownWriteBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		coordinator, manager, _ := newGatedManager(t, "shutdown")
		defer coordinator.unblock()
		var killed atomic.Int64
		manager.kill = func(pid int) error { killed.Store(int64(pid)); return nil }
		if err := manager.Discover([]string{"worker-1"}); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- manager.Shutdown() }()
		synctest.Wait()
		time.Sleep(10*time.Second + time.Millisecond)
		synctest.Wait()
		if killed.Load() != 123 {
			t.Fatalf("SIGKILL pid = %d after the shutdown grace with the shutdown write blocked, want 123", killed.Load())
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Shutdown = %v, want nil", err)
			}
		default:
			t.Fatal("Shutdown did not complete while the shutdown write was blocked")
		}
		coordinator.unblock()
		manager.background.Wait()
	})
}

// upstream: session-worker-manager.ts:shutdown sends each pending worker's shutdown with `void send().catch()` (:411) and arms the kill timer without waiting for the write.
func TestSessionWorkerShutdownKillsPendingChildWhileShutdownWriteBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		coordinator, manager, metadata := newGatedManager(t, "shutdown")
		defer coordinator.unblock()
		child := &InternalProcess{done: make(chan struct{})}
		var killed atomic.Bool
		child.signal = func() error { killed.Store(true); close(child.done); return nil }
		manager.pending[metadata.Path] = &workerLaunch{sessionKey: metadata.Path, peerID: "pending-1", child: child, done: make(chan struct{})}
		result := make(chan error, 1)
		go func() { result <- manager.Shutdown() }()
		synctest.Wait()
		time.Sleep(10*time.Second + time.Millisecond)
		synctest.Wait()
		if !killed.Load() {
			t.Fatal("pending child was not killed after the grace period while its shutdown write was blocked")
		}
		if err := <-result; err != nil {
			t.Fatalf("Shutdown = %v, want nil", err)
		}
		coordinator.unblock()
		manager.background.Wait()
	})
}

// upstream: session-worker-manager.ts:649 chains each listener call onto deliveryTail, and shutdown (:405-436) never awaits it. A subscription listener that never returns must not hold a shutdown whose workers all exited.
func TestSessionWorkerShutdownDoesNotWaitForStalledSubscriptionListener(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		coordinator, manager, metadata := newGatedManager(t)
		if err := manager.Discover([]string{"worker-1"}); err != nil {
			t.Fatal(err)
		}
		scope := WorkerOperationScope{ServerConnectionID: "server-generation-1", AttachmentID: "attachment-1"}
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		var second atomic.Bool
		manager.mu.Lock()
		tail := make(chan struct{})
		close(tail)
		manager.subscriptions[subscriptionKey(scope, "sub-1")] = &workerServiceSubscription{worker: manager.workersByPeer["worker-1"], scope: scope, subscriptionID: "sub-1", tail: tail, publish: func(_ context.Context, _ string, update chord.ServiceProviderUpdate) error {
			if update.Sequence == 2 {
				second.Store(true)
				return nil
			}
			close(entered)
			<-release
			return nil
		}}
		manager.mu.Unlock()
		for sequence := 1; sequence <= 2; sequence++ {
			coordinator.emit("worker-1", map[string]any{"type": "service_update", "token": "worker-token", "sessionKey": metadata.Path, "scope": scope, "subscriptionId": "sub-1", "update": map[string]any{"type": "state", "member": "state", "sequence": sequence, "ops": []any{}}})
		}
		<-entered
		result := make(chan error, 1)
		go func() { result <- manager.Shutdown() }()
		synctest.Wait()
		coordinator.disconnect("worker-1")
		synctest.Wait()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Shutdown = %v, want nil", err)
			}
		default:
			t.Fatal("Shutdown is held by a stalled subscription listener")
		}
		if second.Load() {
			t.Fatal("the second update was delivered before the first listener call returned")
		}
		releaseOnce.Do(func() { close(release) })
		manager.background.Wait()
		// session-worker-manager.ts:649 chains deliveryTail with .then, so shutdown does not cancel the queued update: it reaches the listener once the stalled call returns.
		if !second.Load() {
			t.Fatal("the queued second update was never delivered after the stalled listener returned")
		}
	})
}

// upstream: coordinator.ts:send runs #write up to socket.write synchronously, so the coordinator socket carries writes in call order. #invoke's unawaited operation (session-worker-manager.ts:353-357) therefore reaches the wire before a later #applyDemand session_demand (:307) for the same worker, even while the operation write has not completed.
func TestSessionWorkerDemandWriteFollowsQueuedOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		coordinator, manager, metadata := newGatedManager(t, "operation")
		defer coordinator.unblock()
		defer manager.Detach()
		attachment := attachedWorker(t, coordinator, manager, metadata)
		invoked := make(chan error, 1)
		go func() {
			_, err := attachment.InvokeService(context.Background(), chord.ServiceCall{}, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
			invoked <- err
		}()
		synctest.Wait()
		released := make(chan error, 1)
		go func() { released <- attachment.Release(context.Background()) }()
		synctest.Wait()
		coordinator.unblock()
		synctest.Wait()
		if err := <-released; err != nil {
			t.Fatalf("Release = %v, want nil", err)
		}
		operation, detach := -1, -1
		for i, sent := range coordinator.sends() {
			switch {
			case sent.payload["type"] == "operation":
				operation = i
			case sent.payload["type"] == "session_demand" && sent.payload["attached"] == false:
				detach = i
			}
		}
		if operation < 0 || detach < operation {
			t.Fatalf("wire order = %v, want the operation before the detach session_demand", coordinator.sentTypes())
		}
		if want := []string{"session_demand", "operation", "session_demand"}; !slices.Equal(coordinator.sentTypes(), want) {
			t.Fatalf("wire order = %v, want %v", coordinator.sentTypes(), want)
		}
		manager.Detach()
		<-invoked
		manager.work.Wait()
		manager.background.Wait()
		coordinator.callbacks.Wait()
	})
}

// upstream: session-worker-manager.ts:669 answers a duplicate worker_ready with `void send(shutdown).catch()`, so a blocked coordinator write never holds the event handler that read the message.
func TestSessionWorkerDuplicateReadyShutdownDoesNotBlockEventHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		coordinator, manager, metadata := newGatedManager(t, "shutdown")
		defer coordinator.unblock()
		if err := manager.Discover([]string{"worker-1"}); err != nil {
			t.Fatal(err)
		}
		handled := make(chan struct{})
		go func() {
			coordinator.emit("worker-2", map[string]any{"type": "worker_ready", "token": "other-token", "sessionKey": metadata.Path, "sessionId": metadata.ID, "pid": 456, "metadata": metadata, "pluginManifestPaths": []string{}})
			close(handled)
		}()
		synctest.Wait()
		select {
		case <-handled:
		default:
			t.Fatal("duplicate worker_ready handling is blocked by the shutdown write")
		}
		coordinator.unblock()
		manager.background.Wait()
		if types := coordinator.sentTypes(); !slices.Contains(types, "shutdown") {
			t.Fatalf("sent = %v, want a shutdown to the duplicate worker", types)
		}
		manager.mu.Lock()
		queued := len(manager.sendTails)
		manager.mu.Unlock()
		if queued != 0 {
			t.Fatalf("%d write queues remain after every write completed, want none", queued)
		}
		manager.Detach()
		manager.work.Wait()
		coordinator.callbacks.Wait()
	})
}

// upstream: session-worker-manager.ts:#applyDemand arms the WORKER_DEMAND_TIMEOUT_MS timer (:298-302) before it awaits the session_demand write (:307), so the timeout reconciles the demand while that write is still blocked. #reconcileDemandTimeout (:757-786) deletes the pending demand, so a later write failure reaches the no-op #rejectDemand in the catch (:314-316) and the caller receives the reconciliation result once its own write settles. Timeline: t=5s outer timeout starts the detach demand (queued behind the blocked write); t=10s the detach demand times out; the writes fail at t=30s; the detach demand then rejects with its timeout, #stopWorker kills after its 10s grace, and attach rejects with the two-cause AggregateError.
func TestSessionWorkerDemandTimeoutReconcilesWhileDemandWriteBlocked(t *testing.T) {
	for _, row := range []struct {
		name    string
		failure error
	}{
		{name: "blocked write then fails", failure: errors.New("coordinator socket failed")},
		{name: "blocked write then completes"},
	} {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				coordinator, manager, metadata := newGatedManager(t, "session_demand")
				coordinator.failure = row.failure
				defer coordinator.unblock()
				var killed atomic.Int64
				manager.kill = func(pid int) error { killed.Store(int64(pid)); return nil }
				if err := manager.Discover([]string{"worker-1"}); err != nil {
					t.Fatal(err)
				}
				handle, err := manager.OpenSession(context.Background(), metadata, []string{})
				if err != nil {
					t.Fatal(err)
				}
				finished := make(chan error, 1)
				go func() { _, err := handle.AttachClient(context.Background()); finished <- err }()
				synctest.Wait()
				time.Sleep(30 * time.Second)
				synctest.Wait()
				select {
				case err := <-finished:
					t.Fatalf("AttachClient settled with %v before its awaited session_demand write settled", err)
				default:
				}
				coordinator.unblock()
				synctest.Wait()
				select {
				case err := <-finished:
					t.Fatalf("AttachClient settled with %v before the reconciliation stop grace elapsed", err)
				default:
				}
				if killed.Load() != 0 {
					t.Fatal("worker killed before the detach demand settled")
				}
				time.Sleep(10*time.Second + time.Millisecond)
				synctest.Wait()
				var got error
				select {
				case got = <-finished:
				default:
					t.Fatal("AttachClient did not settle when the reconciliation stop grace elapsed")
				}
				var aggregate *services.AggregateError
				if !errors.As(got, &aggregate) || got.Error() != "Session worker demand reconciliation failed; worker was terminated" || len(aggregate.Errors) != 2 {
					t.Fatalf("AttachClient error = %#v, want the two-cause reconciliation AggregateError", got)
				}
				for i, cause := range aggregate.Errors {
					if err, ok := cause.(error); !ok || err.Error() != "Session worker demand update timed out" {
						t.Fatalf("cause %d = %v, want the demand timeout", i, cause)
					}
				}
				if killed.Load() != 123 {
					t.Fatalf("SIGKILL pid = %d, want 123", killed.Load())
				}
				manager.Detach()
				manager.work.Wait()
				manager.background.Wait()
				coordinator.callbacks.Wait()
			})
		})
	}
}

// upstream: session-worker-manager.ts:#applyDemand arms WORKER_DEMAND_TIMEOUT_MS (:298-302) before it awaits the session_demand write (:307), and #reconcileDemandTimeout (:757-786) starts on expiry while that write, or an earlier unawaited operation write (:353-357) queued ahead of it on the coordinator socket (coordinator.ts:116-121), is still blocked. The expired demand leaves #pendingDemand at once and the compensating demand takes its place; the compensating demand expires on its own timer. Each #applyDemand caller still awaits its own send (:307), so #stopWorker begins only after the blocked write settles.
func TestSessionWorkerReleaseDemandTimesOutWhileQueuedWriteBlocked(t *testing.T) {
	for _, row := range []struct {
		name  string
		gated string
	}{
		{"earlier operation write blocked", "operation"},
		{"demand write blocked", "session_demand"},
	} {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				coordinator, manager, metadata := newGatedManager(t)
				defer coordinator.unblock()
				var killed atomic.Int64
				manager.kill = func(pid int) error { killed.Store(int64(pid)); return nil }
				attachment := attachedWorker(t, coordinator, manager, metadata)
				coordinator.mu.Lock()
				coordinator.gated[row.gated] = true
				coordinator.mu.Unlock()
				invoked := make(chan error, 1)
				go func() {
					_, err := attachment.InvokeService(context.Background(), chord.ServiceCall{}, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
					invoked <- err
				}()
				synctest.Wait()
				released := make(chan error, 1)
				go func() { released <- attachment.Release(context.Background()) }()
				synctest.Wait()
				pendingDemands := func() (count int, attached []bool) {
					manager.mu.Lock()
					defer manager.mu.Unlock()
					for _, pending := range manager.pendingDemand {
						attached = append(attached, pending.attached)
					}
					return len(manager.pendingDemand), attached
				}
				if count, _ := pendingDemands(); count != 1 {
					t.Fatalf("pending demands = %d before the deadline, want the detach demand", count)
				}
				time.Sleep(5*time.Second - time.Millisecond)
				synctest.Wait()
				if count, _ := pendingDemands(); count != 1 {
					t.Fatalf("pending demands = %d before the deadline, want the detach demand", count)
				}
				time.Sleep(2 * time.Millisecond)
				synctest.Wait()
				if count, attached := pendingDemands(); count != 1 || attached[0] {
					t.Fatalf("pending demands = %d (attached %v) after the deadline, want only the compensating detach demand", count, attached)
				}
				time.Sleep(5 * time.Second)
				synctest.Wait()
				if count, _ := pendingDemands(); count != 0 {
					t.Fatalf("pending demands = %d after the compensating deadline, want 0", count)
				}
				select {
				case err := <-released:
					t.Fatalf("Release settled with %v before its awaited session_demand write settled", err)
				default:
				}
				coordinator.unblock()
				synctest.Wait()
				if killed.Load() != 0 {
					t.Fatal("worker killed before the reconciliation stop grace elapsed")
				}
				time.Sleep(10*time.Second + time.Millisecond)
				synctest.Wait()
				var got error
				select {
				case got = <-released:
				default:
					t.Fatal("Release did not settle when the reconciliation stop grace elapsed")
				}
				var aggregate *services.AggregateError
				if !errors.As(got, &aggregate) || got.Error() != "Session worker demand reconciliation failed; worker was terminated" || len(aggregate.Errors) != 2 {
					t.Fatalf("Release error = %#v, want the two-cause reconciliation AggregateError", got)
				}
				if killed.Load() != 123 {
					t.Fatalf("SIGKILL pid = %d, want 123", killed.Load())
				}
				manager.Detach()
				<-invoked
				manager.work.Wait()
				manager.background.Wait()
				coordinator.callbacks.Wait()
			})
		})
	}
}

// upstream: session-worker-manager.ts:#invoke checks the attachment scope (:328) and sends the operation (:354-357), and #applyDemand checks worker.stopping (:289) and sends session_demand (:307), each in one synchronous step; #stopWorkerInternal sets worker.stopping (:383) in the step that sends shutdown (:385). coordinator.ts:#write reaches socket.write before its first await (:116-121), so an operation or session_demand that passed its stopping check is always written before the worker's shutdown, and none is written after it. Concurrent Go callers must keep that invariant.
func TestSessionWorkerAcceptedWriteNeverFollowsShutdown(t *testing.T) {
	for _, row := range []struct {
		name  string
		write string
	}{
		{"operation", "operation"},
		{"session_demand", "session_demand"},
	} {
		t.Run(row.name, func(t *testing.T) {
			for range 300 {
				synctest.Test(t, func(t *testing.T) {
					coordinator, manager, metadata := newGatedManager(t)
					defer coordinator.unblock()
					manager.kill = func(int) error { return nil }
					attachment := attachedWorker(t, coordinator, manager, metadata)
					handle := &RoutedSessionHandle{manager, attachment.worker}
					start := make(chan struct{})
					// The call may run before or after the stop, so only its wire position is asserted.
					called := make(chan struct{})
					go func() {
						defer close(called)
						<-start
						if row.write == "operation" {
							_, _ = attachment.InvokeService(context.Background(), chord.ServiceCall{}, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
							return
						}
						_, _ = handle.AttachClient(context.Background())
					}()
					closed := make(chan error, 1)
					go func() {
						<-start
						closed <- handle.Close(context.Background())
					}()
					close(start)
					synctest.Wait()
					time.Sleep(10*time.Second + time.Millisecond)
					synctest.Wait()
					if err := <-closed; err != nil {
						t.Fatalf("Close error = %v", err)
					}
					<-called
					manager.background.Wait()
					types := coordinator.sentTypes()
					shutdown := slices.Index(types, "shutdown")
					if shutdown < 0 {
						t.Fatalf("wire order = %v, want a shutdown", types)
					}
					if slices.Contains(types[shutdown+1:], row.write) {
						t.Fatalf("wire order = %v, want no %s after shutdown", types, row.write)
					}
					manager.Detach()
					manager.work.Wait()
					coordinator.callbacks.Wait()
				})
			}
		})
	}
}

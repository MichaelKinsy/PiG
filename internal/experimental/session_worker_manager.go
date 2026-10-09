package experimental

// Ports packages/coding-agent/src/experimental/session-worker-manager.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// SessionWorkerCoordinator is the coordinator boundary consumed by one replaceable server's worker manager.
type SessionWorkerCoordinator interface {
	ControlPath() string
	ServerConnectionID() string
	WasReplaced() bool
	OnEvent(*CoordinatorConnectionListener) func()
	Send(string, any) error
	Broadcast(any) error
}

// SessionWorkerModel preserves a present model override even when empty. A nil Provider is omitted; a non-nil empty Provider reaches worker validation instead of selecting a default.
type SessionWorkerModel struct {
	Provider *string `json:"provider,omitempty"`
	Model    string  `json:"model"`
}
type WorkerOperationScope struct {
	ServerConnectionID string `json:"serverConnectionId"`
	AttachmentID       string `json:"attachmentId"`
}

// SessionPluginSelectionConflictError rejects changing the plugin selection of a running or starting Session.
type SessionPluginSelectionConflictError struct{ Message string }

func (e *SessionPluginSelectionConflictError) Error() string { return e.Message }

type workerRecord struct {
	peerID, token          string
	metadata               SessionCatalogMetadata
	pid                    int
	pluginManifestPaths    []string
	terminated             chan struct{}
	terminalError          error
	attachmentIDs          map[string]bool
	expectedStop, stopping bool
	stopDone               chan struct{}
	stopError              error
	// exitReason is the child's exit status once the exit watcher observes it while the worker is still registered; the ordered peer_disconnected removal reports it.
	exitReason string
}
type workerLaunch struct {
	sessionKey, peerID, token string
	pluginManifestPaths       []string
	child                     *InternalProcess
	done                      chan struct{}
	worker                    *workerRecord
	err                       error
}
type workerDemandRequest struct {
	worker       *workerRecord
	attachmentID string
	attached     bool
	done         chan struct{}
	err          error
}
type workerOperationRequest struct {
	worker *workerRecord
	scope  WorkerOperationScope
	done   chan struct{}
	result json.RawMessage
	err    error
}
type workerServiceSubscription struct {
	worker         *workerRecord
	scope          WorkerOperationScope
	publish        chord.ServiceUpdatePublisher
	subscriptionID string
	tail           chan struct{}
}

// SessionWorkerManager owns process bookkeeping, generation-fenced requests, and attachment demand for one server generation. Blocking methods preserve awaited operations; Detach forgets workers without stopping their processes.
type SessionWorkerManager struct {
	mu                     sync.Mutex
	coordinator            SessionWorkerCoordinator
	sessionDir             string
	model                  *SessionWorkerModel
	onWorkerCountChanged   func(int)
	countMu                sync.Mutex
	removeListener         func()
	workersBySession       map[string]*workerRecord
	workersByPeer          map[string]*workerRecord
	workerPids             map[string]int
	workerOrder            []*workerRecord
	pending                map[string]*workerLaunch
	pendingDemand          map[string]*workerDemandRequest
	pendingOperations      map[string]*workerOperationRequest
	subscriptions          map[string]*workerServiceSubscription
	discoveryPeers         map[string]bool
	discoveryDone          chan struct{}
	detached, shuttingDown bool
	done                   chan struct{}
	work                   sync.WaitGroup
	// background owns coordinator writes and subscription listener deliveries that upstream starts without awaiting (`void coordinator.send(...)`, deliveryTail). It also runs the awaited session_demand write so the demand timer runs while the write is in flight; applyDemand joins that write itself. Shutdown does not wait for them, so a peer that stops reading or a listener that never returns cannot hold it.
	background sync.WaitGroup
	sendTails  map[string]chan struct{}
	// Process boundaries mirror upstream spawnInternalProcess and process.kill. They are instance-owned, not global process hooks.
	spawn func(InternalProcessRole, []string, InternalProcessSpawnOptions) (*InternalProcess, error)
	kill  func(int) error
}

func NewSessionWorkerManager(coordinator SessionWorkerCoordinator, sessionDir string, model *SessionWorkerModel, onWorkerCountChanged func(int)) *SessionWorkerManager {
	m := &SessionWorkerManager{coordinator: coordinator, sessionDir: sessionDir, model: model, onWorkerCountChanged: onWorkerCountChanged, workersBySession: make(map[string]*workerRecord), workersByPeer: make(map[string]*workerRecord), workerPids: make(map[string]int), pending: make(map[string]*workerLaunch), pendingDemand: make(map[string]*workerDemandRequest), pendingOperations: make(map[string]*workerOperationRequest), subscriptions: make(map[string]*workerServiceSubscription), sendTails: make(map[string]chan struct{}), done: make(chan struct{}), spawn: SpawnInternalProcess, kill: func(pid int) error {
		process, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		return process.Kill()
	}}
	m.removeListener = coordinator.OnEvent(NewCoordinatorConnectionListener(m.handleCoordinatorEvent))
	return m
}

func (m *SessionWorkerManager) WorkerPids() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.workerPids)
}
func (m *SessionWorkerManager) TrackedSessions() []SessionCatalogMetadata {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]SessionCatalogMetadata, 0, len(m.workerOrder))
	for _, worker := range m.workerOrder {
		result = append(result, worker.metadata)
	}
	return result
}
func (m *SessionWorkerManager) AssertSessionPluginManifestPaths(metadata SessionCatalogMetadata, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.assertPluginPaths(metadata, paths)
}
func (m *SessionWorkerManager) assertPluginPaths(metadata SessionCatalogMetadata, paths []string) error {
	if worker := m.workersBySession[metadata.Path]; worker != nil && !slices.Equal(worker.pluginManifestPaths, paths) {
		return &SessionPluginSelectionConflictError{fmt.Sprintf("Session %s is active with a different plugin selection", metadata.ID)}
	}
	if pending := m.pending[metadata.Path]; pending != nil && !slices.Equal(pending.pluginManifestPaths, paths) {
		return &SessionPluginSelectionConflictError{fmt.Sprintf("Session %s is starting with a different plugin selection", metadata.ID)}
	}
	return nil
}

func (m *SessionWorkerManager) Discover(peerIDs []string) error {
	m.mu.Lock()
	if m.detached {
		m.mu.Unlock()
		return nil
	}
	undiscovered := make(map[string]bool)
	for _, peer := range peerIDs {
		if m.workersByPeer[peer] == nil && m.pendingPeer(peer) == nil {
			undiscovered[peer] = true
		}
	}
	if len(undiscovered) == 0 {
		m.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	m.discoveryDone = done
	m.discoveryPeers = undiscovered
	m.mu.Unlock()
	if err := m.coordinator.Broadcast(map[string]any{"type": "discover_workers"}); err != nil {
		return err
	}
	// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:WORKER_DISCOVERY_TIMEOUT_MS
	timer := time.NewTimer(5_000 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	m.mu.Lock()
	if m.discoveryDone == done {
		m.discoveryDone = nil
		m.discoveryPeers = nil
	}
	m.mu.Unlock()
	return nil
}

// RoutedSessionHandle retains the worker generation selected by OpenSession.
type RoutedSessionHandle struct {
	manager *SessionWorkerManager
	worker  *workerRecord
}

func (h *RoutedSessionHandle) Terminated() <-chan struct{} { return h.worker.terminated }
func (h *RoutedSessionHandle) TerminalError() error {
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return h.worker.terminalError
}
func (h *RoutedSessionHandle) Close(ctx context.Context) error {
	return h.manager.stopWorker(h.worker, ctx)
}
func (h *RoutedSessionHandle) AttachClient(ctx context.Context) (*RoutedSessionAttachment, error) {
	return h.manager.attachClient(h.worker, ctx)
}

func (m *SessionWorkerManager) OpenSession(ctx context.Context, metadata SessionCatalogMetadata, pluginManifestPaths []string) (*RoutedSessionHandle, error) {
	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		return nil, errors.New("Experimental server is shutting down")
	}
	if err := m.assertPluginPaths(metadata, pluginManifestPaths); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if worker := m.workersBySession[metadata.Path]; worker != nil {
		m.mu.Unlock()
		return &RoutedSessionHandle{m, worker}, nil
	}
	pending := m.pending[metadata.Path]
	if pending == nil {
		var err error
		pending, err = m.launch(metadata, pluginManifestPaths)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	m.mu.Unlock()
	m.notifyWorkerCountChanged()
	<-pending.done
	if pending.err != nil {
		return nil, pending.err
	}
	return &RoutedSessionHandle{m, pending.worker}, nil
}
func (m *SessionWorkerManager) CloseSession(ctx context.Context, metadata SessionCatalogMetadata) error {
	m.mu.Lock()
	worker, pending := m.workersBySession[metadata.Path], m.pending[metadata.Path]
	m.mu.Unlock()
	if worker == nil && pending != nil {
		<-pending.done
		if pending.err != nil {
			return pending.err
		}
		worker = pending.worker
	}
	if worker == nil {
		return nil
	}
	return m.stopWorker(worker, ctx)
}

// RoutedSessionAttachment fences all service invocations to one server generation and acknowledged client attachment.
type RoutedSessionAttachment struct {
	manager  *SessionWorkerManager
	worker   *workerRecord
	scope    WorkerOperationScope
	mu       sync.Mutex
	released bool
}

func (m *SessionWorkerManager) attachClient(worker *workerRecord, ctx context.Context) (*RoutedSessionAttachment, error) {
	m.mu.Lock()
	if m.detached || m.shuttingDown || worker.stopping {
		m.mu.Unlock()
		return nil, errors.New("Experimental Session worker is stopping")
	}
	if m.workersByPeer[worker.peerID] != worker {
		m.mu.Unlock()
		return nil, errors.New("Experimental Session worker is no longer available")
	}
	id := uuid.NewString()
	worker.attachmentIDs[id] = true
	m.mu.Unlock()
	if err := m.applyDemand(worker, id, true, true, ctx); err != nil {
		m.mu.Lock()
		delete(worker.attachmentIDs, id)
		m.mu.Unlock()
		return nil, err
	}
	scope := WorkerOperationScope{m.coordinator.ServerConnectionID(), id}
	return &RoutedSessionAttachment{manager: m, worker: worker, scope: scope}, nil
}
func (a *RoutedSessionAttachment) Release(ctx context.Context) error {
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return nil
	}
	a.released = true
	a.mu.Unlock()
	m := a.manager
	m.mu.Lock()
	absent := m.detached || !a.worker.attachmentIDs[a.scope.AttachmentID]
	m.mu.Unlock()
	if absent {
		return nil
	}
	err := m.applyDemand(a.worker, a.scope.AttachmentID, false, true, ctx)
	m.mu.Lock()
	delete(a.worker.attachmentIDs, a.scope.AttachmentID)
	m.removeSubscriptions(func(s *workerServiceSubscription) bool { return s.worker == a.worker && s.scope == a.scope })
	detached := m.detached
	m.mu.Unlock()
	if err != nil && (detached || m.coordinator.WasReplaced()) {
		return nil
	}
	return err
}

// InvokeService forwards one call to the worker and waits for its outcome.
func (a *RoutedSessionAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	invocation, err := a.BeginInvokeService(ctx, call, publish)
	if err != nil {
		return nil, err
	}
	return invocation.Wait(context.Background())
}

// BeginInvokeService is the attachment's admission boundary. Subscription bookkeeping, the pending operation and the queued
// worker write happen before it returns, in one synchronous step as upstream's invokeService (session-worker-manager.ts:328-357),
// so calls begun in sequence reach the worker in that sequence.
func (a *RoutedSessionAttachment) BeginInvokeService(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (*chord.ServiceInvocation, error) {
	m := a.manager
	control, isControl := chord.DecodeServiceControlCall(call)
	added := ""
	if isControl && control.Type == "subscribe" {
		added = subscriptionKey(a.scope, control.SubscriptionId)
		m.mu.Lock()
		if m.subscriptions[added] != nil {
			m.mu.Unlock()
			return nil, errors.New("Service subscription ID is already active")
		}
		tail := make(chan struct{})
		close(tail)
		m.subscriptions[added] = &workerServiceSubscription{worker: a.worker, scope: a.scope, publish: publish, subscriptionID: control.SubscriptionId, tail: tail}
		m.mu.Unlock()
	}
	wait, err := m.beginInvoke(ctx, a.worker, a.scope, call)
	if err != nil {
		a.settleInvocation(call, added, err)
		return nil, err
	}
	return chord.NewServiceInvocation(func(context.Context) (json.RawMessage, error) {
		result, err := wait()
		a.settleInvocation(call, added, err)
		return result, err
	}), nil
}

// settleInvocation applies the subscription bookkeeping that follows a control call's outcome.
func (a *RoutedSessionAttachment) settleInvocation(call chord.ServiceCall, added string, err error) {
	m := a.manager
	control, isControl := chord.DecodeServiceControlCall(call)
	if err != nil && added != "" {
		m.mu.Lock()
		delete(m.subscriptions, added)
		if !m.detached {
			m.work.Go(func() {
				_, _ = m.invoke(context.Background(), a.worker, a.scope, chord.CreateServiceUnsubscribeCall(control.SubscriptionId))
			})
		}
		m.mu.Unlock()
	}
	if err == nil && isControl && control.Type == "unsubscribe" {
		m.mu.Lock()
		delete(m.subscriptions, subscriptionKey(a.scope, control.SubscriptionId))
		m.mu.Unlock()
	}
}

func (m *SessionWorkerManager) applyDemand(worker *workerRecord, attachmentID string, attached, compensate bool, ctx context.Context) error {
	m.mu.Lock()
	if worker.stopping || m.workersByPeer[worker.peerID] != worker {
		m.mu.Unlock()
		return errors.New("Experimental Session worker is stopping")
	}
	id := uuid.NewString()
	pending := &workerDemandRequest{worker: worker, attachmentID: attachmentID, attached: attached, done: make(chan struct{})}
	m.pendingDemand[id] = pending
	// session-worker-manager.ts:289-307 checks worker.stopping, registers the demand and reaches socket.write in one synchronous step, so the slot is reserved under the same lock: a concurrent stopWorker cannot queue its shutdown between the check and this write.
	previous, slot := m.enqueueSendLocked(worker.peerID)
	m.mu.Unlock()
	// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:WORKER_DEMAND_TIMEOUT_MS
	timer := time.NewTimer(5_000 * time.Millisecond)
	defer timer.Stop()
	// session-worker-manager.ts:307 awaits this write; coordinator.ts:send writes in call order, so it follows every earlier unawaited write to the worker. The timer armed at :298-302 runs while the write is in flight, and the caller returns only after the write settles.
	written := make(chan error, 1)
	payload := map[string]any{"type": "session_demand", "serverConnectionId": m.coordinator.ServerConnectionID(), "requestId": id, "attachmentId": attachmentID, "attached": attached}
	m.background.Go(func() { written <- m.writeQueued(worker.peerID, payload, previous, slot) })
	select {
	case err := <-written:
		if err != nil {
			m.mu.Lock()
			m.rejectDemand(id, err)
			m.mu.Unlock()
		}
		select {
		case <-pending.done:
			return pending.err
		case <-timer.C:
		}
		return m.demandTimedOut(worker, id, pending, attachmentID, attached, compensate)
	case <-pending.done:
		// :314-316 rejects through #rejectDemand, a no-op once the demand has settled.
		<-written
		return pending.err
	case <-timer.C:
		outcome := m.demandTimedOut(worker, id, pending, attachmentID, attached, compensate)
		// #reconcileDemandTimeout (:757-761) deleted the pending demand, so a failed write reaches the no-op #rejectDemand.
		<-written
		return outcome
	}
}

// demandTimedOut runs the WORKER_DEMAND_TIMEOUT_MS callback of session-worker-manager.ts:298-302 and returns how it settles the demand.
func (m *SessionWorkerManager) demandTimedOut(worker *workerRecord, id string, pending *workerDemandRequest, attachmentID string, attached, compensate bool) error {
	m.mu.Lock()
	if m.pendingDemand[id] != pending {
		m.mu.Unlock()
		<-pending.done
		return pending.err
	}
	delete(m.pendingDemand, id)
	m.mu.Unlock()
	timeoutError := errors.New("Session worker demand update timed out")
	if !compensate {
		return timeoutError
	}
	cleanupError := m.applyDemand(worker, attachmentID, false, false, context.Background())
	if cleanupError == nil {
		if attached {
			return timeoutError
		}
		return nil
	}
	if stopError := m.stopWorker(worker, context.Background()); stopError != nil {
		// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:#reconcileDemandTimeout rejects with an AggregateError whose message excludes its causes.
		return &services.AggregateError{Message: "Session worker demand reconciliation and termination failed", Errors: []any{timeoutError, cleanupError, stopError}}
	}
	return &services.AggregateError{Message: "Session worker demand reconciliation failed; worker was terminated", Errors: []any{timeoutError, cleanupError}}
}

func (m *SessionWorkerManager) invoke(ctx context.Context, worker *workerRecord, scope WorkerOperationScope, call chord.ServiceCall) (json.RawMessage, error) {
	wait, err := m.beginInvoke(ctx, worker, scope, call)
	if err != nil {
		return nil, err
	}
	return wait()
}

// beginInvoke registers the operation and queues its worker write synchronously; wait settles it.
func (m *SessionWorkerManager) beginInvoke(ctx context.Context, worker *workerRecord, scope WorkerOperationScope, call chord.ServiceCall) (func() (json.RawMessage, error), error) {
	m.mu.Lock()
	if m.detached || m.shuttingDown || worker.stopping {
		m.mu.Unlock()
		return nil, errors.New("Experimental Session worker is stopping")
	}
	if m.workersByPeer[worker.peerID] != worker || !worker.attachmentIDs[scope.AttachmentID] {
		m.mu.Unlock()
		return nil, errors.New("Experimental Session worker has no active attachment")
	}
	if err := context.Cause(ctx); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	id := uuid.NewString()
	pending := &workerOperationRequest{worker: worker, scope: scope, done: make(chan struct{})}
	m.pendingOperations[id] = pending
	// session-worker-manager.ts:353-357 sends the operation without awaiting it and rejects the pending operation if the send fails. The scope check at :328 and this send run in one synchronous step, so the write is queued before any later shutdown.
	m.sendAsyncLocked(worker.peerID, map[string]any{"type": "operation", "requestId": id, "scope": scope, "call": call}, func(err error) {
		m.mu.Lock()
		m.rejectOperation(id, err)
		m.mu.Unlock()
	})
	m.mu.Unlock()
	return func() (json.RawMessage, error) { return m.awaitOperation(ctx, worker, scope, id, pending) }, nil
}

func (m *SessionWorkerManager) awaitOperation(ctx context.Context, worker *workerRecord, scope WorkerOperationScope, id string, pending *workerOperationRequest) (json.RawMessage, error) {
	select {
	case <-pending.done:
		return pending.result, pending.err
	case <-ctx.Done():
		m.mu.Lock()
		if m.pendingOperations[id] == pending {
			// session-worker-manager.ts:343 sends operation_cancel without awaiting it, so a peer that stopped reading cannot delay the abort, and then rejects the operation.
			m.sendAsyncLocked(worker.peerID, map[string]any{"type": "operation_cancel", "requestId": id, "scope": scope}, nil)
			m.rejectOperation(id, context.Cause(ctx))
		}
		m.mu.Unlock()
		<-pending.done
		return pending.result, pending.err
	}
}

func (m *SessionWorkerManager) stopWorker(worker *workerRecord, ctx context.Context) error {
	m.mu.Lock()
	if m.detached || m.workersByPeer[worker.peerID] != worker {
		m.mu.Unlock()
		return nil
	}
	if worker.stopDone != nil {
		done := worker.stopDone
		m.mu.Unlock()
		<-done
		return worker.stopError
	}
	done := make(chan struct{})
	worker.stopDone = done
	worker.stopping = true
	worker.expectedStop = true
	m.rejectWorkerOperations(worker, errors.New("Session worker is stopping"))
	// session-worker-manager.ts:385 sends shutdown without awaiting it, in the same synchronous step that sets worker.stopping; the kill timer below starts regardless of the write.
	m.sendAsyncLocked(worker.peerID, map[string]any{"type": "shutdown"}, nil)
	m.mu.Unlock()
	// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:WORKER_SHUTDOWN_TIMEOUT_MS
	timer := time.NewTimer(10_000 * time.Millisecond)
	defer timer.Stop()
	var failure error
	select {
	case <-worker.terminated:
	case <-timer.C:
		failure = m.kill(worker.pid)
		if errors.Is(failure, os.ErrProcessDone) || errors.Is(failure, syscall.ESRCH) {
			failure = nil
		}
		if failure == nil {
			m.mu.Lock()
			m.removeWorker(worker, nil)
			m.mu.Unlock()
			m.notifyWorkerCountChanged()
			<-worker.terminated
		}
	}
	m.mu.Lock()
	worker.stopError = failure
	close(done)
	m.mu.Unlock()
	return failure
}

// Detach rejects pending calls and removes manager state without sending shutdown to surviving workers.
func (m *SessionWorkerManager) Detach() {
	m.mu.Lock()
	if m.detached {
		m.mu.Unlock()
		return
	}
	m.detached = true
	for key, pending := range m.pending {
		delete(m.pending, key)
		pending.err = errors.New("Experimental server was replaced")
		close(pending.done)
	}
	for id := range m.pendingOperations {
		m.rejectOperation(id, errors.New("Experimental server was replaced during a worker operation"))
	}
	m.detachState()
	m.mu.Unlock()
	m.removeListener()
}
func (m *SessionWorkerManager) detachState() {
	for id := range m.pendingOperations {
		m.rejectOperation(id, errors.New("Experimental server detached during a worker operation"))
	}
	for id := range m.pendingDemand {
		m.rejectDemand(id, nil)
	}
	clear(m.pending)
	clear(m.subscriptions)
	clear(m.workersByPeer)
	clear(m.workerPids)
	clear(m.workersBySession)
	m.workerOrder = nil
	if m.discoveryDone != nil {
		close(m.discoveryDone)
		m.discoveryDone = nil
	}
	m.discoveryPeers = nil
	select {
	case <-m.done:
	default:
		close(m.done)
	}
}

func (m *SessionWorkerManager) Shutdown() error {
	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		return nil
	}
	m.shuttingDown = true
	pending := make([]*workerLaunch, 0, len(m.pending))
	for _, p := range m.pending {
		pending = append(pending, p)
	}
	workers := slices.Clone(m.workerOrder)
	// session-worker-manager.ts:410-412 sends each pending shutdown without awaiting it, in the same synchronous step that sets #shuttingDown.
	for _, p := range pending {
		m.sendAsyncLocked(p.peerID, map[string]any{"type": "shutdown"}, nil)
	}
	m.mu.Unlock()
	// session-worker-manager.ts:431-435 awaits Promise.all([stopPending, ...stopWorker]): the first rejection rejects shutdown() while the remaining stops keep running, and #detachState is skipped. Each stop is an owned task that reports once into a buffered channel, so a task that outlives the first failure never blocks; m.work drains them.
	results := make(chan error, len(workers)+1)
	m.work.Go(func() {
		// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:WORKER_SHUTDOWN_TIMEOUT_MS
		timer := time.NewTimer(10_000 * time.Millisecond)
		defer timer.Stop()
		for _, p := range pending {
			select {
			case <-p.child.Done():
				continue
			case <-timer.C:
			}
			// session-worker-manager.ts:426-427 sends SIGKILL to every pending child before awaiting pendingFinished, and child.kill returns false instead of throwing. A child stuck after SIGKILL therefore never delays the others' kill.
			for _, child := range pending {
				_ = child.child.kill()
			}
			for _, child := range pending {
				<-child.child.Done()
			}
			results <- nil
			return
		}
		results <- nil
	})
	for _, worker := range workers {
		m.work.Go(func() { results <- m.stopWorker(worker, context.Background()) })
	}
	for range len(workers) + 1 {
		if err := <-results; err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.detached = true
	m.detachState()
	m.mu.Unlock()
	m.removeListener()
	m.work.Wait()
	return nil
}

// sendAsyncLocked mirrors upstream's `void coordinator.send(...).catch(...)`: it returns before the socket write completes. onError, when set, runs if the write fails. The caller holds m.mu, so the write slot is taken in the same step as the state change that decided to send.
func (m *SessionWorkerManager) sendAsyncLocked(peerID string, payload map[string]any, onError func(error)) {
	previous, done := m.enqueueSendLocked(peerID)
	m.background.Go(func() {
		if err := m.writeQueued(peerID, payload, previous, done); err != nil && onError != nil {
			onError(err)
		}
	})
}

// enqueueSendLocked reserves the next write slot for peerID; the caller holds m.mu. coordinator.ts:send reaches socket.write synchronously, so upstream writes reach the coordinator socket in call order whether or not the caller awaits them; every write to one worker takes a slot here to keep that order.
func (m *SessionWorkerManager) enqueueSendLocked(peerID string) (previous, done chan struct{}) {
	done = make(chan struct{})
	previous = m.sendTails[peerID]
	m.sendTails[peerID] = done
	return previous, done
}

// writeQueued writes after the previous slot completes and releases its own slot. The last slot for a peer removes its map entry, so the map holds only peers with a write in flight.
func (m *SessionWorkerManager) writeQueued(peerID string, payload map[string]any, previous, done chan struct{}) error {
	defer func() {
		m.mu.Lock()
		if m.sendTails[peerID] == done {
			delete(m.sendTails, peerID)
		}
		m.mu.Unlock()
		close(done)
	}()
	if previous != nil {
		<-previous
	}
	return m.coordinator.Send(peerID, payload)
}

func (m *SessionWorkerManager) launch(metadata SessionCatalogMetadata, paths []string) (*workerLaunch, error) {
	peerID, token := "worker-"+uuid.NewString(), uuid.NewString()
	options := struct {
		SessionDir          string                 `json:"sessionDir"`
		Metadata            SessionCatalogMetadata `json:"metadata"`
		PluginManifestPaths []string               `json:"pluginManifestPaths"`
		Provider            *string                `json:"provider,omitempty"`
		Model               *string                `json:"model,omitempty"`
	}{SessionDir: m.sessionDir, Metadata: metadata, PluginManifestPaths: append([]string{}, paths...)}
	if m.model != nil {
		options.Provider = m.model.Provider
		options.Model = &m.model.Model
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	child, err := m.spawn("session-worker", []string{string(encoded)}, InternalProcessSpawnOptions{Env: map[string]string{"PI_SESSION_WORKER_CONTROL_ADDRESS": m.coordinator.ControlPath(), "PI_SESSION_WORKER_CONTROL_TOKEN": token, "PI_SESSION_WORKER_SESSION_KEY_BASE64": encodeSessionKey(metadata.Path), "PI_SESSION_WORKER_PEER_ID": peerID}})
	if err != nil {
		return nil, err
	}
	pending := &workerLaunch{sessionKey: metadata.Path, peerID: peerID, token: token, pluginManifestPaths: slices.Clone(paths), child: child, done: make(chan struct{})}
	m.pending[metadata.Path] = pending
	m.work.Go(func() {
		// upstream: packages/coding-agent/src/experimental/session-worker-manager.ts:WORKER_STARTUP_TIMEOUT_MS
		timer := time.NewTimer(15_000 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-m.done:
			return
		case <-pending.done:
		case <-child.Done():
			m.mu.Lock()
			m.failPending(pending.sessionKey, fmt.Errorf("Session worker exited before readiness (%s)", workerExitReason(child)))
			m.mu.Unlock()
			m.notifyWorkerCountChanged()
			return
		case <-timer.C:
			m.mu.Lock()
			m.failPending(pending.sessionKey, errors.New("Session worker startup timed out"))
			m.mu.Unlock()
			m.notifyWorkerCountChanged()
			return
		}
		select {
		case <-m.done:
			return
		case <-child.Done():
		}
		// session-worker-manager.ts:706-722 (#childExited) and :518-528 (peer_disconnected) both remove the worker; upstream reaches either first. The coordinator relays the worker's control-socket data and then peer_disconnected in order, so the exit is recorded here and the peer_disconnected removal applies it: an exit that overtook a queued demand_applied or operation_response would reject a request the worker had already answered. The recorded exit keeps #childExited's error for the exit-first order.
		m.mu.Lock()
		if worker := m.workersByPeer[peerID]; worker != nil {
			worker.exitReason = workerExitReason(child)
		}
		m.mu.Unlock()
	})
	return pending, nil
}

func workerExitReason(child *InternalProcess) string {
	state := child.ProcessState()
	if state == nil {
		return "unknown"
	}
	if signal := workerSignalName(state); signal != "" {
		return signal
	}
	if code := state.ExitCode(); code >= 0 {
		return fmt.Sprint(code)
	}
	return "unknown"
}

func (m *SessionWorkerManager) pendingPeer(peer string) *workerLaunch {
	for _, pending := range m.pending {
		if pending.peerID == peer {
			return pending
		}
	}
	return nil
}
func (m *SessionWorkerManager) markDiscovered(peer string) {
	delete(m.discoveryPeers, peer)
	if len(m.discoveryPeers) == 0 && m.discoveryDone != nil {
		close(m.discoveryDone)
		m.discoveryDone = nil
	}
}
func (m *SessionWorkerManager) failPending(key string, err error) {
	pending := m.pending[key]
	if pending == nil {
		return
	}
	delete(m.pending, key)
	pending.err = err
	close(pending.done)
	// The child owns its wait/reap task; terminating it prevents a failed launch from retaining Session ownership.
	_ = TerminateInternalProcess(pending.child)
}
func (m *SessionWorkerManager) rejectDemand(id string, err error) {
	if p := m.pendingDemand[id]; p != nil {
		delete(m.pendingDemand, id)
		p.err = err
		close(p.done)
	}
}
func (m *SessionWorkerManager) rejectOperation(id string, err error) {
	if p := m.pendingOperations[id]; p != nil {
		delete(m.pendingOperations, id)
		p.err = err
		close(p.done)
	}
}
func (m *SessionWorkerManager) rejectWorkerOperations(worker *workerRecord, err error) {
	for id, pending := range m.pendingOperations {
		if pending.worker == worker {
			m.rejectOperation(id, err)
		}
	}
}
func (m *SessionWorkerManager) removeSubscriptions(matches func(*workerServiceSubscription) bool) {
	for key, entry := range m.subscriptions {
		if matches(entry) {
			delete(m.subscriptions, key)
		}
	}
}
func (m *SessionWorkerManager) removeWorker(worker *workerRecord, err error) {
	if m.workersByPeer[worker.peerID] != worker {
		return
	}
	m.rejectWorkerOperations(worker, errors.New("Session worker disconnected during an operation"))
	m.removeSubscriptions(func(entry *workerServiceSubscription) bool { return entry.worker == worker })
	for id, pending := range m.pendingDemand {
		if pending.worker == worker {
			m.rejectDemand(id, errors.New("Session worker disconnected during demand update"))
		}
	}
	delete(m.workersByPeer, worker.peerID)
	delete(m.workersBySession, worker.metadata.Path)
	if m.workerPids[worker.metadata.ID] == worker.pid {
		delete(m.workerPids, worker.metadata.ID)
	}
	m.workerOrder = slices.DeleteFunc(m.workerOrder, func(w *workerRecord) bool { return w == worker })
	worker.terminalError = err
	close(worker.terminated)
}

// WorkerCount is the registered plus launching worker count that notifyWorkerCountChanged delivers (session-worker-manager.ts:810-812).
func (m *SessionWorkerManager) WorkerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.workersBySession) + len(m.pending)
}

// notifyWorkerCountChanged reads and delivers the count under one ordering lock. Upstream delivers each count synchronously on its only thread; concurrent Go callers must not deliver an older count after a newer one.
func (m *SessionWorkerManager) notifyWorkerCountChanged() {
	if m.onWorkerCountChanged != nil {
		m.countMu.Lock()
		defer m.countMu.Unlock()
		m.onWorkerCountChanged(m.WorkerCount())
	}
}
func subscriptionKey(scope WorkerOperationScope, id string) string {
	return scope.ServerConnectionID + "\x00" + scope.AttachmentID + "\x00" + id
}

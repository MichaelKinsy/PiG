package experimental

// Ports packages/coding-agent/src/experimental/server.ts

import (
	"sync"
	"time"
)

// ServerLifetime reconciles operator, startup, client, and worker holds for one server generation.
// The idle timer commits retirement (the lifetime stops) atomically with its eligibility check. The retire callback then runs outside the state lock and may stop the lifetime again.
type ServerLifetime struct {
	mu                            sync.Mutex
	keepAlive                     bool
	connectionCount, workerCount  int
	startupHeld, stopped          bool
	startupTimer, retirementTimer *time.Timer
	retirementGeneration          uint64
	retire                        func()
	workerCountSource             func() int
}

func NewServerLifetime(keepAlive bool) *ServerLifetime {
	return &ServerLifetime{keepAlive: keepAlive, startupHeld: !keepAlive}
}

func (l *ServerLifetime) Start(retire func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retire = retire
	if l.startupHeld {
		// upstream: packages/coding-agent/src/experimental/server.ts:AUTO_SERVER_STARTUP_GRACE_MS
		l.startupTimer = time.AfterFunc(10_000*time.Millisecond, func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.startupTimer = nil
			l.startupHeld = false
			l.reconcile()
		})
	}
	l.reconcile()
}

func (l *ServerLifetime) SetConnectionCount(count int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.connectionCount = count
	if count > 0 && l.startupHeld {
		l.startupHeld = false
		if l.startupTimer != nil {
			l.startupTimer.Stop()
			l.startupTimer = nil
		}
	}
	l.reconcile()
}

func (l *ServerLifetime) SetWorkerCount(count int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.workerCount = count
	l.reconcile()
}

// SetWorkerCountSource names the live worker registry. The retirement check reads it under the lifetime lock. Upstream runs a worker-count change and the idle timer callback on one event loop, so a registered worker is never invisible to the check (server.ts:303-317, session-worker-manager.ts:699-703,810-812). Go delivers SetWorkerCount from other goroutines; a delayed delivery must not let the timer retire a generation that already has a worker.
func (l *ServerLifetime) SetWorkerCountSource(source func() int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.workerCountSource = source
}

func (l *ServerLifetime) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	if l.startupTimer != nil {
		l.startupTimer.Stop()
		l.startupTimer = nil
	}
	if l.retirementTimer != nil {
		l.retirementTimer.Stop()
		l.retirementTimer = nil
	}
	l.retirementGeneration++
}

func (l *ServerLifetime) reconcile() {
	if l.stopped || l.keepAlive || l.startupHeld || l.connectionCount != 0 || l.workerCount != 0 {
		if l.retirementTimer != nil {
			l.retirementTimer.Stop()
			l.retirementTimer = nil
		}
		l.retirementGeneration++
		return
	}
	if l.retirementTimer != nil || l.retire == nil {
		return
	}
	generation := l.retirementGeneration
	retire := l.retire
	// upstream: packages/coding-agent/src/experimental/server.ts:AUTO_SERVER_IDLE_GRACE_MS
	l.retirementTimer = time.AfterFunc(1_000*time.Millisecond, func() {
		l.mu.Lock()
		if generation != l.retirementGeneration {
			l.mu.Unlock()
			return
		}
		l.retirementTimer = nil
		workerCount := l.workerCount
		if l.workerCountSource != nil {
			workerCount = l.workerCountSource()
		}
		ready := !l.stopped && !l.startupHeld && l.connectionCount == 0 && workerCount == 0
		if ready {
			// upstream: packages/coding-agent/src/experimental/server.ts:684-686 retire begins runtime.close(), whose synchronous prefix is lifetime.stop() (server.ts:667). Committing that stop with the eligibility check keeps a hold that arrives afterwards from reviving or re-arming a retired generation.
			l.stopped = true
			l.retirementGeneration++
		}
		l.mu.Unlock()
		if ready {
			retire()
		}
	})
}

// newLifetimeSessionWorkerManager is server.ts:618: the manager reports each worker-count change to the lifetime, which also reads the registry when its idle timer decides.
func newLifetimeSessionWorkerManager(coordinator SessionWorkerCoordinator, sessionDir string, model *SessionWorkerModel, lifetime *ServerLifetime) *SessionWorkerManager {
	workers := NewSessionWorkerManager(coordinator, sessionDir, model, lifetime.SetWorkerCount)
	lifetime.SetWorkerCountSource(workers.WorkerCount)
	return workers
}

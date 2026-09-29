package experimental

// Ports packages/coding-agent/src/experimental/session-worker.ts

import (
	"errors"
	"math"
	"sync"
	"time"
)

// WorkerLifecycleOptions selects the initial generation and the initial/orphan demand grace periods in milliseconds.
type WorkerLifecycleOptions struct {
	InitialServerConnectionID *string
	InitialDemandGraceMs      int
	OrphanDemandGraceMs       int
	// OnRetire runs once, under the lifecycle lock, when retirement commits.
	OnRetire func()
}

type workerDemandKey struct{ serverConnectionID, attachmentID string }
type workerDemand struct {
	timer      *time.Timer
	generation uint64
}
type workerOperationKey struct{ kind, lane, operationID string }

// WorkerLifecycle reconciles generation-scoped attachment demand with Harness activity. OnRetire runs inside the critical section that commits retirement, as upstream's synchronous #reconcile does, so it must be brief and must not call back into the lifecycle.
type WorkerLifecycle struct {
	mu                        sync.Mutex
	currentServerConnectionID *string
	orphanDemandGrace         time.Duration
	onRetire                  func()
	demands                   map[workerDemandKey]*workerDemand
	activeOperations          map[workerOperationKey]struct{}
	initialTimer              *time.Timer
	demandInitialized         bool
	retirementHolds           int
	retiring                  bool
}

// Node's setTimeout clamps nonpositive and overflowing signed-32-bit delays to one millisecond.
func workerTimerDelay(milliseconds int) time.Duration {
	if milliseconds < 1 || milliseconds > math.MaxInt32 {
		return time.Millisecond
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func NewWorkerLifecycle(options WorkerLifecycleOptions) *WorkerLifecycle {
	l := &WorkerLifecycle{currentServerConnectionID: options.InitialServerConnectionID, orphanDemandGrace: workerTimerDelay(options.OrphanDemandGraceMs), onRetire: options.OnRetire, demands: make(map[workerDemandKey]*workerDemand), activeOperations: make(map[workerOperationKey]struct{})}
	l.mu.Lock()
	l.initialTimer = time.AfterFunc(workerTimerDelay(options.InitialDemandGraceMs), func() {
		l.mu.Lock()
		if l.initialTimer == nil {
			l.mu.Unlock()
			return
		}
		l.initialTimer = nil
		l.demandInitialized = true
		l.reconcileUnlock()
	})
	l.mu.Unlock()
	return l
}

func (l *WorkerLifecycle) ServerConnected(serverConnectionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.currentServerConnectionID = &serverConnectionID
	for key, demand := range l.demands {
		if key.serverConnectionID == serverConnectionID && demand.timer != nil {
			demand.timer.Stop()
			demand.timer = nil
			demand.generation++
		}
	}
}

func (l *WorkerLifecycle) ServerDisconnected(serverConnectionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.currentServerConnectionID != nil && *l.currentServerConnectionID == serverConnectionID {
		l.currentServerConnectionID = nil
	}
	for key, demand := range l.demands {
		if key.serverConnectionID != serverConnectionID || demand.timer != nil {
			continue
		}
		generation := demand.generation
		demand.timer = time.AfterFunc(l.orphanDemandGrace, func() {
			l.mu.Lock()
			if l.demands[key] != demand || demand.timer == nil || demand.generation != generation {
				l.mu.Unlock()
				return
			}
			delete(l.demands, key)
			l.reconcileUnlock()
		})
	}
}

func (l *WorkerLifecycle) BeginRequest(serverConnectionID, attachmentID string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.retiring {
		return nil, errors.New("Session worker is retiring")
	}
	if l.currentServerConnectionID == nil || serverConnectionID != *l.currentServerConnectionID {
		return nil, errors.New("Session worker received a request from a stale server generation")
	}
	demand := l.demands[workerDemandKey{serverConnectionID, attachmentID}]
	if demand == nil || demand.timer != nil {
		return nil, errors.New("Session worker request does not match the active attachment")
	}
	return l.holdRetirement(), nil
}

func (l *WorkerLifecycle) HoldRetirement() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.holdRetirement()
}

func (l *WorkerLifecycle) holdRetirement() func() {
	l.retirementHolds++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.retirementHolds--
			l.reconcileUnlock()
		})
	}
}

func (l *WorkerLifecycle) SetDemand(serverConnectionID, attachmentID string, attached bool) error {
	l.mu.Lock()
	if l.retiring {
		l.mu.Unlock()
		return errors.New("Session worker is retiring")
	}
	if l.currentServerConnectionID == nil || serverConnectionID != *l.currentServerConnectionID {
		l.mu.Unlock()
		return errors.New("Session worker received demand from a stale server generation")
	}
	l.demandInitialized = true
	if l.initialTimer != nil {
		l.initialTimer.Stop()
		l.initialTimer = nil
	}
	key := workerDemandKey{serverConnectionID, attachmentID}
	if previous := l.demands[key]; previous != nil && previous.timer != nil {
		previous.timer.Stop()
	}
	if attached {
		l.demands[key] = &workerDemand{}
	} else {
		delete(l.demands, key)
	}
	l.reconcileUnlock()
	return nil
}

func (l *WorkerLifecycle) OperationStarted(kind, lane, operationID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.activeOperations[workerOperationKey{kind, lane, operationID}] = struct{}{}
}

func (l *WorkerLifecycle) OperationStopped(kind, lane, operationID string) {
	l.mu.Lock()
	delete(l.activeOperations, workerOperationKey{kind, lane, operationID})
	l.reconcileUnlock()
}

func (l *WorkerLifecycle) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.initialTimer != nil {
		l.initialTimer.Stop()
		l.initialTimer = nil
	}
	for _, demand := range l.demands {
		if demand.timer != nil {
			demand.timer.Stop()
		}
	}
	clear(l.demands)
}

func (l *WorkerLifecycle) reconcileUnlock() {
	ready := !l.retiring && l.demandInitialized && l.retirementHolds == 0 && len(l.activeOperations) == 0 && len(l.demands) == 0
	if ready {
		l.retiring = true
		// upstream: packages/coding-agent/src/experimental/session-worker.ts:#reconcile sets #retiring and calls #onRetire synchronously, so no operation can start between the commit and the callback.
		l.onRetire()
	}
	l.mu.Unlock()
}

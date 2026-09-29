package chord

// Ports packages/chord/src/services/consumer.ts.
// Go separates the synchronous prefix of packages/chord/src/services/consumer.ts:501-551 from its awaited continuation. Connection sources must invalidate a generation before returning to their caller.

import (
	"context"
	"errors"
	"sync"
)

// BindingWait is an already-started binding operation. The caller owns any goroutine waiting for it; constructing it never detaches a waiter.
type BindingWait struct {
	wait    func() error
	settled func() (bool, error)
}

func (waiting *BindingWait) Wait() error            { return waiting.wait() }
func (waiting *BindingWait) Settled() (bool, error) { return waiting.settled() }
func settledBindingWait(err error) *BindingWait {
	return &BindingWait{wait: func() error { return err }, settled: func() (bool, error) { return true, err }}
}

type readinessSnapshot struct {
	revision int
	starts   []*task
}

func (binding *RemoteServiceBinding) readinessSnapshot() (readinessSnapshot, error) {
	binding.mu.Lock()
	if binding.disposed {
		binding.mu.Unlock()
		return readinessSnapshot{}, errors.New("Remote service binding is disposed")
	}
	snapshot := readinessSnapshot{revision: binding.readinessRevision, starts: []*task{binding.transition}}
	for _, id := range binding.singletonOrder {
		if single := binding.singletons[id]; single != nil && single.starting != nil {
			snapshot.starts = append(snapshot.starts, single.starting)
		}
	}
	keyedBindings := make([]*keyedBinding, 0, len(binding.keyedOrder))
	for _, id := range binding.keyedOrder {
		if keyed := binding.keyed[id]; keyed != nil {
			keyedBindings = append(keyedBindings, keyed)
		}
	}
	binding.mu.Unlock()
	for _, keyed := range keyedBindings {
		snapshot.starts = append(snapshot.starts, keyed.ready())
	}
	return snapshot, nil
}
func (binding *RemoteServiceBinding) readinessCurrent(snapshot readinessSnapshot) (bool, error) {
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.disposed {
		return false, errors.New("Remote service binding is disposed")
	}
	return binding.readinessRevision == snapshot.revision, nil
}
func readinessSettled(ctx context.Context, starts []*task) (bool, error) {
	if ctx.Err() != nil {
		return true, context.Cause(ctx)
	}
	complete := true
	for _, start := range starts {
		select {
		case <-start.done:
			if start.err != nil {
				return true, start.err
			}
		default:
			complete = false
		}
	}
	return complete, nil
}

// BeginReady captures the current hydration work synchronously. Cancellation rejects only this waiter, never the underlying subscriptions.
func (binding *RemoteServiceBinding) BeginReady(ctx context.Context) *BindingWait {
	snapshot, err := binding.readinessSnapshot()
	if err != nil {
		return settledBindingWait(err)
	}
	return &BindingWait{settled: func() (bool, error) {
		complete, err := readinessSettled(ctx, snapshot.starts)
		if !complete || err != nil {
			return complete, err
		}
		return binding.readinessCurrent(snapshot)
	}, wait: func() error {
		current := snapshot
		for {
			if err := waitReadiness(ctx, current.starts); err != nil {
				return err
			}
			same, err := binding.readinessCurrent(current)
			if err != nil {
				return err
			}
			if same {
				return nil
			}
			current, err = binding.readinessSnapshot()
			if err != nil {
				return err
			}
		}
	}}
}
func waitReadiness(ctx context.Context, starts []*task) error {
	if complete, err := readinessSettled(ctx, starts); complete {
		return err
	}
	waiting, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	results := make(chan error, len(starts))
	var workers sync.WaitGroup
	for _, start := range starts {
		workers.Go(func() {
			select {
			case <-start.done:
				results <- start.err
			case <-waiting.Done():
				results <- context.Cause(waiting)
			}
		})
	}
	var failure error
	for range starts {
		if err := <-results; err != nil && failure == nil {
			failure = err
			cancel(err)
		}
	}
	workers.Wait()
	return failure
}

// BeginRebind performs revision changes and replica fencing before returning. Its continuation joins all transitions, as upstream rebind's Promise.allSettled does.
func (binding *RemoteServiceBinding) BeginRebind(ctx context.Context, bound bool) *BindingWait {
	binding.mu.Lock()
	if binding.disposed {
		binding.mu.Unlock()
		return settledBindingWait(errors.New("Remote service binding is disposed"))
	}
	binding.bound = bound
	binding.readinessRevision++
	var transitions []*task
	for _, serviceId := range binding.singletonOrder {
		single := binding.singletons[serviceId]
		single.revision++
		single.facade.fence()
		subscription := single.subscription
		single.subscription = nil
		revision := single.revision
		single.starting = startTask(func() error {
			if subscription != nil {
				if err := subscription.Close(ctx); err != nil {
					return err
				}
			}
			if bound {
				return binding.startSingleton(serviceId, single, revision)
			}
			return nil
		})
		transitions = append(transitions, single.starting)
	}
	for _, id := range binding.keyedOrder {
		transitions = append(transitions, binding.keyed[id].beginRebind(ctx, bound))
	}
	completion := startTask(func() error {
		var failures []error
		for _, transition := range transitions {
			if err := transition.wait(context.Background()); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) > 0 {
			return NewAggregateError("Failed to rebind services", failures)
		}
		return nil
	})
	binding.transition = completion
	binding.mu.Unlock()
	return &BindingWait{wait: func() error { return completion.wait(context.Background()) }, settled: func() (bool, error) {
		select {
		case <-completion.done:
			return true, completion.err
		default:
			return false, nil
		}
	}}
}

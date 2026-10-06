package codingagent

import (
	"context"
	"errors"
	"sync"
)

// modelTaskOwner separates public Promise settlement from the lifetime of its remaining work. Services owns the registry and closes this one task group.
// Ports packages/coding-agent/src/core/model-runtime.ts:runAvailabilityRefresh,refreshProviderAvailability,registerNativeProvider.
type modelTaskOwner struct {
	mu       sync.Mutex
	root     *modelTaskBinding
	cancel   context.CancelFunc
	closed   bool
	bindings map[<-chan struct{}]*modelTaskBinding
	work     sync.WaitGroup
}

type modelTaskBinding struct {
	context.Context
	owner  *modelTaskOwner
	cancel context.CancelFunc
}

type modelTaskBindingKey struct{}

// cancellationOnlyContext forwards cancellation metadata but hides request values from a shared cancellation binding. Per-request values belong to modelTaskContext.
type cancellationOnlyContext struct {
	context.Context
	values context.Context
}

func (ctx cancellationOnlyContext) Value(key any) any {
	if ctx.values.Value(key) != nil {
		return nil
	}
	return ctx.Context.Value(key)
}

type modelTaskContext struct {
	context.Context
	values  context.Context
	binding *modelTaskBinding
}

func (ctx modelTaskContext) Value(key any) any {
	if key == (modelTaskBindingKey{}) {
		return ctx.binding
	}
	if value := ctx.values.Value(key); value != nil {
		return value
	}
	return ctx.Context.Value(key)
}

func (owner *modelTaskOwner) initLocked() {
	if owner.root == nil {
		ctx, cancel := context.WithCancel(context.Background())
		owner.root = &modelTaskBinding{Context: ctx, owner: owner, cancel: cancel}
		owner.cancel = cancel
		owner.bindings = make(map[<-chan struct{}]*modelTaskBinding)
	}
}

func (owner *modelTaskOwner) context(parent context.Context) context.Context {
	if parent == nil {
		return nil
	}
	if binding, _ := parent.Value(modelTaskBindingKey{}).(*modelTaskBinding); binding != nil && binding.owner == owner && binding.Done() == parent.Done() {
		return parent
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.initLocked()
	binding := owner.root
	if !owner.closed && parent.Done() != nil {
		key := parent.Done()
		binding = owner.bindings[key]
		if binding == nil {
			// One cancellation binding per live parent signal prevents repeated refreshes from retaining one child context per operation. Returned Done observers remain valid after settlement.
			ctx, cancel := context.WithCancel(cancellationOnlyContext{Context: parent, values: context.WithoutCancel(parent)})
			binding = &modelTaskBinding{Context: ctx, owner: owner, cancel: cancel}
			owner.bindings[key] = binding
			context.AfterFunc(ctx, func() {
				owner.mu.Lock()
				if owner.bindings[key] == binding {
					delete(owner.bindings, key)
				}
				owner.mu.Unlock()
			})
		}
	}
	return modelTaskContext{Context: binding.Context, values: context.WithoutCancel(parent), binding: binding}
}

func (owner *modelTaskOwner) start(ctx context.Context, work func(context.Context)) bool {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.initLocked()
	if owner.closed {
		return false
	}
	owner.work.Go(func() { work(ctx) })
	return true
}

func (owner *modelTaskOwner) close() {
	owner.mu.Lock()
	owner.initLocked()
	owner.closed = true
	owner.cancel()
	for _, binding := range owner.bindings {
		binding.cancel()
	}
	clear(owner.bindings)
	owner.mu.Unlock()
	owner.work.Wait()
}

// ModelTaskContext links operation cancellation to the Services lifetime while preserving caller values and cancellation causes. It does not cancel a retained signal when an operation settles.
func (r *ModelRegistry) ModelTaskContext(ctx context.Context) context.Context {
	return r.modelTasks.context(ctx)
}

// StartModelTask admits background model work under the caller and Services lifetimes. False means the owner is closed or the context is nil. Work must report errors through its owning operation and must not close its own Services.
func (r *ModelRegistry) StartModelTask(ctx context.Context, work func(context.Context)) bool {
	if ctx == nil {
		return false
	}
	ctx = r.ModelTaskContext(ctx)
	return r.modelTasks.start(ctx, work)
}

// AwaitModelTasks mirrors Promise.all: first rejection settles the waiter, success requires every task, and an owned coordinator joins all remaining tasks without cancelling them on rejection.
func (r *ModelRegistry) AwaitModelTasks(ctx context.Context, tasks ...func(context.Context) error) error {
	if ctx == nil {
		return errors.New("model tasks: nil context")
	}
	ctx = r.ModelTaskContext(ctx)
	settled := make(chan error, 1)
	if !r.modelTasks.start(ctx, func(ctx context.Context) {
		results := make(chan error, len(tasks))
		var children sync.WaitGroup
		for _, task := range tasks {
			children.Go(func() { results <- task(ctx) })
		}
		rejected := false
		for range tasks {
			if err := <-results; err != nil && !rejected {
				rejected = true
				settled <- err
			}
		}
		children.Wait()
		if !rejected {
			settled <- ctx.Err()
		}
	}) {
		return ctx.Err()
	}
	select {
	case err := <-settled:
		return err
	default:
	}
	select {
	case err := <-settled:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseModelTasks stops admission, cancels the owner, and waits for admitted jobs and failed Promise siblings to drain. It then cancels the remote catalog collection's refreshes and waits for its operations, which are only this registry's catalog refreshes, so no store or credential read they started outlives Close. It also cancels the native Models collection's active refreshes, including a queued registration refresh's provider callbacks, without waiting for caller-owned Models operations such as in-flight streams or logins. Call it outside model callbacks.
func (r *ModelRegistry) CloseModelTasks() {
	r.modelTasks.close()
	// Admission is closed, so no remote refresh can add an operation while Close waits for them.
	r.remoteMu.Lock()
	remote := r.remoteCollection
	r.remoteMu.Unlock()
	if remote != nil {
		remote.Close()
	}
	r.nativeMu.Lock()
	models := r.nativeModels
	r.nativeMu.Unlock()
	if models != nil {
		// Models.Close cancels refreshes and then joins every collection operation. Only the cancellation is part of this contract, so the join runs off the caller.
		go models.Close()
	}
}

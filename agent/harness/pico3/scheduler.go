package pico3

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var errInvocationAborted = errors.New("aborted")

type invocation struct {
	task   Task
	mode   string
	cancel context.CancelCauseFunc
	done   chan struct{}
}

type idleWaiter struct {
	conversationId *Id
	ready          chan struct{}
}

// scheduler reserves eligible tasks on the line and dispatches each as one
// owned invocation goroutine.
type scheduler struct {
	session  *Session
	kinds    *kindRegistry
	runtime  func(task Task, authority invoker, ctx context.Context) *Runtime
	onReport func(error)
	ctx      context.Context

	mu           sync.Mutex
	enabled      bool
	holds        int
	dirty        bool
	draining     bool
	invocations  map[Id]*invocation
	taskWaiters  map[Id]map[chan Task]bool
	inputWaiters map[Id]map[chan Input]bool
	idleWaiters  map[*idleWaiter]bool
	drains       sync.WaitGroup
	running      sync.WaitGroup
}

func newScheduler(session *Session, kinds *kindRegistry, runtime func(Task, invoker, context.Context) *Runtime, onReport func(error), ctx context.Context) *scheduler {
	scheduler := &scheduler{
		session: session, kinds: kinds, runtime: runtime, onReport: onReport, ctx: ctx,
		invocations: map[Id]*invocation{}, taskWaiters: map[Id]map[chan Task]bool{},
		inputWaiters: map[Id]map[chan Input]bool{}, idleWaiters: map[*idleWaiter]bool{},
	}
	session.addListener(scheduler.onCommit)
	return scheduler
}

func (scheduler *scheduler) onCommit(result CommitResult) {
	scheduler.mu.Lock()
	for _, task := range result.Changes.Tasks {
		if inv := scheduler.invocations[task.Id]; task.Abort && inv != nil && inv.mode == "run" {
			inv.cancel(errInvocationAborted)
		}
	}
	for _, task := range result.Changes.Tasks {
		if task.Status == TaskTerminal {
			for waiter := range scheduler.taskWaiters[task.Id] {
				waiter <- task.clone()
				delete(scheduler.taskWaiters[task.Id], waiter)
			}
		}
	}
	for _, input := range result.Changes.Inputs {
		if input.Status == InputDone || input.Status == InputUnanswered {
			for waiter := range scheduler.inputWaiters[input.Id] {
				waiter <- cloneInput(input)
				delete(scheduler.inputWaiters[input.Id], waiter)
			}
		}
	}
	scheduler.mu.Unlock()
	if len(result.Changes.Tasks) > 0 {
		scheduler.kick()
	}
	scheduler.checkIdle()
}

func (scheduler *scheduler) resume() {
	scheduler.mu.Lock()
	scheduler.enabled = true
	scheduler.mu.Unlock()
	scheduler.kick()
}

func (scheduler *scheduler) hold() func() {
	scheduler.mu.Lock()
	scheduler.holds++
	scheduler.mu.Unlock()
	released := false
	var once sync.Mutex
	return func() {
		once.Lock()
		defer once.Unlock()
		if released {
			return
		}
		released = true
		scheduler.mu.Lock()
		scheduler.holds--
		idle := scheduler.holds == 0
		scheduler.mu.Unlock()
		if idle {
			scheduler.kick()
		}
	}
}

func (scheduler *scheduler) kick() {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if !scheduler.enabled {
		return
	}
	scheduler.dirty = true
	if scheduler.holds == 0 && !scheduler.draining {
		scheduler.draining = true
		scheduler.drains.Go(scheduler.drain)
	}
}

func (scheduler *scheduler) drain() {
	defer func() {
		scheduler.mu.Lock()
		scheduler.draining = false
		if scheduler.enabled && scheduler.dirty && scheduler.holds == 0 {
			scheduler.draining = true
			scheduler.drains.Go(scheduler.drain)
		}
		scheduler.mu.Unlock()
		scheduler.checkIdle()
	}()
	for scheduler.shouldDrain() {
		reserved, err := scheduler.reserveEligible()
		if err != nil {
			scheduler.onReport(err)
			return
		}
		scheduler.mu.Lock()
		stopped := !scheduler.enabled || scheduler.holds > 0
		if stopped {
			scheduler.dirty = true
		}
		scheduler.mu.Unlock()
		if stopped {
			return
		}
		for _, item := range reserved {
			scheduler.dispatch(item.task, item.mode)
		}
	}
}

func (scheduler *scheduler) shouldDrain() bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if !scheduler.dirty || !scheduler.enabled || scheduler.holds > 0 {
		return false
	}
	scheduler.dirty = false
	return true
}

type reservation struct {
	task Task
	mode string
}

func (scheduler *scheduler) invocationOf(id Id) *invocation {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.invocations[id]
}

// reserveEligible reserves every eligible task on the line.
func (scheduler *scheduler) reserveEligible() ([]reservation, error) {
	var out []reservation
	_, err := scheduler.session.commit(scheduler.ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		for _, task := range scheduler.session.liveTaskList() {
			if scheduler.kinds.get(task.Kind) == nil {
				continue
			}
			item, ok, err := scheduler.reserve(tx, task)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, item)
			}
		}
		return nil, nil
	}, commitOptions{})
	return out, err
}

func (scheduler *scheduler) reserve(tx *Tx, task Task) (reservation, bool, error) {
	current := scheduler.invocationOf(task.Id)
	if task.Abort {
		if current != nil {
			return reservation{}, false, nil
		}
		return scheduler.markRunning(tx, task, "abort")
	}
	if current != nil {
		return reservation{}, false, nil
	}
	if task.Status == TaskRunning {
		return reservation{task: task, mode: "run"}, true, nil
	}
	if task.Status != TaskPending {
		return reservation{}, false, nil
	}
	for _, dependency := range task.After {
		found, err := tx.Task(dependency)
		if err != nil {
			return reservation{}, false, err
		}
		if found == nil || found.Status != TaskTerminal {
			return reservation{}, false, nil
		}
	}
	return scheduler.markRunning(tx, task, "run")
}

func (scheduler *scheduler) markRunning(tx *Tx, task Task, mode string) (reservation, bool, error) {
	if task.Status == TaskPending {
		task.Status = TaskRunning
		if err := tx.setTask(task); err != nil {
			return reservation{}, false, err
		}
	}
	return reservation{task: task, mode: mode}, true, nil
}

func (scheduler *scheduler) dispatch(task Task, mode string) {
	kind := scheduler.kinds.get(task.Kind)
	if kind == nil {
		scheduler.onReport(fmt.Errorf("unknown kind %s for task %d", task.Kind, task.Id))
		return
	}
	ctx, cancel := context.WithCancelCause(scheduler.ctx)
	inv := &invocation{task: task, mode: mode, cancel: cancel, done: make(chan struct{})}
	scheduler.mu.Lock()
	if !scheduler.enabled || scheduler.holds > 0 {
		scheduler.dirty = true
		scheduler.mu.Unlock()
		cancel(nil)
		return
	}
	scheduler.invocations[task.Id] = inv
	scheduler.running.Add(1)
	scheduler.mu.Unlock()
	go func() {
		defer scheduler.running.Done()
		scheduler.invoke(ctx, inv, kind)
	}()
}

func (scheduler *scheduler) invoke(ctx context.Context, inv *invocation, kind *Kind) {
	defer close(inv.done)
	defer inv.cancel(nil)
	err := scheduler.runInvocation(ctx, inv.task, kind, inv.mode)
	if err != nil && ctx.Err() == nil {
		scheduler.onReport(err)
		scheduler.fault(inv.task, kind, err)
	}
	scheduler.mu.Lock()
	delete(scheduler.invocations, inv.task.Id)
	scheduler.mu.Unlock()
	if _, live := scheduler.session.liveTask(inv.task.Id); !live {
		if err := scheduler.session.retire(scheduler.ctx, inv.task); err != nil {
			scheduler.onReport(err)
		}
	}
	scheduler.kick()
}

func (scheduler *scheduler) runInvocation(ctx context.Context, task Task, kind *Kind, mode string) (err error) {
	defer recoverInto(&err)
	if mode == "abort" {
		return scheduler.runAbort(ctx, task, kind)
	}
	closure, err := scheduler.runPhases(ctx, task, kind)
	if err != nil || closure == nil || ctx.Err() != nil {
		return err
	}
	return scheduler.finishRun(ctx, task, kind, closure)
}

func (scheduler *scheduler) finishRun(ctx context.Context, task Task, kind *Kind, closure Closure) error {
	lease := scheduler.lease(ctx, task, kind, "run")
	defer lease.token.revoke()
	_, err := scheduler.session.commit(ctx, lease.invoker, func(lineCtx context.Context, tx *Tx, control TransactionControl) (any, error) {
		current, ok := scheduler.session.liveTask(task.Id)
		if !ok || current.Abort {
			return nil, nil
		}
		completion, err := closure(lineCtx, tx, current)
		if err != nil {
			return nil, err
		}
		if err := validateCompletion(kind, completion); err != nil {
			return nil, err
		}
		return nil, terminate(tx, control, task.Id, current, &Outcome{Status: completion.Status, Result: completion.Result, Failure: completion.Failure})
	}, commitOptions{closing: true})
	return err
}

// terminate writes a terminal outcome onto the latest overlay of a task.
func terminate(tx *Tx, control TransactionControl, id Id, current Task, outcome *Outcome) error {
	patched, err := tx.Task(id)
	if err != nil {
		return err
	}
	if patched == nil {
		patched = &current
	}
	stored, err := plain(*outcome)
	if err != nil {
		return err
	}
	next := *patched
	next.Status = TaskTerminal
	next.Outcome = &stored
	return control.SetTask(next)
}

func (scheduler *scheduler) runAbort(ctx context.Context, task Task, kind *Kind) error {
	if kind.Abort == nil {
		return &TaskContractFault{Kind: kind.Name, What: "no abort handler"}
	}
	handlerLease := scheduler.lease(ctx, task, kind, "abort")
	closure, err := func() (AbortClosure, error) {
		defer handlerLease.token.revoke()
		return kind.Abort(ctx, task.clone(), handlerLease.runtime)
	}()
	if err != nil {
		return err
	}
	closureLease := scheduler.lease(ctx, task, kind, "abort")
	defer closureLease.token.revoke()
	_, err = scheduler.session.commit(ctx, closureLease.invoker, func(lineCtx context.Context, tx *Tx, control TransactionControl) (any, error) {
		current, ok := scheduler.session.liveTask(task.Id)
		if !ok {
			return nil, nil
		}
		result, err := closure(lineCtx, tx, current)
		if err != nil {
			return nil, err
		}
		stored, err := ToStored(result)
		if err != nil {
			return nil, err
		}
		return nil, terminate(tx, control, task.Id, current, &Outcome{Status: OutcomeAborted, Result: stored})
	}, commitOptions{closing: true})
	return err
}

// fault terminalizes a task whose kind broke its contract or threw.
func (scheduler *scheduler) fault(task Task, kind *Kind, cause error) {
	message := fmt.Sprintf("%s: %s", kind.Name, errorString(cause))
	if contractFault, ok := errors.AsType[*TaskContractFault](cause); ok {
		message = contractFault.Error()
	}
	_, err := scheduler.session.commit(scheduler.ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		current, ok := scheduler.session.liveTask(task.Id)
		if !ok {
			return nil, nil
		}
		current.Status = TaskTerminal
		current.Outcome = &Outcome{Status: OutcomeFaulted, Error: message}
		if err := tx.setTask(current); err != nil {
			return nil, err
		}
		if !kind.Turn {
			return nil, nil
		}
		inputs := idList(asObject(current.Input)["inputs"])
		if err := tx.resolveInputs(inputs, InputResolution{Status: InputUnanswered, Reason: "failed", Detail: message}); err != nil {
			return nil, err
		}
		sticky, err := tx.raw(StickyDoc(current.ConversationId))
		if err != nil {
			return nil, err
		}
		sticky["turn"] = JsonObject{"tools": []any{}}
		return nil, nil
	}, commitOptions{docs: []DocRef{StickyDoc(task.ConversationId)}})
	if err != nil {
		scheduler.onReport(err)
	}
}

type lease struct {
	token   *InvocationToken
	invoker invoker
	runtime *Runtime
}

func (scheduler *scheduler) lease(ctx context.Context, task Task, kind *Kind, mode string) lease {
	token := newInvocationToken(task.Id, mode)
	conversationId := task.ConversationId
	authority := invoker{kind: invokerTask, token: token, id: task.Id, conversationId: &conversationId, taskKind: kind, core: IsCoreKind(task.Kind), mode: mode}
	return lease{token: token, invoker: authority, runtime: scheduler.runtime(task, authority, ctx)}
}

func handlerFor(kind *Kind, phase string) (PhaseHandler, error) {
	if phase == "" {
		if kind.Initial == nil {
			return nil, &TaskContractFault{Kind: kind.Name, What: "no handler for phase initial"}
		}
		return kind.Initial, nil
	}
	handler, ok := kind.Phases[phase]
	if !ok || handler == nil {
		return nil, &TaskContractFault{Kind: kind.Name, What: "no handler for phase " + phase}
	}
	return handler, nil
}

func validateStep(kind *Kind, step Step) error {
	if step.Done != nil || step.Build != nil {
		return nil
	}
	if step.Next != nil {
		phase, ok := step.Next["phase"].(string)
		if !ok {
			return &TaskContractFault{Kind: kind.Name, What: "handler returned an invalid step"}
		}
		if _, known := kind.Phases[phase]; !known {
			return &TaskContractFault{Kind: kind.Name, What: "transition to unknown phase " + phase}
		}
		return nil
	}
	return &TaskContractFault{Kind: kind.Name, What: "handler returned an invalid step"}
}

func validateCompletion(kind *Kind, completion Completion) error {
	if completion.Status == OutcomeCompleted || completion.Status == OutcomeFailed {
		return nil
	}
	return &TaskContractFault{Kind: kind.Name, What: "closure returned an invalid completion"}
}

func validateCheckpoint(kind *Kind, checkpoint Checkpoint) (Checkpoint, error) {
	phase, ok := checkpoint["phase"].(string)
	if !ok {
		return nil, &TaskContractFault{Kind: kind.Name, What: "invalid checkpoint"}
	}
	if _, known := kind.Phases[phase]; !known {
		return nil, &TaskContractFault{Kind: kind.Name, What: "checkpoint names unknown phase " + phase}
	}
	if kind.isInflight(phase) {
		return nil, &TaskContractFault{Kind: kind.Name, What: fmt.Sprintf("transition into in-flight phase %s; write it with rt.commit before the effect instead", phase)}
	}
	stored, err := plain(checkpoint)
	if err != nil {
		return nil, &TaskContractFault{Kind: kind.Name, What: "invalid checkpoint"}
	}
	return stored, nil
}

// runPhases runs the phase loop. Each handler gets a capability lease revoked
// when that handler returns.
func (scheduler *scheduler) runPhases(ctx context.Context, task Task, kind *Kind) (Closure, error) {
	current := task
	for {
		step, err := scheduler.runHandler(ctx, task, kind, current)
		if err != nil {
			return nil, err
		}
		if step.Done != nil {
			return step.Done, nil
		}
		if ctx.Err() != nil {
			return nil, nil
		}
		outcome, next, err := scheduler.transition(ctx, task, kind, step)
		if err != nil {
			return nil, err
		}
		switch outcome {
		case "retry":
			if live, ok := scheduler.session.liveTask(task.Id); ok {
				current = live
			}
		case "advanced":
			current = next
		default:
			return nil, nil
		}
	}
}

func (scheduler *scheduler) runHandler(ctx context.Context, task Task, kind *Kind, current Task) (step Step, err error) {
	handler, err := handlerFor(kind, Phase(current.Checkpoint))
	if err != nil {
		return Step{}, err
	}
	handlerLease := scheduler.lease(ctx, task, kind, "run")
	defer handlerLease.token.revoke()
	step, err = handler(ctx, current.clone(), handlerLease.runtime)
	if err != nil {
		return Step{}, err
	}
	return step, validateStep(kind, step)
}

func (scheduler *scheduler) transition(ctx context.Context, task Task, kind *Kind, step Step) (string, Task, error) {
	transitionLease := scheduler.lease(ctx, task, kind, "run")
	defer transitionLease.token.revoke()
	var next Task
	result, err := scheduler.session.commit(ctx, transitionLease.invoker, func(lineCtx context.Context, tx *Tx, control TransactionControl) (any, error) {
		live, ok := scheduler.session.liveTask(task.Id)
		if !ok || live.Abort {
			return "discard", nil
		}
		chosen := Transition{Checkpoint: step.Next}
		if step.Build != nil {
			var err error
			if chosen, err = step.Build(lineCtx, tx, live); err != nil {
				return nil, err
			}
		}
		if chosen.Retry {
			return "retry", nil
		}
		if chosen.Completion != nil {
			if err := validateCompletion(kind, *chosen.Completion); err != nil {
				return nil, err
			}
			return "terminal", terminate(tx, control, task.Id, live, &Outcome{Status: chosen.Completion.Status, Result: chosen.Completion.Result, Failure: chosen.Completion.Failure})
		}
		checkpoint, err := validateCheckpoint(kind, chosen.Checkpoint)
		if err != nil {
			return nil, err
		}
		if err := tx.Checkpoint(checkpoint); err != nil {
			return nil, err
		}
		next = live
		next.Checkpoint = checkpoint
		return "advanced", nil
	}, commitOptions{})
	if err != nil {
		return "", Task{}, err
	}
	outcome, _ := result.Value.(string)
	return outcome, next, nil
}

// abortTask marks a task, signals and joins its run invocation, and lets the
// next drain reserve the abort.
func (scheduler *scheduler) abortTask(ctx context.Context, id Id) (string, error) {
	result, err := scheduler.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		task, err := tx.Task(id)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("task %d not found", id)
		}
		if task.Status == TaskTerminal {
			return "terminal", nil
		}
		if !task.Abort {
			task.Abort = true
			if err := tx.setTask(*task); err != nil {
				return nil, err
			}
		}
		return "marked", nil
	}, commitOptions{})
	if err != nil {
		return "", err
	}
	if result.Value == "terminal" {
		return "terminal", nil
	}
	if inv := scheduler.invocationOf(id); inv != nil && inv.mode == "run" {
		inv.cancel(errInvocationAborted)
		<-inv.done
	}
	scheduler.kick()
	return "marked", nil
}

// waitForTask registers on the line atomically with the state read and waits
// off the line.
func (scheduler *scheduler) waitForTask(ctx context.Context, id Id) (Task, error) {
	if ctx.Err() != nil {
		return Task{}, context.Cause(ctx)
	}
	waiter := make(chan Task, 1)
	value, err := scheduler.session.onLine(ctx, func(lineCtx context.Context) (any, error) {
		if _, live := scheduler.session.liveTask(id); !live {
			stored, err := scheduler.session.storage.Task(lineCtx, id)
			if err != nil || stored != nil {
				return stored, err
			}
		}
		scheduler.mu.Lock()
		if scheduler.taskWaiters[id] == nil {
			scheduler.taskWaiters[id] = map[chan Task]bool{}
		}
		scheduler.taskWaiters[id][waiter] = true
		scheduler.mu.Unlock()
		return nil, nil
	})
	if err != nil {
		return Task{}, err
	}
	if stored, ok := value.(*Task); ok && stored != nil {
		return *stored, nil
	}
	select {
	case task := <-waiter:
		return task, nil
	case <-ctx.Done():
		scheduler.mu.Lock()
		delete(scheduler.taskWaiters[id], waiter)
		scheduler.mu.Unlock()
		return Task{}, context.Cause(ctx)
	}
}

// waitForInput resolves when an input is done or unanswered.
func (scheduler *scheduler) waitForInput(ctx context.Context, id Id) (Input, error) {
	if ctx.Err() != nil {
		return Input{}, context.Cause(ctx)
	}
	waiter := make(chan Input, 1)
	value, err := scheduler.session.onLine(ctx, func(lineCtx context.Context) (any, error) {
		input, err := scheduler.session.storage.Input(lineCtx, id)
		if err != nil {
			return nil, err
		}
		if input != nil && (input.Status == InputDone || input.Status == InputUnanswered) {
			return input, nil
		}
		scheduler.mu.Lock()
		if scheduler.inputWaiters[id] == nil {
			scheduler.inputWaiters[id] = map[chan Input]bool{}
		}
		scheduler.inputWaiters[id][waiter] = true
		scheduler.mu.Unlock()
		return nil, nil
	})
	if err != nil {
		return Input{}, err
	}
	if input, ok := value.(*Input); ok && input != nil {
		return *input, nil
	}
	select {
	case input := <-waiter:
		return input, nil
	case <-ctx.Done():
		scheduler.mu.Lock()
		delete(scheduler.inputWaiters[id], waiter)
		scheduler.mu.Unlock()
		return Input{}, context.Cause(ctx)
	}
}

// waitForIdle resolves when no live foreground task remains.
func (scheduler *scheduler) waitForIdle(ctx context.Context, conversationId *Id) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	waiter := &idleWaiter{conversationId: conversationId, ready: make(chan struct{})}
	value, err := scheduler.session.onLine(ctx, func(context.Context) (any, error) {
		if scheduler.isIdle(conversationId) {
			return true, nil
		}
		scheduler.mu.Lock()
		scheduler.idleWaiters[waiter] = true
		scheduler.mu.Unlock()
		return false, nil
	})
	if err != nil || value == true {
		return err
	}
	select {
	case <-waiter.ready:
		return nil
	case <-ctx.Done():
		scheduler.mu.Lock()
		delete(scheduler.idleWaiters, waiter)
		scheduler.mu.Unlock()
		return context.Cause(ctx)
	}
}

func (scheduler *scheduler) isIdle(conversationId *Id) bool {
	for _, task := range scheduler.session.liveTaskList() {
		if (conversationId == nil || task.ConversationId == *conversationId) && !task.Background {
			return false
		}
	}
	return true
}

func (scheduler *scheduler) checkIdle() {
	scheduler.mu.Lock()
	draining := scheduler.draining
	waiters := make([]*idleWaiter, 0, len(scheduler.idleWaiters))
	for waiter := range scheduler.idleWaiters {
		waiters = append(waiters, waiter)
	}
	scheduler.mu.Unlock()
	if draining {
		return
	}
	for _, waiter := range waiters {
		if !scheduler.isIdle(waiter.conversationId) {
			continue
		}
		scheduler.mu.Lock()
		if scheduler.idleWaiters[waiter] {
			delete(scheduler.idleWaiters, waiter)
			close(waiter.ready)
		}
		scheduler.mu.Unlock()
	}
}

// joinAll signals every invocation and waits for them; nothing is written.
func (scheduler *scheduler) joinAll() {
	scheduler.mu.Lock()
	scheduler.enabled = false
	invocations := make([]*invocation, 0, len(scheduler.invocations))
	for _, inv := range scheduler.invocations {
		invocations = append(invocations, inv)
	}
	scheduler.mu.Unlock()
	for _, inv := range invocations {
		inv.cancel(errInvocationAborted)
	}
	scheduler.drains.Wait()
	scheduler.running.Wait()
}

func (scheduler *scheduler) quiescent() bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return len(scheduler.invocations) == 0
}

package runtime

// Ports packages/agent/src/harness/runtime/lane.ts.

import (
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// EmitBatch binds a batch synchronously and returns its deferred delivery. Delivery runs after the Session mutation line is released.
type EmitBatch func(harness.Context, []agentharness.HarnessEvent) (func() error, error)

// LaneWatchInstaller binds a watcher before capturing a lane snapshot.
type LaneWatchInstaller func(harness.Context, agentharness.LaneSnapshot, func(agentharness.HarnessEvent) bool, agentharness.ResnapshotCapture[agentharness.LaneSnapshot]) (agentharness.WatchHandle[agentharness.LaneSnapshot], error)

// Lane owns the live projection of one durable lane. Commands serialize through its Session. State and ActiveDrive are command-owned; concurrent observers use SnapshotState and CurrentDrive.
type Lane struct {
	name        string
	Session     session.Session
	Models      Models
	Hooks       *agentharness.HookRegistry
	State       LaneState
	ActiveDrive *Drive
	ClosedError error

	mu           sync.RWMutex
	stateChange  chan struct{}
	idleOwner    chan struct{}
	onFault      func(harness.Context, error) error
	prepareBatch EmitBatch
	installWatch LaneWatchInstaller
	readConfig   func() Config
}

// NewLane binds one restored lane to its process-local collaborators.
func NewLane(name string, sess session.Session, models Models, hooks *agentharness.HookRegistry, state LaneState, onFault func(harness.Context, error) error, emitBatch EmitBatch, installWatch LaneWatchInstaller, readConfig func() Config) *Lane {
	return &Lane{name: name, Session: sess, Models: models, Hooks: hooks, State: state, onFault: onFault, prepareBatch: emitBatch, installWatch: installWatch, readConfig: readConfig, stateChange: make(chan struct{})}
}

func (lane *Lane) Name() string       { return lane.name }
func (lane *Lane) ReadConfig() Config { return lane.readConfig() }
func (lane *Lane) SnapshotState() LaneState {
	lane.mu.RLock()
	defer lane.mu.RUnlock()
	return lane.State
}
func (lane *Lane) CurrentDrive() *Drive {
	lane.mu.RLock()
	defer lane.mu.RUnlock()
	return lane.ActiveDrive
}
func (lane *Lane) AssertOpen() error {
	lane.mu.RLock()
	defer lane.mu.RUnlock()
	return lane.ClosedError
}
func (lane *Lane) Fault(ctx harness.Context, err error) error { return lane.onFault(ctx, err) }

// EmitBatch publishes effects outside the mutation line and waits for delivery.
func (lane *Lane) EmitBatch(ctx harness.Context, events []agentharness.HarnessEvent) error {
	delivery, err := lane.prepareBatch(ctx, events)
	if err != nil {
		return err
	}
	if delivery != nil {
		return delivery()
	}
	return nil
}

func (lane *Lane) signalStateChangeLocked() {
	close(lane.stateChange)
	lane.stateChange = make(chan struct{})
}

// Seal refuses new work, rejects drive observation, and returns the completion of any already-running idle callback. Admitted commits may still publish.
func (lane *Lane) Seal(err error) <-chan struct{} {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.ClosedError == nil {
		lane.ClosedError = err
	}
	if lane.ActiveDrive != nil {
		lane.ActiveDrive.CloseGate(err)
	}
	lane.signalStateChangeLocked()
	if lane.idleOwner != nil {
		return lane.idleOwner
	}
	done := make(chan struct{})
	close(done)
	return done
}

func lanePanic(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

func (lane *Lane) trusted(ctx harness.Context, action func() (any, error)) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = lanePanic(recovered)
		}
		if err != nil {
			if closed := lane.AssertOpen(); closed != nil {
				err = closed
			} else {
				err = lane.Fault(ctx, err)
			}
		}
	}()
	return action()
}

// readLane permits coherent bounded reads from a runWhenIdle callback without acquiring idle ownership again.
func (lane *Lane) readLane(ctx harness.Context, read func(LaneState, session.SessionReader) (any, error)) (any, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	return lane.Session.Mutate(ctx, func(ctx harness.Context, reader session.SessionMutator) (any, error) {
		if err := lane.AssertOpen(); err != nil {
			return nil, err
		}
		return lane.trusted(ctx, func() (any, error) { return read(lane.SnapshotState(), reader) })
	})
}

type laneCommandOutcome struct {
	result        any
	rejection     error
	delivery      func() error
	owner, change <-chan struct{}
}

// commandAwaiter is a Promise-valued return. Waiting belongs outside the mutation line; materialization must instead produce a synchronous value.
type commandAwaiter interface{ Wait() (any, error) }

func waitLaneChange(ctx harness.Context, owner, change <-chan struct{}) error {
	if ctx.Err() != nil {
		return harness.AbortError(ctx)
	}
	select {
	case <-owner:
		return nil
	case <-change:
		return nil
	case <-ctx.Done():
		return harness.AbortError(ctx)
	}
}

// Command plans against the latest committed state. Commit, memory publication, materialization and event binding precede release of the Session line. Expected rejections and delivery occur outside the fault boundary.
func (lane *Lane) Command(ctx harness.Context, plan func(LaneState, session.SessionReader) (LaneCommand[any], error)) (any, error) {
	job, err := lane.CommandAsync(ctx, plan)
	if err != nil {
		return nil, err
	}
	return job.Wait()
}

// CommandJob owns an admitted asynchronous command through event delivery and result observation.
type CommandJob struct {
	done  chan struct{}
	value any
	err   error
}

func (job *CommandJob) Done() <-chan struct{} { return job.done }
func (job *CommandJob) Wait() (any, error)    { <-job.done; return job.value, job.err }

// CommandAsync admits an ordinary command synchronously to the Session line. Idle-owned commands await that owner before admission. Callers join the job to observe failures and completion.
func (lane *Lane) CommandAsync(ctx harness.Context, plan func(LaneState, session.SessionReader) (LaneCommand[any], error)) (*CommandJob, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	lane.mu.RLock()
	owner := lane.idleOwner
	lane.mu.RUnlock()
	var admitted *session.LineJob
	var err error
	if owner == nil {
		admitted, err = lane.Session.EnqueueMutation(ctx, func(ctx harness.Context, mutator session.SessionMutator) (any, error) {
			return lane.planCommand(ctx, mutator, plan)
		})
		if err != nil {
			return nil, err
		}
	}
	job := &CommandJob{done: make(chan struct{})}
	go func() {
		defer close(job.done)
		defer func() {
			if recovered := recover(); recovered != nil {
				job.err = lanePanic(recovered)
			}
		}()
		job.value, job.err = lane.awaitCommand(ctx, admitted, plan)
	}()
	return job, nil
}

func (lane *Lane) awaitCommand(ctx harness.Context, admitted *session.LineJob, plan func(LaneState, session.SessionReader) (LaneCommand[any], error)) (any, error) {
	for {
		if admitted == nil {
			if err := lane.AssertOpen(); err != nil {
				return nil, err
			}
			lane.mu.RLock()
			owner, change := lane.idleOwner, lane.stateChange
			lane.mu.RUnlock()
			if owner != nil {
				if err := waitLaneChange(ctx, owner, change); err != nil {
					return nil, err
				}
				continue
			}
			var err error
			admitted, err = lane.Session.EnqueueMutation(ctx, func(ctx harness.Context, mutator session.SessionMutator) (any, error) {
				return lane.planCommand(ctx, mutator, plan)
			})
			if err != nil {
				return nil, err
			}
		}
		raw, err := admitted.Wait()
		admitted = nil
		if err != nil {
			if closed := lane.AssertOpen(); closed != nil {
				return nil, closed
			}
			return nil, err
		}
		outcome := raw.(laneCommandOutcome)
		if outcome.owner != nil {
			if err := waitLaneChange(ctx, outcome.owner, outcome.change); err != nil {
				return nil, err
			}
			continue
		}
		if outcome.rejection != nil {
			return nil, outcome.rejection
		}
		if outcome.delivery != nil {
			if err := outcome.delivery(); err != nil {
				return nil, err
			}
		}
		if async, ok := outcome.result.(commandAwaiter); ok {
			return async.Wait()
		}
		return outcome.result, nil
	}
}

func (lane *Lane) planCommand(ctx harness.Context, mutator session.SessionMutator, plan func(LaneState, session.SessionReader) (LaneCommand[any], error)) (any, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	lane.mu.RLock()
	owner, change := lane.idleOwner, lane.stateChange
	lane.mu.RUnlock()
	if owner != nil {
		return laneCommandOutcome{owner: owner, change: change}, nil
	}
	return lane.trusted(ctx, func() (any, error) {
		decision, err := plan(lane.SnapshotState(), mutator)
		if err != nil {
			return nil, err
		}
		switch decision.Kind {
		case CommandReturn:
			return laneCommandOutcome{result: decision.Result}, nil
		case CommandReject:
			return laneCommandOutcome{rejection: decision.Error}, nil
		case CommandCommit:
			commit, err := mutator.Commit(ctx, decision.Writes)
			if err != nil {
				return nil, err
			}
			lane.mu.Lock()
			lane.State = decision.Next
			lane.signalStateChangeLocked()
			lane.mu.Unlock()
			result := decision.Materialize(commit)
			if _, async := result.(commandAwaiter); async {
				return nil, fmt.Errorf("Lane command materialize() must be synchronous")
			}
			var delivery func() error
			if decision.Events != nil {
				events := decision.Events(commit)
				if len(events) > 0 {
					delivery, err = lane.prepareBatch(ctx, events)
					if err != nil {
						return nil, err
					}
				}
			}
			return laneCommandOutcome{result: result, delivery: delivery}, nil
		default:
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Invalid lane command %q", decision.Kind)}
		}
	})
}

// Command is the typed form of Lane.Command (Go has no generic methods).
func Command[T any](ctx harness.Context, lane *Lane, plan func(LaneState, session.SessionReader) (LaneCommand[T], error)) (T, error) {
	raw, err := lane.Command(ctx, func(state LaneState, reader session.SessionReader) (LaneCommand[any], error) {
		decision, err := plan(state, reader)
		converted := LaneCommand[any]{Kind: decision.Kind, Writes: decision.Writes, Events: decision.Events, Next: decision.Next, Result: decision.Result, Error: decision.Error}
		if decision.Materialize != nil {
			converted.Materialize = func(commit session.CommitResult) any { return decision.Materialize(commit) }
		}
		return converted, err
	})
	var zero T
	if err != nil {
		return zero, err
	}
	if raw == nil {
		return zero, nil
	}
	return raw.(T), nil
}

func durableLaneState(state LaneState, current *string, inbox []session.InboxItem, last *string) session.LaneState {
	return session.LaneState{CurrentOperationID: current, LastOperationID: last, Inbox: inbox}
}

func patchLane(state LaneState, patch *LanePatch) LaneState {
	if patch == nil {
		return state
	}
	if patch.SetTipID {
		state.TipID = patch.TipID
	}
	if patch.SetConfiguration {
		state.Configuration = patch.Configuration
	}
	if patch.SetInbox {
		state.Inbox = patch.Inbox
	}
	return state
}

// SettleOperation settles admitted work against the latest durable control, including cancellation. The capability identifies the caller's leaf type, not a stale state snapshot to publish.
func (lane *Lane) SettleOperation(ctx harness.Context, _ session.OperationState, plan func(LaneState, session.OperationState, session.OperationMeta, session.SessionReader) (OperationCommand[any], error)) (any, error) {
	return lane.Command(ctx, func(state LaneState, reader session.SessionReader) (LaneCommand[any], error) {
		operation := state.Operation
		if operation == nil {
			return LaneCommand[any]{}, &session.SessionInvariantError{Message: "Operation settlement requires a current operation"}
		}
		decision, err := plan(state, operation.State, operation.Meta, reader)
		if err != nil {
			return LaneCommand[any]{}, err
		}
		result := LaneCommand[any]{Kind: decision.Kind, Result: decision.Result, Materialize: decision.Materialize, Events: decision.Events, Writes: append([]session.Write{}, decision.Writes...), Next: patchLane(state, decision.Lane)}
		switch decision.Kind {
		case CommandCommit:
			result.Writes = append(result.Writes, session.SetValue(session.OperationStateValue(operation.Meta.OperationID), decision.OperationState))
			if decision.Lane != nil && decision.Lane.SetInbox {
				result.Writes = append(result.Writes, session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, &operation.Meta.OperationID, decision.Lane.Inbox, state.LastOperationID)))
			}
			result.Next.Operation = &session.Operation{Meta: operation.Meta, State: decision.OperationState}
		case CommandFinish:
			result.Kind = CommandCommit
			result.Writes = append(result.Writes, session.SetValue(session.OperationResult(operation.Meta.OperationID), decision.Record), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, nil, result.Next.Inbox, &operation.Meta.OperationID)))
			result.Next.LastOperationID = &operation.Meta.OperationID
			result.Next.Operation = nil
		}
		return result, nil
	})
}

// ContinueOperation refuses ordinary forward progress after cancellation without invoking its planner.
func (lane *Lane) ContinueOperation(ctx harness.Context, capability session.OperationState, plan func(LaneState, session.OperationState, session.OperationMeta, session.SessionReader) (OperationCommand[any], error)) (ContinueOperationResult[any], error) {
	raw, err := lane.SettleOperation(ctx, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[any], error) {
		if current.Control.Status == session.ControlCancelRequested {
			return OperationCommand[any]{Kind: CommandReturn, Result: ContinueOperationResult[any]{CancelRequested: true}}, nil
		}
		decision, err := plan(state, current, meta, reader)
		if err != nil {
			return decision, err
		}
		if decision.Kind == CommandReturn {
			decision.Result = ContinueOperationResult[any]{Value: decision.Result}
		} else {
			materialize := decision.Materialize
			decision.Materialize = func(commit session.CommitResult) any { return ContinueOperationResult[any]{Value: materialize(commit)} }
		}
		return decision, nil
	})
	if err != nil {
		return ContinueOperationResult[any]{}, err
	}
	return raw.(ContinueOperationResult[any]), nil
}

func eraseOperationCommand[T any](decision OperationCommand[T]) OperationCommand[any] {
	converted := OperationCommand[any]{Kind: decision.Kind, Writes: decision.Writes, Events: decision.Events, OperationState: decision.OperationState, Record: decision.Record, Lane: decision.Lane, Result: decision.Result}
	if decision.Materialize != nil {
		converted.Materialize = func(commit session.CommitResult) any { return decision.Materialize(commit) }
	}
	return converted
}

// SettleOperation is the typed form of Lane.SettleOperation.
func SettleOperation[T any](ctx harness.Context, lane *Lane, capability session.OperationState, plan func(LaneState, session.OperationState, session.OperationMeta, session.SessionReader) (OperationCommand[T], error)) (T, error) {
	raw, err := lane.SettleOperation(ctx, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[any], error) {
		decision, err := plan(state, current, meta, reader)
		return eraseOperationCommand(decision), err
	})
	var zero T
	if err != nil || raw == nil {
		return zero, err
	}
	return raw.(T), nil
}

// ContinueOperation is the typed form of Lane.ContinueOperation.
func ContinueOperation[T any](ctx harness.Context, lane *Lane, capability session.OperationState, plan func(LaneState, session.OperationState, session.OperationMeta, session.SessionReader) (OperationCommand[T], error)) (ContinueOperationResult[T], error) {
	raw, err := lane.ContinueOperation(ctx, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[any], error) {
		decision, err := plan(state, current, meta, reader)
		return eraseOperationCommand(decision), err
	})
	result := ContinueOperationResult[T]{CancelRequested: raw.CancelRequested}
	if err == nil && raw.Value != nil {
		result.Value = raw.Value.(T)
	}
	return result, err
}

package runtime

// Ports packages/agent/src/harness/runtime/lane.ts.

import (
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

type driveClaim struct {
	drive               *Drive
	installed, occupied bool
	outcome             *session.OperationResultRecord
	mismatch            error
}

// Drive installs one owned pass or joins an existing pass. Caller cancellation affects only observation, while close rejects observation without waiting for non-cooperative effects.
func (lane *Lane) Drive(ctx harness.Context, options agentharness.DriveOptions) (agentharness.DriveOutcome, error) {
	if err := lane.expectedOpen(); err != nil {
		return agentharness.DriveOutcome{}, err
	}
	for {
		claim, err := Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[driveClaim], error) {
			if ctx.Err() != nil {
				return LaneCommand[driveClaim]{Kind: CommandReject, Error: harness.AbortError(ctx)}, nil
			}
			if state.Operation != nil && state.Operation.Meta.OperationID == options.OperationID {
				lane.mu.Lock()
				defer lane.mu.Unlock()
				if lane.ActiveDrive == nil {
					drive := NewDrive(ctx, options)
					lane.ActiveDrive = drive
					lane.signalStateChangeLocked()
					return LaneCommand[driveClaim]{Kind: CommandReturn, Result: driveClaim{drive: drive, installed: true}}, nil
				}
				return LaneCommand[driveClaim]{Kind: CommandReturn, Result: driveClaim{drive: lane.ActiveDrive, occupied: lane.ActiveDrive.OperationID != options.OperationID}}, nil
			}
			stored, err := session.GetValue(ctx, reader, session.OperationResult(options.OperationID))
			if err != nil {
				return LaneCommand[driveClaim]{}, err
			}
			if stored != nil {
				return LaneCommand[driveClaim]{Kind: CommandReturn, Result: driveClaim{outcome: &stored.Value}}, nil
			}
			return LaneCommand[driveClaim]{Kind: CommandReturn, Result: driveClaim{mismatch: lane.Mismatch(options.OperationID, currentOperationID(state), state.LastOperationID)}}, nil
		})
		if err != nil {
			return agentharness.DriveOutcome{}, err
		}
		if claim.outcome != nil {
			return agentharness.DriveOutcome{Kind: agentharness.DriveSettled, Outcome: claim.outcome}, nil
		}
		if claim.mismatch != nil {
			return agentharness.DriveOutcome{}, claim.mismatch
		}
		if claim.occupied {
			if _, err := claim.drive.Completion(ctx); err != nil {
				return agentharness.DriveOutcome{}, err
			}
			continue
		}
		if claim.installed {
			go lane.runDrive(claim.drive)
		}
		return claim.drive.Completion(ctx)
	}
}

func (lane *Lane) runDrive(drive *Drive) {
	var outcome agentharness.DriveOutcome
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = lanePanic(recovered)
			}
		}()
		outcome, err = DriveOperation(drive.Context, lane, drive)
	}()
	drive.beginSettlement()
	if err != nil {
		if closed := lane.AssertOpen(); closed != nil {
			err = closed
		} else {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						err = lanePanic(recovered)
					}
				}()
				err = lane.Fault(drive.Context, err)
			}()
		}
	}
	lane.mu.Lock()
	if lane.ActiveDrive == drive {
		lane.ActiveDrive = nil
		lane.signalStateChangeLocked()
	}
	lane.mu.Unlock()
	if err != nil {
		drive.Fail(err)
	} else {
		drive.Settle(outcome)
	}
}

// RequestOperationAbort closes effect admission before persisting cancellation and signals already-admitted effects only after the cancellation marker commits.
func (lane *Lane) RequestOperationAbort(ctx harness.Context, operationID string) (agentharness.AbortRequest, error) {
	if err := lane.expectedOpen(); err != nil {
		return agentharness.AbortRequest{}, err
	}
	drive := lane.CurrentDrive()
	if drive != nil && drive.OperationID != operationID {
		drive = nil
	}
	cancellation := make(chan struct{})
	var cancellationErr error
	var once sync.Once
	if drive != nil {
		drive.BeginAbort(func() error { <-cancellation; return cancellationErr })
	}
	settle := func(signal bool) {
		once.Do(func() {
			close(cancellation)
			if signal && drive != nil {
				drive.SignalAbort()
			}
		})
	}
	result, err := Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[agentharness.AbortRequest], error) {
		operation := state.Operation
		if operation == nil || operation.Meta.OperationID != operationID {
			return LaneCommand[agentharness.AbortRequest]{Kind: CommandReject, Error: lane.Mismatch(operationID, currentOperationID(state), state.LastOperationID)}, nil
		}
		if operation.State.Control.Status == session.ControlCancelRequested {
			return LaneCommand[agentharness.AbortRequest]{Kind: CommandReturn, Result: agentharness.AbortRequest{OperationID: operationID, Steer: []agent.AgentMessage{}, FollowUp: []agent.AgentMessage{}}}, nil
		}
		inbox := []session.InboxItem{}
		writes := []session.Write{}
		steer, followUp := []agent.AgentMessage{}, []agent.AgentMessage{}
		for _, item := range state.Inbox {
			if item.Kind != session.InboxSteer && item.Kind != session.InboxFollowUp {
				inbox = append(inbox, item)
				continue
			}
			stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(item.EntryID))
			if err != nil {
				return LaneCommand[agentharness.AbortRequest]{}, err
			}
			if stored == nil || stored.Value.Type != session.PendingEntryMessage {
				return LaneCommand[agentharness.AbortRequest]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its message", item.Kind, item.EntryID)}
			}
			if item.Kind == session.InboxSteer {
				steer = append(steer, stored.Value.Message)
			} else {
				followUp = append(followUp, stored.Value.Message)
			}
			writes = append(writes, session.DeleteValue(session.PendingEntryValue(item.EntryID)))
		}
		removed := len(writes) > 0
		queues, err := ReadLaneQueues(ctx, reader, inbox)
		if err != nil {
			return LaneCommand[agentharness.AbortRequest]{}, err
		}
		operationState := operation.State
		operationState.Control = session.Control{Status: session.ControlCancelRequested, RequestedAt: runtimeNow()}
		writes = append(writes, session.SetValue(session.OperationStateValue(operationID), operationState), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, &operationID, inbox, state.LastOperationID)))
		next := state
		next.Inbox = inbox
		next.Operation = &session.Operation{Meta: operation.Meta, State: operationState}
		return LaneCommand[agentharness.AbortRequest]{Kind: CommandCommit, Writes: writes, Next: next, Materialize: func(session.CommitResult) agentharness.AbortRequest {
			settle(true)
			return agentharness.AbortRequest{OperationID: operationID, NewlyRequested: true, Steer: steer, FollowUp: followUp}
		}, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			events := []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.OperationAbortPayload{OperationID: operationID, Steer: steer, FollowUp: followUp}}}
			if removed {
				events = append(events, agentharness.HarnessEvent{Lane: lane.name, Payload: agentharness.QueueUpdatePayload{Queues: queues}})
			}
			return events
		}}, nil
	})
	if err != nil {
		// An expected mismatch releases the stale gate's cancellation wait without signalling it.
		if taggedName(err) == "OperationMismatch" {
			settle(false)
		} else {
			once.Do(func() { cancellationErr = err; close(cancellation) })
		}
		return result, err
	}
	settle(!result.NewlyRequested)
	return result, nil
}
func (lane *Lane) RequestAbort(ctx harness.Context, operationID string) (agentharness.AbortRequest, error) {
	return lane.RequestOperationAbort(ctx, operationID)
}

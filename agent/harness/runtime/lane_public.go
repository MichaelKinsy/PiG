package runtime

// Ports packages/agent/src/harness/runtime/lane.ts.

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

var _ agentharness.AgentLane = (*Lane)(nil)

func (lane *Lane) Prompt(ctx harness.Context, text string, images []ai.ImageContent) (agentharness.RunOutcome, error) {
	return lane.driveRunRequest(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &text, Images: images})
}
func (lane *Lane) PromptMessages(ctx harness.Context, messages []agent.AgentMessage) (agentharness.RunOutcome, error) {
	return lane.driveRunRequest(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptMessages: messages})
}
func (lane *Lane) Skill(ctx harness.Context, name string, instructions *string) (agentharness.RunOutcome, error) {
	return lane.driveRunRequest(ctx, agentharness.OperationRequest{Kind: agentharness.RequestSkill, Name: name, AdditionalInstructions: instructions})
}
func (lane *Lane) PromptFromTemplate(ctx harness.Context, name string, args []string) (agentharness.RunOutcome, error) {
	return lane.driveRunRequest(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPromptTemplate, Name: name, Args: args})
}

func (lane *Lane) invariant(ctx harness.Context, message string) error {
	return lane.Fault(ctx, &session.SessionInvariantError{Message: message})
}
func taggedName(err error) string {
	if tagged, ok := err.(harness.TaggedError); ok { //nolint:errorlint // Upstream inspects the outer Result error tag, not a wrapped cause.
		return tagged.Tag()
	}
	return ""
}
func (lane *Lane) driveRunRequest(ctx harness.Context, request agentharness.OperationRequest) (agentharness.RunOutcome, error) {
	admission, err := lane.Accept(ctx, request)
	if err != nil {
		switch taggedName(err) {
		case "LaneBusy", "InvalidMessage", "UnknownSkill", "UnknownTemplate", "Closed":
			return agentharness.RunOutcome{}, err
		}
		if taggedName(err) == "" {
			return agentharness.RunOutcome{}, err
		}
		return agentharness.RunOutcome{}, lane.invariant(ctx, "Run acceptance returned "+taggedName(err))
	}
	driven, err := lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID, WaitForRetry: true})
	if err != nil {
		if taggedName(err) == "OperationMismatch" {
			return agentharness.RunOutcome{}, lane.invariant(ctx, fmt.Sprintf("Accepted run %s no longer matches its lane", admission.OperationID))
		}
		return agentharness.RunOutcome{}, err
	}
	return lane.runOutcome(ctx, admission.OperationID, "Run", driven)
}
func (lane *Lane) runOutcome(ctx harness.Context, id, description string, driven agentharness.DriveOutcome) (agentharness.RunOutcome, error) {
	if driven.Kind == agentharness.DriveSettled {
		return agentharness.RunOutcome{Record: driven.Outcome}, nil
	}
	if driven.Reason == agentharness.DriveWaitDeferred && driven.Deferred != nil {
		return agentharness.RunOutcome{Suspended: &agentharness.SuspendedRun{OperationID: id, Status: "suspended", Deferred: *driven.Deferred}}, nil
	}
	return agentharness.RunOutcome{}, lane.invariant(ctx, fmt.Sprintf("%s %s returned an unwaited retry", description, id))
}
func (lane *Lane) Compact(ctx harness.Context, instructions *string) (agentharness.CompactionOutcome, error) {
	admission, err := lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestCompaction, CustomInstructions: instructions})
	if err != nil {
		switch taggedName(err) {
		case "LaneBusy", "NothingToCompact", "Closed":
			return agentharness.CompactionOutcome{}, err
		}
		if taggedName(err) == "" {
			return agentharness.CompactionOutcome{}, err
		}
		return agentharness.CompactionOutcome{}, lane.invariant(ctx, "Compaction acceptance returned "+taggedName(err))
	}
	record, err := lane.driveStructuralAdmission(ctx, admission)
	if err != nil {
		return agentharness.CompactionOutcome{}, err
	}
	continuation, err := lane.continueAfterStructural(ctx, record)
	if err != nil {
		return agentharness.CompactionOutcome{}, err
	}
	return agentharness.CompactionOutcome{Compaction: record, Run: continuation}, nil
}
func (lane *Lane) NavigateTree(ctx harness.Context, target *string, options *agentharness.NavigateOptions) (agentharness.NavigationOutcome, error) {
	admission, err := lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestNavigation, TargetID: target, NavigateOptions: options})
	if err != nil {
		switch taggedName(err) {
		case "LaneBusy", "InvalidNavigation", "UnknownTarget", "Closed":
			return agentharness.NavigationOutcome{}, err
		}
		if taggedName(err) == "" {
			return agentharness.NavigationOutcome{}, err
		}
		return agentharness.NavigationOutcome{}, lane.invariant(ctx, "Navigation acceptance returned "+taggedName(err))
	}
	record, err := lane.driveStructuralAdmission(ctx, admission)
	if err != nil {
		return agentharness.NavigationOutcome{}, err
	}
	continuation, err := lane.continueAfterStructural(ctx, record)
	if err != nil {
		return agentharness.NavigationOutcome{}, err
	}
	return agentharness.NavigationOutcome{Navigation: record, Run: continuation}, nil
}
func (lane *Lane) driveStructuralAdmission(ctx harness.Context, admission agentharness.OperationAdmission) (session.OperationResultRecord, error) {
	driven, err := lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID, WaitForRetry: true})
	if err != nil {
		if taggedName(err) == "OperationMismatch" {
			return session.OperationResultRecord{}, lane.invariant(ctx, fmt.Sprintf("Accepted %s %s no longer matches its lane", admission.Kind, admission.OperationID))
		}
		return session.OperationResultRecord{}, err
	}
	if driven.Kind == agentharness.DriveSettled && driven.Outcome != nil {
		return *driven.Outcome, nil
	}
	return session.OperationResultRecord{}, lane.invariant(ctx, fmt.Sprintf("%s %s returned %s", admission.Kind, admission.OperationID, driven.Reason))
}
func (lane *Lane) continueAfterStructural(ctx harness.Context, record session.OperationResultRecord) (*agentharness.RunOutcome, error) {
	if record.Status == session.TerminalAborted {
		return nil, nil
	}
	empty := ""
	admission, err := lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &empty})
	if err != nil {
		switch taggedName(err) {
		case "InvalidMessage", "LaneBusy":
			return nil, nil
		case "Closed":
			return nil, err
		}
		if taggedName(err) == "" {
			return nil, err
		}
		return nil, lane.invariant(ctx, "Structural continuation acceptance returned "+taggedName(err))
	}
	driven, err := lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID, WaitForRetry: true})
	if err != nil {
		if taggedName(err) == "OperationMismatch" {
			return nil, lane.invariant(ctx, fmt.Sprintf("Continuation run %s no longer matches its lane", admission.OperationID))
		}
		return nil, err
	}
	outcome, err := lane.runOutcome(ctx, admission.OperationID, "Continuation run", driven)
	if err != nil {
		return nil, err
	}
	return &outcome, nil
}
func (lane *Lane) Resume(ctx harness.Context) (agentharness.RunOutcome, error) {
	if err := lane.expectedOpen(); err != nil {
		return agentharness.RunOutcome{}, err
	}
	id, err := Command(ctx, lane, func(state LaneState, _ session.SessionReader) (LaneCommand[string], error) {
		if state.Operation == nil {
			return LaneCommand[string]{Kind: CommandReject, Error: &harness.NothingToResume{Lane: lane.name, Message: fmt.Sprintf("Lane %q has no active operation to resume", lane.name)}}, nil
		}
		return LaneCommand[string]{Kind: CommandReturn, Result: state.Operation.Meta.OperationID}, nil
	})
	if err != nil {
		return agentharness.RunOutcome{}, err
	}
	driven, err := lane.Drive(ctx, agentharness.DriveOptions{OperationID: id, PollDeferred: true, WaitForRetry: true})
	if err != nil {
		if taggedName(err) == "OperationMismatch" {
			return agentharness.RunOutcome{}, lane.invariant(ctx, fmt.Sprintf("Operation %s no longer matches its lane", id))
		}
		return agentharness.RunOutcome{}, err
	}
	return lane.runOutcome(ctx, id, "Operation", driven)
}
func (lane *Lane) Abort(ctx harness.Context) (agentharness.AbortOutcome, error) {
	if err := lane.expectedOpen(); err != nil {
		return agentharness.AbortOutcome{}, err
	}
	id, err := Command(ctx, lane, func(state LaneState, _ session.SessionReader) (LaneCommand[*string], error) {
		return LaneCommand[*string]{Kind: CommandReturn, Result: currentOperationID(state)}, nil
	})
	if err != nil {
		return agentharness.AbortOutcome{}, err
	}
	if id == nil {
		return agentharness.AbortOutcome{}, &harness.NoActiveOperation{Lane: lane.name, Message: fmt.Sprintf("Lane %q has no active operation", lane.name)}
	}
	requested, err := lane.RequestAbort(ctx, *id)
	if err != nil {
		if taggedName(err) == "OperationMismatch" {
			return agentharness.AbortOutcome{}, &harness.NoActiveOperation{Lane: lane.name, Message: fmt.Sprintf("Lane %q no longer has the inspected operation", lane.name)}
		}
		return agentharness.AbortOutcome{}, err
	}
	_, err = lane.Drive(ctx, agentharness.DriveOptions{OperationID: *id})
	if err != nil {
		if taggedName(err) == "OperationMismatch" {
			return agentharness.AbortOutcome{}, lane.invariant(ctx, fmt.Sprintf("Cancelled operation %s no longer matches its lane", *id))
		}
		return agentharness.AbortOutcome{}, err
	}
	return agentharness.AbortOutcome{OperationID: *id, Steer: requested.Steer, FollowUp: requested.FollowUp}, nil
}

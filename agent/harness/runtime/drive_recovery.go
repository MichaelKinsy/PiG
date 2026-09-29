package runtime

// Ports packages/agent/src/harness/runtime/drive/recovery.ts

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func interruptedAssistantMessage(identity session.ModelRef, partial *ai.AssistantMessage) *ai.AssistantMessage {
	const warning = "Assistant request was interrupted. The preceding content is the latest committed partial; newer live output may be missing and the external outcome is unknown."
	message := ai.AssistantMessage{API: "unknown", Provider: identity.Provider, Model: identity.ModelID, Content: []ai.AssistantContentBlock{}, Timestamp: runtimeNow()}
	if partial != nil {
		message = *partial
	}
	message.Usage = ai.Usage{}
	message.StopReason = ai.StopReasonError
	message.ErrorMessage = warning
	return &message
}

func publishRecoveredResponse(ctx harness.Context, lane *Lane, drive *Drive, effect session.OperationState, frames []ai.AssistantMessageFrame) (ProcedureResult, error) {
	partial, err := ai.ReduceAssistantMessageFrames(frames)
	if err != nil {
		return ProcedureResult{}, err
	}
	identity := effect.Configuration.Model
	if effect.At == session.AtAssistantEffectPending {
		identity = effect.GenerationContext.Configuration.Model
	}
	message := interruptedAssistantMessage(identity, partial)
	agentMessage := agent.AgentMessage{Assistant: new(runtimeAssistantMessage(message))}
	if err := lane.EmitBatch(ctx, []agentharness.HarnessEvent{
		{Lane: lane.Name(), Recovery: true, Payload: agentharness.MessageStartPayload{RunID: drive.OperationID, Message: agentMessage}},
		{Lane: lane.Name(), Recovery: true, Payload: agentharness.MessageEndPayload{RunID: drive.OperationID, Message: agentMessage, EntryID: effect.ResponseEntryID}},
	}); err != nil {
		return ProcedureResult{}, err
	}
	return PublishResponse(ctx, lane, drive, effect, message, true)
}

// RecoverAssistantGeneration settles an orphan from its committed frame prefix without another provider request.
func RecoverAssistantGeneration(ctx harness.Context, lane *Lane, drive *Drive, generation session.OperationState) (ProcedureResult, error) {
	frames, err := ContinueOperation(ctx, lane, generation, func(_ LaneState, _ session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[[]ai.AssistantMessageFrame], error) {
		frames, err := ReadAssistantFrames(ctx, reader, drive.OperationID, generation.ResponseEntryID)
		return OperationCommand[[]ai.AssistantMessageFrame]{Kind: CommandReturn, Result: frames}, err
	})
	if err != nil {
		return ProcedureResult{}, err
	}
	if frames.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	return publishRecoveredResponse(ctx, lane, drive, generation, frames.Value)
}

// RecoverCancelledAssistantEffect settles a cancelled orphan under its reserved response and usage ids.
func RecoverCancelledAssistantEffect(ctx harness.Context, lane *Lane, drive *Drive, effect session.OperationState) (ProcedureResult, error) {
	frames, err := SettleOperation(ctx, lane, effect, func(_ LaneState, _ session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[[]ai.AssistantMessageFrame], error) {
		frames, err := ReadAssistantFrames(ctx, reader, drive.OperationID, effect.ResponseEntryID)
		return OperationCommand[[]ai.AssistantMessageFrame]{Kind: CommandReturn, Result: frames}, err
	})
	if err != nil {
		return ProcedureResult{}, err
	}
	return publishRecoveredResponse(ctx, lane, drive, effect, frames)
}

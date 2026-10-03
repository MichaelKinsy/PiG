package services

// Ports packages/coding-agent/src/experimental/services/agent-controller.ts (Promise admission through the selected service view).

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// AgentControllerInitiator separates the admitted Promise from its observer without changing the AgentController wire contract. Begin methods preserve the operation context; Wait uses a separate observer context. A selected local override must expose its own admission boundary.
type AgentControllerInitiator interface {
	BeginPrompt(context.Context, AgentPromptRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error)
	BeginAbort(context.Context) (*chord.ServiceInvocation, error)
	BeginSteer(context.Context, AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error)
	BeginFollowUp(context.Context, AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error)
	BeginCancelQueued(context.Context, string) (*chord.ServiceResultInvocation[AgentCancelQueuedResponse], error)
	BeginCompact(context.Context, AgentCompactionRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error)
	BeginWaitForPrompt(context.Context, string) (*chord.ServiceResultInvocation[AgentPromptResult], error)
}

// errSelectedAdmission reports the selected controller's missing admission while remaining identifiable as chord.ErrInvocationAdmissionUnavailable.
var errSelectedAdmission error = selectedAdmissionError{}

type selectedAdmissionError struct{}

func (selectedAdmissionError) Error() string {
	return "Selected AgentController does not expose invocation admission"
}
func (selectedAdmissionError) Unwrap() error { return chord.ErrInvocationAdmissionUnavailable }

// selectedAdmission reports a selected implementation reached through the in-host loopback without an admission boundary as the selected controller's own missing admission.
func selectedAdmission[T any](operation T, err error) (T, error) {
	if errors.Is(err, chord.ErrInvocationAdmissionUnavailable) {
		var zero T
		return zero, errSelectedAdmission
	}
	return operation, err
}

func (view agentControllerView) initiator() (AgentControllerInitiator, error) {
	selected, err := view.resolve()
	if err != nil {
		return nil, err
	}
	initiator, ok := selected.(AgentControllerInitiator)
	if !ok {
		return nil, errSelectedAdmission
	}
	return initiator, nil
}

func (view agentControllerView) BeginPrompt(ctx context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginPrompt(ctx, request))
}
func (view agentControllerView) BeginAbort(ctx context.Context) (*chord.ServiceInvocation, error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginAbort(ctx))
}
func (view agentControllerView) BeginWaitForPrompt(ctx context.Context, operationID string) (*chord.ServiceResultInvocation[AgentPromptResult], error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginWaitForPrompt(ctx, operationID))
}
func (view agentControllerView) BeginSteer(ctx context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginSteer(ctx, request))
}
func (view agentControllerView) BeginFollowUp(ctx context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginFollowUp(ctx, request))
}
func (view agentControllerView) BeginCancelQueued(ctx context.Context, id string) (*chord.ServiceResultInvocation[AgentCancelQueuedResponse], error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginCancelQueued(ctx, id))
}
func (view agentControllerView) BeginCompact(ctx context.Context, request AgentCompactionRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error) {
	selected, err := view.initiator()
	if err != nil {
		return nil, err
	}
	return selectedAdmission(selected.BeginCompact(ctx, request))
}

// BeginServiceMember forwards an in-host loopback admission to this client's own remote admission.
func (client remoteAgentController) BeginServiceMember(ctx context.Context, member string, args []json.RawMessage) (*chord.ServiceInvocation, error) {
	values := make([]any, len(args))
	for index, arg := range args {
		values[index] = arg
	}
	return client.service.BeginCall(ctx, member, values...)
}

func (client remoteAgentController) BeginPrompt(ctx context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error) {
	return chord.BeginCallResult[AgentOperationResponse](ctx, client.service, "prompt", request)
}
func (client remoteAgentController) BeginAbort(ctx context.Context) (*chord.ServiceInvocation, error) {
	return client.service.BeginCall(ctx, "abort")
}
func (client remoteAgentController) BeginWaitForPrompt(ctx context.Context, operationID string) (*chord.ServiceResultInvocation[AgentPromptResult], error) {
	return chord.BeginCallResult[AgentPromptResult](ctx, client.service, "waitForPrompt", operationID)
}
func (client remoteAgentController) BeginSteer(ctx context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error) {
	return chord.BeginCallResult[AgentQueueResponse](ctx, client.service, "steer", request)
}
func (client remoteAgentController) BeginFollowUp(ctx context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error) {
	return chord.BeginCallResult[AgentQueueResponse](ctx, client.service, "followUp", request)
}
func (client remoteAgentController) BeginCancelQueued(ctx context.Context, id string) (*chord.ServiceResultInvocation[AgentCancelQueuedResponse], error) {
	return chord.BeginCallResult[AgentCancelQueuedResponse](ctx, client.service, "cancelQueued", id)
}
func (client remoteAgentController) BeginCompact(ctx context.Context, request AgentCompactionRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error) {
	return chord.BeginCallResult[AgentOperationResponse](ctx, client.service, "compact", request)
}

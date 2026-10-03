// Ports packages/coding-agent/src/experimental/services/agent-controller.ts.
package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// AgentControllerDefinition names the remote service token; Go shares type and value names.
var AgentControllerDefinition = chord.DefineService[AgentController](AgentControllerID)

func init() {
	chord.RegisterServiceView(AgentControllerDefinition, func(resolve func() (AgentController, error)) AgentController {
		return agentControllerView{resolve: resolve}
	})
	chord.RegisterRemoteClient(AgentControllerDefinition, func(service *chord.RemoteService) AgentController { return remoteAgentController{service: service} })
}

type agentControllerView struct {
	resolve func() (AgentController, error)
}

func (view agentControllerView) Prompt(ctx context.Context, request AgentPromptRequest) (AgentOperationResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentOperationResponse{}, err
	}
	return service.Prompt(ctx, request)
}
func (view agentControllerView) Abort(ctx context.Context) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.Abort(ctx)
}
func (view agentControllerView) WaitForPrompt(ctx context.Context, operationID string) (AgentPromptResult, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentPromptResult{}, err
	}
	return service.WaitForPrompt(ctx, operationID)
}
func (view agentControllerView) Steer(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentQueueResponse{}, err
	}
	return service.Steer(ctx, request)
}
func (view agentControllerView) FollowUp(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentQueueResponse{}, err
	}
	return service.FollowUp(ctx, request)
}
func (view agentControllerView) CancelQueued(ctx context.Context, id string) (AgentCancelQueuedResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentCancelQueuedResponse{}, err
	}
	return service.CancelQueued(ctx, id)
}
func (view agentControllerView) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentOperationResponse{}, err
	}
	return service.Compact(ctx, request)
}

type remoteAgentController struct{ service *chord.RemoteService }

func (client remoteAgentController) Prompt(ctx context.Context, request AgentPromptRequest) (AgentOperationResponse, error) {
	return chord.CallResult[AgentOperationResponse](ctx, client.service, "prompt", request)
}
func (client remoteAgentController) Abort(ctx context.Context) error {
	_, err := client.service.Call(ctx, "abort")
	return err
}
func (client remoteAgentController) WaitForPrompt(ctx context.Context, operationID string) (AgentPromptResult, error) {
	return chord.CallResult[AgentPromptResult](ctx, client.service, "waitForPrompt", operationID)
}
func (client remoteAgentController) Steer(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return chord.CallResult[AgentQueueResponse](ctx, client.service, "steer", request)
}
func (client remoteAgentController) FollowUp(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return chord.CallResult[AgentQueueResponse](ctx, client.service, "followUp", request)
}
func (client remoteAgentController) CancelQueued(ctx context.Context, id string) (AgentCancelQueuedResponse, error) {
	return chord.CallResult[AgentCancelQueuedResponse](ctx, client.service, "cancelQueued", id)
}
func (client remoteAgentController) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	return chord.CallResult[AgentOperationResponse](ctx, client.service, "compact", request)
}

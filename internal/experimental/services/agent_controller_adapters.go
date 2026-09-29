// Ports packages/coding-agent/src/experimental/services/agent-controller.ts.
package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// AgentControllerDefinition names the remote service token; Go shares type and value names.
var AgentControllerDefinition = pico3.DefineService[AgentController](AgentControllerID)

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
func (view agentControllerView) RequestAbort(ctx context.Context, id string) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.RequestAbort(ctx, id)
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
func (view agentControllerView) NextRun(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentQueueResponse{}, err
	}
	return service.NextRun(ctx, request)
}
func (view agentControllerView) CancelQueued(ctx context.Context, id string) (AgentCancelQueuedResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentCancelQueuedResponse{}, err
	}
	return service.CancelQueued(ctx, id)
}
func (view agentControllerView) Resume(ctx context.Context) (AgentOperationResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentOperationResponse{}, err
	}
	return service.Resume(ctx)
}
func (view agentControllerView) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentOperationResponse{}, err
	}
	return service.Compact(ctx, request)
}
func (view agentControllerView) Navigate(ctx context.Context, request AgentNavigationRequest) (AgentOperationResponse, error) {
	service, err := view.resolve()
	if err != nil {
		return AgentOperationResponse{}, err
	}
	return service.Navigate(ctx, request)
}

type remoteAgentController struct{ service *chord.RemoteService }

func (client remoteAgentController) Prompt(ctx context.Context, request AgentPromptRequest) (AgentOperationResponse, error) {
	return chord.CallResult[AgentOperationResponse](ctx, client.service, "prompt", request)
}
func (client remoteAgentController) RequestAbort(ctx context.Context, id string) error {
	_, err := client.service.Call(ctx, "requestAbort", id)
	return err
}
func (client remoteAgentController) Steer(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return chord.CallResult[AgentQueueResponse](ctx, client.service, "steer", request)
}
func (client remoteAgentController) FollowUp(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return chord.CallResult[AgentQueueResponse](ctx, client.service, "followUp", request)
}
func (client remoteAgentController) NextRun(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return chord.CallResult[AgentQueueResponse](ctx, client.service, "nextRun", request)
}
func (client remoteAgentController) CancelQueued(ctx context.Context, id string) (AgentCancelQueuedResponse, error) {
	return chord.CallResult[AgentCancelQueuedResponse](ctx, client.service, "cancelQueued", id)
}
func (client remoteAgentController) Resume(ctx context.Context) (AgentOperationResponse, error) {
	return chord.CallResult[AgentOperationResponse](ctx, client.service, "resume")
}
func (client remoteAgentController) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	return chord.CallResult[AgentOperationResponse](ctx, client.service, "compact", request)
}
func (client remoteAgentController) Navigate(ctx context.Context, request AgentNavigationRequest) (AgentOperationResponse, error) {
	return chord.CallResult[AgentOperationResponse](ctx, client.service, "navigate", request)
}

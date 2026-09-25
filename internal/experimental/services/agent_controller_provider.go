package services

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// LaneOperation is the portion of OperationResultRecord or SuspendedRun consumed by the presentation facade. A lane adapter projects compaction.compaction and navigation.navigation into the same observation.
type LaneOperation struct {
	OperationID string
	Status      string
	Error       *AgentOperationError
}

// LaneCompactionOptions preserves absent options separately from empty instructions.
type LaneCompactionOptions struct {
	CustomInstructions *string
}

// LaneNavigateOptions carries the options consumed by navigateTree.
type LaneNavigateOptions struct {
	Summarize          bool
	Label              *string
	CustomInstructions *string
}

// AgentLane is the controller's consumer-owned lane boundary. Implementations unwrap Result failures as harness.TaggedError and return rejected Promises as ordinary errors. Queue methods return value.entryId; CancelQueued returns value.kind. RequestAbort discards the successful observation, not its completion.
type AgentLane interface {
	Prompt(context.Context, string, []ai.ImageContent) (LaneOperation, error)
	RequestAbort(context.Context, string) error
	Steer(context.Context, string, []ai.ImageContent) (string, error)
	FollowUp(context.Context, string, []ai.ImageContent) (string, error)
	NextRun(context.Context, string, []ai.ImageContent) (string, error)
	CancelQueued(context.Context, string) (string, error)
	Resume(context.Context) (LaneOperation, error)
	Compact(context.Context, *LaneCompactionOptions) (LaneOperation, error)
	NavigateTree(context.Context, *string, LaneNavigateOptions) (LaneOperation, error)
}

// AgentControllerProvider adapts lane outcomes without exposing worker-owned result details.
type AgentControllerProvider struct {
	lane AgentLane
}

var _ AgentController = (*AgentControllerProvider)(nil)

// CreateAgentController creates the presentation-safe facade for one lane.
func CreateAgentController(lane AgentLane) *AgentControllerProvider {
	return &AgentControllerProvider{lane: lane}
}

func (controller *AgentControllerProvider) Prompt(ctx context.Context, request AgentPromptRequest) (AgentOperationResponse, error) {
	value, err := controller.lane.Prompt(ctx, request.Message, toTextPrompt(request))
	return operationResponse(value, err, true)
}

func (controller *AgentControllerProvider) RequestAbort(ctx context.Context, operationID string) error {
	return commandError(controller.lane.RequestAbort(ctx, operationID))
}

func (controller *AgentControllerProvider) Steer(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	entryID, err := controller.lane.Steer(ctx, request.Message, toTextPrompt(request))
	return queueResponse(entryID, err)
}

func (controller *AgentControllerProvider) FollowUp(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	entryID, err := controller.lane.FollowUp(ctx, request.Message, toTextPrompt(request))
	return queueResponse(entryID, err)
}

func (controller *AgentControllerProvider) NextRun(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	entryID, err := controller.lane.NextRun(ctx, request.Message, toTextPrompt(request))
	return queueResponse(entryID, err)
}

func (controller *AgentControllerProvider) CancelQueued(ctx context.Context, entryID string) (AgentCancelQueuedResponse, error) {
	outcome, err := controller.lane.CancelQueued(ctx, entryID)
	if err != nil {
		return AgentCancelQueuedResponse{}, commandError(err)
	}
	return AgentCancelQueuedResponse{Outcome: outcome}, nil
}

func (controller *AgentControllerProvider) Resume(ctx context.Context) (AgentOperationResponse, error) {
	value, err := controller.lane.Resume(ctx)
	return operationResponse(value, err, false)
}

func (controller *AgentControllerProvider) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	var options *LaneCompactionOptions
	if request.CustomInstructions != nil {
		options = &LaneCompactionOptions{CustomInstructions: request.CustomInstructions}
	}
	value, err := controller.lane.Compact(ctx, options)
	return operationResponse(value, err, true)
}

func (controller *AgentControllerProvider) Navigate(ctx context.Context, request AgentNavigationRequest) (AgentOperationResponse, error) {
	value, err := controller.lane.NavigateTree(ctx, request.TargetID, LaneNavigateOptions{
		Summarize: request.Summarize, Label: request.Label, CustomInstructions: request.CustomInstructions,
	})
	return operationResponse(value, err, true)
}

func operationResponse(value LaneOperation, err error, includeOperationID bool) (AgentOperationResponse, error) {
	if err == nil {
		response := AgentOperationResponse{Accepted: true, OperationID: new(value.OperationID)}
		if value.Status == "failed" && value.Error != nil {
			response.Error = &AgentOperationError{Code: value.Error.Code, Message: value.Error.Message}
		}
		return response, nil
	}
	tagged, ok := errors.AsType[harness.TaggedError](err)
	if !ok {
		return AgentOperationResponse{}, err
	}
	response := AgentOperationResponse{Error: toAgentError(tagged)}
	if busy, ok := tagged.(*harness.LaneBusy); includeOperationID && ok {
		response.OperationID = new(busy.OperationID)
	}
	return response, nil
}

func queueResponse(entryID string, err error) (AgentQueueResponse, error) {
	if err == nil {
		return AgentQueueResponse{Accepted: true, EntryID: new(entryID)}, nil
	}
	if tagged, ok := errors.AsType[harness.TaggedError](err); ok {
		return AgentQueueResponse{Error: toAgentError(tagged)}, nil
	}
	return AgentQueueResponse{}, err
}

func commandError(err error) error {
	if tagged, ok := errors.AsType[harness.TaggedError](err); ok {
		return errors.New(tagged.Error())
	}
	return err
}

func toAgentError(err harness.TaggedError) *AgentOperationError {
	code := "operation_failed"
	switch err.Tag() {
	case "LaneBusy":
		code = "lane_busy"
	case "InvalidMessage":
		code = "invalid_message"
	case "UnknownSkill":
		code = "unknown_skill"
	case "UnknownTemplate":
		code = "unknown_template"
	case "NothingToCompact":
		code = "nothing_to_compact"
	case "NothingToResume":
		code = "nothing_to_resume"
	case "InvalidNavigation":
		code = "invalid_navigation"
	case "UnknownTarget":
		code = "unknown_target"
	case "Closed":
		code = "closed"
	}
	return &AgentOperationError{Code: code, Message: err.Error()}
}

func toTextPrompt(request AgentPromptRequest) []ai.ImageContent {
	if request.Images == nil {
		return nil
	}
	images := make([]ai.ImageContent, len(request.Images))
	for i, image := range request.Images {
		images[i] = ai.ImageContent{Data: image.Data, MimeType: image.MimeType}
	}
	return images
}

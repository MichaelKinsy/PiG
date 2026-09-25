// Package services contains the opt-in experimental presentation service contracts.
package services

import "context"

// AgentControllerID identifies the remote service. Go's shared type/value namespace requires a separate name for the service token.
const AgentControllerID = "pi.agent-controller"

// AgentPromptImage carries an image in a presentation prompt. Type is "image".
type AgentPromptImage struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// AgentPromptRequest preserves null images separately from an empty image list.
type AgentPromptRequest struct {
	Message string             `json:"message"`
	Images  []AgentPromptImage `json:"images"`
}

// AgentOperationError is the presentation-safe code and message of a failure.
type AgentOperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AgentOperationResponse distinguishes admission from completion. Accepted operations have an operation ID and may carry a terminal error; rejected operations always carry an error and may identify an existing operation.
type AgentOperationResponse struct {
	Accepted    bool                 `json:"accepted"`
	OperationID *string              `json:"operationId"`
	Error       *AgentOperationError `json:"error"`
}

// AgentQueueResponse has an entry ID and no error when accepted, or an error and no entry ID when rejected.
type AgentQueueResponse struct {
	Accepted bool                 `json:"accepted"`
	EntryID  *string              `json:"entryId"`
	Error    *AgentOperationError `json:"error"`
}

// AgentCompactionRequest uses null to select the default instructions.
type AgentCompactionRequest struct {
	CustomInstructions *string `json:"customInstructions"`
}

// AgentNavigationRequest selects a target and optional summary instructions. A null target selects the root.
type AgentNavigationRequest struct {
	TargetID           *string `json:"targetId"`
	Summarize          bool    `json:"summarize"`
	Label              *string `json:"label"`
	CustomInstructions *string `json:"customInstructions"`
}

// AgentCancelQueuedResponse reports "cancelled", "already_consumed", or "not_found".
type AgentCancelQueuedResponse struct {
	Outcome string `json:"outcome"`
}

// AgentController is the presentation-safe command facade over the worker-owned main AgentLane. Calls wait for the lane result, propagate context cancellation, and return transport/rejection errors separately from admission responses.
type AgentController interface {
	Prompt(context.Context, AgentPromptRequest) (AgentOperationResponse, error)
	RequestAbort(context.Context, string) error
	Steer(context.Context, AgentPromptRequest) (AgentQueueResponse, error)
	FollowUp(context.Context, AgentPromptRequest) (AgentQueueResponse, error)
	NextRun(context.Context, AgentPromptRequest) (AgentQueueResponse, error)
	CancelQueued(context.Context, string) (AgentCancelQueuedResponse, error)
	Resume(context.Context) (AgentOperationResponse, error)
	Compact(context.Context, AgentCompactionRequest) (AgentOperationResponse, error)
	Navigate(context.Context, AgentNavigationRequest) (AgentOperationResponse, error)
}

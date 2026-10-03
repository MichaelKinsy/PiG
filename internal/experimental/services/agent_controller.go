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

// AgentOperationResponse distinguishes admission from rejection. An accepted operation has an operation ID and no error; a rejected one has an error and no operation ID. OperationID identifies the durable submission of a prompt, or the task of a compaction.
type AgentOperationResponse struct {
	Accepted    bool                 `json:"accepted"`
	OperationID *string              `json:"operationId"`
	Error       *AgentOperationError `json:"error"`
}

// AgentQueueResponse has an entry ID and no error when accepted, or an error and no entry ID when rejected. EntryID identifies the durable submission; CancelQueued withdraws it while it is still queued.
type AgentQueueResponse struct {
	Accepted bool                 `json:"accepted"`
	EntryID  *string              `json:"entryId"`
	Error    *AgentOperationError `json:"error"`
}

// AgentCompactionRequest uses null to select the default instructions.
type AgentCompactionRequest struct {
	CustomInstructions *string `json:"customInstructions"`
}

// AgentPromptResult is the settled outcome of a prompt: "done" with the answer's text, or "unanswered" with the reason it got none.
type AgentPromptResult struct {
	Status string  `json:"status"`
	Text   *string `json:"text"`
	Reason *string `json:"reason"`
}

// AgentCancelQueuedResponse reports "cancelled", "already_consumed", or "not_found".
type AgentCancelQueuedResponse struct {
	Outcome string `json:"outcome"`
}

// AgentController is the presentation-safe command facade over the worker-owned root conversation. Calls wait for the conversation's result, propagate context cancellation, and return transport errors separately from admission responses.
type AgentController interface {
	// Prompt starts a run; it is rejected with code "busy" while one is active.
	Prompt(context.Context, AgentPromptRequest) (AgentOperationResponse, error)
	// Steer steers the active run, or starts one when idle.
	Steer(context.Context, AgentPromptRequest) (AgentQueueResponse, error)
	// FollowUp queues input for after the active run, or starts one when idle.
	FollowUp(context.Context, AgentPromptRequest) (AgentQueueResponse, error)
	CancelQueued(context.Context, string) (AgentCancelQueuedResponse, error)
	// Abort withdraws queued input and aborts the active run and compaction.
	Abort(context.Context) error
	Compact(context.Context, AgentCompactionRequest) (AgentOperationResponse, error)
	// WaitForPrompt waits until the prompt with this operation ID is answered or settles unanswered.
	WaitForPrompt(context.Context, string) (AgentPromptResult, error)
}

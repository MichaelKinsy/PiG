package services

// Ports packages/coding-agent/src/experimental/services/agent-controller-provider.ts

import (
	"context"
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// SettledPrompt is a prompt submission that finished: unanswered with a reason, or answered by Answer. Answer is the first model message of the answer entry; it is nil when the settled submission has no answer.
type SettledPrompt struct {
	Unanswered bool
	Reason     string
	Answer     ai.Message
}

// PromptSubmission is one durable submission that a prompt created.
type PromptSubmission interface {
	// Wait returns once the submission is answered or settles unanswered.
	Wait(context.Context) (SettledPrompt, error)
}

// AgentConversation is the root durable conversation boundary the controller drives. Submit adds one input and returns its submission; Compact starts a compaction task and returns its task ID.
type AgentConversation interface {
	ID() durable.ConversationId
	Submit(ctx context.Context, content ai.UserContent, whenBusy durable.WhenBusy) (durable.SubmissionId, error)
	Abort(context.Context) error
	Compact(ctx context.Context, customInstructions *string) (int64, error)
}

// AgentHarness is the durable Harness boundary the worker services consult: queued and settled submissions for the controller, and a conversation's pi.agent document for the Models service. Submission returns (nil, nil) for an unknown ID; AgentDocument returns (nil, nil) for a conversation without one.
type AgentHarness interface {
	AbortSubmission(ctx context.Context, id durable.SubmissionId, conversationID durable.ConversationId) (durable.SubmissionAbortResult, error)
	Submission(ctx context.Context, id durable.SubmissionId) (PromptSubmission, error)
	AgentDocument(ctx context.Context, conversationID durable.ConversationId) (AgentDocument, error)
}

// AgentControllerProvider adapts durable conversation outcomes without exposing worker-owned details.
type AgentControllerProvider struct {
	harness      AgentHarness
	conversation AgentConversation
}

var _ AgentController = (*AgentControllerProvider)(nil)

// CreateAgentController creates the presentation-safe facade for one root conversation.
func CreateAgentController(harness AgentHarness, conversation AgentConversation) *AgentControllerProvider {
	return &AgentControllerProvider{harness: harness, conversation: conversation}
}

func (controller *AgentControllerProvider) Prompt(ctx context.Context, request AgentPromptRequest) (AgentOperationResponse, error) {
	id, err := controller.conversation.Submit(ctx, toInput(request), durable.WhenBusyReject)
	if err != nil {
		return AgentOperationResponse{Error: toAgentError(err)}, nil
	}
	return AgentOperationResponse{Accepted: true, OperationID: new(formatSubmissionID(id))}, nil
}

func (controller *AgentControllerProvider) Steer(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return controller.queue(ctx, durable.WhenBusySteer, request)
}

func (controller *AgentControllerProvider) FollowUp(ctx context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return controller.queue(ctx, durable.WhenBusyFollowUp, request)
}

func (controller *AgentControllerProvider) queue(ctx context.Context, whenBusy durable.WhenBusy, request AgentPromptRequest) (AgentQueueResponse, error) {
	id, err := controller.conversation.Submit(ctx, toInput(request), whenBusy)
	if err != nil {
		return AgentQueueResponse{Error: toAgentError(err)}, nil
	}
	return AgentQueueResponse{Accepted: true, EntryID: new(formatSubmissionID(id))}, nil
}

func (controller *AgentControllerProvider) CancelQueued(ctx context.Context, entryID string) (AgentCancelQueuedResponse, error) {
	id, ok := parseSubmissionID(entryID)
	if !ok {
		return AgentCancelQueuedResponse{Outcome: "not_found"}, nil
	}
	result, err := controller.harness.AbortSubmission(ctx, id, controller.conversation.ID())
	if err != nil {
		return AgentCancelQueuedResponse{}, err
	}
	switch result {
	case durable.SubmissionAborted:
		return AgentCancelQueuedResponse{Outcome: "cancelled"}, nil
	case durable.SubmissionNotFound:
		return AgentCancelQueuedResponse{Outcome: "not_found"}, nil
	}
	return AgentCancelQueuedResponse{Outcome: "already_consumed"}, nil
}

func (controller *AgentControllerProvider) Abort(ctx context.Context) error {
	return controller.conversation.Abort(ctx)
}

func (controller *AgentControllerProvider) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	id, err := controller.conversation.Compact(ctx, request.CustomInstructions)
	if err != nil {
		return AgentOperationResponse{Error: toAgentError(err)}, nil
	}
	return AgentOperationResponse{Accepted: true, OperationID: new(strconv.FormatInt(id, 10))}, nil
}

func (controller *AgentControllerProvider) WaitForPrompt(ctx context.Context, operationID string) (AgentPromptResult, error) {
	id, ok := parseSubmissionID(operationID)
	var submission PromptSubmission
	if ok {
		var err error
		if submission, err = controller.harness.Submission(ctx, id); err != nil {
			return AgentPromptResult{}, err
		}
	}
	if submission == nil {
		return AgentPromptResult{}, errors.New("Unknown prompt: " + operationID)
	}
	settled, err := submission.Wait(ctx)
	if err != nil {
		return AgentPromptResult{}, err
	}
	if settled.Unanswered {
		return AgentPromptResult{Status: "unanswered", Reason: &settled.Reason}, nil
	}
	text := ""
	if assistant, ok := settled.Answer.(ai.AssistantMessage); ok {
		for _, block := range assistant.Content {
			if content, ok := block.(ai.TextContent); ok {
				text += content.Text
			}
		}
	}
	return AgentPromptResult{Status: "done", Text: &text}, nil
}

// maxSafeJSInteger is Number.MAX_SAFE_INTEGER.
const maxSafeJSInteger = 1<<53 - 1

func formatSubmissionID(id durable.SubmissionId) string { return strconv.FormatInt(int64(id), 10) }

// parseSubmissionID accepts only the canonical positive decimal form that formatSubmissionID returns, within the safe-integer range of the wire's JavaScript numbers.
func parseSubmissionID(value string) (durable.SubmissionId, bool) {
	if value == "" || value[0] == '0' {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id > maxSafeJSInteger {
		return 0, false
	}
	return durable.SubmissionId(id), true
}

// toInput is a plain string without images; with images it is the text block followed by the images.
func toInput(request AgentPromptRequest) ai.UserContent {
	if len(request.Images) == 0 {
		return ai.UserText(request.Message)
	}
	content := make(ai.UserContentBlocks, 0, 1+len(request.Images))
	content = append(content, ai.TextContent{Text: request.Message})
	for _, image := range request.Images {
		content = append(content, ai.ImageContent{Data: image.Data, MimeType: image.MimeType})
	}
	return content
}

func toAgentError(err error) *AgentOperationError {
	if _, ok := errors.AsType[*durable.ConversationBusy](err); ok {
		return &AgentOperationError{Code: "busy", Message: err.Error()}
	}
	return &AgentOperationError{Code: "operation_failed", Message: err.Error()}
}

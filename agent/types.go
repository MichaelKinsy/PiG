package agent

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// AgentTurnContext is passed to completed-turn callbacks (FinishTurn and
// PrepareNextTurn). Mirrors upstream packages/agent/src/types.ts
// AgentTurnContext.
type AgentTurnContext struct {
	// Message is the assistant message that completed the turn.
	Message *AssistantMessage
	// ToolResults are the tool result messages emitted for the completed turn.
	ToolResults []ToolResultMessage
	// Context is the loop context after the turn's assistant message and tool
	// results have been appended.
	Context []AgentMessage
	// NewMessages are the messages this run returns if it exits now. Prompt
	// runs include the initial prompt messages; continuation runs do not
	// include pre-existing context messages.
	NewMessages []AgentMessage
}

// PrepareNextTurnContext mirrors upstream's PrepareNextTurnContext, which
// extends AgentTurnContext without adding fields.
type PrepareNextTurnContext = AgentTurnContext

// AgentTurnAction is the action of an AgentTurnDecision.
type AgentTurnAction string

const (
	// AgentTurnContinue ensures one more provider request after the turn.
	AgentTurnContinue AgentTurnAction = "continue"
	// AgentTurnEnd ends the run without polling queues or preparing another request.
	AgentTurnEnd AgentTurnAction = "end"
)

// AgentTurnDecision is returned by FinishTurn. A nil decision preserves normal
// scheduling. Mirrors upstream AgentTurnDecision.
type AgentTurnDecision struct {
	Action AgentTurnAction
}

// FinishTurn is called after a completed assistant turn and all of its
// tool-result messages, but before TurnEndEvent. On a normal turn,
// AgentTurnContinue ensures one next provider request: tool-result, steering,
// or follow-up scheduling satisfies that request without adding another;
// otherwise the loop continues once with the current context. Error and
// aborted responses remain hard exits. ctx is the run's context (upstream's
// abort signal). Mirrors upstream FinishTurn.
type FinishTurn func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision

// AgentLoopTurnUpdate carries optional next-turn overrides returned by
// PrepareNextTurn. Mirrors upstream AgentLoopTurnUpdate.
type AgentLoopTurnUpdate struct {
	// Context replaces the loop context for the next provider request.
	Context []AgentMessage
	// Messages are appended before the next provider request, with normal
	// lifecycle events.
	Messages []AgentMessage
	// Model is the model for the next provider request.
	Model *ai.Model
	// ThinkingLevel is the thinking level for the next provider request.
	ThinkingLevel *ai.ThinkingLevel
}

// PrepareNextTurn is called after TurnEndEvent when the loop continues,
// immediately before the next turn starts. ctx is the run's context.
// Mirrors upstream prepareNextTurnWithContext.
type PrepareNextTurn func(ctx context.Context, turn PrepareNextTurnContext) *AgentLoopTurnUpdate

// PrepareRequestContext is the runtime state available immediately before a
// provider request. Mirrors upstream PrepareRequestContext.
type PrepareRequestContext struct {
	Context       []AgentMessage
	Model         *ai.Model
	ThinkingLevel ai.ThinkingLevel
}

// AgentRequestUpdate replaces runtime state for the provider request being
// prepared and later requests in the run. Mirrors upstream AgentRequestUpdate
// (AgentLoopTurnUpdate without messages).
type AgentRequestUpdate struct {
	Context       []AgentMessage
	Model         *ai.Model
	ThinkingLevel *ai.ThinkingLevel
}

// PrepareRequest is called immediately before every provider request,
// including the first. Pending messages have already been appended and emitted
// when it runs, and it does not poll queues. Mirrors upstream PrepareRequest.
type PrepareRequest func(ctx context.Context, request PrepareRequestContext) *AgentRequestUpdate

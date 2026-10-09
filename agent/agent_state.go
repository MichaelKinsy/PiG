package agent

import (
	"github.com/MichaelKinsy/PiG/ai"
)

// AgentState is the live view of the agent's state that Pi exposes as `agent.state` (types.ts:382-421). Reads see the agent's current values and the writable properties write through: SetModel, SetThinkingLevel, SetTools and SetMessages are Pi's `state.model = ...`, `state.thinkingLevel = ...`, `state.tools = ...` and `state.messages = ...`.
// The value is cheap and holds no copy, so it may be kept: it always reflects the agent it came from.
type AgentState struct {
	agent *Agent
}

// State returns the live state view (upstream agent.state).
func (a *Agent) State() AgentState { return AgentState{agent: a} }

// SystemPrompt is the current system prompt, replayed from the transcript's system messages. It is read-only: append a system message to change it.
func (s AgentState) SystemPrompt() string { return s.agent.SystemPrompt() }

// Model is the active model used for future turns.
func (s AgentState) Model() *ai.Model { return s.agent.Model() }

// SetModel assigns the active model (upstream `state.model = model`).
func (s AgentState) SetModel(model *ai.Model) { s.agent.SetModel(model) }

// ThinkingLevel is the requested reasoning level for future turns.
func (s AgentState) ThinkingLevel() ai.ModelThinkingLevel { return s.agent.ThinkingLevel() }

// SetThinkingLevel assigns the requested reasoning level (upstream `state.thinkingLevel = level`).
func (s AgentState) SetThinkingLevel(level ai.ModelThinkingLevel) { s.agent.SetThinkingLevel(level) }

// Tools are the executable tools; the slice is a copy of the list.
func (s AgentState) Tools() []AgentTool { return s.agent.Tools() }

// SetTools assigns the executable tools; the top-level slice is copied (upstream `state.tools = tools`).
func (s AgentState) SetTools(tools []AgentTool) { s.agent.SetTools(tools) }

// Messages is the conversation transcript.
func (s AgentState) Messages() []AgentMessage { return s.agent.MessagesSnapshot() }

// SetMessages assigns the transcript; the top-level slice is copied (upstream `state.messages = messages`).
func (s AgentState) SetMessages(messages []AgentMessage) { s.agent.SetMessages(messages) }

// IsStreaming is true while the agent is processing a prompt or continuation.
func (s AgentState) IsStreaming() bool { return s.agent.IsStreaming() }

// StreamingMessage is the partial assistant message of the current streamed response, if any.
func (s AgentState) StreamingMessage() *AgentMessage { return s.agent.StreamingMessage() }

// PendingToolCalls are the tool call ids currently executing, in start order, as Pi's insertion-ordered Set iterates them.
func (s AgentState) PendingToolCalls() []string {
	return s.agent.PendingToolCalls()
}

// ErrorMessage is the error of the most recent failed or aborted assistant turn; empty when there is none.
func (s AgentState) ErrorMessage() string { return s.agent.ErrorMessage() }

// AgentStateSnapshot is the values of the agent's state read at one moment. It holds copies, so it does not follow the agent.
type AgentStateSnapshot struct {
	// SystemPrompt is the current system prompt, replayed from the transcript's system messages.
	SystemPrompt string
	// Model is the active model used for future turns.
	Model *ai.Model
	// ThinkingLevel is the requested reasoning level for future turns.
	ThinkingLevel ai.ModelThinkingLevel
	// Tools are the executable tools.
	Tools []AgentTool
	// Messages is the conversation transcript.
	Messages []AgentMessage
	// IsStreaming is true while the agent is processing a prompt or continuation.
	IsStreaming bool
	// StreamingMessage is the partial assistant message of the current streamed response, if any.
	StreamingMessage *AgentMessage
	// PendingToolCalls are the tool call ids currently executing, in start order, as Pi's insertion-ordered Set iterates them.
	PendingToolCalls []string
	// ErrorMessage is the error of the most recent failed or aborted assistant turn, if any; empty when there is none.
	ErrorMessage string
}

// Snapshot reads every value of the state at once.
func (s AgentState) Snapshot() AgentStateSnapshot {
	return AgentStateSnapshot{
		SystemPrompt:     s.SystemPrompt(),
		Model:            s.Model(),
		ThinkingLevel:    s.ThinkingLevel(),
		Tools:            s.Tools(),
		Messages:         s.Messages(),
		IsStreaming:      s.IsStreaming(),
		StreamingMessage: s.StreamingMessage(),
		PendingToolCalls: s.PendingToolCalls(),
		ErrorMessage:     s.ErrorMessage(),
	}
}

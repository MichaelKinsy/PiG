package agentharness

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// HarnessEventType is the discriminant of a harness event.
type HarnessEventType string

// Harness event types (upstream HarnessEventPayload["type"]).
const (
	EventRunStart        HarnessEventType = "run_start"
	EventRunResume       HarnessEventType = "run_resume"
	EventRunSuspend      HarnessEventType = "run_suspend"
	EventOperationAbort  HarnessEventType = "operation_abort"
	EventRunEnd          HarnessEventType = "run_end"
	EventFault           HarnessEventType = "fault"
	EventHandlerError    HarnessEventType = "handler_error"
	EventTurnStart       HarnessEventType = "turn_start"
	EventTurnEnd         HarnessEventType = "turn_end"
	EventRetryScheduled  HarnessEventType = "retry_scheduled"
	EventRetryStart      HarnessEventType = "retry_start"
	EventRetryEnd        HarnessEventType = "retry_end"
	EventMessageStart    HarnessEventType = "message_start"
	EventMessageUpdate   HarnessEventType = "message_update"
	EventMessageEnd      HarnessEventType = "message_end"
	EventToolStart       HarnessEventType = "tool_start"
	EventToolUpdate      HarnessEventType = "tool_update"
	EventToolEnd         HarnessEventType = "tool_end"
	EventEntryAdded      HarnessEventType = "entry_added"
	EventQueueUpdate     HarnessEventType = "queue_update"
	EventValueUpdate     HarnessEventType = "value_update"
	EventConfigUpdate    HarnessEventType = "config_update"
	EventCompactionStart HarnessEventType = "compaction_start"
	EventCompactionEnd   HarnessEventType = "compaction_end"
	EventNavigationStart HarnessEventType = "navigation_start"
	EventNavigationEnd   HarnessEventType = "navigation_end"
	EventLaneCreated     HarnessEventType = "lane_created"
	EventUsage           HarnessEventType = "usage"
)

// HarnessEventPayload is one variant of the upstream HarnessEventPayload
// union. Implementations are the *Payload structs of this file; payloads are
// value types and are compared/copied as such.
type HarnessEventPayload interface {
	EventType() HarnessEventType
}

// HarnessEvent is a payload plus its lane envelope (upstream HarnessEvent).
// Lane is empty for session-global events (fault, value_update, global
// config_update, lane-free handler_error) and required for lane events and
// usage. Recovery marks events replayed while restoring a lane.
type HarnessEvent struct {
	Payload  HarnessEventPayload
	Lane     string
	Recovery bool
}

// Type returns the payload discriminant.
func (event HarnessEvent) Type() HarnessEventType {
	if event.Payload == nil {
		return ""
	}
	return event.Payload.EventType()
}

// MarshalJSON flattens the payload with type, lane and recovery fields, like
// the upstream object shape.
func (event HarnessEvent) MarshalJSON() ([]byte, error) {
	if event.Payload == nil {
		return nil, fmt.Errorf("harness event has no payload")
	}
	body, err := json.Marshal(event.Payload)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["type"], _ = json.Marshal(event.Payload.EventType())
	if event.Lane != "" {
		fields["lane"], _ = json.Marshal(event.Lane)
	}
	if event.Recovery {
		fields["recovery"] = json.RawMessage("true")
	}
	return json.Marshal(fields)
}

// RunStartPayload opens a run.
type RunStartPayload struct {
	RunID     string `json:"runId"`
	StartedAt int64  `json:"startedAt"`
}

// RunResumePayload resumes a suspended run.
type RunResumePayload struct {
	RunID string `json:"runId"`
}

// RunSuspendPayload suspends a run on a deferred handle. Reason is "deferred".
type RunSuspendPayload struct {
	RunID    string            `json:"runId"`
	Reason   string            `json:"reason"`
	Deferred ai.DeferredHandle `json:"deferred"`
	Poll     int               `json:"poll"`
}

// OperationAbortPayload reports an abort request and the drained queues.
type OperationAbortPayload struct {
	OperationID string               `json:"operationId"`
	Steer       []agent.AgentMessage `json:"steer"`
	FollowUp    []agent.AgentMessage `json:"followUp"`
}

// RunEndPayload closes a run. Status is completed, aborted or failed; only
// failed carries Error.
type RunEndPayload struct {
	RunID     string                  `json:"runId"`
	FromTipID *string                 `json:"fromTipId"`
	TipID     *string                 `json:"tipId"`
	EndedAt   int64                   `json:"endedAt"`
	Status    string                  `json:"status"`
	Error     *session.OperationError `json:"error,omitempty"`
}

// FaultPayload reports a harness fault.
type FaultPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HandlerErrorPayload reports an isolated hook or event handler failure. Kind
// "hook" names Hook; kind "event" names Event.
type HandlerErrorPayload struct {
	Error string `json:"error"`
	Stack string `json:"stack,omitempty"`
	Kind  string `json:"kind"`
	Hook  string `json:"hook,omitempty"`
	Event string `json:"event,omitempty"`
}

// TurnStartPayload opens a turn.
type TurnStartPayload struct {
	RunID  string `json:"runId"`
	TurnID string `json:"turnId"`
}

// TurnEndPayload closes a turn.
type TurnEndPayload struct {
	RunID       string                    `json:"runId"`
	TurnID      string                    `json:"turnId"`
	Message     agent.AssistantMessage    `json:"message"`
	ToolResults []agent.ToolResultMessage `json:"toolResults"`
}

// RetryScheduledPayload reports a scheduled retry.
type RetryScheduledPayload struct {
	RunID        string `json:"runId"`
	Step         string `json:"step"`
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	DelayMs      int64  `json:"delayMs"`
	NotBefore    int64  `json:"notBefore"`
	ErrorMessage string `json:"errorMessage"`
}

// RetryStartPayload starts a retry attempt.
type RetryStartPayload struct {
	RunID   string `json:"runId"`
	Step    string `json:"step"`
	Attempt int    `json:"attempt"`
}

// RetryEndPayload ends a retry attempt.
type RetryEndPayload struct {
	RunID      string  `json:"runId"`
	Step       string  `json:"step"`
	Attempt    int     `json:"attempt"`
	Success    bool    `json:"success"`
	FinalError *string `json:"finalError,omitempty"`
}

// MessageStartPayload starts a message; RunID is empty outside a run.
type MessageStartPayload struct {
	RunID   string             `json:"runId,omitempty"`
	Message agent.AgentMessage `json:"message"`
}

// MessageUpdatePayload streams an assistant update.
type MessageUpdatePayload struct {
	RunID   string                   `json:"runId"`
	Message agent.AgentMessage       `json:"message"`
	Event   ai.AssistantMessageEvent `json:"event"`
	Frame   ai.AssistantMessageFrame `json:"frame,omitempty"`
}

// MessageEndPayload ends a message; RunID and EntryID are optional.
type MessageEndPayload struct {
	RunID   string             `json:"runId,omitempty"`
	Message agent.AgentMessage `json:"message"`
	EntryID string             `json:"entryId,omitempty"`
}

// ToolStartPayload starts a tool execution.
type ToolStartPayload struct {
	RunID      string `json:"runId"`
	TurnID     string `json:"turnId"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       any    `json:"args"`
}

// ToolUpdatePayload streams a partial tool result.
type ToolUpdatePayload struct {
	RunID         string                  `json:"runId"`
	TurnID        string                  `json:"turnId"`
	ToolCallID    string                  `json:"toolCallId"`
	ToolName      string                  `json:"toolName"`
	PartialResult harness.AgentToolResult `json:"partialResult"`
}

// ToolEndPayload settles a tool execution.
type ToolEndPayload struct {
	RunID      string                  `json:"runId"`
	TurnID     string                  `json:"turnId"`
	ToolCallID string                  `json:"toolCallId"`
	ToolName   string                  `json:"toolName"`
	Result     harness.AgentToolResult `json:"result"`
	IsError    bool                    `json:"isError"`
	Terminate  bool                    `json:"terminate"`
}

// EntryAddedPayload reports a committed transcript entry.
type EntryAddedPayload struct {
	Entry session.Entry `json:"entry"`
}

// QueueUpdatePayload reports the lane queues.
type QueueUpdatePayload struct {
	Queues []LaneQueuedItem `json:"queues"`
}

// ValueUpdatePayload reports a session value change. Value "session_name"
// carries Name; "entry_label" carries TargetID and Label. Nil means cleared.
type ValueUpdatePayload struct {
	Value    string  `json:"value"`
	Name     *string `json:"name,omitempty"`
	TargetID string  `json:"targetId,omitempty"`
	Label    *string `json:"label,omitempty"`
}

// Config update properties.
const (
	ConfigModel              = "model"
	ConfigThinkingLevel      = "thinkingLevel"
	ConfigActiveTools        = "activeTools"
	ConfigTools              = "tools"
	ConfigResources          = "resources"
	ConfigStreamOptions      = "streamOptions"
	ConfigRetryPolicy        = "retryPolicy"
	ConfigCompactionSettings = "compactionSettings"
	ConfigSteeringMode       = "steeringMode"
	ConfigFollowUpMode       = "followUpMode"
)

// ConfigUpdatePayload reports a configuration change. Value/Previous types
// follow Property: model ModelIdentity (Previous is unknown/any);
// thinkingLevel ai.ThinkingLevel; activeTools []string; streamOptions
// harness.AgentHarnessStreamOptions; retryPolicy ai.RetryPolicy;
// compactionSettings harness.CompactionSettings; steeringMode/followUpMode
// agent.QueueMode. tools and resources carry neither. model, thinkingLevel and
// activeTools are lane configuration events and carry a lane; the rest are
// session-global.
type ConfigUpdatePayload struct {
	Property string `json:"property"`
	Value    any    `json:"value,omitempty"`
	Previous any    `json:"previous,omitempty"`
}

// IsLaneConfig reports whether the property is lane-scoped configuration.
func (payload ConfigUpdatePayload) IsLaneConfig() bool {
	switch payload.Property {
	case ConfigModel, ConfigThinkingLevel, ConfigActiveTools:
		return true
	}
	return false
}

// CompactionStartPayload opens a compaction; Reason is manual, threshold or
// overflow.
type CompactionStartPayload struct {
	RunID     string `json:"runId"`
	Reason    string `json:"reason"`
	StartedAt int64  `json:"startedAt"`
}

// CompactionEndPayload closes a compaction. Status completed carries EntryID;
// failed carries Error; declined and aborted carry neither.
type CompactionEndPayload struct {
	RunID   string                  `json:"runId"`
	Reason  string                  `json:"reason"`
	EndedAt int64                   `json:"endedAt"`
	Status  string                  `json:"status"`
	EntryID string                  `json:"entryId,omitempty"`
	Error   *session.OperationError `json:"error,omitempty"`
}

// NavigationStartPayload opens a navigation; nil TargetID is the root.
type NavigationStartPayload struct {
	RunID     string  `json:"runId"`
	TargetID  *string `json:"targetId"`
	StartedAt int64   `json:"startedAt"`
}

// NavigationEndPayload closes a navigation; only failed carries Error.
type NavigationEndPayload struct {
	RunID     string                  `json:"runId"`
	FromTipID *string                 `json:"fromTipId"`
	TipID     *string                 `json:"tipId"`
	EndedAt   int64                   `json:"endedAt"`
	Status    string                  `json:"status"`
	Error     *session.OperationError `json:"error,omitempty"`
}

// LaneCreatedPayload reports a created lane Branch.
type LaneCreatedPayload struct {
	At *string `json:"at"`
}

// UsagePayload reports a usage ledger row and the session totals. The event
// envelope's Lane carries the upstream payload lane.
type UsagePayload struct {
	Row    session.UsageRow `json:"row"`
	Totals ai.Usage         `json:"totals"`
}

// EventType implementations.

func (RunStartPayload) EventType() HarnessEventType        { return EventRunStart }
func (RunResumePayload) EventType() HarnessEventType       { return EventRunResume }
func (RunSuspendPayload) EventType() HarnessEventType      { return EventRunSuspend }
func (OperationAbortPayload) EventType() HarnessEventType  { return EventOperationAbort }
func (RunEndPayload) EventType() HarnessEventType          { return EventRunEnd }
func (FaultPayload) EventType() HarnessEventType           { return EventFault }
func (HandlerErrorPayload) EventType() HarnessEventType    { return EventHandlerError }
func (TurnStartPayload) EventType() HarnessEventType       { return EventTurnStart }
func (TurnEndPayload) EventType() HarnessEventType         { return EventTurnEnd }
func (RetryScheduledPayload) EventType() HarnessEventType  { return EventRetryScheduled }
func (RetryStartPayload) EventType() HarnessEventType      { return EventRetryStart }
func (RetryEndPayload) EventType() HarnessEventType        { return EventRetryEnd }
func (MessageStartPayload) EventType() HarnessEventType    { return EventMessageStart }
func (MessageUpdatePayload) EventType() HarnessEventType   { return EventMessageUpdate }
func (MessageEndPayload) EventType() HarnessEventType      { return EventMessageEnd }
func (ToolStartPayload) EventType() HarnessEventType       { return EventToolStart }
func (ToolUpdatePayload) EventType() HarnessEventType      { return EventToolUpdate }
func (ToolEndPayload) EventType() HarnessEventType         { return EventToolEnd }
func (EntryAddedPayload) EventType() HarnessEventType      { return EventEntryAdded }
func (QueueUpdatePayload) EventType() HarnessEventType     { return EventQueueUpdate }
func (ValueUpdatePayload) EventType() HarnessEventType     { return EventValueUpdate }
func (ConfigUpdatePayload) EventType() HarnessEventType    { return EventConfigUpdate }
func (CompactionStartPayload) EventType() HarnessEventType { return EventCompactionStart }
func (CompactionEndPayload) EventType() HarnessEventType   { return EventCompactionEnd }
func (NavigationStartPayload) EventType() HarnessEventType { return EventNavigationStart }
func (NavigationEndPayload) EventType() HarnessEventType   { return EventNavigationEnd }
func (LaneCreatedPayload) EventType() HarnessEventType     { return EventLaneCreated }
func (UsagePayload) EventType() HarnessEventType           { return EventUsage }

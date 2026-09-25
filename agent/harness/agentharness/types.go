// Package agentharness holds the public durable AgentHarness data contracts of
// packages/agent/src/harness/agent-harness.ts, the passive HarnessEventBus of
// harness/events.ts and the ordered HookRegistry of harness/hooks.ts.
//
// These declarations live in their own package, rather than in package
// harness, because they reference Session entries, operation records and usage
// rows from agent/harness/session, which itself imports package harness. The
// durable runtime (agent/harness/runtime and its drive procedures) imports this
// package; this package never imports the runtime.
//
// This is not yet the complete public agent-harness.ts contract:
// AgentHarnessOptions, the AgentHarness.create constructor, and the
// strict-JSON LaneTranscriptSnapshot/LaneWatchEvent representations (with
// HarnessEvent decoding) belong to the durable runtime/public lanes and are
// not declared here.
//
// Every operation takes a context.Context first (Go convention) where upstream
// takes a trailing chord Context; see harness.Context.
package agentharness

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// OperationKind is "run", "compaction" or "navigation".
type OperationKind string

// Operation kinds.
const (
	OperationRun        OperationKind = "run"
	OperationCompaction OperationKind = "compaction"
	OperationNavigation OperationKind = "navigation"
)

// OperationStatus is "running", "open" or "aborting".
type OperationStatus string

// Operation statuses.
const (
	OperationStatusRunning  OperationStatus = "running"
	OperationStatusOpen     OperationStatus = "open"
	OperationStatusAborting OperationStatus = "aborting"
)

// ModelIdentity names a provider model (upstream ModelIdentity).
type ModelIdentity = session.ModelRef

// Resources are the harness skills and prompt templates (upstream Resources).
type Resources = harness.AgentHarnessResources

// SuspendedRun is the convenience-only suspended run observation.
type SuspendedRun struct {
	OperationID string            `json:"operationId"`
	Status      string            `json:"status"` // always "suspended"
	Deferred    ai.DeferredHandle `json:"deferred"`
}

// RunOutcome is exactly one of a settled record or a suspended run
// (upstream OperationResultRecord | SuspendedRun).
type RunOutcome struct {
	Record    *session.OperationResultRecord
	Suspended *SuspendedRun
}

// NavigateOptions configure tree navigation.
type NavigateOptions struct {
	Summarize          bool    `json:"summarize,omitempty"`
	Label              *string `json:"label,omitempty"`
	CustomInstructions *string `json:"customInstructions,omitempty"`
}

// OperationRequestKind discriminates OperationRequest.
type OperationRequestKind string

// Operation request kinds.
const (
	RequestPrompt         OperationRequestKind = "prompt"
	RequestSkill          OperationRequestKind = "skill"
	RequestPromptTemplate OperationRequestKind = "prompt_template"
	RequestCompaction     OperationRequestKind = "compaction"
	RequestNavigation     OperationRequestKind = "navigation"
)

// OperationRequest is the accept() request union. Fields apply per Kind:
//   - prompt: exactly one of PromptText (with optional Images) or
//     PromptMessages;
//   - skill: Name, AdditionalInstructions;
//   - prompt_template: Name, Args;
//   - compaction: CustomInstructions;
//   - navigation: TargetID (nil is the root), NavigateOptions.
type OperationRequest struct {
	Kind                   OperationRequestKind
	OperationID            *string
	PromptText             *string
	Images                 []ai.ImageContent
	PromptMessages         []agent.AgentMessage
	Name                   string
	AdditionalInstructions *string
	Args                   []string
	CustomInstructions     *string
	TargetID               *string
	NavigateOptions        *NavigateOptions
}

// OperationAdmission reports an accepted operation.
type OperationAdmission struct {
	OperationID string        `json:"operationId"`
	Kind        OperationKind `json:"kind"`
	StartedAt   int64         `json:"startedAt"`
}

// DriveOptions select the operation to drive and its waiting policy.
type DriveOptions struct {
	OperationID  string
	WaitForRetry bool
	PollDeferred bool
}

// CurrentOperationInfo describes a lane's current operation.
type CurrentOperationInfo struct {
	ID            string          `json:"id"`
	Kind          OperationKind   `json:"kind"`
	StartedAt     int64           `json:"startedAt"`
	Status        OperationStatus `json:"status"`
	CapturedModel *ModelIdentity  `json:"capturedModel,omitempty"`
}

// LaneExecutionInfo is inspectExecution's observation.
type LaneExecutionInfo struct {
	Lane            string                `json:"lane"`
	TipID           *string               `json:"tipId"`
	ConfiguredModel ModelIdentity         `json:"configuredModel"`
	Current         *CurrentOperationInfo `json:"current"`
	LastOperationID *string               `json:"lastOperationId"`
}

// DriveOutcomeKind discriminates DriveOutcome.
type DriveOutcomeKind string

// Drive outcome kinds.
const (
	DriveSettled DriveOutcomeKind = "settled"
	DriveWaiting DriveOutcomeKind = "waiting"
)

// DriveWaitReason is "retry" or "deferred".
type DriveWaitReason string

// Drive wait reasons.
const (
	DriveWaitRetry    DriveWaitReason = "retry"
	DriveWaitDeferred DriveWaitReason = "deferred"
)

// DriveOutcome is a settled record, or a wait for a retry deadline
// (NotBefore) or a deferred provider handle (Deferred).
type DriveOutcome struct {
	Kind        DriveOutcomeKind
	Outcome     *session.OperationResultRecord
	OperationID string
	Reason      DriveWaitReason
	NotBefore   int64
	Deferred    *ai.DeferredHandle
}

// AbortRequest reports a requested abort.
type AbortRequest struct {
	OperationID    string               `json:"operationId"`
	NewlyRequested bool                 `json:"newlyRequested"`
	Steer          []agent.AgentMessage `json:"steer"`
	FollowUp       []agent.AgentMessage `json:"followUp"`
}

// LaneInfo is one lane in a session snapshot.
type LaneInfo struct {
	Name      string                `json:"name"`
	TipID     *string               `json:"tipId"`
	Operation *CurrentOperationInfo `json:"operation"`
}

// LaneSnapshotTool is a running or settled tool in a lane snapshot. Status is
// "running" (Result optional) or "settled" (Result and IsError set).
type LaneSnapshotTool struct {
	Status     string                   `json:"status"`
	ToolCallID string                   `json:"toolCallId"`
	ToolName   string                   `json:"toolName"`
	Args       any                      `json:"args"`
	Result     *harness.AgentToolResult `json:"result,omitempty"`
	IsError    bool                     `json:"isError,omitempty"`
}

// MarshalJSON includes isError for settled tools, even when false, and omits it for running tools.
func (tool LaneSnapshotTool) MarshalJSON() ([]byte, error) {
	type toolFields LaneSnapshotTool
	fields := struct {
		toolFields
		IsError *bool `json:"isError,omitempty"`
	}{toolFields: toolFields(tool)}
	if tool.Status == "settled" {
		fields.IsError = &tool.IsError
	}
	return json.Marshal(fields)
}

// OpenOperation is an operation left open by a previous process.
type OpenOperation struct {
	Lane        string        `json:"lane"`
	OperationID string        `json:"operationId"`
	Kind        OperationKind `json:"kind"`
	StartedAt   int64         `json:"startedAt"`
	Aborting    bool          `json:"aborting,omitempty"`
}

// LaneQueuedItem is a queued inbox item. Type "message" carries Message with
// Kind steer/followUp/nextRun/write; type "custom" carries CustomType/Data with
// Kind write. HasData distinguishes absent Data from JSON null.
type LaneQueuedItem struct {
	EntryID    string             `json:"entryId"`
	Kind       string             `json:"kind"`
	Type       string             `json:"type"`
	Message    agent.AgentMessage `json:"-"`
	CustomType string             `json:"-"`
	Data       session.JsonValue  `json:"-"`
	HasData    bool               `json:"-"`
}

// LaneSnapshotRetry is the pending retry of a lane snapshot operation.
type LaneSnapshotRetry struct {
	Attempt       int   `json:"attempt"`
	MaxAttempts   int   `json:"maxAttempts"`
	NextAttemptAt int64 `json:"nextAttemptAt"`
}

// LaneSnapshotDeferred is the pending deferred handle of a snapshot operation.
type LaneSnapshotDeferred struct {
	Handle ai.DeferredHandle `json:"handle"`
	Poll   int               `json:"poll"`
}

// LaneSnapshotOperation is the operation projection of a lane snapshot.
type LaneSnapshotOperation struct {
	ID               string                  `json:"id"`
	Kind             OperationKind           `json:"kind"`
	StartedAt        int64                   `json:"startedAt"`
	FromTipID        *string                 `json:"fromTipId"`
	Status           OperationStatus         `json:"status"`
	Retry            *LaneSnapshotRetry      `json:"retry,omitempty"`
	Deferred         *LaneSnapshotDeferred   `json:"deferred,omitempty"`
	StreamingMessage *agent.AssistantMessage `json:"streamingMessage,omitempty"`
	RunningTools     []LaneSnapshotTool      `json:"runningTools"`
}

// LaneSnapshot is the watchable projection of one lane.
type LaneSnapshot struct {
	Lane          string                         `json:"lane"`
	Transcript    []session.Entry                `json:"transcript"`
	TipID         *string                        `json:"tipId"`
	LastResult    *session.OperationResultRecord `json:"lastResult,omitempty"`
	Configuration session.LaneConfiguration      `json:"configuration"`
	Stats         session.SessionStats           `json:"stats"`
	Operation     *LaneSnapshotOperation         `json:"operation"`
	Queues        []LaneQueuedItem               `json:"queues"`
	Faulted       bool                           `json:"faulted"`
}

// SessionSnapshot is the watchable projection of a whole session.
type SessionSnapshot struct {
	Lanes   []LaneInfo `json:"lanes"`
	Faulted bool       `json:"faulted"`
}

// EventListener receives one harness event (upstream EventListener). A
// returned error is isolated and reported as a handler_error event.
type EventListener func(ctx harness.Context, event HarnessEvent) error

// Events registers typed event listeners (upstream Events). The returned
// function unsubscribes. Registration fails once the bus is closed.
type Events interface {
	On(eventType HarnessEventType, listener EventListener) (func(), error)
}

// WatchHandle is a snapshot plus the ordered events after it
// (upstream WatchHandle<T>).
type WatchHandle[T any] interface {
	// Snapshot returns the current snapshot (upstream snapshot field).
	Snapshot() T
	// Start begins delivery of buffered then live events; call at most once.
	Start(listener EventListener) error
	// Resnapshot replaces the snapshot at a delivery boundary.
	Resnapshot(ctx harness.Context) (T, error)
	Unsubscribe()
}

// MarshalJSON emits the message or custom queued-item shape.
func (item LaneQueuedItem) MarshalJSON() ([]byte, error) {
	if item.Type == "custom" {
		fields := map[string]any{"entryId": item.EntryID, "kind": item.Kind, "type": item.Type, "customType": item.CustomType}
		if item.HasData {
			fields["data"] = item.Data
		}
		return json.Marshal(fields)
	}
	return json.Marshal(struct {
		EntryID string             `json:"entryId"`
		Kind    string             `json:"kind"`
		Type    string             `json:"type"`
		Message agent.AgentMessage `json:"message"`
	}{item.EntryID, item.Kind, item.Type, item.Message})
}

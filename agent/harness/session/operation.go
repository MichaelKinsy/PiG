package session

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
)

// Operation intent kinds.
const (
	OperationKindRun        = "run"
	OperationKindCompaction = "compaction"
	OperationKindNavigation = "navigation"
)

// OperationIntent is the immutable intent of one operation, discriminated by
// Kind: run uses PromptEntryIDs; compaction uses CustomInstructions;
// navigation uses TargetID (nil targets the branch root), Summarize, Label,
// and CustomInstructions.
type OperationIntent struct {
	Kind               string   `json:"kind"`
	PromptEntryIDs     []string `json:"promptEntryIds"`
	TargetID           *string  `json:"targetId"`
	Summarize          bool     `json:"summarize"`
	Label              *string  `json:"label,omitempty"`
	CustomInstructions *string  `json:"customInstructions,omitempty"`
}

var intentKeys = map[string][]string{
	OperationKindRun:        {"kind", "promptEntryIds"},
	OperationKindCompaction: {"kind", "customInstructions"},
	OperationKindNavigation: {"kind", "targetId", "summarize", "label", "customInstructions"},
}

// MarshalJSON emits the members of Kind.
func (intent OperationIntent) MarshalJSON() ([]byte, error) {
	keys, ok := intentKeys[intent.Kind]
	if !ok {
		return nil, fmt.Errorf("unknown operation intent kind %q", intent.Kind)
	}
	type plain OperationIntent
	intent.PromptEntryIDs = stringsOrEmpty(intent.PromptEntryIDs)
	return marshalOrdered(plain(intent), keys)
}

// OperationMeta is the immutable acceptance data of one operation.
type OperationMeta struct {
	OperationID string          `json:"operationId"`
	Lane        string          `json:"lane"`
	SourceTipID *string         `json:"sourceTipId"`
	StartedAt   int64           `json:"startedAt"`
	Intent      OperationIntent `json:"intent"`
}

// Control statuses.
const (
	ControlRunning         = "running"
	ControlCancelRequested = "cancel_requested"
)

// Control is the orthogonal cancellation state of an operation.
type Control struct {
	Status      string `json:"status"`
	RequestedAt int64  `json:"requestedAt"`
}

// MarshalJSON emits requestedAt only for cancellation.
func (control Control) MarshalJSON() ([]byte, error) {
	type plain Control
	keys := []string{"status"}
	if control.Status == ControlCancelRequested {
		keys = append(keys, "requestedAt")
	}
	return marshalOrdered(plain(control), keys)
}

// OperationError is a machine-readable terminal failure.
type OperationError struct {
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Details *JsonValue `json:"details,omitempty"`
}

// UnmarshalJSON keeps explicit null details distinct from absent details.
func (operationError *OperationError) UnmarshalJSON(data []byte) error {
	type plain OperationError
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	fields, err := rawFields(data)
	if err != nil {
		return err
	}
	if decoded.Details, err = optionalJSON(fields, "details"); err != nil {
		return err
	}
	*operationError = OperationError(decoded)
	return nil
}

// TerminalStatus values.
const (
	TerminalCompleted = "completed"
	TerminalDeclined  = "declined"
	TerminalAborted   = "aborted"
	TerminalFailed    = "failed"
)

// OperationResultRecord is the immutable lane-lived observation written by one
// terminal transaction.
type OperationResultRecord struct {
	OperationID string          `json:"operationId"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Error       *OperationError `json:"error,omitempty"`
	FromTipID   *string         `json:"fromTipId"`
	TipID       *string         `json:"tipId"`
	StartedAt   int64           `json:"startedAt"`
	EndedAt     int64           `json:"endedAt"`
}

// Continuation kinds.
const (
	ContinuationNeedAssistant = "need_assistant"
	ContinuationMayFinish     = "may_finish"
)

// Continuation selects the checkpoint's next step: need_assistant uses
// OverflowRecoveryUsed; may_finish uses IncludeFinalAssistant.
type Continuation struct {
	Kind                  string `json:"kind"`
	OverflowRecoveryUsed  bool   `json:"overflowRecoveryUsed"`
	IncludeFinalAssistant bool   `json:"includeFinalAssistant"`
}

// MarshalJSON emits the members of Kind.
func (continuation Continuation) MarshalJSON() ([]byte, error) {
	type plain Continuation
	switch continuation.Kind {
	case ContinuationNeedAssistant:
		return marshalOrdered(plain(continuation), []string{"kind", "overflowRecoveryUsed"})
	case ContinuationMayFinish:
		return marshalOrdered(plain(continuation), []string{"kind", "includeFinalAssistant"})
	default:
		return nil, fmt.Errorf("unknown continuation kind %q", continuation.Kind)
	}
}

// CheckpointData is the checkpoint payload.
type CheckpointData struct {
	Continuation   Continuation `json:"continuation"`
	TriggerEntryID string       `json:"triggerEntryId"`
}

// Inbox item kinds.
const (
	InboxSteer    = "steer"
	InboxFollowUp = "followUp"
	InboxNextRun  = "nextRun"
	InboxWrite    = "write"
)

// InboxItem is one ordered lane inbox item referencing its pending entry.
type InboxItem struct {
	EntryID string `json:"entryId"`
	Kind    string `json:"kind"`
}

// NormalizedRetryPolicy is the retry policy captured in operation state.
type NormalizedRetryPolicy struct {
	MaxAttempts     int `json:"maxAttempts"`
	BaseDelayMs     int `json:"baseDelayMs"`
	MaxAgentDelayMs int `json:"maxAgentDelayMs"`
}

// GenerationContext is the assistant request snapshot of one step.
type GenerationContext struct {
	StepID               string                            `json:"stepId"`
	TriggerEntryID       string                            `json:"triggerEntryId"`
	Configuration        LaneConfiguration                 `json:"configuration"`
	StreamOptions        harness.AgentHarnessStreamOptions `json:"streamOptions"`
	RetryPolicy          NormalizedRetryPolicy             `json:"retryPolicy"`
	OverflowRecoveryUsed bool                              `json:"overflowRecoveryUsed"`
}

// Tool call statuses.
const (
	ToolCallPlanned       = "planned"
	ToolCallEffectPending = "effect_pending"
	ToolCallOutcomeReady  = "outcome_ready"
	ToolCallCompleted     = "completed"
)

// ToolCall tracks one call of a tool batch by its zero-based index in the
// assistant message's complete content array. Replay applies to
// effect_pending; Terminate to outcome_ready and completed.
type ToolCall struct {
	Status        string `json:"status"`
	SourceIndex   int    `json:"sourceIndex"`
	ResultEntryID string `json:"resultEntryId"`
	Replay        string `json:"replay"`
	Terminate     bool   `json:"terminate"`
}

// MarshalJSON emits the members of Status.
func (call ToolCall) MarshalJSON() ([]byte, error) {
	type plain ToolCall
	keys := []string{"status", "sourceIndex", "resultEntryId"}
	switch call.Status {
	case ToolCallPlanned:
	case ToolCallEffectPending:
		keys = append(keys, "replay")
	case ToolCallOutcomeReady, ToolCallCompleted:
		keys = append(keys, "terminate")
	default:
		return nil, fmt.Errorf("unknown tool call status %q", call.Status)
	}
	return marshalOrdered(plain(call), keys)
}

// ToolBatch is the nested tool-call state machine of one assistant response.
type ToolBatch struct {
	AssistantEntryID string            `json:"assistantEntryId"`
	Configuration    LaneConfiguration `json:"configuration"`
	TurnID           string            `json:"turnId"`
	Calls            []ToolCall        `json:"calls"`
}

// SummaryContext is the structural request snapshot of one summary task.
type SummaryContext struct {
	ResultEntryID string                            `json:"resultEntryId"`
	Configuration LaneConfiguration                 `json:"configuration"`
	StreamOptions harness.AgentHarnessStreamOptions `json:"streamOptions"`
	RetryPolicy   NormalizedRetryPolicy             `json:"retryPolicy"`
}

// Tool execution modes.
const (
	ToolExecutionSequential = "sequential"
	ToolExecutionParallel   = "parallel"
)

// RunSettings are the harness settings captured by an operation.
type RunSettings struct {
	Compaction    harness.CompactionSettings `json:"compaction"`
	SteeringMode  agent.QueueMode            `json:"steeringMode"`
	FollowUpMode  agent.QueueMode            `json:"followUpMode"`
	ToolExecution string                     `json:"toolExecution"`
}

// OperationScope is the uniform scope carried by every operation leaf.
type OperationScope struct {
	Control                Control     `json:"control"`
	Settings               RunSettings `json:"settings"`
	LatestAssistantEntryID *string     `json:"latestAssistantEntryId"`
}

// Result boundary kinds.
const (
	BoundaryResumeCheckpoint = "resume_checkpoint"
	BoundaryFinish           = "finish"
	BoundaryCommitNavigation = "commit_navigation"
)

// ResultBoundary is where a summary result goes: resume an enclosing run
// (ResumeAfter), finish standalone compaction, or commit navigation to
// TargetID with an optional Label.
type ResultBoundary struct {
	Kind        string          `json:"kind"`
	ResumeAfter *CheckpointData `json:"resumeAfter,omitempty"`
	TargetID    string          `json:"targetId"`
	Label       *string         `json:"label,omitempty"`
}

// MarshalJSON emits the members of Kind.
func (boundary ResultBoundary) MarshalJSON() ([]byte, error) {
	type plain ResultBoundary
	switch boundary.Kind {
	case BoundaryResumeCheckpoint:
		if boundary.ResumeAfter == nil {
			return nil, fmt.Errorf("resume_checkpoint boundary requires resumeAfter")
		}
		return marshalOrdered(plain(boundary), []string{"kind", "resumeAfter"})
	case BoundaryFinish:
		return marshalOrdered(plain(boundary), []string{"kind"})
	case BoundaryCommitNavigation:
		return marshalOrdered(plain(boundary), []string{"kind", "targetId", "label"})
	default:
		return nil, fmt.Errorf("unknown result boundary kind %q", boundary.Kind)
	}
}

// Summary task reasons.
const (
	SummaryReasonManual    = "manual"
	SummaryReasonThreshold = "threshold"
	SummaryReasonOverflow  = "overflow"
)

// SummaryTask is the structural task shared by the summary.* leaves.
type SummaryTask struct {
	TaskID             string         `json:"taskId"`
	Reason             string         `json:"reason,omitempty"`
	CustomInstructions *string        `json:"customInstructions,omitempty"`
	Boundary           ResultBoundary `json:"boundary"`
}

// SummaryRequest identifies one nested structural provider request.
type SummaryRequest struct {
	Index   int    `json:"index"`
	UsageID string `json:"usageId"`
}

// OperationAt is the discriminator of the flat 13-leaf operation state.
type OperationAt string

const (
	AtStarting                OperationAt = "starting"
	AtCheckpoint              OperationAt = "checkpoint"
	AtAssistantReady          OperationAt = "assistant.ready"
	AtAssistantEffectPending  OperationAt = "assistant.effect_pending"
	AtAssistantRetryWait      OperationAt = "assistant.retry_wait"
	AtTools                   OperationAt = "tools"
	AtDeferredSuspended       OperationAt = "deferred.suspended"
	AtDeferredEffectPending   OperationAt = "deferred.effect_pending"
	AtSummaryDeciding         OperationAt = "summary.deciding"
	AtSummaryReady            OperationAt = "summary.ready"
	AtSummaryEffectPending    OperationAt = "summary.effect_pending"
	AtSummaryRetryWait        OperationAt = "summary.retry_wait"
	AtNavigationReadyToCommit OperationAt = "navigation.ready_to_commit"
)

// OperationState is the total durable restart point of an operation: the
// uniform OperationScope plus the members of leaf At, listed per leaf in
// operationStateKeys. Members of other leaves are ignored.
type OperationState struct {
	OperationScope
	At OperationAt `json:"at"`

	// checkpoint
	Continuation   Continuation `json:"continuation"`
	TriggerEntryID string       `json:"triggerEntryId"`

	// assistant.*, summary.*
	GenerationContext   GenerationContext `json:"generationContext"`
	NextAttempt         int               `json:"nextAttempt"`
	Attempt             int               `json:"attempt"`
	ResponseEntryID     string            `json:"responseEntryId"`
	UsageID             string            `json:"usageId"`
	IntendedOutputLimit int               `json:"intendedOutputLimit"`
	ContextWindow       int               `json:"contextWindow"`
	NotBefore           int64             `json:"notBefore"`
	ErrorMessage        string            `json:"errorMessage"`

	// tools
	Batch ToolBatch `json:"batch"`

	// deferred.*
	StepID        string                            `json:"stepId"`
	SourceEntryID string                            `json:"sourceEntryId"`
	Poll          int                               `json:"poll"`
	Configuration LaneConfiguration                 `json:"configuration"`
	StreamOptions harness.AgentHarnessStreamOptions `json:"streamOptions"`

	// summary.*
	Task           SummaryTask     `json:"task"`
	SummaryContext SummaryContext  `json:"summaryContext"`
	Request        *SummaryRequest `json:"request,omitempty"`
	UsageIDs       []string        `json:"usageIds"`

	// navigation.ready_to_commit; a nil TargetID targets the branch root.
	TargetID *string `json:"targetId"`
	Label    *string `json:"label,omitempty"`
}

var operationScopeKeys = []string{"control", "settings", "latestAssistantEntryId", "at"}

var operationStateKeys = map[OperationAt][]string{
	AtStarting:                nil,
	AtCheckpoint:              {"continuation", "triggerEntryId"},
	AtAssistantReady:          {"generationContext", "nextAttempt"},
	AtAssistantEffectPending:  {"generationContext", "attempt", "responseEntryId", "usageId", "intendedOutputLimit", "contextWindow"},
	AtAssistantRetryWait:      {"generationContext", "nextAttempt", "notBefore", "errorMessage"},
	AtTools:                   {"batch"},
	AtDeferredSuspended:       {"stepId", "sourceEntryId", "poll", "configuration", "streamOptions"},
	AtDeferredEffectPending:   {"stepId", "sourceEntryId", "poll", "responseEntryId", "usageId", "configuration", "streamOptions"},
	AtSummaryDeciding:         {"task"},
	AtSummaryReady:            {"task", "summaryContext", "nextAttempt"},
	AtSummaryEffectPending:    {"task", "summaryContext", "attempt", "request", "usageIds"},
	AtSummaryRetryWait:        {"task", "summaryContext", "nextAttempt", "notBefore", "errorMessage"},
	AtNavigationReadyToCommit: {"targetId", "label"},
}

// OperationAts lists every durable leaf.
func OperationAts() []OperationAt {
	return []OperationAt{
		AtStarting, AtCheckpoint, AtAssistantReady, AtAssistantEffectPending, AtAssistantRetryWait, AtTools,
		AtDeferredSuspended, AtDeferredEffectPending, AtSummaryDeciding, AtSummaryReady, AtSummaryEffectPending,
		AtSummaryRetryWait, AtNavigationReadyToCommit,
	}
}

// MarshalJSON emits the scope and the members of leaf At.
func (state OperationState) MarshalJSON() ([]byte, error) {
	leafKeys, ok := operationStateKeys[state.At]
	if !ok {
		return nil, fmt.Errorf("unknown operation state %q", state.At)
	}
	type plain OperationState
	state.UsageIDs = stringsOrEmpty(state.UsageIDs)
	keys := append(append([]string{}, operationScopeKeys...), leafKeys...)
	return marshalOrdered(plain(state), keys)
}

// UnmarshalJSON decodes any leaf and rejects unknown discriminators.
func (state *OperationState) UnmarshalJSON(data []byte) error {
	type plain OperationState
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if _, ok := operationStateKeys[decoded.At]; !ok {
		return fmt.Errorf("unknown operation state %q", decoded.At)
	}
	*state = OperationState(decoded)
	return nil
}

// OperationScopeOf copies only the uniform scope for a successor leaf.
func OperationScopeOf(state OperationState) OperationScope {
	return state.OperationScope
}

// Operation pairs process-local meta and state; it is never stored as one
// object.
type Operation struct {
	Meta  OperationMeta
	State OperationState
}

// LaneState is the lane's current/last operation ids and ordered inbox.
type LaneState struct {
	CurrentOperationID *string     `json:"currentOperationId"`
	LastOperationID    *string     `json:"lastOperationId"`
	Inbox              []InboxItem `json:"inbox"`
}

// MarshalJSON keeps an empty inbox as [].
func (state LaneState) MarshalJSON() ([]byte, error) {
	type plain LaneState
	if state.Inbox == nil {
		state.Inbox = []InboxItem{}
	}
	return json.Marshal(plain(state))
}

// Pending entry types.
const (
	PendingEntryMessage = "message"
	PendingEntryCustom  = "custom"
)

// PendingEntry is complete content awaiting tree placement: a message
// payload, or a custom entry with an optional payload.
type PendingEntry struct {
	Type          string
	Message       agent.AgentMessage
	CustomType    string
	CustomPayload *JsonValue
}

// MarshalJSON emits {type, payload} or {type, customType, payload?}.
func (entry PendingEntry) MarshalJSON() ([]byte, error) {
	switch entry.Type {
	case PendingEntryMessage:
		return json.Marshal(struct {
			Type    string             `json:"type"`
			Payload agent.AgentMessage `json:"payload"`
		}{entry.Type, entry.Message})
	case PendingEntryCustom:
		return json.Marshal(struct {
			Type       string     `json:"type"`
			CustomType string     `json:"customType"`
			Payload    *JsonValue `json:"payload,omitempty"`
		}{entry.Type, entry.CustomType, entry.CustomPayload})
	default:
		return nil, fmt.Errorf("unknown pending entry type %q", entry.Type)
	}
}

// UnmarshalJSON decodes either pending entry form.
func (entry *PendingEntry) UnmarshalJSON(data []byte) error {
	fields, err := rawFields(data)
	if err != nil {
		return err
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil {
		return fmt.Errorf("pending entry type: %w", err)
	}
	switch kind {
	case PendingEntryMessage:
		var message agent.AgentMessage
		if err := json.Unmarshal(fields["payload"], &message); err != nil {
			return err
		}
		*entry = PendingEntry{Type: kind, Message: message}
	case PendingEntryCustom:
		var customType string
		if err := json.Unmarshal(fields["customType"], &customType); err != nil {
			return fmt.Errorf("pending entry customType: %w", err)
		}
		payload, err := optionalJSON(fields, "payload")
		if err != nil {
			return err
		}
		*entry = PendingEntry{Type: kind, CustomType: customType, CustomPayload: payload}
	default:
		return fmt.Errorf("unknown pending entry type %q", kind)
	}
	return nil
}

// DurableFileOperations are the file paths touched by summarized content.
type DurableFileOperations struct {
	Read    []string `json:"read"`
	Written []string `json:"written"`
	Edited  []string `json:"edited"`
}

// MarshalJSON keeps empty path lists as [].
func (operations DurableFileOperations) MarshalJSON() ([]byte, error) {
	type plain DurableFileOperations
	return json.Marshal(plain{stringsOrEmpty(operations.Read), stringsOrEmpty(operations.Written), stringsOrEmpty(operations.Edited)})
}

// Structural preparation kinds.
const (
	PreparationCompaction    = "compaction"
	PreparationBranchSummary = "branch_summary"
)

// DurableStructuralPreparation is the immutable content of one summary task:
// compaction members, or branch_summary Messages and TotalTokens.
type DurableStructuralPreparation struct {
	Kind                string                     `json:"kind"`
	MessagesToSummarize []agent.AgentMessage       `json:"messagesToSummarize"`
	TurnPrefixMessages  []agent.AgentMessage       `json:"turnPrefixMessages"`
	RetainedTail        []agent.AgentMessage       `json:"retainedTail"`
	IsSplitTurn         bool                       `json:"isSplitTurn"`
	TokensBefore        int                        `json:"tokensBefore"`
	PreviousSummary     *string                    `json:"previousSummary,omitempty"`
	Messages            []agent.AgentMessage       `json:"messages"`
	FileOps             DurableFileOperations      `json:"fileOps"`
	Settings            harness.CompactionSettings `json:"settings"`
	TotalTokens         int                        `json:"totalTokens"`
}

// MarshalJSON emits the members of Kind.
func (preparation DurableStructuralPreparation) MarshalJSON() ([]byte, error) {
	type plain DurableStructuralPreparation
	for _, messages := range []*[]agent.AgentMessage{&preparation.MessagesToSummarize, &preparation.TurnPrefixMessages, &preparation.RetainedTail, &preparation.Messages} {
		if *messages == nil {
			*messages = []agent.AgentMessage{}
		}
	}
	switch preparation.Kind {
	case PreparationCompaction:
		return marshalOrdered(plain(preparation), []string{"kind", "messagesToSummarize", "turnPrefixMessages", "retainedTail", "isSplitTurn", "tokensBefore", "previousSummary", "fileOps", "settings"})
	case PreparationBranchSummary:
		return marshalOrdered(plain(preparation), []string{"kind", "messages", "fileOps", "totalTokens"})
	default:
		return nil, fmt.Errorf("unknown structural preparation kind %q", preparation.Kind)
	}
}

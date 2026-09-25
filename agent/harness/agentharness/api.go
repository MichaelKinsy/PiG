package agentharness

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream Result<T, E> values are Go (T, error) pairs. Expected failures are
// the tagged errors of package harness (LaneBusy, InvalidMessage, UnknownSkill,
// UnknownTemplate, NothingToCompact, InvalidNavigation, UnknownTarget,
// NothingToResume, NoActiveOperation, OperationMismatch, Closed); any other
// error is an upstream rejection.

// CompactionOutcome is compact()'s success value.
type CompactionOutcome struct {
	Compaction session.OperationResultRecord
	Run        *RunOutcome
}

// NavigationOutcome is navigateTree()'s success value.
type NavigationOutcome struct {
	Navigation session.OperationResultRecord
	Run        *RunOutcome
}

// AbortOutcome is abort()'s success value.
type AbortOutcome struct {
	OperationID string
	Steer       []agent.AgentMessage
	FollowUp    []agent.AgentMessage
}

// CancelQueuedKind is "cancelled", "already_consumed" or "not_found".
type CancelQueuedKind string

// Cancel-queued outcomes.
const (
	CancelQueuedCancelled       CancelQueuedKind = "cancelled"
	CancelQueuedAlreadyConsumed CancelQueuedKind = "already_consumed"
	CancelQueuedNotFound        CancelQueuedKind = "not_found"
)

// RecordUsageOptions attach a usage row to an entry and/or details.
type RecordUsageOptions struct {
	EntryID *string
	Details *session.JsonValue
}

// AgentLane is one named durable lane of an AgentHarness (upstream
// AgentLane).
type AgentLane interface {
	Name() string
	GetTipID(ctx harness.Context) (*string, error)
	FindEntries(ctx harness.Context, query *session.BranchScan) ([]session.Entry, error)
	FindEntry(ctx harness.Context, query *session.BranchScan) (*session.Entry, error)
	AppendMessage(ctx harness.Context, message agent.AgentMessage) (string, error)
	AppendCustomEntry(ctx harness.Context, customType string, data *session.JsonValue) (string, error)
	GetResult(ctx harness.Context, operationID string) (*session.OperationResultRecord, error)
	Accept(ctx harness.Context, request OperationRequest) (OperationAdmission, error)
	Drive(ctx harness.Context, options DriveOptions) (DriveOutcome, error)
	RequestAbort(ctx harness.Context, operationID string) (AbortRequest, error)
	InspectExecution(ctx harness.Context) (LaneExecutionInfo, error)
	Prompt(ctx harness.Context, text string, images []ai.ImageContent) (RunOutcome, error)
	PromptMessages(ctx harness.Context, messages []agent.AgentMessage) (RunOutcome, error)
	Skill(ctx harness.Context, name string, additionalInstructions *string) (RunOutcome, error)
	PromptFromTemplate(ctx harness.Context, name string, args []string) (RunOutcome, error)
	Compact(ctx harness.Context, customInstructions *string) (CompactionOutcome, error)
	NavigateTree(ctx harness.Context, targetID *string, options *NavigateOptions) (NavigationOutcome, error)
	Resume(ctx harness.Context) (RunOutcome, error)
	Abort(ctx harness.Context) (AbortOutcome, error)
	// Steer, FollowUp and NextRun queue a message (upstream `message:
	// AgentMessage`). The *Text variants are the upstream `string` form with
	// optional images, which the lane converts to a user message.
	Steer(ctx harness.Context, message agent.AgentMessage) (entryID string, err error)
	FollowUp(ctx harness.Context, message agent.AgentMessage) (entryID string, err error)
	NextRun(ctx harness.Context, message agent.AgentMessage) (entryID string, err error)
	// The *WithImages variants are the upstream AgentMessage-plus-images form:
	// images are appended to a user message; images with any other role are
	// rejected with InvalidMessage reason "images_with_non_user".
	SteerWithImages(ctx harness.Context, message agent.AgentMessage, images []ai.ImageContent) (entryID string, err error)
	FollowUpWithImages(ctx harness.Context, message agent.AgentMessage, images []ai.ImageContent) (entryID string, err error)
	NextRunWithImages(ctx harness.Context, message agent.AgentMessage, images []ai.ImageContent) (entryID string, err error)
	SteerText(ctx harness.Context, text string, images []ai.ImageContent) (entryID string, err error)
	FollowUpText(ctx harness.Context, text string, images []ai.ImageContent) (entryID string, err error)
	NextRunText(ctx harness.Context, text string, images []ai.ImageContent) (entryID string, err error)
	CancelQueued(ctx harness.Context, entryID string) (CancelQueuedKind, error)
	RecordUsage(ctx harness.Context, usage ai.Usage, options *RecordUsageOptions) (usageID string, err error)
	WaitForIdle(ctx harness.Context) error
	RunWhenIdle(ctx harness.Context, callback func(ctx harness.Context) error) error
	GetModel(ctx harness.Context) (*ai.Model, error)
	SetModel(ctx harness.Context, model ModelIdentity) error
	GetThinkingLevel(ctx harness.Context) (ai.ThinkingLevel, error)
	SetThinkingLevel(ctx harness.Context, level ai.ThinkingLevel) error
	GetActiveTools(ctx harness.Context) ([]string, error)
	SetActiveTools(ctx harness.Context, names []string) error
	Watch(ctx harness.Context) (WatchHandle[LaneSnapshot], error)
}

// AcquireLaneOptions configure lane acquisition. CreateAt applies only when
// the lane Branch is absent; SetCreateAt with a nil CreateAt creates at the
// root.
type AcquireLaneOptions struct {
	SetCreateAt bool
	CreateAt    *string
}

// AgentHarness is the durable harness attached to one open session
// (upstream AgentHarness).
type AgentHarness interface {
	Lane(ctx harness.Context, name string, options *AcquireLaneOptions) (AgentLane, error)
	Lanes(ctx harness.Context) ([]LaneInfo, error)
	GetName(ctx harness.Context) (*string, error)
	SetName(ctx harness.Context, name *string) error
	GetLabel(ctx harness.Context, targetID string) (*string, error)
	SetLabel(ctx harness.Context, targetID string, label *string) error
	GetTools(ctx harness.Context) ([]harness.AgentHarnessTool, error)
	SetTools(ctx harness.Context, tools []harness.AgentHarnessTool) error
	GetResources(ctx harness.Context) (Resources, error)
	SetResources(ctx harness.Context, resources Resources) error
	GetStreamOptions(ctx harness.Context) (harness.AgentHarnessStreamOptions, error)
	SetStreamOptions(ctx harness.Context, options harness.AgentHarnessStreamOptions) error
	GetRetryPolicy(ctx harness.Context) (ai.RetryPolicy, error)
	SetRetryPolicy(ctx harness.Context, policy ai.RetryPolicy) error
	GetCompactionSettings(ctx harness.Context) (harness.CompactionSettings, error)
	SetCompactionSettings(ctx harness.Context, settings harness.CompactionSettings) error
	GetSteeringMode(ctx harness.Context) (agent.QueueMode, error)
	SetSteeringMode(ctx harness.Context, mode agent.QueueMode) error
	GetFollowUpMode(ctx harness.Context) (agent.QueueMode, error)
	SetFollowUpMode(ctx harness.Context, mode agent.QueueMode) error
	WatchSession(ctx harness.Context) (WatchHandle[SessionSnapshot], error)
	Hooks() Hooks
	Events() Events
	Close(ctx harness.Context) error
}

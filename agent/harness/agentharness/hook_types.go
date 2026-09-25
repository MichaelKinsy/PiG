package agentharness

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// HookName names a harness hook (upstream keyof HookMap).
type HookName string

// Hook names, in upstream HookMap order.
const (
	HookBeforeRun        HookName = "before_run"
	HookBeforeDrive      HookName = "before_drive"
	HookBeforeRunEnd     HookName = "before_run_end"
	HookTransformContext HookName = "transform_context"
	HookBeforeRequest    HookName = "before_request"
	HookBeforePayload    HookName = "before_payload"
	HookAfterResponse    HookName = "after_response"
	HookBeforeTool       HookName = "before_tool"
	HookAfterTool        HookName = "after_tool"
	HookBeforeCompaction HookName = "before_compaction"
	HookBeforeNavigation HookName = "before_navigation"
)

// HookScope is the lane/run envelope every hook invocation carries (upstream
// HookInvocation's `lane` and `runId`).
type HookScope struct {
	Lane  string
	RunID string
}

// HookOptions configure one hook registration. ID is optional; an absent ID
// and an empty ID are distinct (telemetry records only a present ID).
type HookOptions struct {
	ID *string
}

// HookHandler handles one hook invocation. A nil result is upstream
// `undefined`. A returned error is reported through the registry's error
// reporter and handled per hook (see HookRegistry).
type HookHandler[E any, R any] func(ctx harness.Context, event E) (R, error)

// BeforeRunEvent may inject messages after the prompt.
type BeforeRunEvent struct {
	HookScope
	Prompt    []agent.AgentMessage
	Resources Resources
}

// BeforeRunResult injects Messages (nil: none).
type BeforeRunResult struct {
	Messages []agent.AgentMessage
}

// BeforeDriveEvent precedes one drive pass of an operation. Handler failure is
// fail-closed.
type BeforeDriveEvent struct {
	HookScope
	Operation OperationKind
}

// BeforeRunEndEvent may request a follow-up before a run ends.
type BeforeRunEndEvent struct {
	HookScope
	Messages []agent.AgentMessage
}

// BeforeRunEndResult requests a follow-up prompt (nil: none).
type BeforeRunEndResult struct {
	FollowUp *string
}

// TransformContextEvent may rewrite the request messages and system prompt.
type TransformContextEvent struct {
	HookScope
	Messages     []agent.AgentMessage
	SystemPrompt string
}

// TransformContextResult replaces Messages (non-nil) and/or SystemPrompt
// (non-nil).
type TransformContextResult struct {
	Messages     []agent.AgentMessage
	SystemPrompt *string
}

// Request steps.
const (
	StepAssistant     = "assistant"
	StepDeferred      = "deferred"
	StepCompaction    = "compaction"
	StepBranchSummary = "branch_summary"
)

// BeforeRequestEvent may patch the stream options of one provider request.
type BeforeRequestEvent struct {
	HookScope
	Model         *ai.Model
	Step          string
	Attempt       int
	StreamOptions harness.AgentHarnessStreamOptions
}

// BeforeRequestResult patches stream options (nil: unchanged).
type BeforeRequestResult struct {
	StreamOptions *harness.AgentHarnessStreamOptionsPatch
}

// BeforePayloadEvent may replace the provider payload.
type BeforePayloadEvent struct {
	HookScope
	Model   *ai.Model
	Payload any
}

// BeforePayloadResult replaces the payload. A nil Payload is an explicit null; a nil *BeforePayloadResult leaves the payload unchanged.
type BeforePayloadResult struct {
	Payload any
}

// AfterResponseEvent may replace the settled assistant message.
type AfterResponseEvent struct {
	HookScope
	Status  *int
	Headers map[string]string
	Message session.SettledAssistantMessage
}

// AfterResponseResult replaces the message when Message is non-nil.
type AfterResponseResult struct {
	Message *session.SettledAssistantMessage
}

// BeforeToolEvent may rewrite tool arguments or block the call.
type BeforeToolEvent struct {
	HookScope
	ToolCallID string
	ToolName   string
	Args       map[string]session.JsonValue
}

// ToolBlock blocks a tool call with a reason.
type ToolBlock struct {
	Reason    string
	Terminate bool
}

// BeforeToolResult replaces Args (non-nil) and/or blocks (non-nil Block).
type BeforeToolResult struct {
	Args  map[string]session.JsonValue
	Block *ToolBlock
}

// AfterToolEvent may rewrite a settled tool result.
type AfterToolEvent struct {
	HookScope
	ToolCallID string
	ToolName   string
	Args       map[string]session.JsonValue
	Content    []ai.ToolResultMessageContent
	// Details is the tool result details. HasDetails distinguishes present
	// JSON null (nil Details, HasDetails true) from absent details; a non-nil
	// Details is present regardless of HasDetails.
	Details    session.JsonValue
	HasDetails bool
	IsError    bool
	Usage      *ai.Usage
}

// AfterToolResult patches the tool result; nil fields are unchanged.
type AfterToolResult struct {
	Content []ai.ToolResultMessageContent
	Details session.JsonValue
	// SetDetails patches details even when Details is nil (JSON null); a
	// non-nil Details patches regardless.
	SetDetails bool
	IsError    *bool
	Usage      *ai.Usage
	Terminate  *bool
}

// Compaction reasons.
const (
	CompactionManual    = "manual"
	CompactionThreshold = "threshold"
	CompactionOverflow  = "overflow"
)

// BeforeCompactionEvent may decline or supply a compaction.
type BeforeCompactionEvent struct {
	HookScope
	Reason             string
	Preparation        compaction.CompactionPreparation
	CustomInstructions *string
}

// BeforeCompactionResult declines or supplies a compaction; returning both is
// reported as a hook error and ignored.
type BeforeCompactionResult struct {
	Decline    bool
	Compaction *compaction.CompactionResult
}

// BeforeNavigationEvent may decline or supply a branch summary.
type BeforeNavigationEvent struct {
	HookScope
	TargetID           string
	Preparation        compaction.BranchPreparation
	CustomInstructions *string
}

// BeforeNavigationResult declines or supplies a summary; returning both is
// reported as a hook error and ignored.
type BeforeNavigationResult struct {
	Decline bool
	Summary *compaction.BranchSummaryResult
}

// Hooks registers ordered hook handlers (upstream Hooks.on). Each method
// returns an unregister function, or the registry's close error once closed.
type Hooks interface {
	OnBeforeRun(handler HookHandler[BeforeRunEvent, *BeforeRunResult], options HookOptions) (func(), error)
	OnBeforeDrive(handler func(ctx harness.Context, event BeforeDriveEvent) error, options HookOptions) (func(), error)
	OnBeforeRunEnd(handler HookHandler[BeforeRunEndEvent, *BeforeRunEndResult], options HookOptions) (func(), error)
	OnTransformContext(handler HookHandler[TransformContextEvent, *TransformContextResult], options HookOptions) (func(), error)
	OnBeforeRequest(handler HookHandler[BeforeRequestEvent, *BeforeRequestResult], options HookOptions) (func(), error)
	OnBeforePayload(handler HookHandler[BeforePayloadEvent, *BeforePayloadResult], options HookOptions) (func(), error)
	OnAfterResponse(handler HookHandler[AfterResponseEvent, *AfterResponseResult], options HookOptions) (func(), error)
	OnBeforeTool(handler HookHandler[BeforeToolEvent, *BeforeToolResult], options HookOptions) (func(), error)
	OnAfterTool(handler HookHandler[AfterToolEvent, *AfterToolResult], options HookOptions) (func(), error)
	OnBeforeCompaction(handler HookHandler[BeforeCompactionEvent, *BeforeCompactionResult], options HookOptions) (func(), error)
	OnBeforeNavigation(handler HookHandler[BeforeNavigationEvent, *BeforeNavigationResult], options HookOptions) (func(), error)
}

// Gate is the procedure-facing synchronous effect admission capability used by
// hook runners (upstream execution/effect-gate Gate). *execution.Gate
// implements it.
type Gate interface {
	// Signal is cancelled when the drive pass is aborted or closed.
	Signal() harness.Context
	// Admit runs invoke unless the gate refuses admission.
	Admit(invoke func() error) error
}

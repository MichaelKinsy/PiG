// Package runtime holds the durable AgentHarness runtime declarations of
// packages/agent/src/harness/runtime/types.ts: the process-local
// configuration, the lane state projection, lane/operation command decisions
// and the Drive pass with its effect gate.
//
// Drive procedures (agent/harness/runtime/drive) import this package. Upstream
// runtime/lane.ts and the drive procedures import each other, which Go does
// not allow; the Lane implementation that invokes drive procedures must live
// in a package that imports both, or hand procedures its capabilities through
// interfaces declared beside them.
package runtime

import (
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// SliceNotImplemented reports an AgentHarness operation that belongs to a
// later slice (upstream SliceNotImplemented).
type SliceNotImplemented struct {
	Operation string
}

func (err *SliceNotImplemented) Error() string {
	return err.Operation + " is not implemented until its later AgentHarness slice"
}

// ToolContextFactory resolves the tool context for one invocation (upstream
// AgentHarnessOptions.toolContext; a static value is a factory returning it).
type ToolContextFactory func(ctx harness.Context) (any, error)

// SystemPromptFactory builds the system prompt (upstream
// AgentHarnessOptions.systemPrompt; a static string is a factory returning it).
type SystemPromptFactory func(ctx harness.Context, toolContext any) (string, error)

// ProviderMessageConverter converts agent messages to provider messages
// (upstream AgentHarnessOptions.toProviderMessages).
type ProviderMessageConverter func(ctx harness.Context, messages []agent.AgentMessage) ([]ai.Message, error)

// Config is the current process-local harness configuration (upstream
// Config). Values are replaced, never mutated in place.
type Config struct {
	Tools              []harness.AgentHarnessTool
	Resources          agentharness.Resources
	StreamOptions      harness.AgentHarnessStreamOptions
	RetryPolicy        ai.RetryPolicy
	Compaction         harness.CompactionSettings
	SteeringMode       agent.QueueMode
	FollowUpMode       agent.QueueMode
	ToolExecution      agent.ToolExecutionMode
	ToolContext        ToolContextFactory
	SystemPrompt       SystemPromptFactory
	ToProviderMessages ProviderMessageConverter
	EntryProjectors    map[string]session.EntryProjector
}

// LaneState is the current durable state owned by one lane (upstream
// LaneState). Operation is nil when the lane is idle.
type LaneState struct {
	TipID           *string
	Configuration   session.LaneConfiguration
	Inbox           []session.InboxItem
	LastOperationID *string
	Operation       *session.Operation
}

// CommandKind discriminates LaneCommand and OperationCommand.
type CommandKind string

// Command kinds.
const (
	CommandCommit CommandKind = "commit"
	CommandFinish CommandKind = "finish"
	CommandReturn CommandKind = "return"
	CommandReject CommandKind = "reject"
)

// Materializer turns a successful commit into the command result. It runs
// synchronously on the mutation line and must not perform effects.
type Materializer[T any] func(commit session.CommitResult) T

// EventsForCommit derives the events published after a commit.
type EventsForCommit func(commit session.CommitResult) []agentharness.HarnessEvent

// LaneCommand is one effect-free decision made on a lane's serialized
// mutation line (upstream LaneCommand):
//   - commit: Writes, Materialize, optional Events, and Next lane state;
//   - return: Result without writing;
//   - reject: Error without writing.
type LaneCommand[T any] struct {
	Kind        CommandKind
	Writes      []session.Write
	Materialize Materializer[T]
	Events      EventsForCommit
	Next        LaneState
	Result      T
	Error       error
}

// ContinueOperationResult is the outcome of continuing an operation: either
// the operation's cancellation was requested, or Value was produced.
type ContinueOperationResult[T any] struct {
	CancelRequested bool
	Value           T
}

// LanePatch updates selected lane fields together with an operation
// transition (upstream Partial<Pick<LaneState, "tipId" | "configuration" |
// "inbox">>). A false Set* flag leaves the field unchanged; SetTipID with a
// nil TipID sets the tip to the root.
type LanePatch struct {
	SetTipID         bool
	TipID            *string
	SetConfiguration bool
	Configuration    session.LaneConfiguration
	SetInbox         bool
	Inbox            []session.InboxItem
}

// OperationCommand is a durable operation transition (upstream
// OperationCommand). The Lane pairs the state write with projection
// publication:
//   - commit: Writes, Materialize, optional Events, the next OperationState and
//     an optional Lane patch;
//   - finish: Writes, the terminal Record, optional Lane patch, Materialize and
//     optional Events;
//   - return: Result without writing.
type OperationCommand[T any] struct {
	Kind           CommandKind
	Writes         []session.Write
	Materialize    Materializer[T]
	Events         EventsForCommit
	OperationState session.OperationState
	Record         session.OperationResultRecord
	Lane           *LanePatch
	Result         T
}

// ProcedureResultKind discriminates ProcedureResult.
type ProcedureResultKind string

// Procedure result kinds.
const (
	ProcedureContinue ProcedureResultKind = "continue"
	ProcedureWaiting  ProcedureResultKind = "waiting"
	ProcedureSettled  ProcedureResultKind = "settled"
)

// ProcedureResult is one drive procedure step: continue driving, wait with
// Outcome, or settle with Record.
type ProcedureResult struct {
	Kind    ProcedureResultKind
	Outcome agentharness.DriveOutcome
	Record  session.OperationResultRecord
}

// AbortRequested is the expected internal control flow when cancellation wins
// effect admission (upstream AbortRequested); it is the execution gate's
// refusal error.
type AbortRequested = execution.AbortRequested

// Drive is one installed process-local drive pass (upstream Drive). Its
// Context keeps the caller's values without the caller's cancellation. The
// effect gate is procedure-facing; BeginAbort/SignalAbort/CloseGate are the
// owner-facing controls. Completion settles once, with the first of Settle,
// Fail or CloseGate.
type Drive struct {
	OperationID  string
	Gate         *execution.Gate
	Context      harness.Context
	WaitForRetry bool
	// CloseSignal is cancelled, with the close error as cause, by CloseGate.
	CloseSignal harness.Context
	// DeferredPermits counts permitted deferred polls. Upstream mutates it
	// only from the lane's serialized procedures; access it the same way.
	DeferredPermits int

	control    *execution.GateControl
	closeAbort func(cause error)

	completionOnce sync.Once
	completed      chan struct{}
	outcome        agentharness.DriveOutcome
	err            error
}

// NewDrive installs a drive pass for options.OperationID.
func NewDrive(ctx harness.Context, options agentharness.DriveOptions) *Drive {
	gate, control := execution.CreateGate()
	closeSignal, closeAbort := harness.WithCancel(harness.BackgroundContext())
	drive := &Drive{
		OperationID:  options.OperationID,
		Gate:         gate,
		Context:      harness.WithoutAbortSignal(ctx),
		WaitForRetry: options.WaitForRetry,
		CloseSignal:  closeSignal,
		control:      control,
		closeAbort:   closeAbort,
		completed:    make(chan struct{}),
	}
	if options.PollDeferred {
		drive.DeferredPermits = 1
	}
	return drive
}

// Settle resolves the completion with outcome unless already settled.
func (drive *Drive) Settle(outcome agentharness.DriveOutcome) {
	drive.complete(outcome, nil)
}

// Fail rejects the completion with err unless already settled.
func (drive *Drive) Fail(err error) {
	drive.complete(agentharness.DriveOutcome{}, err)
}

func (drive *Drive) complete(outcome agentharness.DriveOutcome, err error) {
	drive.completionOnce.Do(func() {
		drive.outcome, drive.err = outcome, err
		close(drive.completed)
	})
}

// Done is closed once the completion has settled.
func (drive *Drive) Done() <-chan struct{} { return drive.completed }

// Completion waits for the drive completion. A cancelled ctx stops only this
// wait and returns its abort error (upstream awaitWithContext on completion).
func (drive *Drive) Completion(ctx harness.Context) (agentharness.DriveOutcome, error) {
	if err := harness.AwaitWithContext(ctx, drive.completed); err != nil {
		return agentharness.DriveOutcome{}, err
	}
	return drive.outcome, drive.err
}

// BeginAbort closes effect admission with an AbortRequested carrying
// cancellation, without signalling admitted effects yet.
func (drive *Drive) BeginAbort(cancellation func() error) {
	drive.control.BeginAbort(cancellation)
}

// SignalAbort signals admitted effects after the abort marker is durable.
func (drive *Drive) SignalAbort() {
	drive.control.SignalAbort()
}

// CloseGate permanently refuses admission with err, cancels CloseSignal and
// rejects the completion with err unless it already settled.
func (drive *Drive) CloseGate(err error) {
	if err == nil {
		err = errors.New("drive closed")
	}
	drive.control.Close(err)
	drive.closeAbort(err)
	drive.Fail(err)
}

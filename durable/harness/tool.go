// Ports packages/durable/src/harness/tool.ts.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/output"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// ToolTaskKind is the `kind` of a ToolTaskInput and a ToolTaskResult (tool.ts): "model" for a call the model issued,
// "nested" for one a running tool made through executeTool().
type ToolTaskKind string

const (
	// ToolTaskModel is a call the model issued.
	ToolTaskModel ToolTaskKind = "model"
	// ToolTaskNested is a call a running tool made through executeTool().
	ToolTaskNested ToolTaskKind = "nested"
)

// ToolTaskInput is the input of the built-in tool task (tool.ts ToolTaskInput): a model-issued call, read from its
// assistant entry (Kind "model": Assistant, CallId), or a nested call a running tool made through executeTool(), whose
// call the input carries because no entry holds it (Kind "nested": Parent, ParentCallId, Key, Call, Progress).
type ToolTaskInput struct {
	Kind         ToolTaskKind    `json:"kind"`
	Assistant    durable.EntryId `json:"assistant,omitempty"`
	CallId       string          `json:"callId,omitempty"`
	Parent       durable.TaskId  `json:"parent,omitempty"`
	ParentCallId string          `json:"parentCallId,omitempty"`
	Key          string          `json:"key,omitempty"`
	Call         *ai.ToolCall    `json:"call,omitempty"`
	// Progress false commits no running output, details, or diagnostics to the slot; the result still gets them.
	Progress *bool `json:"progress,omitempty"`
}

// ToolTaskResult is the result of the built-in tool task (tool.ts ToolTaskResult): a model-issued call's result is its
// transcript entry and the controls it requested (Kind "model"); a nested call's result is in its caller's
// NestedResultDoc, so the receipt is only {kind: "nested"}. A version 1 task that was already holding its outcome as
// `completing` when the Harness upgraded finishes with a version 1 result, `{entryId, control?}` without kind: a result
// without a kind is a model-issued call's.
type ToolTaskResult struct {
	Kind    ToolTaskKind         `json:"kind"`
	EntryId durable.EntryId      `json:"entryId,omitempty"`
	Control *durable.ToolControl `json:"control,omitempty"`
}

// HarnessError is an error result the Harness writes itself: no content and one error diagnostic with code
// (tool.ts:413-415).
func HarnessError(code, message string) durable.ToolExecutionResult {
	isError := true
	return durable.ToolExecutionResult{
		Output:      []ai.ToolResultMessageContent{},
		IsError:     &isError,
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityError, Code: code, Message: message}},
	}
}

// AppendToolResult appends a pi.tool-result entry (tool.ts:438-460). The content ends with the rendered diagnostics,
// so the stored message is exactly what the model sees; data keeps the structured list. A result's usage is added to
// pi.usage in the same commit.
func AppendToolResult(tx durable.Tx, conversationId durable.ConversationId, call ai.ToolCall, result durable.ToolExecutionResult, timestamp float64, durationMs *int64) (*durable.TypedEntry[durable.ToolResultEntryData], error) {
	diagnostics := append([]durable.ToolDiagnostic{}, result.Diagnostics...)
	content := append([]ai.ToolResultMessageContent{}, result.Output...)
	if len(diagnostics) > 0 {
		content = append(content, ai.TextContent{Text: renderDiagnostics(diagnostics)})
	}
	message := ai.ToolResultMessage{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    content,
		IsError:    result.IsError != nil && *result.IsError,
		DurationMs: durationMs,
		Timestamp:  int64(timestamp),
	}
	if result.HasDetails {
		message.Details, message.DetailsNull = result.Details, result.Details == nil
	}
	if result.Usage != nil {
		usage := *result.Usage
		message.Usage = &usage
		if err := RecordUsage(tx, conversationId, UsageTools, call.Name, usage); err != nil {
			return nil, err
		}
	}
	return durable.TxAppendEntry(tx, durable.ToolResultEntry, conversationId, durable.TypedEntryDraft[durable.ToolResultEntryData]{
		Model: []ai.Message{message},
		Data:  durable.ToolResultEntryData{Diagnostics: diagnostics},
	})
}

func renderDiagnostics(diagnostics []durable.ToolDiagnostic) string {
	lines := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		lines[index] = "[" + string(diagnostic.Severity) + "] " + diagnostic.Message
	}
	return "<harness>\n" + strings.Join(lines, "\n") + "\n</harness>"
}

// ToolTaskCheckpoint is the tool task's checkpoint: call, or execute with the durable intent, the final arguments and
// the replay policy recorded before execution (tool.ts:35-38).
type ToolTaskCheckpoint struct {
	Phase     ToolTaskPhase      `json:"phase"`
	Arguments map[string]any     `json:"arguments"`
	Replay    durable.ToolReplay `json:"replay"`
	// argumentsJSON is Arguments in the call's member order, which the stored intent keeps (tool.ts:163); nil: sorted.
	argumentsJSON json.RawMessage
}

// ToolTaskPhase names a phase of the tool task (tool.ts:36-38).
type ToolTaskPhase string

const (
	toolPhaseCall    ToolTaskPhase = "call"
	toolPhaseExecute ToolTaskPhase = "execute"
)

// MarshalJSON writes {"phase":"call"} or the execute intent with its arguments and replay.
func (checkpoint ToolTaskCheckpoint) MarshalJSON() ([]byte, error) {
	if checkpoint.Phase != toolPhaseExecute {
		return json.Marshal(struct {
			Phase ToolTaskPhase `json:"phase"`
		}{checkpoint.Phase})
	}
	var arguments any = checkpoint.Arguments
	if checkpoint.argumentsJSON != nil {
		arguments = checkpoint.argumentsJSON
	} else if checkpoint.Arguments == nil {
		arguments = map[string]any{}
	}
	return json.Marshal(struct {
		Phase     ToolTaskPhase      `json:"phase"`
		Arguments any                `json:"arguments"`
		Replay    durable.ToolReplay `json:"replay"`
	}{checkpoint.Phase, arguments, checkpoint.Replay})
}

type toolRuntime = durable.TaskRuntime[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, *ToolHooks]
type toolNext = durable.NextTaskState[ToolTaskCheckpoint, ToolTaskResult]
type toolRecord = durable.RunningTask[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult]

// ToolTask is the built-in tool task (tool.ts:48-120): it resolves the called tool among its phase agent's tools,
// validates, runs beforeTool, records intent, executes, runs afterTool, and appends the result, all in one call
// handler so nothing separates resolution from settlement. execute is reached only by recovery and applies the replay
// rule.
var ToolTask durable.Task[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, *ToolHooks]

func init() {
	ToolTask = durable.DefineTask(durable.TaskDefinition[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, *ToolHooks]{
		Name:    "pi.tool",
		Version: 2,
		Initial: func(ToolTaskInput) ToolTaskCheckpoint { return ToolTaskCheckpoint{Phase: toolPhaseCall} },
		// Version 1 had only model-issued calls.
		Migrate: func(input durable.JsonValue, checkpoint durable.JsonValue, _ int) (ToolTaskInput, ToolTaskCheckpoint, error) {
			typedInput, err := durable.FromJsonValue[ToolTaskInput](input)
			if err != nil {
				return ToolTaskInput{}, ToolTaskCheckpoint{}, err
			}
			typedInput.Kind = ToolTaskModel
			typedCheckpoint, err := durable.FromJsonValue[ToolTaskCheckpoint](checkpoint)
			return typedInput, typedCheckpoint, err
		},
		Phases: map[string]durable.PhaseHandler[ToolTaskInput, ToolTaskCheckpoint, ToolTaskResult, *ToolHooks]{
			string(toolPhaseCall):    toolCallPhase,
			string(toolPhaseExecute): toolExecutePhase,
		},
		Abort: toolAbort,
	})
}

func toolCallPhase(ctx context.Context, task toolRecord, runtime toolRuntime) error {
	call, err := readCall(ctx, runtime, task.Input)
	if err != nil {
		return err
	}
	tool, err := findTool(ctx, runtime, task.Input, call.Name)
	if err != nil {
		return err
	}
	if tool == nil {
		unavailable := HarnessError("tool_unavailable", fmt.Sprintf("Tool %s is not available", call.Name))
		return settleTool(ctx, runtime, task.Input, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return unavailable })
	}
	args, invalidReason := prepareArguments(tool, call.Arguments)
	if invalidReason == "" {
		args, invalidReason = validateArguments(tool, call, args)
	}
	if invalidReason != "" {
		return settleTool(ctx, runtime, task.Input, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return invalidArguments(invalidReason) })
	}
	var block *string
	if err := runtime.Hooks().Each("beforeTool", func(hooks *ToolHooks) error {
		if block != nil || hooks == nil || hooks.BeforeTool == nil {
			return nil
		}
		decided := hookCall(task.Input, call)
		decided.Arguments = args
		var decision *BeforeToolResult
		// A panicking hook blocks the call, as a throwing hook does upstream (tool.ts:69-71); it must not fail open.
		err := callTool(func() error {
			var err error
			decision, err = hooks.BeforeTool(ctx, decided, runtime)
			return err
		})
		if err != nil {
			if runtime.Signal().Err() != nil {
				return err
			}
			reason := err.Error()
			block = &reason
			return nil
		}
		if decision != nil && decision.Block != nil {
			block = decision.Block
		} else if decision != nil && decision.Arguments != nil {
			args = decision.Arguments
		}
		return nil
	}); err != nil {
		return err
	}
	if block != nil {
		blocked := HarnessError("blocked", "Tool call blocked: "+*block)
		return settleTool(ctx, runtime, task.Input, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return blocked })
	}
	final, invalidReason := validateArguments(tool, call, args)
	if invalidReason != "" {
		return settleTool(ctx, runtime, task.Input, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return invalidArguments(invalidReason) })
	}
	// final keeps the member order of the call's arguments, as the clone validateToolArguments returns does (tool.ts:155).
	ordered := call
	ordered.Arguments = final
	finalJSON, err := ordered.ArgumentsJSON()
	if err != nil {
		return err
	}
	replay := tool.Replay
	if replay == "" {
		replay = durable.ReplayUnsafe
	}
	if err := runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if slot := FindToolSlot(live, runtime.TaskId()); slot != nil {
			if err := slot.Set("status", string(ToolSlotRunning)); err != nil {
				return nil, err
			}
			// A nested slot shows the arguments the call runs with, which repair, hooks, and coercion may have changed. They
			// are compared with the input, which the slot got at admission: reading the draft would track every leaf.
			if task.Input.Kind == ToolTaskNested && isNestedSlot(slot) && !jsonEqual(task.Input.Call.Arguments, final) {
				shown, err := delta.DecodeJson(finalJSON)
				if err != nil {
					return nil, err
				}
				if err := slot.Set("arguments", shown); err != nil {
					return nil, err
				}
			}
		}
		intent := ToolTaskCheckpoint{Phase: toolPhaseExecute, Arguments: final, Replay: replay, argumentsJSON: finalJSON}
		return &toolNext{Status: durable.TaskRunning, Checkpoint: &intent}, nil
	}); err != nil {
		return err
	}
	return runTool(ctx, runtime, task.Input, call, tool, final)
}

// toolExecutePhase is recovery after intent: rerun only when the stored and the current policy both say safe.
func toolExecutePhase(ctx context.Context, task toolRecord, runtime toolRuntime) error {
	checkpoint := *task.State.Checkpoint
	call, err := readCall(ctx, runtime, task.Input)
	if err != nil {
		return err
	}
	tool, err := findTool(ctx, runtime, task.Input, call.Name)
	if err != nil {
		return err
	}
	if checkpoint.Replay == durable.ReplaySafe && tool != nil && tool.Replay == durable.ReplaySafe {
		// The rerun reports from scratch; clear what the interrupted attempt published.
		if err := runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
			live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
			if err != nil {
				return nil, err
			}
			if slot := FindToolSlot(live, runtime.TaskId()); slot != nil {
				ClearProgress(slot)
			}
			return nil, nil
		}); err != nil {
			return err
		}
		return runTool(ctx, runtime, task.Input, call, tool, checkpoint.Arguments)
	}
	message := fmt.Sprintf("Tool %s was interrupted and may have partially run", call.Name)
	// failed records cancellation intent, so the call's owned conversations, left unsupervised, are aborted.
	ending := toolEnding{status: durable.OutcomeFailed, message: message}
	return settleTool(ctx, runtime, task.Input, call, ending, func(slot *ToolSlot) durable.ToolExecutionResult {
		return fromSlot(slot, "interrupted", message)
	})
}

func toolAbort(ctx context.Context, task toolRecord, runtime toolRuntime) error {
	call, err := readCall(ctx, runtime, task.Input)
	if err != nil {
		return err
	}
	if err := awaitEarlierAborts(ctx, runtime); err != nil {
		return err
	}
	// Abandoned after a restart (TaskOptions.AbandonOnRestart): say whether the call may have run (tool.ts abort).
	code, message := "aborted", fmt.Sprintf("Tool %s was aborted", call.Name)
	if task.AbortReason == durable.AbortedByRestart {
		if task.State.Checkpoint != nil && task.State.Checkpoint.Phase == toolPhaseExecute {
			code, message = "interrupted", fmt.Sprintf("Tool %s was interrupted by a restart and may have partially run", call.Name)
		} else {
			code, message = "abandoned", fmt.Sprintf("Tool %s was not started: its caller ended with a restart", call.Name)
		}
	}
	return settleTool(ctx, runtime, task.Input, call, toolEnding{status: durable.OutcomeAborted}, func(slot *ToolSlot) durable.ToolExecutionResult {
		return fromSlot(slot, code, message)
	})
}

// awaitEarlierAborts waits for the abort-marked, still live earlier calls of this call's round to settle. Pi appends the
// results of a round aborted together in call order: their abort handlers start in reservation order and settle
// through the same number of steps on one microtask queue. Go runs the handlers concurrently, so each one settles
// behind its earlier siblings to keep that order. Earlier calls without an abort mark are not awaited.
func awaitEarlierAborts(ctx context.Context, runtime toolRuntime) error {
	var earlier []durable.TaskId
	err := runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
		state, err := durable.TxDoc(tx, LiveDoc, runtime.ConversationId())
		if err != nil || state == nil {
			return nil, err
		}
		live, err := decodeDraft[LiveState](state)
		if err != nil {
			return nil, err
		}
		for _, slot := range live.Tools {
			if slot.TaskId == nil {
				continue
			}
			if *slot.TaskId == runtime.TaskId() {
				return nil, nil
			}
			if slot.Status == ToolSlotDone {
				continue
			}
			record, err := tx.Task(*slot.TaskId)
			if err != nil {
				return nil, err
			}
			if record != nil && record.AbortRequested && record.State.Status != durable.TaskTerminal {
				earlier = append(earlier, *slot.TaskId)
			}
		}
		// This call has no slot: it belongs to no live round.
		earlier = nil
		return nil, nil
	})
	if err != nil {
		return err
	}
	for _, id := range earlier {
		if _, err := runtime.WaitForTask(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// readCall returns the call: from the input for a nested call, else the tool call callId of the assistant entry (tool.ts readCall).
func readCall(ctx context.Context, runtime toolRuntime, input ToolTaskInput) (ai.ToolCall, error) {
	if input.Kind == ToolTaskNested {
		// A nested call carries its call, because no entry holds it.
		if span := chordctx.Telemetry(ctx); span != telemetry.NoopTelemetryContext {
			span.SetAttributes(telemetry.SpanAttributes{telemetry.Attr("tool.name", input.Call.Name), telemetry.Attr("tool.call_id", input.Call.ID)})
		}
		return *input.Call, nil
	}
	call, found, err := durable.TaskAssistantToolCall(ctx, runtime, input.Assistant, input.CallId)
	if err != nil {
		return ai.ToolCall{}, err
	}
	if found {
		if span := chordctx.Telemetry(ctx); span != telemetry.NoopTelemetryContext {
			span.SetAttributes(telemetry.SpanAttributes{telemetry.Attr("tool.name", call.Name), telemetry.Attr("tool.call_id", call.ID)})
		}
		return call, nil
	}
	return ai.ToolCall{}, fmt.Errorf("Entry %d has no tool call %s", input.Assistant, input.CallId)
}

func findTool(ctx context.Context, runtime toolRuntime, input ToolTaskInput, name string) (*durable.ToolRegistration, error) {
	agent, err := runtime.Agent(ctx)
	if err != nil {
		return nil, err
	}
	candidates := agent.Callable
	if input.Kind == ToolTaskModel {
		candidates = agent.Tools
	}
	for _, tool := range candidates {
		if tool.Name == name {
			return tool, nil
		}
	}
	return nil, nil
}

// prepareArguments returns the call's arguments as repaired by the tool; a failing repair makes them invalid, and
// the reason is returned.
func prepareArguments(tool *durable.ToolRegistration, args map[string]any) (map[string]any, string) {
	if tool.PrepareArguments == nil {
		return args, ""
	}
	var prepared any
	err := callTool(func() error {
		var err error
		prepared, err = tool.PrepareArguments(args)
		return err
	})
	if err != nil {
		return nil, err.Error()
	}
	// tool.ts:141 hands the repaired value to validation as is. A typed Go object (ai.JsonObject, a struct) is that
	// object's JSON copy; a value validation cannot clone makes the arguments invalid, as structuredClone throws
	// inside validate's try. A non-object reaches validation as null and fails its object check.
	if object, ok := prepared.(map[string]any); ok || prepared == nil {
		return object, ""
	}
	copied, err := durable.ToJsonValue(prepared)
	if err != nil {
		return nil, err.Error()
	}
	// Tool arguments are pi-ai values, which Go keeps as ai.JsonObject maps.
	var object map[string]any
	if encoded, isObject := copied.(*delta.JsonObject); isObject {
		text, _ := json.Marshal(encoded)
		_ = json.Unmarshal(text, &object)
	}
	return object, ""
}

// validateArguments returns the arguments validated and coerced against the implementation's schema, or why they
// are invalid.
func validateArguments(tool *durable.ToolRegistration, call ai.ToolCall, args map[string]any) (map[string]any, string) {
	checked := call
	checked.Arguments = args
	validated, err := ai.ValidateToolArguments(tool.ToolSchema, checked)
	if err != nil {
		return nil, err.Error()
	}
	return validated, ""
}

func invalidArguments(message string) durable.ToolExecutionResult {
	return HarnessError("invalid_arguments", message)
}

// callTool runs tool code and turns a panic into an error, as a throw is an error upstream.
func callTool(run func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(error); ok {
				err = failure
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return run()
}

// toolReported is what a running tool reported through its api: output, the last details, and diagnostics.
type toolReported struct {
	mu          sync.Mutex
	output      *output.OutputBuffer
	limits      output.OutputLimits
	diagnostics []durable.ToolDiagnostic
	details     durable.JsonValue
	hasDetails  bool
	// detailsVersion counts details() calls; each is a new value, as each upstream copy is a new object.
	detailsVersion int
}

// toolEnding is how a tool task ends; the result entry is appended either way. failed (execution threw or was
// interrupted) records cancellation intent for the conversations the call owns; a result with isError still
// completes.
type toolEnding struct {
	status  durable.TaskOutcomeStatus
	message string
}

var toolCompleted = toolEnding{status: durable.OutcomeCompleted}

// ToolOutputLimits is the bound a tool's retained output keeps: the tool's own limits over the defaults (tool.ts run).
func ToolOutputLimits(tool *durable.ToolRegistration) output.OutputLimits {
	limits := output.OutputLimits{MaxBytes: durable.DEFAULT_MAX_BYTES, MaxLines: durable.DEFAULT_MAX_LINES, Retain: string(durable.RetainHead)}
	if tool.OutputLimits != nil {
		if tool.OutputLimits.MaxBytes != nil {
			limits.MaxBytes = *tool.OutputLimits.MaxBytes
		}
		if tool.OutputLimits.MaxLines != nil {
			limits.MaxLines = *tool.OutputLimits.MaxLines
		}
		if tool.OutputLimits.Retain != "" {
			limits.Retain = string(tool.OutputLimits.Retain)
		}
	}
	return limits
}

// runTool executes with the resolved implementation, then settles its result (tool.ts:157-262).
func runTool(ctx context.Context, runtime toolRuntime, input ToolTaskInput, call ai.ToolCall, tool *durable.ToolRegistration, args map[string]any) error {
	limits := ToolOutputLimits(tool)
	reported := &toolReported{output: output.NewOutputBuffer(limits), limits: limits}
	progress := publishProgress(ctx, runtime, reported, input.Kind != ToolTaskNested || input.Progress == nil || *input.Progress)
	api := &toolApi{runtime: runtime, call: call, tool: tool, reported: reported, progress: progress, nested: NewNestedCalls(call.ID, tool.Replay == durable.ReplaySafe)}
	if limits.Retain == string(durable.RetainTail) {
		api.window = &env.ShellOutputWindow{
			MaxBytes:       limits.MaxBytes,
			MaxLines:       limits.MaxLines,
			MinIntervalMs:  runtime.Settings().Progress.OutputIntervalMs,
			BytesPerSecond: output.ProgressBytesPerSecond,
		}
	}

	var result durable.ToolExecutionResult
	ending := toolCompleted
	// Execution time of this attempt; a rerun after recovery measures only itself. The environment build and the hooks are
	// not part of it, and a failed Execute still has a duration.
	var durationMs *int64
	err := callTool(func() error {
		// Built for this call, so a rerun after recovery gets the conversation's environment at that time.
		environment, err := runtime.Env(ctx)
		if err != nil {
			return err
		}
		api.env = environment
		startedAt := time.Now()
		defer func() {
			durationMs = new(int64(math.Round(float64(time.Since(startedAt)) / float64(time.Millisecond))))
		}()
		result, err = tool.Execute(ctx, args, api)
		return err
	})
	if err != nil {
		if runtime.Signal().Err() != nil {
			api.ended.Store(true)
			api.nested.End()
			output.SettleInOrder(progress.Stop(), err)
			return err
		}
		isError := true
		result = durable.ToolExecutionResult{IsError: &isError, Diagnostics: []durable.ToolDiagnostic{toolDiagnostic("tool_error", err.Error())}}
		// A failure, from Execute or from building the environment, ends the task failed, which cancels what the call
		// owned; it no longer supervises it. The error text is already in the result entry.
		ending = toolEnding{status: durable.OutcomeFailed, message: fmt.Sprintf("Tool %s threw", call.Name)}
	}
	api.ended.Store(true)
	// No admission starts after ended; let those underway commit, so settlement aborts and lists their calls.
	api.nested.End()
	reported.mu.Lock()
	reported.output.End()
	reported.mu.Unlock()
	// Details still waiting for a progress commit settle with the terminal commit, the final flush.
	pending := progress.Stop()
	// The caller of the details() the last progress commit settled may still be running; it runs before what follows.
	if trailing := progress.TrailingWaiter(); trailing != nil {
		output.AwaitCaller(trailing)
	}
	settled, err := finalResult(ctx, runtime, input, tool, call, result, reported)
	commitCtx := ctx
	if len(pending) > 0 {
		// Upstream resolves each of them after the terminal commit (tool.ts:420-428) and runs their continuations before the
		// scheduler pass the commit's publication queued, so they run before the owner's next step (harness-tools.test.ts:1019).
		// The commit keeps the Session line position behind it until they ran, and the pass and the owner's steps queue behind that.
		var hold *lineHold
		commitCtx, hold = withLineHold(ctx)
		defer hold.release()
	}
	if err == nil {
		err = settleTool(commitCtx, runtime, input, call, ending, func(*ToolSlot) durable.ToolExecutionResult { return settled }, durationMs)
	}
	output.SettleAndAwait(pending, err)
	return err
}

// toolApi is the ToolExecutionApi of one call (tool.ts:177-238).
type toolApi struct {
	runtime  toolRuntime
	call     ai.ToolCall
	env      env.ExecutionEnv
	reported *toolReported
	progress *output.Progress
	window   *env.ShellOutputWindow
	ended    atomic.Bool
	tool     *durable.ToolRegistration
	nested   *NestedCalls
}

var _ durable.ToolExecutionApi = (*toolApi)(nil)

// assertLive panics once the call settled, as upstream's api methods throw synchronously.
func (api *toolApi) assertLive() {
	if api.ended.Load() {
		panic(fmt.Errorf("Tool call %s has settled", api.call.ID))
	}
}

func (api *toolApi) TaskId() durable.TaskId { return api.runtime.TaskId() }

func (api *toolApi) ConversationId() durable.ConversationId { return api.runtime.ConversationId() }

func (api *toolApi) CallId() string { return api.call.ID }

func (api *toolApi) Registry() durable.RegistrySnapshot { return api.runtime.Registry() }

func (api *toolApi) Models() durable.Models { return api.runtime.Models() }

func (api *toolApi) Agent(ctx context.Context) (durable.Agent, error) { return api.runtime.Agent(ctx) }

func (api *toolApi) Env() env.ExecutionEnv { return api.env }

func (api *toolApi) Now() float64 { return api.runtime.Now() }

// Output appends a string or byte chunk to the retained output. A chunk that follows output an environment omitted
// carries skipped; tail retention only.
func (api *toolApi) Output(chunk any, skipped ...env.ShellOutputSkip) {
	if len(skipped) == 0 {
		api.output(chunk, nil)
		return
	}
	api.output(chunk, &skipped[0])
}

// OutputWindow is the tail window offered to the environment; nil for head retention.
func (api *toolApi) OutputWindow() *env.ShellOutputWindow { return api.window }

// RetainedOutput is the output retained so far and whether earlier output was dropped.
func (api *toolApi) RetainedOutput() (string, bool) {
	api.assertLive()
	api.reported.mu.Lock()
	defer api.reported.mu.Unlock()
	retained := api.reported.output.Snapshot()
	return retained.Text, retained.DroppedBytes > 0
}

func (api *toolApi) output(chunk any, skipped *env.ShellOutputSkip) {
	api.assertLive()
	api.reported.mu.Lock()
	var accepted bool
	var err error
	switch typed := chunk.(type) {
	case string:
		if skipped == nil {
			accepted = api.reported.output.PushString(typed)
		} else {
			accepted, err = api.reported.output.PushStringSkipping(typed, output.OutputSkip(*skipped))
		}
	case []byte:
		if skipped == nil {
			accepted = api.reported.output.PushBytes(typed)
		} else {
			accepted, err = api.reported.output.PushBytesSkipping(typed, output.OutputSkip(*skipped))
		}
	default:
		api.reported.mu.Unlock()
		panic(fmt.Errorf("tool output chunk is %T, not a string or bytes", chunk))
	}
	api.reported.mu.Unlock()
	if err != nil {
		panic(err)
	}
	if accepted {
		api.progress.Mark()
	}
}

func (api *toolApi) Diagnostic(diagnostic durable.ToolDiagnostic) {
	api.assertLive()
	api.reported.mu.Lock()
	api.reported.diagnostics = append(api.reported.diagnostics, diagnostic)
	api.reported.mu.Unlock()
	api.progress.Mark()
}

// Details replaces the details and waits for the progress commit that publishes them; cancelling ctx cancels only the
// wait.
func (api *toolApi) Details(ctx context.Context, value durable.JsonValue) error {
	api.assertLive()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	copied, err := durable.CopyJson(value)
	if err != nil {
		return err
	}
	// The value and its waiter are queued in one step, as upstream's details() does (tool.ts:322-327), so a call that saw the
	// value has its waiter in the final flush.
	api.reported.mu.Lock()
	api.reported.details = copied
	api.reported.hasDetails = true
	api.reported.detailsVersion++
	waiter := api.progress.MarkAndWait()
	api.reported.mu.Unlock()
	return waiter.Wait(ctx)
}

func (api *toolApi) Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
	var result any
	err := api.runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
		var err error
		result, err = change(tx)
		return nil, err
	})
	return result, err
}

func (api *toolApi) Memo(ctx context.Context, name string) (durable.JsonValue, bool, error) {
	return api.runtime.Memo(ctx, name)
}

func (api *toolApi) MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error) {
	return api.runtime.MemoCandidate(ctx, name, candidate)
}

// CreateTaskErased creates a task in its own commit; the conversation is the tool task's own default.
func (api *toolApi) CreateTaskErased(ctx context.Context, task durable.AnyTask, input durable.JsonValue, options durable.TaskOptions) (durable.TaskId, error) {
	options.ConversationId = nil
	// A caller that can rerun reattaches to its child tasks after a restart; any other caller never resumes, so they are
	// abandoned (TaskOptions.AbandonOnRestart) unless the options say otherwise (tool.ts:338).
	if options.Ownership.Kind == durable.TaskOwnedByTask && !api.resumes() && options.AbandonOnRestart == nil {
		options.AbandonOnRestart = new(true)
	}
	var id durable.TaskId
	err := api.runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
		var err error
		id, err = tx.CreateTaskErased(task, input, options)
		return nil, err
	})
	return id, err
}

func (api *toolApi) GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	return api.runtime.GetTask(ctx, id)
}

func (api *toolApi) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	return api.runtime.WaitForTask(ctx, id)
}

func (api *toolApi) Conversation(ctx context.Context, id durable.ConversationId) (durable.ConversationHandle, error) {
	return api.runtime.Conversation(ctx, id)
}

func (api *toolApi) SnapshotErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	return api.runtime.SnapshotErased(ctx, token, args...)
}

func (api *toolApi) SnapshotAsOfErased(ctx context.Context, token durable.AnyDocToken, at durable.EntryId, args ...any) (durable.JsonObject, error) {
	return api.runtime.SnapshotAsOfErased(ctx, token, at, args...)
}

func (api *toolApi) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	return api.runtime.WatchDocErased(ctx, token, args...)
}

// toolProgressClock, when set, times tool progress commits instead of the Harness clock and timers; tests replace it to
// hold the throttled commits.
var toolProgressClock *output.ProgressClock

// progressClock is the Harness clock and timers for progress pacing (tool.ts progressClock). After the invocation ends nothing more
// commits, and the clock answers with the last time it read.
func progressClock(runtime toolRuntime) output.ProgressClock {
	if toolProgressClock != nil {
		return *toolProgressClock
	}
	var mu sync.Mutex
	last := 0.0
	return output.ProgressClock{
		Now: func() (now float64) {
			defer func() {
				if recovered := recover(); recovered != nil {
					// Only the ended invocation's check is Pi's caught throw; any other panic is a failure and propagates.
					if err, ok := recovered.(error); !ok || runtime.Signal().Err() == nil || err.Error() != fmt.Sprintf("Task %d invocation has ended", runtime.TaskId()) {
						panic(recovered)
					}
					mu.Lock()
					now = last
					mu.Unlock()
				}
			}()
			now = runtime.Now()
			mu.Lock()
			last = now
			mu.Unlock()
			return now
		},
		SetTimer: func(callback func(), delayMs float64) func() { return runtime.SetTimer(callback, delayMs) },
	}
}

// publishProgress returns throttled commits of what the tool reported into its pi.live.tools slot, each writing only
// what changed since the last one (tool.ts:268-318).
func publishProgress(ctx context.Context, runtime toolRuntime, reported *toolReported, enabled bool) *output.Progress {
	written := struct {
		text           string
		detailsVersion int
		diagnostics    int
	}{}
	return output.NewProgressWithClock(func() (int, error) {
		if !enabled {
			return 0, nil
		}
		// Capture everything at once: the tool keeps reporting while the commit is in flight.
		reported.mu.Lock()
		snapshot := reported.output.Snapshot()
		details, hasDetails, detailsVersion := reported.details, reported.hasDetails, reported.detailsVersion
		added := slices.Clone(reported.diagnostics[written.diagnostics:])
		diagnostics := len(reported.diagnostics)
		reported.mu.Unlock()
		detailsChanged := detailsVersion != written.detailsVersion
		// What the commit writes, as Chord diffs the string: an append, a trim plus an append of what follows the
		// shared part, or the whole window when its bounded overlap search finds nothing.
		bytes := 0
		if snapshot.Text != written.text {
			shared := len(written.text)
			if !strings.HasPrefix(snapshot.Text, written.text) {
				shared = delta.Overlap(written.text, snapshot.Text, 65_536)
			}
			bytes += durable.Utf8ByteLength(snapshot.Text[shared:])
		}
		if detailsChanged {
			bytes += durable.Utf8ByteLength(ai.SafeJsonStringify(details))
		}
		if len(added) > 0 {
			bytes += durable.Utf8ByteLength(ai.SafeJsonStringify(added))
		}
		if err := runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
			live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
			if err != nil {
				return nil, err
			}
			slot := FindToolSlot(live, runtime.TaskId())
			if slot == nil {
				return nil, nil
			}
			// Assign output as one string field. Chord then diffs it into an append, or a trim plus an append for a
			// sliding tail; replacing the slot object would record the whole window on every commit.
			if current, _ := slot.Get("output").(string); current != snapshot.Text {
				if err := slot.Set("output", snapshot.Text); err != nil {
					return nil, err
				}
			}
			if snapshot.DroppedBytes > 0 {
				if err := slot.Set("droppedBytes", float64(snapshot.DroppedBytes)); err != nil {
					return nil, err
				}
			}
			if snapshot.DroppedLines > 0 {
				if err := slot.Set("droppedLines", float64(snapshot.DroppedLines)); err != nil {
					return nil, err
				}
			}
			// Diff details leaf by leaf and append new diagnostics, so each commit writes only what changed.
			if detailsChanged && hasDetails {
				if err := AssignJson(slot, "details", details); err != nil {
					return nil, err
				}
			}
			if len(added) > 0 {
				if slot.Array("diagnostics") == nil {
					if err := slot.Set("diagnostics", []any{}); err != nil {
						return nil, err
					}
				}
				values := make([]any, len(added))
				for index, diagnostic := range added {
					values[index] = diagnostic
				}
				if err := pushJSON(slot.Array("diagnostics"), values...); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}); err != nil {
			return 0, err
		}
		written.text, written.detailsVersion, written.diagnostics = snapshot.Text, detailsVersion, diagnostics
		return bytes, nil
	}, func(err error) {
		// Rejections after an abort mark or close are expected; the committed state stays consistent. A close signals the
		// invocations only after its listeners ran, so a commit rejected in between reports the same closed Session.
		if _, failed := errors.AsType[*durable.SessionFailed](err); runtime.Signal().Err() == nil && !failed && !errors.Is(err, session.ErrSessionClosed) {
			runtime.Report(err)
		}
	}, runtime.Settings().Progress.OutputIntervalMs, progressClock(runtime))
}

// finalResult is the settled result (tool.ts:325-353): the tool's result with the retained output and last details
// as fallbacks, its diagnostics after those reported through the api, afterTool applied, and explicit text bounded,
// with the Harness's truncation diagnostic last.
func finalResult(ctx context.Context, runtime toolRuntime, input ToolTaskInput, tool *durable.ToolRegistration, call ai.ToolCall, result durable.ToolExecutionResult, reported *toolReported) (durable.ToolExecutionResult, error) {
	harness := []durable.ToolDiagnostic{}
	reported.mu.Lock()
	var retained *output.BoundedOutput
	if result.Output == nil {
		snapshot := reported.output.Snapshot()
		retained = &snapshot
	}
	details, hasDetails := reported.details, reported.hasDetails
	diagnostics := slices.Clone(reported.diagnostics)
	reported.mu.Unlock()
	content := result.Output
	if retained != nil {
		content = []ai.ToolResultMessageContent{}
		if retained.Text != "" {
			content = append(content, ai.TextContent{Text: retained.Text})
		}
	}
	final := result
	final.Output = content
	if !result.HasDetails {
		final.Details, final.HasDetails = details, hasDetails
	}
	final.Diagnostics = slices.Concat(diagnostics, result.Diagnostics)
	if err := runtime.Hooks().Each("afterTool", func(hooks *ToolHooks) error {
		if hooks == nil || hooks.AfterTool == nil {
			return nil
		}
		replaced, err := hooks.AfterTool(ctx, hookCall(input, call), final, runtime)
		if err != nil {
			return err
		}
		if replaced != nil {
			final = *replaced
		}
		return nil
	}); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	// The retained output's truncation applies only while afterTool kept that content.
	if retained != nil && retained.DroppedBytes > 0 && sameContent(final.Output, content) {
		harness = append(harness, truncated(retained.DroppedLines, retained.DroppedBytes, reported.limits.Retain))
	}
	bounded, droppedBytes, droppedLines := boundContent(final.Output, reported.limits)
	if droppedBytes > 0 {
		harness = append(harness, truncated(droppedLines, droppedBytes, reported.limits.Retain))
	}
	if broken := StructuredOutputError(tool, final); broken != "" {
		final.StructuredOutput, final.HasStructuredOutput = nil, false
		if input.Kind == ToolTaskNested {
			// A nested call whose structuredOutput breaks the contract gets an error result without it.
			isError := true
			final.IsError = &isError
			harness = append(harness, toolDiagnostic("invalid_structured_output", broken))
		} else {
			// The model never sees structured output, so its call stands; the host learns of the broken tool.
			// Only names the extension: a failed resolution leaves the report without the name.
			var extension string
			if agent, err := runtime.Agent(ctx); err == nil {
				extension = ToolExtension(agent, tool.Name)
			}
			runtime.Report(errors.New(broken), durable.ReportFrom{Type: durable.ReportFromExtension, Extension: extension})
		}
	}
	// Programs get the tool's own value: without a schema, its output. The model reads only Output, so model-issued calls
	// skip both.
	if input.Kind == ToolTaskNested && tool.StructuredOutputSchema == nil {
		value, err := durable.ToJsonValue(outputValue(bounded))
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		final.StructuredOutput, final.HasStructuredOutput = value, true
	}
	// A schema tool's error result without structured output has its text only in the output, which programs never get: it
	// becomes an error diagnostic, as a thrown error's message does, unless one says what went wrong already.
	if input.Kind == ToolTaskNested && tool.StructuredOutputSchema != nil && final.IsError != nil && *final.IsError && !final.HasStructuredOutput &&
		!slices.ContainsFunc(slices.Concat(final.Diagnostics, harness), func(diagnostic durable.ToolDiagnostic) bool { return diagnostic.Severity == "error" }) {
		var texts []string
		for _, item := range bounded {
			if text, ok := item.(ai.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
		if text := strings.Join(texts, "\n"); text != "" {
			harness = append(harness, toolDiagnostic("tool_error", text))
		}
	}
	final.Output = bounded
	final.Diagnostics = append(slices.Clone(final.Diagnostics), harness...)
	return final, nil
}

// StructuredOutputError is why a result's structuredOutput breaks the tool's contract, or "" when it keeps it
// (tool.ts structuredOutputError): the schema's strict check, without conversion or coercion.
func StructuredOutputError(tool *durable.ToolRegistration, result durable.ToolExecutionResult) string {
	if tool.StructuredOutputSchema == nil {
		if !result.HasStructuredOutput {
			return ""
		}
		return fmt.Sprintf("Tool %s returned structuredOutput but declares no structuredOutputSchema", tool.Name)
	}
	if !result.HasStructuredOutput {
		if result.IsError != nil && *result.IsError {
			return ""
		}
		return fmt.Sprintf("Tool %s returned no structuredOutput", tool.Name)
	}
	encoded, err := json.Marshal(result.StructuredOutput)
	if err != nil {
		return fmt.Sprintf("Tool %s returned structuredOutput that does not match its schema: root: %v", tool.Name, err)
	}
	path, message, ok, err := ai.CheckSchemaValue(tool.StructuredOutputSchema, encoded)
	if err != nil {
		return fmt.Sprintf("Tool %s returned structuredOutput that does not match its schema: root: %v", tool.Name, err)
	}
	if ok {
		return ""
	}
	return fmt.Sprintf("Tool %s returned structuredOutput that does not match its schema: %s: %s", tool.Name, path, message)
}

// sameContent is upstream's identity comparison of the content array: the same first element and length.
func sameContent(left, right []ai.ToolResultMessageContent) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return (left == nil) == (right == nil)
	}
	return &left[0] == &right[0]
}

// settleTool commits the tool's terminal state (tool.ts:359-385): it appends the result entry, marks the slot done,
// and completes or ends aborted with the entry ID. build receives the slot so interruption and abort can report the
// durable partial output.
func settleTool(ctx context.Context, runtime toolRuntime, input ToolTaskInput, call ai.ToolCall, ending toolEnding, build func(slot *ToolSlot) durable.ToolExecutionResult, durationMs ...*int64) error {
	// A crash in between leaves the call in execute: a safe rerun reattaches to the calls, an interruption ends here. Every live
	// nested call the task owns, also those an earlier attempt made and this one never called again.
	nestedIds, err := nestedCallIds(ctx, runtime)
	if err != nil {
		return err
	}
	// Read before the settling commit: no admission starts after the call ended, and those underway were awaited.
	madeNested, err := madeNestedCallsMemo(ctx, runtime)
	if err != nil {
		return err
	}
	// Mark them all before waiting for any: one may run until a sibling is cancelled.
	if err := abortAll(ctx, runtime, nestedIds); err != nil {
		return err
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		slot := FindToolSlot(live, runtime.TaskId())
		var state *ToolSlot
		if slot != nil {
			decoded, err := decodeDraft[ToolSlot](slot)
			if err != nil {
				return nil, err
			}
			state = &decoded
		}
		result := build(state)
		var duration *int64
		if len(durationMs) > 0 {
			duration = durationMs[0]
		}
		var settled ToolTaskResult
		if input.Kind == ToolTaskNested {
			nested := nestedResult(runtime.TaskId(), result, duration)
			if nested.Usage != nil {
				if err := RecordUsage(tx, runtime.ConversationId(), UsageTools, call.Name, *nested.Usage); err != nil {
					return nil, err
				}
			}
			if err := storeNestedResult(tx, input, call, runtime.TaskId(), nested); err != nil {
				return nil, err
			}
			if slot != nil && isNestedSlot(slot) {
				if err := FinishSlot(slot); err != nil {
					return nil, err
				}
				if err := setJSON(slot, "summary", summaryOf(nested, result.Output)); err != nil {
					return nil, err
				}
			}
			settled = ToolTaskResult{Kind: ToolTaskNested}
		} else {
			entry, err := AppendToolResult(tx, runtime.ConversationId(), call, result, runtime.Now(), duration)
			if err != nil {
				return nil, err
			}
			entryId := entry.Id
			if slot != nil && !isNestedSlot(slot) {
				if err := FinishSlot(slot); err != nil {
					return nil, err
				}
				if err := slot.Set("entry", float64(entryId)); err != nil {
					return nil, err
				}
			}
			settled = ToolTaskResult{Kind: ToolTaskModel, EntryId: entryId}
			if ending.status == durable.OutcomeCompleted {
				settled.Control = result.Control
			}
		}
		// A call that made no nested calls, in any attempt, has no slots below it; skipping the scan keeps many leaves linear.
		if madeNested {
			if err := RemoveNestedSlots(live, runtime.TaskId()); err != nil {
				return nil, err
			}
		}
		outcome := durable.TaskOutcome[ToolTaskResult]{Status: ending.status, Result: &settled}
		if ending.status == durable.OutcomeFailed {
			outcome.Error = &durable.TaskOutcomeError{Message: ending.message}
		}
		return &toolNext{Status: durable.TaskTerminal, Outcome: &outcome}, nil
	})
}

// fromSlot is an error result from the slot's durable partial output, details, and diagnostics.
func fromSlot(slot *ToolSlot, code, message string) durable.ToolExecutionResult {
	diagnostics := []durable.ToolDiagnostic{}
	content := []ai.ToolResultMessageContent{}
	isError := true
	result := durable.ToolExecutionResult{IsError: &isError}
	if slot != nil {
		diagnostics = append(diagnostics, slot.Diagnostics...)
		if slot.DroppedBytes != nil && *slot.DroppedBytes > 0 {
			droppedLines := 0
			if slot.DroppedLines != nil {
				droppedLines = *slot.DroppedLines
			}
			diagnostics = append(diagnostics, truncated(droppedLines, *slot.DroppedBytes, ""))
		}
		if slot.Output != nil && *slot.Output != "" {
			content = append(content, ai.TextContent{Text: *slot.Output})
		}
		if slot.Details != nil {
			result.Details, result.HasDetails = *slot.Details, true
		}
	}
	result.Output = content
	result.Diagnostics = slices.Concat(diagnostics, []durable.ToolDiagnostic{toolDiagnostic(code, message)})
	return result
}

func toolDiagnostic(code, message string) durable.ToolDiagnostic {
	return durable.ToolDiagnostic{Severity: durable.SeverityError, Code: code, Message: message}
}

// truncated is the Harness's truncation diagnostic; retain is empty when rebuilt from a slot after recovery.
func truncated(droppedLines, droppedBytes int, retain string) durable.ToolDiagnostic {
	kept := ""
	switch retain {
	case string(durable.RetainHead):
		kept = " to its beginning"
	case string(durable.RetainTail):
		kept = " to its end"
	}
	return durable.ToolDiagnostic{
		Severity: durable.SeverityWarn,
		Code:     "truncated",
		Message:  fmt.Sprintf("Output truncated%s: %d lines, %d bytes dropped", kept, droppedLines, droppedBytes),
	}
}

// boundContent bounds the text of result content (tool.ts:470-484). When the joined text exceeds the limits, the
// text items are replaced by one bounded item at the position of the first (head) or last (tail) text item; other
// content is kept.
func boundContent(content []ai.ToolResultMessageContent, limits output.OutputLimits) ([]ai.ToolResultMessageContent, int, int) {
	var joined strings.Builder
	keep := -1
	for index, item := range content {
		text, ok := item.(ai.TextContent)
		if !ok {
			continue
		}
		joined.WriteString(text.Text)
		if keep < 0 || limits.Retain != string(durable.RetainHead) {
			keep = index
		}
	}
	bounded := output.BoundOutput(joined.String(), limits)
	if bounded.DroppedBytes == 0 {
		return content, 0, 0
	}
	result := []ai.ToolResultMessageContent{}
	for index, item := range content {
		text, ok := item.(ai.TextContent)
		if !ok {
			result = append(result, item)
		} else if index == keep {
			text.Text = bounded.Text
			result = append(result, text)
		}
	}
	return result, bounded.DroppedBytes, bounded.DroppedLines
}

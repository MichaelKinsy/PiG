// Ports packages/durable/src/harness/tool.ts.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// ToolTaskInput is the input of the built-in tool task: the tool-calling answer and the call to run.
type ToolTaskInput struct {
	Assistant durable.EntryId `json:"assistant"`
	CallId    string          `json:"callId"`
}

// ToolTaskResult is the result of the built-in tool task: its result entry and the controls the result requested.
type ToolTaskResult struct {
	EntryId durable.EntryId      `json:"entryId"`
	Control *durable.ToolControl `json:"control,omitempty"`
}

// HarnessError is an error result the Harness writes itself: no content and one error diagnostic with code
// (tool.ts:413-415).
func HarnessError(code, message string) durable.ToolExecutionResult {
	isError := true
	return durable.ToolExecutionResult{
		Content:     []ai.ToolResultMessageContent{},
		IsError:     &isError,
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityError, Code: code, Message: message}},
	}
}

// AppendToolResult appends a pi.tool-result entry (tool.ts:438-460). The content ends with the rendered diagnostics,
// so the stored message is exactly what the model sees; data keeps the structured list. A result's usage is added to
// pi.usage in the same commit.
func AppendToolResult(tx durable.Tx, conversationId durable.ConversationId, call ai.ToolCall, result durable.ToolExecutionResult, timestamp float64, durationMs *int64) (*durable.TypedEntry[durable.ToolResultEntryData], error) {
	diagnostics := append([]durable.ToolDiagnostic{}, result.Diagnostics...)
	content := append([]ai.ToolResultMessageContent{}, result.Content...)
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
	type execute ToolTaskCheckpoint
	arguments := checkpoint.Arguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	return json.Marshal(execute{Phase: checkpoint.Phase, Arguments: arguments, Replay: checkpoint.Replay})
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
		Version: 1,
		Initial: func(ToolTaskInput) ToolTaskCheckpoint { return ToolTaskCheckpoint{Phase: toolPhaseCall} },
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
	tool, err := findTool(ctx, runtime, call.Name)
	if err != nil {
		return err
	}
	if tool == nil {
		unavailable := HarnessError("tool_unavailable", fmt.Sprintf("Tool %s is not available", call.Name))
		return settleTool(ctx, runtime, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return unavailable })
	}
	args, invalidReason := prepareArguments(tool, call.Arguments)
	if invalidReason == "" {
		args, invalidReason = validateArguments(tool, call, args)
	}
	if invalidReason != "" {
		return settleTool(ctx, runtime, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return invalidArguments(invalidReason) })
	}
	var block *string
	if err := runtime.Hooks().Each("beforeTool", func(hooks *ToolHooks) error {
		if block != nil || hooks == nil || hooks.BeforeTool == nil {
			return nil
		}
		decided := call
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
		return settleTool(ctx, runtime, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return blocked })
	}
	final, invalidReason := validateArguments(tool, call, args)
	if invalidReason != "" {
		return settleTool(ctx, runtime, call, toolCompleted, func(*ToolSlot) durable.ToolExecutionResult { return invalidArguments(invalidReason) })
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
		}
		intent := ToolTaskCheckpoint{Phase: toolPhaseExecute, Arguments: final, Replay: replay}
		return &toolNext{Status: durable.TaskRunning, Checkpoint: &intent}, nil
	}); err != nil {
		return err
	}
	return runTool(ctx, runtime, call, tool, final)
}

// toolExecutePhase is recovery after intent: rerun only when the stored and the current policy both say safe.
func toolExecutePhase(ctx context.Context, task toolRecord, runtime toolRuntime) error {
	checkpoint := *task.State.Checkpoint
	call, err := readCall(ctx, runtime, task.Input)
	if err != nil {
		return err
	}
	tool, err := findTool(ctx, runtime, call.Name)
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
		return runTool(ctx, runtime, call, tool, checkpoint.Arguments)
	}
	message := fmt.Sprintf("Tool %s was interrupted and may have partially run", call.Name)
	// failed records cancellation intent, so the call's owned conversations, left unsupervised, are aborted.
	ending := toolEnding{status: durable.OutcomeFailed, message: message}
	return settleTool(ctx, runtime, call, ending, func(slot *ToolSlot) durable.ToolExecutionResult {
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
	message := fmt.Sprintf("Tool %s was aborted", call.Name)
	return settleTool(ctx, runtime, call, toolEnding{status: durable.OutcomeAborted}, func(slot *ToolSlot) durable.ToolExecutionResult {
		return fromSlot(slot, "aborted", message)
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

// readCall returns the tool call callId of the assistant entry (tool.ts:123-135).
func readCall(ctx context.Context, runtime toolRuntime, input ToolTaskInput) (ai.ToolCall, error) {
	entry, err := durable.TaskEntry(ctx, runtime, durable.AssistantEntry, input.Assistant)
	if err != nil {
		return ai.ToolCall{}, err
	}
	if entry != nil && len(entry.Model) > 0 {
		if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
			for _, block := range message.Content {
				if call, ok := block.(ai.ToolCall); ok && call.ID == input.CallId {
					return call, nil
				}
			}
		}
	}
	return ai.ToolCall{}, fmt.Errorf("Entry %d has no tool call %s", input.Assistant, input.CallId)
}

func findTool(ctx context.Context, runtime toolRuntime, name string) (*durable.ToolRegistration, error) {
	agent, err := runtime.Agent(ctx)
	if err != nil {
		return nil, err
	}
	for _, tool := range agent.Tools {
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
	output      *OutputBuffer
	limits      OutputLimits
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

// runTool executes with the resolved implementation, then settles its result (tool.ts:157-262).
func runTool(ctx context.Context, runtime toolRuntime, call ai.ToolCall, tool *durable.ToolRegistration, args map[string]any) error {
	limits := OutputLimits{MaxBytes: durable.DEFAULT_MAX_BYTES, MaxLines: durable.DEFAULT_MAX_LINES, Retain: string(durable.RetainHead)}
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
	reported := &toolReported{output: NewOutputBuffer(limits), limits: limits}
	progress := publishProgress(ctx, runtime, reported)
	api := &toolApi{runtime: runtime, call: call, reported: reported, progress: progress}
	if limits.Retain == string(durable.RetainTail) {
		api.window = &env.ShellOutputWindow{
			MaxBytes:       limits.MaxBytes,
			MaxLines:       limits.MaxLines,
			MinIntervalMs:  runtime.Settings().Progress.OutputIntervalMs,
			BytesPerSecond: progressBytesPerSecond,
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
			for _, waiter := range progress.Stop() {
				waiter.Reject(err)
			}
			return err
		}
		isError := true
		result = durable.ToolExecutionResult{IsError: &isError, Diagnostics: []durable.ToolDiagnostic{toolDiagnostic("tool_error", err.Error())}}
		// A failure, from Execute or from building the environment, ends the task failed, which cancels what the call
		// owned; it no longer supervises it. The error text is already in the result entry.
		ending = toolEnding{status: durable.OutcomeFailed, message: fmt.Sprintf("Tool %s threw", call.Name)}
	}
	api.ended.Store(true)
	reported.mu.Lock()
	reported.output.End()
	reported.mu.Unlock()
	// Details still waiting for a progress commit settle with the terminal commit, the final flush.
	pending := progress.Stop()
	settled, err := finalResult(ctx, runtime, call, result, reported)
	if err == nil {
		err = settleTool(ctx, runtime, call, ending, func(*ToolSlot) durable.ToolExecutionResult { return settled }, durationMs)
	}
	if err != nil {
		for _, waiter := range pending {
			waiter.Reject(err)
		}
		return err
	}
	for _, waiter := range pending {
		waiter.Resolve()
	}
	return nil
}

// toolApi is the ToolExecutionApi of one call (tool.ts:177-238).
type toolApi struct {
	runtime  toolRuntime
	call     ai.ToolCall
	env      env.ExecutionEnv
	reported *toolReported
	progress *Progress
	window   *env.ShellOutputWindow
	ended    atomic.Bool
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
			accepted, err = api.reported.output.PushStringSkipping(typed, *skipped)
		}
	case []byte:
		if skipped == nil {
			accepted = api.reported.output.PushBytes(typed)
		} else {
			accepted, err = api.reported.output.PushBytesSkipping(typed, *skipped)
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
	api.reported.mu.Lock()
	api.reported.details = copied
	api.reported.hasDetails = true
	api.reported.detailsVersion++
	api.reported.mu.Unlock()
	return api.progress.MarkAndWait().Wait(ctx)
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

// publishProgress returns throttled commits of what the tool reported into its pi.live.tools slot, each writing only
// what changed since the last one (tool.ts:268-318).
// toolProgressClock times tool progress commits; tests replace it to hold the throttled commits.
var toolProgressClock progressClock = systemProgressClock{}

func publishProgress(ctx context.Context, runtime toolRuntime, reported *toolReported) *Progress {
	written := struct {
		text           string
		detailsVersion int
		diagnostics    int
	}{}
	return newProgressWithClock(func() (int, error) {
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
		// Rejections after an abort mark or close are expected; the committed state stays consistent.
		if runtime.Signal().Err() == nil {
			runtime.Report(err)
		}
	}, runtime.Settings().Progress.OutputIntervalMs, toolProgressClock)
}

// finalResult is the settled result (tool.ts:325-353): the tool's result with the retained output and last details
// as fallbacks, its diagnostics after those reported through the api, afterTool applied, and explicit text bounded,
// with the Harness's truncation diagnostic last.
func finalResult(ctx context.Context, runtime toolRuntime, call ai.ToolCall, result durable.ToolExecutionResult, reported *toolReported) (durable.ToolExecutionResult, error) {
	harness := []durable.ToolDiagnostic{}
	reported.mu.Lock()
	var retained *BoundedOutput
	if result.Content == nil {
		snapshot := reported.output.Snapshot()
		retained = &snapshot
	}
	details, hasDetails := reported.details, reported.hasDetails
	diagnostics := slices.Clone(reported.diagnostics)
	reported.mu.Unlock()
	content := result.Content
	if retained != nil {
		content = []ai.ToolResultMessageContent{}
		if retained.Text != "" {
			content = append(content, ai.TextContent{Text: retained.Text})
		}
	}
	final := result
	final.Content = content
	if !result.HasDetails {
		final.Details, final.HasDetails = details, hasDetails
	}
	final.Diagnostics = slices.Concat(diagnostics, result.Diagnostics)
	if err := runtime.Hooks().Each("afterTool", func(hooks *ToolHooks) error {
		if hooks == nil || hooks.AfterTool == nil {
			return nil
		}
		replaced, err := hooks.AfterTool(ctx, call, final, runtime)
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
	if retained != nil && retained.DroppedBytes > 0 && sameContent(final.Content, content) {
		harness = append(harness, truncated(retained.DroppedLines, retained.DroppedBytes, reported.limits.Retain))
	}
	bounded, droppedBytes, droppedLines := boundContent(final.Content, reported.limits)
	if droppedBytes > 0 {
		harness = append(harness, truncated(droppedLines, droppedBytes, reported.limits.Retain))
	}
	final.Content = bounded
	final.Diagnostics = append(slices.Clone(final.Diagnostics), harness...)
	return final, nil
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
func settleTool(ctx context.Context, runtime toolRuntime, call ai.ToolCall, ending toolEnding, build func(slot *ToolSlot) durable.ToolExecutionResult, durationMs ...*int64) error {
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
		entry, err := AppendToolResult(tx, runtime.ConversationId(), call, result, runtime.Now(), duration)
		if err != nil {
			return nil, err
		}
		entryId := entry.Id
		if slot != nil {
			if err := FinishSlot(slot, &entryId); err != nil {
				return nil, err
			}
		}
		outcome := durable.TaskOutcome[ToolTaskResult]{Status: ending.status, Result: &ToolTaskResult{EntryId: entryId}}
		switch ending.status {
		case durable.OutcomeFailed:
			outcome.Error = &durable.TaskOutcomeError{Message: ending.message}
		case durable.OutcomeCompleted:
			outcome.Result.Control = result.Control
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
	result.Content = content
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
func boundContent(content []ai.ToolResultMessageContent, limits OutputLimits) ([]ai.ToolResultMessageContent, int, int) {
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
	bounded := BoundOutput(joined.String(), limits)
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

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports the nested-call half of packages/durable/src/harness/tool.ts (executeTool, startNestedCall, settle's nested
// branch, nestedResult, fallbackResult, outputValue, checkKey).

// madeNestedCalls is the memo of a tool task that made a nested call, in any attempt, committed before the first admission
// of each attempt: only then can pi.live hold slots below it, which settling removes. A crash between the two only costs a
// scan.
const madeNestedCalls = "pi.tool.madeNestedCalls"

// NestedCallState is one nested call of a running tool: its task, and once it ended, its result.
type NestedCallState struct {
	// TaskId is absent only inside the commit that creates the call; a creation that fails leaves no member.
	TaskId *durable.TaskId `json:"taskId,omitempty"`
	// Result is exactly what ExecuteTool returns; absent while running, and for a call the scheduler faulted or orphaned.
	Result *durable.NestedToolExecutionResult `json:"result,omitempty"`
}

// nestedCallKind is the kind of NestedCallDoc, which tool events recognize in a commit.
const nestedCallKind = "pi.tool.nested-call"

// NestedCallDoc is the built-in pi.tool.nested-call family of a calling task, keyed by the nested call's key: so a rerun
// finds the calls it already made, and the caller reads each result. Created with the nested task; the nested call's
// terminal commit adds its result. One document per call, so no commit rewrites the others. It retires with the caller.
var NestedCallDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[NestedCallState, any]{
	Kind:              nestedCallKind,
	Version:           1,
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeTask},
	Family:            true,
	Initial:           func(any) NestedCallState { return NestedCallState{} },
})

// nestedCallIds are the live nested tool calls the task owns, in task ID order: also those an earlier attempt made and this one
// never called again (tool.ts settle, over TaskRuntime.ownedTasks).
func nestedCallIds(ctx context.Context, runtime toolRuntime) ([]durable.TaskId, error) {
	owned, err := runtime.OwnedTasks(ctx)
	if err != nil {
		return nil, err
	}
	var ids []durable.TaskId
	for _, record := range owned {
		if record.Kind != ToolTask.AnyDefinition().Name {
			continue
		}
		if input, err := durable.FromJsonValue[ToolTaskInput](record.Input); err == nil && input.Kind == ToolTaskNested {
			ids = append(ids, record.Id)
		}
	}
	return ids, nil
}

// madeNestedCallsMemo is whether the tool task made a nested call in any attempt.
func madeNestedCallsMemo(ctx context.Context, runtime toolRuntime) (bool, error) {
	value, found, err := runtime.Memo(ctx, madeNestedCalls)
	if err != nil || !found {
		return false, err
	}
	made, _ := value.(bool)
	return made, nil
}

// abortAll aborts the owned tasks together and waits until each is terminal, returning the first error in ID order.
func abortAll(ctx context.Context, runtime toolRuntime, ids []durable.TaskId) error {
	errs := make([]error, len(ids))
	var wait sync.WaitGroup
	for index, id := range ids {
		wait.Go(func() { errs[index] = runtime.AbortOwned(ctx, id) })
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// nestedResult is a nested call's result, as stored and returned: strict JSON, without Output, which only the model
// reads, and without Control, which only model-issued calls apply.
func nestedResult(taskId durable.TaskId, result durable.ToolExecutionResult, durationMs *int64) durable.NestedToolExecutionResult {
	nested := durable.NestedToolExecutionResult{
		TaskId:              taskId,
		StructuredOutput:    result.StructuredOutput,
		HasStructuredOutput: result.HasStructuredOutput,
		IsError:             result.IsError != nil && *result.IsError,
		Details:             result.Details,
		HasDetails:          result.HasDetails,
		Diagnostics:         result.Diagnostics,
		Usage:               result.Usage,
		DurationMs:          durationMs,
	}
	if nested.Diagnostics == nil {
		nested.Diagnostics = []durable.ToolDiagnostic{}
	}
	return nested
}

// storeNestedResult adds a nested call's result to its document in its caller's NestedCallDoc, in the nested call's terminal
// commit.
func storeNestedResult(tx durable.Tx, input ToolTaskInput, call ai.ToolCall, taskId durable.TaskId, nested durable.NestedToolExecutionResult) error {
	// The caller is never terminal before its owned nested calls, so its document is open; another task in it is a bug, not a
	// race.
	state, err := tx.Doc(NestedCallDoc, input.Parent, input.Key, nil)
	if err != nil {
		return err
	}
	if state.Get("taskId") != float64(taskId) {
		return fmt.Errorf("Nested call %s is not in its caller's index", call.ID)
	}
	return setJSON(state, "result", nested)
}

// resumes is whether the tool can rerun after a restart, so its caller reattaches to the nested calls it made.
func (api *toolApi) resumes() bool { return api.tool.Replay == durable.ReplaySafe }

// NestedCaller is what a running tool call lends nested-call admission: its identity, reads, waiting for a task, and
// commits fenced to the call.
type NestedCaller interface {
	durable.DocumentReader
	TaskId() durable.TaskId
	ConversationId() durable.ConversationId
	// CommitFenced runs change in one commit that fails once the calling task is aborted or settled.
	CommitFenced(ctx context.Context, change func(tx durable.Tx) error) error
	WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error)
	// MemoCandidate stores a memo of the calling task unless one exists.
	MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error)
}

// runtimeCaller lends a task runtime, whose commits are fenced to its task.
type runtimeCaller struct{ toolRuntime }

func (caller runtimeCaller) CommitFenced(ctx context.Context, change func(tx durable.Tx) error) error {
	return caller.Commit(ctx, func(tx durable.Tx, _ toolRecord) (*toolNext, error) { return nil, change(tx) })
}

// NestedCalls is the nested-call state of one running tool call: the default key sequence and the admissions its cleanup
// awaits. A call's admissions are awaited at cleanup, and none starts after it.
type NestedCalls struct {
	callId   string
	resumes  bool
	mu       sync.Mutex
	sequence int
	ended    bool
	// admissions counts the nested calls being admitted.
	admissions sync.WaitGroup
	// made is whether this attempt committed the madeNestedCalls memo; a failed commit is tried again by the next call.
	madeMu sync.Mutex
	made   bool
}

// markMade commits the madeNestedCalls memo, once per attempt, before the attempt's first admission.
func (n *NestedCalls) markMade(ctx context.Context, caller NestedCaller) error {
	n.madeMu.Lock()
	defer n.madeMu.Unlock()
	if n.made {
		return nil
	}
	if _, err := caller.MemoCandidate(ctx, madeNestedCalls, true); err != nil {
		return err
	}
	n.made = true
	return nil
}

// NewNestedCalls returns the nested-call state of tool call callId. resumes is whether the tool can rerun after a
// restart, so its caller reattaches to the nested calls it made; any other caller never resumes, so its nested calls
// are abandoned.
func NewNestedCalls(callId string, resumes bool) *NestedCalls {
	return &NestedCalls{callId: callId, resumes: resumes}
}

// End refuses further admissions and lets those underway commit, so settlement aborts and lists their calls.
func (n *NestedCalls) End() {
	n.mu.Lock()
	n.ended = true
	n.mu.Unlock()
	n.admissions.Wait()
}

// Execute calls another tool as a nested call (tool.ts executeTool).
func (n *NestedCalls) Execute(ctx context.Context, caller NestedCaller, name string, args map[string]any, options ...durable.NestedCallOptions) (durable.NestedToolExecutionResult, error) {
	var option durable.NestedCallOptions
	if len(options) > 0 {
		option = options[0]
	}
	n.mu.Lock()
	if n.ended {
		n.mu.Unlock()
		// executeTool is async upstream: assertLive is a rejection of the returned promise, not a synchronous throw.
		return durable.NestedToolExecutionResult{}, fmt.Errorf("Tool call %s has settled", n.callId)
	}
	var key string
	if option.Key != nil {
		key = *option.Key
		if err := checkNestedKey(key); err != nil {
			n.mu.Unlock()
			return durable.NestedToolExecutionResult{}, err
		}
	} else {
		n.sequence++
		key = strconv.Itoa(n.sequence)
	}
	// Registered now, so cleanup waits for it, including the memo.
	n.admissions.Add(1)
	n.mu.Unlock()
	var id durable.TaskId
	err := n.markMade(ctx, caller)
	if err == nil {
		id, err = startNestedCall(ctx, caller, n.callId, name, args, key, option, !n.resumes)
	}
	n.admissions.Done()
	if err != nil {
		return durable.NestedToolExecutionResult{}, err
	}
	settled, err := caller.WaitForTask(ctx, id)
	if err != nil {
		return durable.NestedToolExecutionResult{}, err
	}
	stored, err := durable.Snapshot[NestedCallState](ctx, caller, NestedCallDoc, caller.TaskId(), key)
	if err != nil {
		return durable.NestedToolExecutionResult{}, err
	}
	// A copy the tool may change; a nested call the scheduler faulted or orphaned stored none.
	if stored == nil || stored.Result == nil {
		return fallbackResult(id, name, settled.State.Outcome), nil
	}
	return *stored.Result, nil
}

// ExecuteTool calls another tool as a nested call.
func (api *toolApi) ExecuteTool(ctx context.Context, name string, args map[string]any, options ...durable.NestedCallOptions) (durable.NestedToolExecutionResult, error) {
	return api.nested.Execute(ctx, runtimeCaller{api.runtime}, name, args, options...)
}

// checkNestedKey rejects explicit keys that are empty, have a slash, are "__proto__", or are positive integers: keys are
// path segments of the call ID, so IDs of different nested calls never collide, and default keys are positive integers.
func checkNestedKey(key string) error {
	if key == "" || strings.Contains(key, "/") || key == "__proto__" || isPositiveInteger(key) {
		encoded, _ := json.Marshal(key)
		return fmt.Errorf(`Nested call key %s must be non-empty, without "/", not "__proto__", and not a positive integer`, encoded)
	}
	return nil
}

func isPositiveInteger(key string) bool {
	if key == "" || key[0] < '1' || key[0] > '9' {
		return false
	}
	for _, character := range key {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// startNestedCall creates nested call key of the call parentCallId in one commit: its tool task, owned by the calling
// task, and its NestedCallDoc. A key already made returns that call's task when it names the same tool and arguments.
func startNestedCall(ctx context.Context, runtime NestedCaller, parentCallId, name string, args map[string]any, key string, option durable.NestedCallOptions, abandonOnRestart bool) (durable.TaskId, error) {
	call := ai.ToolCall{ID: parentCallId + "/" + key, Name: name}
	// The stored call keeps the caller's member order, as copyJson(args) keeps the object's (tool.ts:686).
	encoded, err := orderedArguments(args, option.ArgumentsJSON)
	if err != nil {
		return 0, err
	}
	if err := call.SetArgumentsJSON(encoded); err != nil {
		return 0, err
	}
	arguments := call.Arguments
	// The slot shows the call's arguments object, in its order (tool.ts:724).
	slotArguments, err := delta.DecodeJson(encoded)
	if err != nil {
		return 0, err
	}
	progress := option.Progress
	var id durable.TaskId
	err = runtime.CommitFenced(ctx, func(tx durable.Tx) error {
		// Opened, or created empty for a key not made yet.
		state, err := tx.Doc(NestedCallDoc, runtime.TaskId(), key, nil)
		if err != nil {
			return err
		}
		if existing, found := state.Get("taskId").(float64); found {
			record, err := tx.Task(durable.TaskId(existing))
			if err != nil {
				return err
			}
			same := false
			if record != nil {
				if input, err := durable.FromJsonValue[ToolTaskInput](record.Input); err == nil {
					same = input.Kind == ToolTaskNested && input.Call != nil && input.Call.Name == name && sameArguments(input.Call.Arguments, arguments)
				}
			}
			if !same {
				return fmt.Errorf("Nested call %s was already made with another tool or other arguments", call.ID)
			}
			id = durable.TaskId(existing)
			return nil
		}
		input := ToolTaskInput{Kind: ToolTaskNested, Parent: runtime.TaskId(), ParentCallId: parentCallId, Key: key, Call: &call}
		if progress != nil && !*progress {
			input.Progress = progress
		}
		created, err := durable.CreateTask(tx, ToolTask, input, durable.TaskOptions{
			Ownership:        durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: runtime.TaskId()},
			AbandonOnRestart: &abandonOnRestart,
		})
		if err != nil {
			return err
		}
		if err := state.Set("taskId", float64(created)); err != nil {
			return err
		}
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return err
		}
		slots := live.Array("nestedTools")
		slot := NestedToolSlot{CallId: call.ID, ParentCallId: parentCallId, ParentTaskId: runtime.TaskId(), Name: name, TaskId: created, Arguments: slotArguments, Status: ToolSlotPending}
		if slots == nil {
			if err := setJSON(live, "nestedTools", []NestedToolSlot{slot}); err != nil {
				return err
			}
		} else if err := pushJSON(slots, slot); err != nil {
			return err
		}
		id = created
		return nil
	})
	return id, err
}

// orderedArguments is args as strict JSON, which a stored call keeps, in the member order of source.
func orderedArguments(args map[string]any, source json.RawMessage) ([]byte, error) {
	if args == nil {
		return []byte("{}"), nil
	}
	return ai.MarshalJSONInSourceOrder(args, source)
}

// outputValue is what programs get of a tool's output when it declares no schema: one text item as its string, one image
// as itself, nothing as "", and anything else as the content list.
func outputValue(output []ai.ToolResultMessageContent) any {
	switch len(output) {
	case 0:
		return ""
	case 1:
		if text, ok := output[0].(ai.TextContent); ok {
			return text.Text
		}
		return output[0]
	}
	return output
}

// fallbackResult is what the caller gets for a nested call without a stored result: one the scheduler faulted or orphaned.
func fallbackResult(taskId durable.TaskId, name string, outcome *durable.TaskOutcome[durable.JsonValue]) durable.NestedToolExecutionResult {
	status := durable.TaskOutcomeStatus("")
	if outcome != nil {
		status = outcome.Status
	}
	var message string
	switch status {
	case durable.OutcomeFaulted:
		reason := ""
		if outcome.Error != nil {
			reason = outcome.Error.Message
		}
		message = fmt.Sprintf("Tool %s failed: %s", name, reason)
	case durable.OutcomeOrphaned:
		reason := ""
		if outcome.Reason != nil {
			reason = *outcome.Reason
		}
		message = fmt.Sprintf("Tool %s could not resume: %s", name, reason)
	default:
		message = fmt.Sprintf("Tool %s ended without a result", name)
	}
	error := HarnessError(string(status), message)
	return durable.NestedToolExecutionResult{TaskId: taskId, IsError: true, Diagnostics: error.Diagnostics}
}

// sameArguments is structural equality of two argument objects; key order does not matter.
func sameArguments(left, right map[string]any) bool {
	encodedLeft, err := durable.ToJsonValue(left)
	if err != nil {
		return false
	}
	encodedRight, err := durable.ToJsonValue(right)
	return err == nil && jsonEqual(encodedLeft, encodedRight)
}

// isNestedSlot is whether a slot draft is a nested call's (it names its parent task).
func isNestedSlot(slot *delta.Object) bool { return slot.Get("parentTaskId") != nil }

// maxSummaryErrorChars is the error text a nested call's summary keeps.
const maxSummaryErrorChars = 500

// summaryOf is how a nested call ended, for its slot: error text, from its diagnostics or else its output, bounded.
func summaryOf(result durable.NestedToolExecutionResult, output []ai.ToolResultMessageContent) NestedToolSummary {
	summary := NestedToolSummary{IsError: result.IsError, DurationMs: result.DurationMs, Usage: result.Usage}
	if result.IsError {
		summary.Error = jsstring.Slice(errorTextOf(result, output), 0, maxSummaryErrorChars)
	}
	return summary
}

// errorTextOf is the error messages of a nested result, else the text items of its output, joined with newlines.
func errorTextOf(result durable.NestedToolExecutionResult, output []ai.ToolResultMessageContent) string {
	var errorMessages []string
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity == durable.SeverityError {
			errorMessages = append(errorMessages, diagnostic.Message)
		}
	}
	if len(errorMessages) > 0 {
		return strings.Join(errorMessages, "\n")
	}
	var texts []string
	for _, item := range output {
		if text, ok := item.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// hookCall is the call a tool hook sees: with its parent, for a nested call.
func hookCall(input ToolTaskInput, call ai.ToolCall) ToolHookCall {
	if input.Kind == ToolTaskNested {
		return NewToolHookCall(call, &ToolParent{TaskId: input.Parent, CallId: input.ParentCallId})
	}
	return NewToolHookCall(call, nil)
}

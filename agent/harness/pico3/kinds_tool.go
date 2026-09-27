package pico3

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/ai"
)

// ToolHooks are the hook points pi.tool calls.
type ToolHooks struct {
	// BeforeTool runs before invocation, before the started checkpoint. Results
	// chain on Call; Block stops the call. A failing handler blocks.
	BeforeTool func(ctx context.Context, call JsonObject, api *BeforeToolApi) (*BeforeToolResult, error)
	// AfterTool runs after the tool returns and may replace the result;
	// results chain.
	AfterTool func(ctx context.Context, call JsonObject, result ToolResult, info AfterToolInfo) (*ToolResult, error)
}

// BeforeToolResult replaces the call or blocks it.
type BeforeToolResult struct {
	Call  JsonObject
	Block *string
}

// AfterToolInfo identifies the call an AfterTool handler observes.
type AfterToolInfo struct {
	HookApi
	CallId string
}

// BeforeToolApi is what a BeforeTool handler gets.
type BeforeToolApi struct {
	HookApi
	CallId  string
	waiting func(ctx context.Context) error
	memo    func(ctx context.Context, name string, candidate *JsonValue) (JsonValue, bool, error)
	emit    func(ctx context.Context, name string, data JsonValue) error
}

// Waiting marks the call as waiting on this handler's namespace.
func (api *BeforeToolApi) Waiting(ctx context.Context) error { return api.waiting(ctx) }

// Memo stores candidate under name unless a value exists and returns the
// durable winner.
func (api *BeforeToolApi) Memo(ctx context.Context, name string, candidate JsonValue) (JsonValue, error) {
	value, _, err := api.memo(ctx, name, &candidate)
	return value, err
}

// MemoGet reads a memo; ok is false when unset.
func (api *BeforeToolApi) MemoGet(ctx context.Context, name string) (JsonValue, bool, error) {
	return api.memo(ctx, name, nil)
}

// Emit emits a namespaced plugin event.
func (api *BeforeToolApi) Emit(ctx context.Context, name string, data JsonValue) error {
	return api.emit(ctx, name, data)
}

func toolHooksOf(handlers any) *ToolHooks {
	switch typed := handlers.(type) {
	case *ToolHooks:
		return typed
	case ToolHooks:
		return &typed
	default:
		return nil
	}
}

// DefaultToolBounds bound tool output when a declaration omits a limit.
var DefaultToolBounds = ToolOutput{MaxBytes: new(64 * 1024), MaxLines: new(200), Retain: "head"}

type toolBounds struct {
	maxBytes int
	maxLines int
	retain   string
}

func boundsOf(declared *ToolOutput) toolBounds {
	bounds := toolBounds{maxBytes: *DefaultToolBounds.MaxBytes, maxLines: *DefaultToolBounds.MaxLines, retain: DefaultToolBounds.Retain}
	if declared == nil {
		return bounds
	}
	if declared.MaxBytes != nil {
		bounds.maxBytes = *declared.MaxBytes
	}
	if declared.MaxLines != nil {
		bounds.maxLines = *declared.MaxLines
	}
	if declared.Retain != "" {
		bounds.retain = declared.Retain
	}
	return bounds
}

type toolInput struct {
	Assistant Id         `json:"assistant"`
	Call      JsonObject `json:"call"`
	Offered   []string   `json:"offered"`
	Index     int        `json:"index"`
}

func toolInputOf(task Task) toolInput {
	var input toolInput
	_ = decodeInto(task.Input, &input)
	return input
}

func synthetic(text, code string) ToolResult {
	return ToolResult{
		Content:     []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		IsError:     true,
		Diagnostics: []ToolDiagnostic{{Severity: "error", Message: text, Code: code}},
	}
}

func sameIdentity(left, right JsonObject) bool {
	return jsonEqual(left["id"], right["id"]) && jsonEqual(left["name"], right["name"]) && jsonEqual(left["namespace"], right["namespace"])
}

// toolKind runs one tool call: offered-set check, lookup, validation,
// beforeTool, and revalidation; then the durable started checkpoint and the
// invocation. A crash before started reruns all of this.
var toolKind = &Kind{
	Name:     "pi.tool",
	Turn:     true,
	Inflight: []string{"started"},
	Initial:  toolInitial,
	Phases:   map[string]PhaseHandler{"started": toolStarted},
	Abort:    toolAbort,
}

func toolInitial(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	input := toolInputOf(task)
	call := input.Call
	name := str(call, "name")
	if !slices.Contains(input.Offered, name) {
		return Step{Done: closeTool(task, synthetic(fmt.Sprintf("tool %s was not offered", name), "not_offered"), nil, rt.Now(), nil, nil)}, nil
	}
	declaration := rt.Tools()[name]
	if declaration == nil {
		return Step{Done: closeTool(task, synthetic(fmt.Sprintf("tool %s is not registered", name), "missing_tool"), nil, rt.Now(), nil, nil)}, nil
	}
	if bad := invalidArguments(declaration, call["arguments"]); bad != "" {
		return Step{Done: closeTool(task, synthetic("invalid arguments: "+bad, "invalid_arguments"), declaration, rt.Now(), nil, nil)}, nil
	}
	hooked, block, err := beforeTool(ctx, task, rt, call)
	if err != nil {
		return Step{}, err
	}
	if block != nil {
		return Step{Done: closeTool(task, synthetic("blocked: "+*block, "blocked"), declaration, rt.Now(), nil, nil)}, nil
	}
	if !sameIdentity(call, hooked) {
		return Step{Done: closeTool(task, synthetic("blocked: call identity changed", "blocked"), declaration, rt.Now(), nil, nil)}, nil
	}
	if bad := invalidArguments(declaration, hooked["arguments"]); bad != "" {
		return Step{Done: closeTool(task, synthetic("invalid arguments after hook: "+bad, "invalid_arguments"), declaration, rt.Now(), nil, nil)}, nil
	}
	final := storedObject(hooked)
	replay := declaration.Replay
	if replay == "" {
		replay = "unsafe"
	}
	if _, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
		if err := tx.Checkpoint(Checkpoint{"phase": "started", "replay": replay, "call": final}); err != nil {
			return nil, err
		}
		slot, err := rawToolSlot(tx, task.ConversationId, input.Index)
		if err != nil {
			return nil, err
		}
		slot["status"] = "running"
		delete(slot, "waitingOn")
		return nil, emit(tx, ViewEvent{"type": "tool.started", "taskId": float64(task.Id), "callId": str(final, "id"), "name": str(final, "name")})
	}); err != nil {
		return Step{}, err
	}
	closure, err := invokeTool(ctx, task, final, declaration, rt)
	return Step{Done: closure}, err
}

// toolStarted is entered only after reopen. The stored final call is the
// evidence; beforeTool never reruns.
func toolStarted(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	call := obj(task.Checkpoint, "call")
	name := str(call, "name")
	declaration := rt.Tools()[name]
	if declaration == nil {
		return Step{Done: closeTool(task, synthetic(fmt.Sprintf("tool %s unavailable after restart", name), "unavailable"), nil, rt.Now(), call, nil)}, nil
	}
	if str(task.Checkpoint, "replay") != "safe" || declaration.Replay != "safe" {
		return Step{Done: closeTool(task, synthetic(fmt.Sprintf("tool %s was interrupted", name), "interrupted"), declaration, rt.Now(), call, nil)}, nil
	}
	if invalidArguments(declaration, call["arguments"]) != "" {
		return Step{Done: closeTool(task, synthetic(fmt.Sprintf("tool %s was interrupted; arguments no longer validate", name), "interrupted"), declaration, rt.Now(), call, nil)}, nil
	}
	closure, err := invokeTool(ctx, task, call, declaration, rt)
	return Step{Done: closure}, err
}

func toolAbort(ctx context.Context, task Task, rt *Runtime) (AbortClosure, error) {
	for _, conversationId := range task.Owns {
		children, err := CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) ([]Task, error) {
			return tx.Tasks(TaskScan{ConversationId: &conversationId, Status: []string{TaskPending, TaskRunning}})
		})
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.Background {
				continue
			}
			if _, err := rt.AbortTask(ctx, child.Id); err != nil {
				return nil, err
			}
		}
	}
	input := toolInputOf(task)
	return func(_ context.Context, tx *Tx, current Task) (JsonValue, error) {
		call := input.Call
		if checkpointCall := obj(task.Checkpoint, "call"); checkpointCall != nil {
			call = checkpointCall
		}
		entry, err := tx.AppendEntry(current.ConversationId, NewEntry{
			Kind:  "pi.tool_result",
			Model: []JsonObject{toolResultMessage(call, synthetic("tool aborted", "aborted"), rt.Now())},
			Data:  JsonObject{"diagnostics": []any{JsonObject{"severity": "error", "message": "aborted", "code": "aborted"}}},
		})
		if err != nil {
			return nil, err
		}
		sticky, err := coreSticky(tx, current.ConversationId)
		if err != nil {
			return nil, err
		}
		if slot, ok := indexOf(arr(obj(sticky, "turn"), "tools"), input.Index).(map[string]any); ok {
			slot["status"] = "aborted"
			slot["entry"] = float64(entry)
			delete(slot, "waitingOn")
		}
		err = emit(tx, ViewEvent{"type": "tool.aborted", "taskId": float64(current.Id), "callId": str(call, "id"), "entry": float64(entry)})
		return JsonObject{"entry": float64(entry)}, err
	}, nil
}

func indexOf(items []any, index int) any {
	if index < 0 || index >= len(items) {
		return nil
	}
	return items[index]
}

// rawToolSlot returns the sticky turn tool slot at index.
func rawToolSlot(tx *Tx, conversationId Id, index int) (JsonObject, error) {
	sticky, err := coreSticky(tx, conversationId)
	if err != nil {
		return nil, err
	}
	slot, ok := indexOf(arr(obj(sticky, "turn"), "tools"), index).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no tool slot at index %d", index)
	}
	return slot, nil
}

// slotMemo implements first-writer-wins memos stored in the tool slot under
// key. A nil candidate only reads.
func slotMemo(ctx context.Context, rt *Runtime, task Task, key string, candidate *JsonValue, prepare func(tx *Tx) error) (JsonValue, bool, error) {
	type memoResult struct {
		value JsonValue
		ok    bool
	}
	index := toolInputOf(task).Index
	result, err := CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) (memoResult, error) {
		if prepare != nil {
			if err := prepare(tx); err != nil {
				return memoResult{}, err
			}
		}
		slot, err := rawToolSlot(tx, task.ConversationId, index)
		if err != nil {
			return memoResult{}, err
		}
		memos, ok := slot["memos"].(map[string]any)
		if !ok {
			memos = JsonObject{}
			slot["memos"] = memos
		}
		if value, exists := memos[key]; exists {
			return memoResult{cloneJSON(value), true}, nil
		}
		if candidate == nil {
			return memoResult{}, nil
		}
		stored, err := ToStored(*candidate)
		if err != nil {
			return memoResult{}, err
		}
		memos[key] = stored
		return memoResult{cloneJSON(stored), true}, nil
	})
	return result.value, result.ok, err
}

// beforeTool chains BeforeTool handlers. It is the one hook point where a
// failing handler blocks rather than being skipped.
func beforeTool(ctx context.Context, task Task, rt *Runtime, call JsonObject) (JsonObject, *string, error) {
	current := call
	for _, binding := range rt.Hooks.Handlers() {
		hooks := toolHooksOf(binding.Handlers)
		if hooks == nil || hooks.BeforeTool == nil {
			continue
		}
		value, err := callBeforeTool(ctx, hooks, current, beforeToolApi(task, rt, binding, str(call, "id")))
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, err
			}
			block := "hook threw: " + errorString(err)
			return nil, &block, nil
		}
		if value == nil {
			continue
		}
		if value.Block != nil {
			return nil, value.Block, nil
		}
		if value.Call != nil {
			current = value.Call
		}
	}
	return current, nil, nil
}

func callBeforeTool(ctx context.Context, hooks *ToolHooks, call JsonObject, api *BeforeToolApi) (result *BeforeToolResult, err error) {
	defer recoverInto(&err)
	return hooks.BeforeTool(ctx, cloneObject(call), api)
}

func beforeToolApi(task Task, rt *Runtime, binding HookBinding, callId string) *BeforeToolApi {
	namespace := binding.Namespace
	touchNamespace := func(tx *Tx) error {
		_, err := tx.Plugins(namespace)
		return err
	}
	return &BeforeToolApi{
		HookApi: binding.Api,
		CallId:  callId,
		waiting: func(ctx context.Context) error {
			_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
				if err := touchNamespace(tx); err != nil {
					return nil, err
				}
				slot, err := rawToolSlot(tx, current.ConversationId, toolInputOf(current).Index)
				if err != nil {
					return nil, err
				}
				if slot["waitingOn"] == namespace.Id {
					return nil, nil
				}
				slot["waitingOn"] = namespace.Id
				return nil, emit(tx, ViewEvent{"type": "tool.waiting", "taskId": float64(current.Id), "callId": callId, "on": namespace.Id})
			})
			return err
		},
		memo: func(ctx context.Context, name string, candidate *JsonValue) (JsonValue, bool, error) {
			return slotMemo(ctx, rt, task, fmt.Sprintf("hook:%s:%s", namespace.Id, name), candidate, touchNamespace)
		},
		emit: func(ctx context.Context, name string, data JsonValue) error {
			_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
				return nil, tx.Emit(namespace, name, data)
			})
			return err
		},
	}
}

// toolStream is the kernel-owned stream: one bounded buffer, one throttle, one
// flush path. Flushes commit in order off the caller's goroutine, and their
// failures are never swallowed.
type toolStream struct {
	task      Task
	rt        *Runtime
	ctx       context.Context
	mu        sync.Mutex
	buffer    *Bounded
	streamed  bool
	lastFlush float64
	tail      chan struct{}
	flushErr  error
}

func (stream *toolStream) push(chunk []byte) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.streamed = true
	stream.buffer.Push(chunk)
	if stream.rt.Now()-stream.lastFlush >= 100 {
		stream.flushLocked()
	}
}

func (stream *toolStream) flush() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.flushLocked()
}

// flushLocked chains one commit of the current bounded text after the
// previous flush.
func (stream *toolStream) flushLocked() {
	stream.lastFlush = stream.rt.Now()
	text := stream.buffer.Text()
	index := toolInputOf(stream.task).Index
	previous, done := stream.tail, make(chan struct{})
	stream.tail = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		_, err := stream.rt.Commit(stream.ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			slot, err := rawToolSlot(tx, stream.task.ConversationId, index)
			if err == nil {
				slot["output"] = text
			}
			return nil, err
		})
		stream.mu.Lock()
		if err != nil && stream.flushErr == nil {
			stream.flushErr = err
		}
		stream.mu.Unlock()
	}()
}

// settle waits for every flush started so far. Upstream chains flushes on
// promise microtasks, so a flush queued by stream() has committed before the
// tool resumes from its next await; PiG flushes on goroutines, so the tool
// API waits here to keep the tool's writes in program order.
func (stream *toolStream) settle() {
	stream.mu.Lock()
	tail := stream.tail
	stream.mu.Unlock()
	if tail != nil {
		<-tail
	}
}

// drain waits for every flush and returns the first failure.
func (stream *toolStream) drain() error {
	stream.mu.Lock()
	tail := stream.tail
	stream.mu.Unlock()
	if tail != nil {
		<-tail
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.flushErr
}

func invokeTool(ctx context.Context, task Task, call JsonObject, declaration *ToolDeclaration, rt *Runtime) (Closure, error) {
	bounds := boundsOf(declaration.Output)
	stream := &toolStream{task: task, rt: rt, ctx: ctx, buffer: NewBounded(bounds.maxBytes, bounds.maxLines, bounds.retain)}
	api := toolApiFor(task, rt, stream)
	api.CallId = str(call, "id")
	result, err := executeTool(ctx, declaration, call["arguments"], api)
	if err != nil {
		if ctx.Err() != nil {
			_ = stream.drain()
			return nil, err
		}
		result = synthetic("tool threw: "+errorString(err), "threw")
	}
	var streamTruncated *truncation
	if stream.streamed {
		stream.flush()
		if err := stream.drain(); err != nil {
			return nil, err
		}
		result = withStreamedContent(result, stream.buffer, bounds)
		streamTruncated = &truncation{bytes: stream.buffer.DroppedBytes, lines: stream.buffer.DroppedLines}
	}
	final, err := afterTool(ctx, rt, call, result)
	if err != nil {
		return nil, err
	}
	return closeTool(task, final, declaration, rt.Now(), call, streamTruncated), nil
}

func withStreamedContent(result ToolResult, buffer *Bounded, bounds toolBounds) ToolResult {
	if result.Content != nil {
		return result
	}
	result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: buffer.Text()}}
	if buffer.Dropped() > 0 {
		result.Diagnostics = append(append([]ToolDiagnostic{}, result.Diagnostics...), ToolDiagnostic{
			Severity: "warn",
			Code:     "truncated",
			Message:  fmt.Sprintf("%d bytes / %d lines dropped (%s %d retained)", buffer.DroppedBytes, buffer.DroppedLines, bounds.retain, bounds.maxBytes),
		})
	}
	return result
}

func executeTool(ctx context.Context, declaration *ToolDeclaration, arguments JsonValue, api *ToolApi) (result ToolResult, err error) {
	defer recoverInto(&err)
	return declaration.Execute(ctx, cloneJSON(arguments), api)
}

func afterTool(ctx context.Context, rt *Runtime, call JsonObject, result ToolResult) (ToolResult, error) {
	final := result
	err := rt.Hooks.Each(ctx, func(handlers any, api HookApi) (any, error) {
		hooks := toolHooksOf(handlers)
		if hooks == nil || hooks.AfterTool == nil {
			return nil, nil
		}
		value, err := hooks.AfterTool(ctx, cloneObject(call), final, AfterToolInfo{HookApi: api, CallId: str(call, "id")})
		if value == nil {
			return nil, err
		}
		return value, err
	}, func(value any) bool {
		final = *value.(*ToolResult)
		return false
	})
	return final, err
}

// toolApiFor is the ToolApi a tool gets: the task API plus stream, progress,
// and memo over its own slot.
func toolApiFor(task Task, rt *Runtime, stream *toolStream) *ToolApi {
	api := taskApi(task, rt)
	index := toolInputOf(task).Index
	api.stream = stream.push
	api.progress = func(ctx context.Context, update func(slot *ToolProgress)) error {
		stream.settle()
		_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			slot, err := rawToolSlot(tx, task.ConversationId, index)
			if err != nil {
				return nil, err
			}
			return nil, applyToolProgress(slot, update)
		})
		return err
	}
	api.memo = func(ctx context.Context, name string, candidate *JsonValue) (JsonValue, bool, error) {
		return slotMemo(ctx, rt, task, "tool:"+name, candidate, nil)
	}
	return api
}

func applyToolProgress(slot JsonObject, update func(slot *ToolProgress)) error {
	free := ToolProgress{Details: cloneJSON(slot["details"])}
	if progress, ok := slot["progress"].(string); ok {
		free.Progress = &progress
	}
	if continuedBy, ok := asID(slot["continuedBy"]); ok {
		free.ContinuedBy = &continuedBy
	}
	update(&free)
	if free.Progress != nil {
		slot["progress"] = *free.Progress
	} else {
		delete(slot, "progress")
	}
	if free.Details != nil {
		stored, err := ToStored(free.Details)
		if err != nil {
			return err
		}
		slot["details"] = stored
	} else {
		delete(slot, "details")
	}
	if free.ContinuedBy != nil {
		slot["continuedBy"] = float64(*free.ContinuedBy)
	} else {
		delete(slot, "continuedBy")
	}
	return nil
}

type truncation struct {
	bytes int
	lines int
}

// closeTool bounds the output, stores the exact model message under the stored
// final call, and keeps the rest as strict-JSON data. A nil call selects the
// input call.
func closeTool(task Task, raw ToolResult, declaration *ToolDeclaration, now float64, call JsonObject, streamTruncated *truncation) Closure {
	input := toolInputOf(task)
	if call == nil {
		call = input.Call
	}
	var output *ToolOutput
	if declaration != nil {
		output = declaration.Output
	}
	return func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		if raw.Content == nil {
			raw.Content = []ai.ToolResultMessageContent{}
		}
		result, bounded := boundResult(raw, output)
		total := truncation{}
		for _, part := range []*truncation{bounded, streamTruncated} {
			if part != nil {
				total.bytes += part.bytes
				total.lines += part.lines
			}
		}
		data, err := toolResultData(result, total)
		if err != nil {
			return Completion{}, err
		}
		entry, err := tx.AppendEntry(current.ConversationId, NewEntry{Kind: "pi.tool_result", Model: []JsonObject{toolResultMessage(call, result, now)}, Data: data})
		if err != nil {
			return Completion{}, err
		}
		if err := finishToolSlot(tx, current, input.Index, call, entry, result); err != nil {
			return Completion{}, err
		}
		if total.bytes > 0 || total.lines > 0 {
			if err := emit(tx, ViewEvent{"type": "warning", "source": "tool", "message": fmt.Sprintf("tool output truncated: %d bytes / %d lines dropped", total.bytes, total.lines)}); err != nil {
				return Completion{}, err
			}
		}
		completion := JsonObject{"entry": float64(entry)}
		if result.Control != nil {
			completion["control"] = mustStored(result.Control)
		}
		return Completed(completion), nil
	}
}

func toolResultData(result ToolResult, total truncation) (JsonObject, error) {
	data := JsonObject{}
	for key, value := range map[string]any{"details": result.Details, "diagnostics": result.Diagnostics, "control": result.Control} {
		if isAbsent(value) {
			continue
		}
		stored, err := ToStored(value)
		if err != nil {
			return nil, err
		}
		data[key] = stored
	}
	if total.bytes > 0 || total.lines > 0 {
		data["truncated"] = JsonObject{"bytes": total.bytes, "lines": total.lines}
	}
	return data, nil
}

func isAbsent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []ToolDiagnostic:
		return typed == nil
	case *ToolControl:
		return typed == nil
	default:
		return false
	}
}

func finishToolSlot(tx *Tx, current Task, index int, call JsonObject, entry Id, result ToolResult) error {
	slot, err := rawToolSlot(tx, current.ConversationId, index)
	if err != nil {
		return err
	}
	slot["status"] = "done"
	if result.IsError {
		slot["status"] = "error"
	}
	slot["entry"] = float64(entry)
	delete(slot, "waitingOn")
	event := ViewEvent{"type": "tool.finished", "taskId": float64(current.Id), "callId": str(call, "id"), "entry": float64(entry), "isError": result.IsError}
	if result.Control != nil {
		event["control"] = mustStored(result.Control)
	}
	return emit(tx, event)
}

func toolResultMessage(call JsonObject, result ToolResult, now float64) JsonObject {
	content := result.Content
	if content == nil {
		content = []ai.ToolResultMessageContent{}
	}
	return JsonObject{
		"role":       "toolResult",
		"toolCallId": str(call, "id"),
		"toolName":   str(call, "name"),
		"content":    mustStored(content),
		"isError":    result.IsError,
		"timestamp":  now,
	}
}

// boundResult bounds the joined text content by lines, then bytes, keeping
// one text block (the first for head retention, the last for tail) and every
// non-text block.
func boundResult(result ToolResult, declared *ToolOutput) (ToolResult, *truncation) {
	bounds := boundsOf(declared)
	var joined strings.Builder
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			joined.WriteString(text.Text)
		}
	}
	text, dropped := boundText(joined.String(), bounds)
	if dropped.bytes == 0 && dropped.lines == 0 {
		return result, nil
	}
	textIndex := -1
	for index, block := range result.Content {
		if _, ok := block.(ai.TextContent); !ok || (bounds.retain == "head" && textIndex >= 0) {
			continue
		}
		textIndex = index
	}
	content := []ai.ToolResultMessageContent{}
	for index, block := range result.Content {
		textBlock, isText := block.(ai.TextContent)
		switch {
		case !isText:
			content = append(content, block)
		case index == textIndex:
			textBlock.Text = text
			content = append(content, textBlock)
		}
	}
	result.Content = content
	result.Diagnostics = append(append([]ToolDiagnostic{}, result.Diagnostics...), ToolDiagnostic{
		Severity: "warn",
		Code:     "truncated",
		Message:  fmt.Sprintf("output truncated: %d lines, %d bytes dropped", dropped.lines, dropped.bytes),
	})
	return result, &dropped
}

func boundText(text string, bounds toolBounds) (string, truncation) {
	dropped := truncation{}
	lines := strings.Split(text, "\n")
	if len(lines) > bounds.maxLines {
		dropped.lines = len(lines) - bounds.maxLines
		if bounds.retain == "head" {
			lines = lines[:bounds.maxLines]
		} else {
			lines = lines[len(lines)-bounds.maxLines:]
		}
		text = strings.Join(lines, "\n")
	}
	if len(text) > bounds.maxBytes {
		dropped.bytes = len(text) - bounds.maxBytes
		if bounds.retain == "head" {
			text = decodeUTF8Lossy([]byte(text[:bounds.maxBytes]))
		} else {
			text = decodeUTF8Lossy([]byte(text[len(text)-bounds.maxBytes:]))
		}
	}
	if !utf8.ValidString(text) {
		text = decodeUTF8Lossy([]byte(text))
	}
	return text, dropped
}

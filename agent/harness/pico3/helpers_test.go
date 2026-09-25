package pico3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

var testModel = JsonObject{"provider": "anthropic", "modelId": "fake-1"}

func failureOf(task *Task) JsonObject {
	if task == nil || task.Outcome == nil {
		return nil
	}
	return asObject(task.Outcome.Failure)
}

func resultOf(task *Task) JsonObject {
	if task == nil || task.Outcome == nil {
		return nil
	}
	return asObject(task.Outcome.Result)
}

// entryKinds renders entries as upstream's kinds() helper: kind without the
// pi. prefix, with * when the entry has a head.
func entryKinds(entries []Entry) string {
	parts := make([]string, len(entries))
	for index, entry := range entries {
		parts[index] = strings.Replace(entry.Kind, "pi.", "", 1)
		if entry.Head != nil {
			parts[index] += "*"
		}
	}
	return strings.Join(parts, " ")
}

func contentOf(entry *Entry) string {
	if entry == nil || len(entry.Model) == 0 {
		return "null"
	}
	content := entry.Model[0]["content"]
	if text, ok := content.(string); ok {
		return text
	}
	return string(mustJSON(content))
}

// testGate is a barrier the test controls: Wait blocks until Open.
type testGate struct {
	mu      sync.Mutex
	opened  bool
	waiters []chan struct{}
	arrived atomic.Int64
}

func (gate *testGate) Open() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.opened = true
	for _, waiter := range gate.waiters {
		close(waiter)
	}
	gate.waiters = nil
}

func (gate *testGate) Close() {
	gate.mu.Lock()
	gate.opened = false
	gate.mu.Unlock()
}

func (gate *testGate) Wait(ctx context.Context) error {
	gate.arrived.Add(1)
	gate.mu.Lock()
	if gate.opened {
		gate.mu.Unlock()
		return nil
	}
	waiter := make(chan struct{})
	gate.waiters = append(gate.waiters, waiter)
	gate.mu.Unlock()
	select {
	case <-waiter:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (gate *testGate) Arrivals(t *testing.T, count int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for gate.arrived.Load() < count {
		if time.Now().After(deadline) {
			t.Fatalf("gate: only %d/%d arrivals", gate.arrived.Load(), count)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type fakeResponse struct {
	text      *string
	toolCalls []fakeToolCall
	err       *string
	stop      string
}

type fakeToolCall struct {
	name      string
	arguments JsonObject
}

type fakeOptions struct {
	respond      func(messages []JsonObject, call int) fakeResponse
	gate         *testGate
	gateWhen     func(messages []JsonObject) bool
	tokenDelayMs int
}

// fakeModels is the scripted provider used by every harness test.
type fakeModels struct {
	options  fakeOptions
	model    *ai.Model
	mu       sync.Mutex
	calls    int
	requests [][]JsonObject
}

func newFake(options fakeOptions) *fakeModels {
	return &fakeModels{options: options, model: &ai.Model{
		ID:           "fake-1",
		DisplayName:  "Fake",
		Capabilities: ai.ModelCapabilities{ContextWindow: 200_000, MaxOutputTokens: 8192},
		ProviderMeta: ai.ProviderMetadata{API: "anthropic-messages", ProviderID: "anthropic"},
	}}
}

func (fake *fakeModels) Calls() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.calls
}

func (fake *fakeModels) Requests() [][]JsonObject {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return slices.Clone(fake.requests)
}

func (fake *fakeModels) Resolve(ModelRef) *ai.Model { return fake.model }

func fakeUsage(input, output int) ai.Usage {
	return ai.Usage{Input: input, Output: output, TotalTokens: input + output}
}

func (fake *fakeModels) Stream(ctx context.Context, _ *ai.Model, request RequestOptions) iter.Seq2[ai.AssistantMessageEvent, error] {
	return func(yield func(ai.AssistantMessageEvent, error) bool) {
		fake.mu.Lock()
		call := fake.calls
		fake.calls++
		fake.requests = append(fake.requests, request.Messages)
		fake.mu.Unlock()
		response := fake.options.respond(request.Messages, call)
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, API: "anthropic-messages", Provider: "anthropic", Model: "fake-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
		if !yield(ai.StartEvent{Partial: partial}, nil) {
			return
		}
		if fake.options.gate != nil && (fake.options.gateWhen == nil || fake.options.gateWhen(request.Messages)) {
			if err := fake.options.gate.Wait(ctx); err != nil {
				yield(nil, err)
				return
			}
		}
		if response.err != nil {
			failed := *partial
			failed.StopReason = ai.StopReasonError
			failed.ErrorMessage = *response.err
			yield(ai.ErrorEvent{Reason: ai.StopReasonError, Error: &failed}, nil)
			return
		}
		fake.streamBody(ctx, call, request, response, partial, yield)
	}
}

func (fake *fakeModels) streamBody(ctx context.Context, call int, request RequestOptions, response fakeResponse, partial *ai.AssistantMessage, yield func(ai.AssistantMessageEvent, error) bool) {
	index, tokens := 0, 0
	if response.text != nil {
		partial.Content = append(partial.Content, ai.TextContent{})
		if !yield(ai.TextStartEvent{ContentIndex: index, Partial: partial}, nil) {
			return
		}
		for word := range strings.SplitSeq(*response.text, " ") {
			if fake.options.tokenDelayMs > 0 {
				time.Sleep(time.Duration(fake.options.tokenDelayMs) * time.Millisecond)
			}
			if ctx.Err() != nil {
				yield(nil, context.Cause(ctx))
				return
			}
			delta := word + " "
			text := partial.Content[index].(ai.TextContent)
			text.Text += delta
			partial.Content[index] = text
			tokens++
			if !yield(ai.TextDeltaEvent{ContentIndex: index, Delta: delta, Partial: partial}, nil) {
				return
			}
		}
		if !yield(ai.TextEndEvent{ContentIndex: index, Content: partial.Content[index].(ai.TextContent).Text, Partial: partial}, nil) {
			return
		}
		index++
	}
	for _, toolCall := range response.toolCalls {
		callBlock := ai.ToolCall{ID: fmt.Sprintf("call_%d_%d", call, index), Name: toolCall.name, Arguments: ai.JsonObject(toolCall.arguments)}
		partial.Content = append(partial.Content, callBlock)
		if !yield(ai.ToolCallStartEvent{ContentIndex: index, Partial: partial}, nil) {
			return
		}
		if !yield(ai.ToolCallEndEvent{ContentIndex: index, ToolCall: callBlock, Partial: partial}, nil) {
			return
		}
		index++
	}
	stop := ai.StopReason(response.stop)
	if stop == "" {
		stop = ai.StopReasonStop
	}
	if len(response.toolCalls) > 0 {
		stop = ai.StopReasonToolUse
	}
	final := *partial
	final.Content = slices.Clone(partial.Content)
	final.Usage = fakeUsage(len(request.Messages)*50, tokens)
	final.StopReason = stop
	yield(ai.DoneEvent{Reason: stop, Message: &final}, nil)
}

func (fake *fakeModels) FetchDeferred(context.Context, *ai.Model, ai.DeferredHandle) (DeferredResult, error) {
	return DeferredResult{}, errors.New("no deferred")
}

func (fake *fakeModels) CancelDeferred(context.Context, *ai.Model, ai.DeferredHandle) error {
	return nil
}

func lastMessage(messages []JsonObject) JsonObject {
	for _, message := range slices.Backward(messages) {
		if str(message, "role") != "system" {
			return message
		}
	}
	return nil
}

func textResponse(text string) fakeResponse { return fakeResponse{text: &text} }

func errorResponse(message string) fakeResponse { return fakeResponse{err: &message} }

// echoScript calls tools for "tool:<names>", errors for "error:<text>", and
// otherwise answers echoing the user.
func echoScript(messages []JsonObject, _ int) fakeResponse {
	last := lastMessage(messages)
	if str(last, "role") == "toolResult" {
		return textResponse("after tools")
	}
	user := fmt.Sprint(last["content"])
	if names, ok := strings.CutPrefix(user, "tool:"); ok {
		var calls []fakeToolCall
		for name := range strings.SplitSeq(names, ",") {
			calls = append(calls, fakeToolCall{name: name, arguments: JsonObject{"v": name}})
		}
		return fakeResponse{toolCalls: calls}
	}
	if message, ok := strings.CutPrefix(user, "error:"); ok {
		return errorResponse(message)
	}
	return textResponse("answer to " + user)
}

var toolSchema = JsonObject{
	"type":                 "object",
	"properties":           JsonObject{"v": JsonObject{"type": "string"}},
	"required":             []any{"v"},
	"additionalProperties": true,
}

type toolOptions struct {
	replay string
	gate   *testGate
	result *ToolResult
	throws string
	output *ToolOutput
}

type countingTool struct {
	*ToolDeclaration
	calls atomic.Int64
}

func newTool(name string, options toolOptions) *countingTool {
	replay := options.replay
	if replay == "" {
		replay = "safe"
	}
	counted := &countingTool{}
	counted.ToolDeclaration = &ToolDeclaration{
		Name: name, Description: name, Parameters: toolSchema, Replay: replay, Output: options.output,
		Execute: func(ctx context.Context, args JsonValue, _ *ToolApi) (ToolResult, error) {
			counted.calls.Add(1)
			if options.gate != nil {
				if err := options.gate.Wait(ctx); err != nil {
					return ToolResult{}, err
				}
			}
			if options.throws != "" {
				return ToolResult{}, errors.New(options.throws)
			}
			result := ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("%s(%s)", name, str(asObject(args), "v"))}}}
			if options.result != nil {
				applyToolResultOverride(&result, *options.result)
			}
			return result, nil
		},
	}
	return counted
}

func applyToolResultOverride(result *ToolResult, override ToolResult) {
	if override.Content != nil {
		result.Content = override.Content
	}
	if override.IsError {
		result.IsError = true
	}
	if override.Details != nil {
		result.Details = override.Details
	}
	if override.Diagnostics != nil {
		result.Diagnostics = override.Diagnostics
	}
	if override.Control != nil {
		result.Control = override.Control
	}
}

// fakeProcessHost is the scripted process host.
type fakeProcessHost struct {
	mu         sync.Mutex
	procs      map[string]ProcessStatus
	startCalls int
}

func newFakeHost() *fakeProcessHost { return &fakeProcessHost{procs: map[string]ProcessStatus{}} }

func (host *fakeProcessHost) Start(_ context.Context, key string, _ ProcessSpec) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	host.startCalls++
	if _, ok := host.procs[key]; !ok {
		host.procs[key] = ProcessStatus{Status: "running"}
	}
	return nil
}

func (host *fakeProcessHost) Status(_ context.Context, key string) (ProcessStatus, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if status, ok := host.procs[key]; ok {
		return status, nil
	}
	return ProcessStatus{Status: "unknown"}, nil
}

func (host *fakeProcessHost) Kill(context.Context, string, string) error { return nil }

func (host *fakeProcessHost) forget(key string) {
	host.mu.Lock()
	defer host.mu.Unlock()
	delete(host.procs, key)
}

func (host *fakeProcessHost) exit(key string, code int) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if _, ok := host.procs[key]; ok {
		host.procs[key] = ProcessStatus{Status: "exited", ExitCode: code, Stdout: "out " + key}
	}
}

func (host *fakeProcessHost) Starts() int {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.startCalls
}

// hooksByKind registers one handler set per built-in kind.
type hooksByKind struct {
	generation *GenerationHooks
	tool       *ToolHooks
	postTools  *PostToolsHooks
	collapse   *CollapseHooks
}

type openOptions struct {
	backend     string
	models      Models
	dir         string
	root        *RootSpec
	hooks       *hooksByKind
	tools       []*ToolDeclaration
	taskKinds   []*Kind
	sections    []*SystemSection
	plugins     map[string]PluginHandler
	processHost ProcessHost
	onReport    func(error)
	setup       func(t *testing.T, h *Harness)
	now         func() float64
}

// testEnv is one open harness plus convenience readers.
type testEnv struct {
	t       *testing.T
	h       *Harness
	root    *ConversationHandle
	storage Storage
	dir     string
	options openOptions
	closed  bool
}

func toolNames(tools []*ToolDeclaration) []any {
	names := make([]any, len(tools))
	for index, tool := range tools {
		names[index] = tool.Name
	}
	return names
}

func openEnv(t *testing.T, options openOptions) *testEnv {
	t.Helper()
	env, err := tryOpenEnv(t, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.close)
	return env
}

func tryOpenEnv(t *testing.T, options openOptions) (*testEnv, error) {
	ctx := context.Background()
	dir := options.dir
	if dir == "" && options.backend == "jsonl" {
		dir = t.TempDir()
		options.dir = dir
	}
	var storage Storage
	if dir != "" {
		opened, err := OpenJsonlStorage(ctx, dir, JsonlOptions{Fsync: new(false)})
		if err != nil {
			return nil, err
		}
		storage = opened
	} else {
		storage = NewMemoryStorage()
	}
	if options.models == nil {
		options.models = newFake(fakeOptions{respond: echoScript})
	}
	root := options.root
	if root == nil {
		root = &RootSpec{Rewindable: JsonObject{"model": testModel, "selectedTools": toolNames(options.tools)}}
	}
	h, err := OpenHarness(ctx, storage, HarnessOptions{
		Models: options.models, Tools: options.tools, TaskKinds: options.taskKinds, Sections: options.sections,
		Plugins: options.plugins, ProcessHost: options.processHost, Root: root, OnReport: options.onReport, Now: options.now,
	})
	if err != nil {
		return nil, err
	}
	if options.setup != nil {
		options.setup(t, h)
	}
	if err := registerTestHooks(h, options.hooks); err != nil {
		return nil, err
	}
	if err := h.Resume(); err != nil {
		return nil, err
	}
	handle, err := h.Root(ctx)
	if err != nil {
		return nil, err
	}
	return &testEnv{t: t, h: h, root: handle, storage: storage, dir: dir, options: options}, nil
}

func registerTestHooks(h *Harness, hooks *hooksByKind) error {
	if hooks == nil {
		return nil
	}
	namespace, err := h.Namespace("test.hooks", NamespaceDefaults{}, nil)
	if err != nil {
		return err
	}
	pairs := []struct {
		kind     *Kind
		handlers any
		present  bool
	}{
		{Kinds.Generation, hooks.generation, hooks.generation != nil},
		{Kinds.Tool, hooks.tool, hooks.tool != nil},
		{Kinds.PostTools, hooks.postTools, hooks.postTools != nil},
		{Kinds.Collapse, hooks.collapse, hooks.collapse != nil},
	}
	for _, pair := range pairs {
		if !pair.present {
			continue
		}
		if _, err := h.Hooks(namespace, pair.kind, pair.handlers); err != nil {
			return err
		}
	}
	return nil
}

func (env *testEnv) close() {
	if env.closed {
		return
	}
	env.closed = true
	_ = env.h.Close(context.Background())
}

// entries returns a conversation's entries ascending (default root id 1).
func (env *testEnv) entries(conversationId ...Id) []Entry {
	env.t.Helper()
	id := Id(1)
	if len(conversationId) > 0 {
		id = conversationId[0]
	}
	entries, err := env.h.Entries(context.Background(), EntryScan{ConversationId: id, Limit: 1000})
	if err != nil {
		env.t.Fatal(err)
	}
	slices.Reverse(entries)
	return entries
}

func (env *testEnv) tasks(conversationId ...Id) []Task {
	env.t.Helper()
	scan := TaskScan{}
	if len(conversationId) > 0 {
		scan.ConversationId = &conversationId[0]
	}
	tasks, err := HostCommit(context.Background(), env.root, func(_ context.Context, tx *Tx) ([]Task, error) { return tx.Tasks(scan) })
	if err != nil {
		env.t.Fatal(err)
	}
	return tasks
}

func (env *testEnv) input(id Id) *Input {
	env.t.Helper()
	input, err := HostCommit(context.Background(), env.root, func(_ context.Context, tx *Tx) (*Input, error) { return tx.Input(id) })
	if err != nil {
		env.t.Fatal(err)
	}
	return input
}

// crash closes the harness (signals and joins invocations, writes nothing:
// the same durable state as a kill) and returns a reopener.
func (env *testEnv) crash() func() *testEnv {
	env.t.Helper()
	if env.dir == "" {
		env.t.Fatal("crash needs jsonl")
	}
	env.close()
	options := env.options
	return func() *testEnv {
		options.backend = "jsonl"
		return openEnv(env.t, options)
	}
}

func phaseOf(task *Task) string {
	if task == nil {
		return ""
	}
	return Phase(task.Checkpoint)
}

func (env *testEnv) liveTasks(conversationId ...Id) []Task {
	return slices.DeleteFunc(env.tasks(conversationId...), func(task Task) bool { return task.Status == TaskTerminal })
}

func (env *testEnv) untilPhase(kind, phase string) Task {
	env.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, task := range env.tasks() {
			if task.Kind == kind && task.Status != TaskTerminal && phaseOf(&task) == phase {
				return task
			}
		}
		if time.Now().After(deadline) {
			var live []string
			for _, task := range env.liveTasks() {
				live = append(live, fmt.Sprintf("%s@%s", task.Kind, phaseOf(&task)))
			}
			env.t.Fatalf("timeout waiting for %s@%s; live: %s", kind, phase, strings.Join(live, ","))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (env *testEnv) untilTerminal(id Id) Task {
	env.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, task := range env.tasks() {
			if task.Id == id && task.Status == TaskTerminal {
				return task
			}
		}
		if time.Now().After(deadline) {
			env.t.Fatalf("timeout waiting for task %d", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type watchCollector struct {
	view      JsonObject
	mu        sync.Mutex
	envelopes []*Envelope
	watch     *Watch
}

func (collector *watchCollector) Envelopes() []*Envelope {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return slices.Clone(collector.envelopes)
}

func collectWatch(t *testing.T, handle *ConversationHandle) *watchCollector {
	t.Helper()
	watch, err := handle.Watch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	collector := &watchCollector{view: watch.View, watch: watch}
	watch.Start(func(envelope *Envelope) {
		collector.mu.Lock()
		collector.envelopes = append(collector.envelopes, envelope)
		collector.mu.Unlock()
	})
	t.Cleanup(watch.Stop)
	return collector
}

// send admits text content and returns its input handle.
func (env *testEnv) send(handle *ConversationHandle, content string) *InputHandle {
	env.t.Helper()
	input, err := handle.Send(context.Background(), SendInput{Content: content})
	if err != nil {
		env.t.Fatal(err)
	}
	return input
}

// wait waits for an input to settle.
func (env *testEnv) wait(input *InputHandle) Input {
	env.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	settled, err := input.Wait(ctx)
	if err != nil {
		env.t.Fatal(err)
	}
	return settled
}

// idle waits for the whole harness to go idle.
func (env *testEnv) idle() {
	env.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := env.h.WaitForIdle(ctx); err != nil {
		env.t.Fatal(err)
	}
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

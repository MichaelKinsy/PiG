package agentharness

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

func userMessage(text string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: text}}, Timestamp: timestamp}}
}

func messageText(message agent.AgentMessage) string {
	if message.User == nil || len(message.User.Content) == 0 {
		return ""
	}
	text, _ := message.User.Content[0].(ai.TextContent)
	return text.Text
}

func newGate() *execution.Gate {
	gate, _ := execution.CreateGate()
	return gate
}

type errorLog struct {
	mu     sync.Mutex
	errors []error
}

func (log *errorLog) reporter() HookErrorReporter {
	return func(_ harness.Context, err error, _ HookName, _ string) error {
		log.mu.Lock()
		defer log.mu.Unlock()
		log.errors = append(log.errors, err)
		return nil
	}
}

func (log *errorLog) snapshot() []error {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.errors)
}

func ignoreErrors(harness.Context, error, HookName, string) error { return nil }

func mustOn(t *testing.T) func(unregister func(), err error) func() {
	return func(unregister func(), err error) func() {
		t.Helper()
		if err != nil {
			t.Fatalf("register hook: %v", err)
		}
		return unregister
	}
}

// Upstream: aggregates before_run messages in registration order with each
// handler seeing prior output.
func TestHookRegistryBeforeRunAggregatesInOrder(t *testing.T) {
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	secondCalls := 0
	mustOn(t)(hooks.OnBeforeRun(func(harness.Context, BeforeRunEvent) (*BeforeRunResult, error) {
		return &BeforeRunResult{Messages: []agent.AgentMessage{userMessage("first", 2)}}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeRun(func(_ harness.Context, event BeforeRunEvent) (*BeforeRunResult, error) {
		secondCalls++
		if len(event.Prompt) != 2 {
			t.Errorf("second handler saw %d prompt messages, want 2", len(event.Prompt))
		}
		return &BeforeRunResult{Messages: []agent.AgentMessage{userMessage("second", 3)}}, nil
	}, HookOptions{}))

	result, err := hooks.RunBeforeRun(context.Background(), newGate(), BeforeRunEvent{
		HookScope: HookScope{Lane: "main", RunID: "run"},
		Prompt:    []agent.AgentMessage{userMessage("prompt", 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.Messages) != 2 || messageText(result.Messages[0]) != "first" || messageText(result.Messages[1]) != "second" {
		t.Fatalf("result = %+v, want first then second", result)
	}
	if secondCalls != 1 {
		t.Fatalf("second handler calls = %d, want 1", secondCalls)
	}
	if errs := log.snapshot(); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}

	empty := NewHookRegistry(ignoreErrors)
	result, err = empty.RunBeforeRun(context.Background(), newGate(), BeforeRunEvent{})
	if err != nil || result != nil {
		t.Fatalf("no handlers: result=%v err=%v, want nil nil", result, err)
	}
}

// Upstream: checks the effect gate immediately before the complete before_run
// pipeline.
func TestHookRegistryGateCheckedBeforeWholePipeline(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	var mu sync.Mutex
	var calls []string
	record := func(call string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call)
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
	started, release := make(chan struct{}), make(chan struct{})
	mustOn(t)(hooks.OnBeforeRun(func(harness.Context, BeforeRunEvent) (*BeforeRunResult, error) {
		record("first:start")
		close(started)
		<-release
		record("first:end")
		return nil, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeRun(func(harness.Context, BeforeRunEvent) (*BeforeRunResult, error) {
		record("second")
		return nil, nil
	}, HookOptions{}))
	event := BeforeRunEvent{HookScope: HookScope{Lane: "main", RunID: "run"}}

	closed := errors.New("closed")
	closedGate, closedControl := execution.CreateGate()
	closedControl.Close(closed)
	if _, err := hooks.RunBeforeRun(context.Background(), closedGate, event); err != closed { //nolint:errorlint // upstream asserts the exact close error identity.
		t.Fatalf("closed gate error = %v, want %v", err, closed)
	}
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("calls after refused run = %v", got)
	}

	done := make(chan error, 1)
	go func() {
		_, err := hooks.RunBeforeRun(context.Background(), newGate(), event)
		done <- err
	}()
	<-started
	mustOn(t)(hooks.OnBeforeRun(func(harness.Context, BeforeRunEvent) (*BeforeRunResult, error) {
		record("late")
		return nil, nil
	}, HookOptions{}))
	hooks.Close(closed)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, want := snapshot(), []string{"first:start", "first:end", "second"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if _, err := hooks.OnBeforeRun(func(harness.Context, BeforeRunEvent) (*BeforeRunResult, error) { return nil, nil }, HookOptions{}); err != closed { //nolint:errorlint // exact close error identity.
		t.Fatalf("registration after close = %v, want %v", err, closed)
	}
	if _, err := hooks.RunBeforeRun(context.Background(), newGate(), event); err != closed { //nolint:errorlint // exact close error identity.
		t.Fatalf("run after close = %v, want %v", err, closed)
	}
}

// Upstream: rejects pre-aborted invocations and combines admitted hook
// cancellation with the effect gate.
func TestHookRegistryPreAbortedAndGateCancellation(t *testing.T) {
	event := BeforeDriveEvent{HookScope: HookScope{Lane: "main", RunID: "run"}, Operation: OperationRun}
	preAborted := NewHookRegistry(ignoreErrors)
	handlerCalls := 0
	mustOn(t)(preAborted.OnBeforeDrive(func(harness.Context, BeforeDriveEvent) error {
		handlerCalls++
		return nil
	}, HookOptions{}))
	gate := newGate()
	invocation, cancel := harness.WithCancel(context.Background())
	abortReason := errors.New("invocation aborted")
	cancel(abortReason)
	if err := preAborted.RunBeforeDrive(invocation, gate, event); err != abortReason { //nolint:errorlint // upstream asserts the abort reason identity.
		t.Fatalf("pre-aborted run = %v, want %v", err, abortReason)
	}
	if handlerCalls != 0 {
		t.Fatalf("handler ran %d times for a pre-aborted invocation", handlerCalls)
	}
	if err := gate.Admit(func() error { return nil }); err != nil {
		t.Fatalf("gate refused after pre-aborted invocation: %v", err)
	}

	admittedHooks := NewHookRegistry(ignoreErrors)
	admittedGate, control := execution.CreateGate()
	var admitted harness.Context
	mustOn(t)(admittedHooks.OnBeforeDrive(func(ctx harness.Context, _ BeforeDriveEvent) error {
		admitted = ctx
		return nil
	}, HookOptions{}))
	if err := admittedHooks.RunBeforeDrive(context.Background(), admittedGate, event); err != nil {
		t.Fatal(err)
	}
	if admitted.Err() != nil {
		t.Fatalf("admitted context already aborted: %v", admitted.Err())
	}
	gateReason := errors.New("gate closed")
	control.Close(gateReason)
	if admitted.Err() == nil {
		t.Fatal("admitted context was not aborted immediately by the gate")
	}
	if cause := context.Cause(admitted); cause != gateReason { //nolint:errorlint // exact abort reason identity.
		t.Fatalf("admitted cause = %v, want %v", cause, gateReason)
	}
}

// Upstream: passes tool handlers a child context of the active hook span.
func TestHookRegistryToolHandlersReceiveHookSpanContext(t *testing.T) {
	telemetry := &recordingTelemetry{}
	valueKey := harness.CreateContextKey[string]("hook.test.value")
	hooks := NewHookRegistry(ignoreErrors)
	var received string
	mustOn(t)(hooks.OnBeforeTool(func(ctx harness.Context, _ BeforeToolEvent) (*BeforeToolResult, error) {
		received, _ = harness.ContextValue(ctx, valueKey)
		return nil, harness.GetTelemetryContext(ctx).StartSpan(harness.SpanOptions{Name: "handler.child"}, func(harness.TelemetrySpan) error { return nil })
	}, HookOptions{}))

	err := telemetry.StartSpan(harness.SpanOptions{Name: "invocation"}, func(invocationSpan harness.TelemetrySpan) error {
		ctx := harness.WithContextValue(harness.WithTelemetryContext(context.Background(), invocationSpan), valueKey, "preserved")
		_, err := hooks.RunBeforeTool(ctx, newGate(), BeforeToolEvent{HookScope: HookScope{Lane: "main", RunID: "run"}, ToolCallID: "call", ToolName: "tool", Args: map[string]any{}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if received != "preserved" {
		t.Fatalf("context value = %q, want preserved", received)
	}
	invocation, hook, child := telemetry.find("invocation"), telemetry.find("pi.harness.hook"), telemetry.find("handler.child")
	if invocation == nil || hook == nil || child == nil {
		t.Fatalf("missing spans: invocation=%v hook=%v child=%v", invocation, hook, child)
	}
	if hook.parentID != invocation.id || child.parentID != hook.id {
		t.Fatalf("parents: hook->%d (want %d), child->%d (want %d)", hook.parentID, invocation.id, child.parentID, hook.id)
	}
	want := harness.SpanAttributes{"pi.lane.name": "main", "pi.operation.id": "run", "pi.hook.name": "before_tool", "pi.hook.outcome": "completed"}
	if !reflect.DeepEqual(hook.attributes, want) {
		t.Fatalf("hook attributes = %v, want %v", hook.attributes, want)
	}
}

// Tool hook spans record blocked/failed outcomes, an explicit error status
// without details, and a present (even empty) registration id only.
func TestHookRegistryToolSpanOutcomes(t *testing.T) {
	telemetry := &recordingTelemetry{}
	ctx := harness.WithTelemetryContext(context.Background(), telemetry)
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	empty, named := "", "blocker"
	failure := errors.New("tool hook failed")
	mustOn(t)(hooks.OnAfterTool(func(harness.Context, AfterToolEvent) (*AfterToolResult, error) {
		return nil, failure
	}, HookOptions{ID: &empty}))
	mustOn(t)(hooks.OnBeforeTool(func(harness.Context, BeforeToolEvent) (*BeforeToolResult, error) {
		return &BeforeToolResult{Block: &ToolBlock{Reason: "no"}}, nil
	}, HookOptions{ID: &named}))

	before, err := hooks.RunBeforeTool(ctx, newGate(), BeforeToolEvent{HookScope: HookScope{Lane: "main", RunID: "run"}, Args: map[string]any{}})
	if err != nil || before.Block == nil || before.Block.Reason != "no" || before.Args != nil {
		t.Fatalf("before_tool = %+v, %v", before, err)
	}
	after, err := hooks.RunAfterTool(ctx, newGate(), AfterToolEvent{HookScope: HookScope{Lane: "main", RunID: "run"}})
	if err != nil || after != nil {
		t.Fatalf("after_tool = %+v, %v; want nil nil", after, err)
	}
	if errs := log.snapshot(); len(errs) != 1 || errs[0] != failure { //nolint:errorlint // reporter receives the original error.
		t.Fatalf("reported errors = %v", errs)
	}
	spans := telemetry.all("pi.harness.hook")
	if len(spans) != 2 {
		t.Fatalf("hook spans = %d, want 2", len(spans))
	}
	if spans[0].attributes["pi.hook.outcome"] != "blocked" || spans[0].attributes["pi.hook.registration_id"] != "blocker" {
		t.Fatalf("before_tool span = %v", spans[0].attributes)
	}
	if spans[1].attributes["pi.hook.outcome"] != "failed" || spans[1].status == nil || spans[1].status.Status != harness.SpanStatusCodeError || spans[1].status.Error != nil {
		t.Fatalf("after_tool span = %v status %+v", spans[1].attributes, spans[1].status)
	}
	if id, ok := spans[1].attributes["pi.hook.registration_id"]; !ok || id != "" {
		t.Fatalf("empty registration id not recorded: %v", spans[1].attributes)
	}
}

// before_tool failures are reported and become a block with the error message;
// replaced args are returned only when replaced.
func TestHookRegistryBeforeToolFailureBlocks(t *testing.T) {
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	replaced := map[string]any{"path": "b"}
	mustOn(t)(hooks.OnBeforeTool(func(harness.Context, BeforeToolEvent) (*BeforeToolResult, error) {
		return &BeforeToolResult{Args: replaced}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeTool(func(_ harness.Context, event BeforeToolEvent) (*BeforeToolResult, error) {
		if !sameMap(event.Args, replaced) {
			t.Errorf("second handler args = %v, want replaced args", event.Args)
		}
		return nil, errors.New("denied")
	}, HookOptions{}))
	later := 0
	mustOn(t)(hooks.OnBeforeTool(func(harness.Context, BeforeToolEvent) (*BeforeToolResult, error) {
		later++
		return nil, nil
	}, HookOptions{}))
	result, err := hooks.RunBeforeTool(context.Background(), newGate(), BeforeToolEvent{Args: map[string]any{"path": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Block == nil || result.Block.Reason != "denied" || result.Block.Terminate || !sameMap(result.Args, replaced) || later != 0 {
		t.Fatalf("result = %+v later=%d", result, later)
	}
	if len(log.snapshot()) != 1 {
		t.Fatalf("errors = %v", log.snapshot())
	}
	// Returning the original args object is not a replacement.
	identity := NewHookRegistry(ignoreErrors)
	original := map[string]any{"path": "a"}
	mustOn(t)(identity.OnBeforeTool(func(_ harness.Context, event BeforeToolEvent) (*BeforeToolResult, error) {
		return &BeforeToolResult{Args: event.Args}, nil
	}, HookOptions{}))
	result, err = identity.RunBeforeTool(context.Background(), newGate(), BeforeToolEvent{Args: original})
	if err != nil || result == nil || result.Args != nil || result.Block != nil {
		t.Fatalf("identity result = %+v, %v; want empty result", result, err)
	}
}

// Upstream: treats registration ids as optional metadata and fails
// before_drive closed.
func TestHookRegistryBeforeDriveFailsClosed(t *testing.T) {
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	failure := errors.New("prerequisite failed")
	duplicate := "duplicate"
	later := 0
	mustOn(t)(hooks.OnBeforeDrive(func(harness.Context, BeforeDriveEvent) error { return failure }, HookOptions{ID: &duplicate}))
	mustOn(t)(hooks.OnBeforeDrive(func(harness.Context, BeforeDriveEvent) error { later++; return nil }, HookOptions{ID: &duplicate}))
	err := hooks.RunBeforeDrive(context.Background(), newGate(), BeforeDriveEvent{HookScope: HookScope{Lane: "main", RunID: "run"}, Operation: OperationRun})
	if err != failure { //nolint:errorlint // upstream asserts rejection identity.
		t.Fatalf("err = %v, want %v", err, failure)
	}
	if later != 0 {
		t.Fatalf("later handler ran")
	}
	if errs := log.snapshot(); len(errs) != 1 || errs[0] != failure { //nolint:errorlint // reporter receives the original error.
		t.Fatalf("errors = %v", errs)
	}
}

// Upstream: chains transform_context output and isolates handler failures.
func TestHookRegistryTransformContextChains(t *testing.T) {
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	messages := []agent.AgentMessage{userMessage("transformed", 2)}
	first := "first"
	final := "final"
	check := func(event TransformContextEvent) {
		if len(event.Messages) != 1 || &event.Messages[0] != &messages[0] || event.SystemPrompt != "first" {
			t.Errorf("event = %+v, want transformed messages and first prompt", event)
		}
	}
	mustOn(t)(hooks.OnTransformContext(func(harness.Context, TransformContextEvent) (*TransformContextResult, error) {
		return &TransformContextResult{Messages: messages, SystemPrompt: &first}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnTransformContext(func(_ harness.Context, event TransformContextEvent) (*TransformContextResult, error) {
		check(event)
		return nil, errors.New("ignored transform failure")
	}, HookOptions{}))
	mustOn(t)(hooks.OnTransformContext(func(_ harness.Context, event TransformContextEvent) (*TransformContextResult, error) {
		check(event)
		return &TransformContextResult{SystemPrompt: &final}, nil
	}, HookOptions{}))
	result, err := hooks.RunTransformContext(context.Background(), newGate(), TransformContextEvent{
		HookScope: HookScope{Lane: "main", RunID: "run"}, Messages: []agent.AgentMessage{userMessage("original", 1)}, SystemPrompt: "base",
	})
	if err != nil {
		t.Fatal(err)
	}
	if &result.Messages[0] != &messages[0] || *result.SystemPrompt != "final" {
		t.Fatalf("result = %+v", result)
	}
	if errs := log.snapshot(); len(errs) != 1 || errs[0].Error() != "ignored transform failure" {
		t.Fatalf("errors = %v", errs)
	}
}

// Upstream: preserves clear-all before_request patches across later handlers.
func TestHookRegistryBeforeRequestClearAllPatch(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	mustOn(t)(hooks.OnBeforeRequest(func(harness.Context, BeforeRequestEvent) (*BeforeRequestResult, error) {
		return &BeforeRequestResult{StreamOptions: &harness.AgentHarnessStreamOptionsPatch{
			Headers: harness.MapPatch[string]{Present: true}, Metadata: harness.MapPatch[any]{Present: true},
		}}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeRequest(func(_ harness.Context, event BeforeRequestEvent) (*BeforeRequestResult, error) {
		if event.StreamOptions.Headers != nil || event.StreamOptions.Metadata != nil {
			t.Errorf("second handler saw headers=%v metadata=%v, want cleared", event.StreamOptions.Headers, event.StreamOptions.Metadata)
		}
		return &BeforeRequestResult{StreamOptions: &harness.AgentHarnessStreamOptionsPatch{
			Headers:  harness.MapPatch[string]{Present: true, Entries: map[string]*string{"c": new("3")}},
			Metadata: harness.MapPatch[any]{Present: true, Entries: map[string]*any{"y": new(any(2))}},
		}}, nil
	}, HookOptions{}))
	result, err := hooks.RunBeforeRequest(context.Background(), newGate(), BeforeRequestEvent{
		HookScope: HookScope{Lane: "main", RunID: "run"}, Step: StepAssistant, Attempt: 1,
		StreamOptions: harness.AgentHarnessStreamOptions{Headers: map[string]string{"a": "1", "b": "2"}, Metadata: map[string]any{"x": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	patch := result.StreamOptions
	if !patch.Headers.Present || len(patch.Headers.Entries) != 3 || patch.Headers.Entries["a"] != nil || patch.Headers.Entries["b"] != nil || *patch.Headers.Entries["c"] != "3" {
		t.Fatalf("headers patch = %+v", patch.Headers)
	}
	if !patch.Metadata.Present || len(patch.Metadata.Entries) != 2 || patch.Metadata.Entries["x"] != nil || *patch.Metadata.Entries["y"] != 2 {
		t.Fatalf("metadata patch = %+v", patch.Metadata)
	}
	if patch.Transport.Present || patch.TimeoutMs.Present || patch.Deferred.Present || patch.CacheRetention.Present {
		t.Fatalf("unexpected scalar patch = %+v", patch)
	}
	unchanged, err := NewHookRegistry(ignoreErrors).RunBeforeRequest(context.Background(), newGate(), BeforeRequestEvent{})
	if err != nil || unchanged != nil {
		t.Fatalf("no handlers: %+v, %v", unchanged, err)
	}
}

func TestApplyAndCreateStreamOptionsPatch(t *testing.T) {
	base := harness.AgentHarnessStreamOptions{Transport: ai.TransportSSE, TimeoutMs: new(10), Headers: map[string]string{"a": "1"}}
	patched := ApplyStreamOptionsPatch(base, harness.AgentHarnessStreamOptionsPatch{
		Transport:  harness.DeleteField[ai.Transport](),
		TimeoutMs:  harness.SetField(20),
		MaxRetries: harness.SetField(3),
		Headers:    harness.MapPatch[string]{Present: true, Entries: map[string]*string{"b": new("2")}},
	})
	if patched.Transport != "" || *patched.TimeoutMs != 20 || *patched.MaxRetries != 3 || !reflect.DeepEqual(patched.Headers, map[string]string{"a": "1", "b": "2"}) {
		t.Fatalf("patched = %+v", patched)
	}
	if !reflect.DeepEqual(base.Headers, map[string]string{"a": "1"}) || *base.TimeoutMs != 10 {
		t.Fatalf("base mutated: %+v", base)
	}
	patch := createStreamOptionsPatch(base, patched)
	if !patch.Transport.Present || patch.Transport.Value != nil || *patch.TimeoutMs.Value != 20 || *patch.MaxRetries.Value != 3 || patch.MaxRetryDelayMs.Present {
		t.Fatalf("scalar patch = %+v", patch)
	}
	if len(patch.Headers.Entries) != 1 || *patch.Headers.Entries["b"] != "2" || patch.Metadata.Present {
		t.Fatalf("map patch = %+v / %+v", patch.Headers, patch.Metadata)
	}
	// A new empty map where none existed is the upstream `{}` patch.
	emptyPatch := createStreamOptionsPatch(harness.AgentHarnessStreamOptions{}, harness.AgentHarnessStreamOptions{Headers: map[string]string{}})
	if !emptyPatch.Headers.Present || emptyPatch.Headers.Entries == nil || len(emptyPatch.Headers.Entries) != 0 {
		t.Fatalf("empty map patch = %+v", emptyPatch.Headers)
	}
	if round := ApplyStreamOptionsPatch(base, patch); !reflect.DeepEqual(round, patched) {
		t.Fatalf("round trip = %+v, want %+v", round, patched)
	}
}

// Upstream: preserves earlier after_tool fields when a later patch returns
// undefined.
func TestHookRegistryAfterToolKeepsEarlierFields(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	patched := []ai.ToolResultMessageContent{ai.TextContent{Text: "patched"}}
	mustOn(t)(hooks.OnAfterTool(func(harness.Context, AfterToolEvent) (*AfterToolResult, error) {
		return &AfterToolResult{Content: patched}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnAfterTool(func(_ harness.Context, event AfterToolEvent) (*AfterToolResult, error) {
		if !reflect.DeepEqual(event.Content, patched) || !event.IsError {
			t.Errorf("second handler event = %+v", event)
		}
		return &AfterToolResult{IsError: new(false)}, nil
	}, HookOptions{}))
	result, err := hooks.RunAfterTool(context.Background(), newGate(), AfterToolEvent{
		HookScope: HookScope{Lane: "main", RunID: "run"}, ToolCallID: "call", ToolName: "tool", Args: map[string]any{},
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "raw"}}, IsError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Content, patched) || result.IsError == nil || *result.IsError || result.Details != nil || result.Usage != nil || result.Terminate != nil {
		t.Fatalf("result = %+v", result)
	}
}

// Upstream: accepts explicit false structural declines and rejects true
// conflicts.
func TestHookRegistryStructuralDeclineConflicts(t *testing.T) {
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	ignored := &compaction.BranchSummaryResult{Summary: "ignored"}
	selected := &compaction.BranchSummaryResult{Summary: "selected"}
	mustOn(t)(hooks.OnBeforeNavigation(func(harness.Context, BeforeNavigationEvent) (*BeforeNavigationResult, error) {
		return &BeforeNavigationResult{Decline: true, Summary: ignored}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeNavigation(func(harness.Context, BeforeNavigationEvent) (*BeforeNavigationResult, error) {
		return &BeforeNavigationResult{Decline: false, Summary: selected}, nil
	}, HookOptions{}))
	result, err := hooks.RunBeforeNavigation(context.Background(), newGate(), BeforeNavigationEvent{HookScope: HookScope{Lane: "main", RunID: "run"}, TargetID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Decline || result.Summary != selected {
		t.Fatalf("result = %+v", result)
	}
	if errs := log.snapshot(); len(errs) != 1 || errs[0].Error() != "before_navigation hook cannot return both decline and summary" {
		t.Fatalf("errors = %v", errs)
	}

	compactions := NewHookRegistry(ignoreErrors)
	mustOn(t)(compactions.OnBeforeCompaction(func(harness.Context, BeforeCompactionEvent) (*BeforeCompactionResult, error) {
		return &BeforeCompactionResult{}, nil
	}, HookOptions{}))
	mustOn(t)(compactions.OnBeforeCompaction(func(harness.Context, BeforeCompactionEvent) (*BeforeCompactionResult, error) {
		return &BeforeCompactionResult{Decline: true}, nil
	}, HookOptions{}))
	declined, err := compactions.RunBeforeCompaction(context.Background(), newGate(), BeforeCompactionEvent{})
	if err != nil || declined == nil || !declined.Decline {
		t.Fatalf("compaction result = %+v, %v", declined, err)
	}
}

// Upstream: admits the complete before_drive pipeline as one gated effect.
func TestHookRegistryBeforeDriveAdmittedAsOneEffect(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	var mu sync.Mutex
	var calls []string
	record := func(call string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call)
	}
	started, release := make(chan struct{}), make(chan struct{})
	mustOn(t)(hooks.OnBeforeDrive(func(harness.Context, BeforeDriveEvent) error {
		record("first:start")
		close(started)
		<-release
		record("first:end")
		return nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeDrive(func(harness.Context, BeforeDriveEvent) error {
		record("second")
		return nil
	}, HookOptions{}))
	event := BeforeDriveEvent{HookScope: HookScope{Lane: "main", RunID: "run"}, Operation: OperationRun}
	cancellation := func() error { return nil }

	abortFirst, abortControl := execution.CreateGate()
	abortControl.BeginAbort(cancellation)
	var abortRequested *execution.AbortRequested
	if err := hooks.RunBeforeDrive(context.Background(), abortFirst, event); !errors.As(err, &abortRequested) {
		t.Fatalf("abort-first err = %v, want AbortRequested", err)
	}
	mu.Lock()
	if len(calls) != 0 {
		t.Fatalf("calls after refusal = %v", calls)
	}
	mu.Unlock()

	startFirst, startControl := execution.CreateGate()
	done := make(chan error, 1)
	go func() { done <- hooks.RunBeforeDrive(context.Background(), startFirst, event) }()
	<-started
	startControl.BeginAbort(cancellation)
	startControl.SignalAbort()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"first:start", "first:end", "second"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// A reporter failure propagates out of the runner, like an awaited upstream
// reporter rejection.
func TestHookRegistryReporterFailurePropagates(t *testing.T) {
	reportFailure := errors.New("report failed")
	hooks := NewHookRegistry(func(harness.Context, error, HookName, string) error { return reportFailure })
	mustOn(t)(hooks.OnBeforePayload(func(harness.Context, BeforePayloadEvent) (*BeforePayloadResult, error) {
		return nil, errors.New("payload failed")
	}, HookOptions{}))
	if _, err := hooks.RunBeforePayload(context.Background(), newGate(), BeforePayloadEvent{Payload: 1}); err != reportFailure { //nolint:errorlint // exact reporter error identity.
		t.Fatalf("err = %v, want %v", err, reportFailure)
	}
}

func TestHookRegistryPayloadResponseAndRunEnd(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	mustOn(t)(hooks.OnBeforePayload(func(_ harness.Context, event BeforePayloadEvent) (*BeforePayloadResult, error) {
		return &BeforePayloadResult{Payload: event.Payload.(int) + 1}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforePayload(func(_ harness.Context, event BeforePayloadEvent) (*BeforePayloadResult, error) {
		return &BeforePayloadResult{Payload: event.Payload.(int) * 10}, nil
	}, HookOptions{}))
	payload, err := hooks.RunBeforePayload(context.Background(), newGate(), BeforePayloadEvent{Payload: 1})
	if err != nil || payload.Payload != 20 {
		t.Fatalf("payload = %+v, %v", payload, err)
	}
	mustOn(t)(hooks.OnAfterResponse(func(_ harness.Context, event AfterResponseEvent) (*AfterResponseResult, error) {
		message := event.Message
		message.StopReason = "patched"
		return &AfterResponseResult{Message: &message}, nil
	}, HookOptions{}))
	response, err := hooks.RunAfterResponse(context.Background(), newGate(), AfterResponseEvent{})
	if err != nil || response.Message == nil || response.Message.StopReason != "patched" {
		t.Fatalf("response = %+v, %v", response, err)
	}
	unregisterFirst := mustOn(t)(hooks.OnBeforeRunEnd(func(harness.Context, BeforeRunEndEvent) (*BeforeRunEndResult, error) {
		return &BeforeRunEndResult{FollowUp: new("first")}, nil
	}, HookOptions{}))
	mustOn(t)(hooks.OnBeforeRunEnd(func(harness.Context, BeforeRunEndEvent) (*BeforeRunEndResult, error) {
		return &BeforeRunEndResult{FollowUp: new("second")}, nil
	}, HookOptions{}))
	runEnd, err := hooks.RunBeforeRunEnd(context.Background(), newGate(), BeforeRunEndEvent{})
	if err != nil || runEnd == nil || *runEnd.FollowUp != "second" {
		t.Fatalf("run end = %+v, %v", runEnd, err)
	}
	if !hooks.Has(HookBeforeRunEnd) {
		t.Fatal("Has(before_run_end) = false")
	}
	unregisterFirst()
	unregisterFirst()
	if !hooks.Has(HookBeforeRunEnd) || hooks.Has(HookBeforeNavigation) {
		t.Fatal("unregister removed the wrong registration")
	}
}

// Review P2: an after_tool handler can replace details with JSON null; the
// next handler sees present-null details and the aggregate carries them.
func TestHookRegistryAfterToolCanReplaceDetailsWithNull(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	mustOn(t)(hooks.OnAfterTool(func(harness.Context, AfterToolEvent) (*AfterToolResult, error) {
		return &AfterToolResult{SetDetails: true}, nil
	}, HookOptions{}))
	var seen AfterToolEvent
	mustOn(t)(hooks.OnAfterTool(func(_ harness.Context, event AfterToolEvent) (*AfterToolResult, error) {
		seen = event
		return nil, nil
	}, HookOptions{}))
	result, err := hooks.RunAfterTool(context.Background(), newGate(), AfterToolEvent{
		HookScope: HookScope{Lane: "main", RunID: "run"}, Details: map[string]any{"previous": true}, HasDetails: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seen.HasDetails || seen.Details != nil {
		t.Fatalf("next handler details = %v (present %v), want present null", seen.Details, seen.HasDetails)
	}
	if result == nil || !result.SetDetails || result.Details != nil {
		t.Fatalf("aggregate = %+v, want present null details", result)
	}
}

// A panicking hook handler is reported and handled like a thrown upstream
// error: before_tool blocks with its message.
func TestHookRegistryIsolatesHandlerPanics(t *testing.T) {
	log := &errorLog{}
	hooks := NewHookRegistry(log.reporter())
	mustOn(t)(hooks.OnBeforeTool(func(harness.Context, BeforeToolEvent) (*BeforeToolResult, error) { panic("hook exploded") }, HookOptions{}))
	result, err := hooks.RunBeforeTool(context.Background(), newGate(), BeforeToolEvent{})
	if err != nil || result.Block == nil || result.Block.Reason != "hook exploded" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if errs := log.snapshot(); len(errs) != 1 || errs[0].Error() != "hook exploded" {
		t.Fatalf("errors = %v", errs)
	}
}

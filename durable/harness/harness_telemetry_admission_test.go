// Ports packages/durable/test/harness-telemetry.test.ts (Pi 2ec55d80): admission, trace inheritance, links, and resume.

package harness

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// telSpy counts what vi.spyOn(telemetry.propagation, ...) observes: calls reaching the recorder's propagation after enabled.
type telSpy struct {
	mu          sync.Mutex
	enabled     bool
	root        telemetry.TelemetryContext
	rootInjects int
	nilExtracts int
}

func (spy *telSpy) enable() {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	spy.enabled = true
}

func (spy *telSpy) inject(target telemetry.TelemetryContext) {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.enabled && target == spy.root {
		spy.rootInjects++
	}
}

func (spy *telSpy) extract(carrier telemetry.TelemetryCarrier) {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.enabled && carrier == nil {
		spy.nilExtracts++
	}
}

func (spy *telSpy) counts() (rootInjects, nilExtracts int) {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	return spy.rootInjects, spy.nilExtracts
}

// telCounting wraps the recorder (or one of its spans or extracted parents) so every Inject and Extract of the whole
// span tree reaches telSpy, as spying on the recorder's own methods does upstream, where every context shares them.
type telCounting struct {
	inner telemetry.TelemetryContext
	spy   *telSpy
}

type telCountingSpan struct {
	*telCounting
	span telemetry.TelemetrySpan
}

type telCountingPropagation struct {
	inner telemetry.TelemetryPropagation
	spy   *telSpy
}

func telUnwrap(context telemetry.TelemetryContext) telemetry.TelemetryContext {
	switch wrapped := context.(type) {
	case *telCountingSpan:
		return wrapped.span
	case *telCounting:
		return wrapped.inner
	}
	return context
}

func telUnwrapLinks(links []telemetry.SpanLink) []telemetry.SpanLink {
	unwrapped := make([]telemetry.SpanLink, len(links))
	for i, link := range links {
		unwrapped[i] = telemetry.SpanLink{Context: telUnwrap(link.Context), Attributes: link.Attributes}
	}
	return unwrapped
}

func (wrapped *telCounting) Metrics() telemetry.TelemetryMetrics { return wrapped.inner.Metrics() }

func (wrapped *telCounting) Propagation() telemetry.TelemetryPropagation {
	return &telCountingPropagation{inner: wrapped.inner.Propagation(), spy: wrapped.spy}
}

func (wrapped *telCounting) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	options.Links = telUnwrapLinks(options.Links)
	return wrapped.inner.StartSpan(options, func(span telemetry.TelemetrySpan) error {
		return callback(&telCountingSpan{telCounting: &telCounting{inner: span, spy: wrapped.spy}, span: span})
	})
}

func (wrapped *telCountingSpan) AddLink(link telemetry.SpanLink) {
	wrapped.span.AddLink(telemetry.SpanLink{Context: telUnwrap(link.Context), Attributes: link.Attributes})
}
func (wrapped *telCountingSpan) AddEvent(name string, attributes telemetry.SpanAttributes) {
	wrapped.span.AddEvent(name, attributes)
}
func (wrapped *telCountingSpan) SetAttributes(attributes telemetry.SpanAttributes) {
	wrapped.span.SetAttributes(attributes)
}
func (wrapped *telCountingSpan) SetStatus(status telemetry.SpanStatus) {
	wrapped.span.SetStatus(status)
}

func (propagation *telCountingPropagation) Inject(target telemetry.TelemetryContext) telemetry.TelemetryCarrier {
	propagation.spy.inject(target)
	return propagation.inner.Inject(telUnwrap(target))
}

func (propagation *telCountingPropagation) Extract(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	propagation.spy.extract(carrier)
	return &telCounting{inner: propagation.inner.Extract(carrier), spy: propagation.spy}
}

// telSubmissionReads counts Storage.Submission reads (vi.spyOn(storage, "submission")) once enabled.
type telSubmissionReads struct {
	*controlledStorage
	enabled atomic.Bool
	reads   atomic.Int64
}

func (store *telSubmissionReads) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if store.enabled.Load() {
		store.reads.Add(1)
	}
	return store.controlledStorage.Submission(ctx, id)
}

// telHeldAnswer is a faux response that signals reached, then answers text once gate is resolved.
func telHeldAnswer(gate, reached *deferredGate, text string) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		reached.resolve()
		if err := gate.wait(options.Signal); err != nil {
			return ai.FauxResponse{}.AssistantMessage(), err
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}.AssistantMessage(), nil
	})
}

// telLinks is the recorded form of links to carriers that carry no attributes.
func telLinks(carriers ...telemetry.TelemetryCarrier) []telemetry.RecordedTelemetryLink {
	links := make([]telemetry.RecordedTelemetryLink, len(carriers))
	for i, carrier := range carriers {
		links[i] = telemetry.RecordedTelemetryLink{Context: carrier, Attributes: telemetry.SpanAttributes{}}
	}
	return links
}

func telSettled(recorder *telemetry.InMemoryTelemetryContext) func() bool {
	return func() bool { return allSettled(recorder) }
}

// telRunning commits a running checkpoint.
func telRunning(phase string, count int) func(context.Context, telemetryRunning, telemetryRuntime) error {
	return func(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, telemetryRunning) (*durable.NextTaskState[telemetryTaskState, durable.JsonValue], error) {
			return &durable.NextTaskState[telemetryTaskState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &telemetryTaskState{Phase: phase, Count: count}}, nil
		})
	}
}

// telCreateTasks creates one conversation-owned task per option set in one commit.
func telCreateTasks(ctx context.Context, t *testing.T, root Conversation, task durable.AnyTask, options ...durable.TaskOptions) []durable.TaskId {
	t.Helper()
	return commitValueCtx(ctx, t, root, func(tx durable.Tx) ([]durable.TaskId, error) {
		ids := make([]durable.TaskId, 0, len(options))
		for _, option := range options {
			option.Ownership = conversationOwned
			id, err := tx.CreateTaskErased(task, nil, option)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	})
}

func commitValueCtx[T any](ctx context.Context, t *testing.T, conversation Conversation, change func(tx durable.Tx) (T, error)) T {
	t.Helper()
	value, err := durable.Commit(ctx, conversation, change)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func telScanTasks(t *testing.T, harness Harness, kind string) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	page := must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 10, nil)
	}))
	return page.Items
}

func telSpanByName(t *testing.T, spans []telemetry.RecordedTelemetrySpan, name string) telemetry.RecordedTelemetrySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("no span %q", name)
	return telemetry.RecordedTelemetrySpan{}
}

func TestDurableTelemetryReturnsAdmissionResultsWithoutRereadingRecordsOrProbingRootLinkTargets(t *testing.T) {
	// harness-telemetry.test.ts: "returns admission results without re-reading records or probing root link targets"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := &telSubmissionReads{controlledStorage: newControlledStorage()}
	gate, reached := deferred(), deferred()
	setup.Faux.SetResponses([]ai.FauxResponseStep{telHeldAnswer(gate, reached, "first"), fauxAnswer("second")})
	spy := &telSpy{}
	root := &telCounting{inner: recorder, spy: spy}
	spy.root = root
	harness, conversation := openChat(t, store, setup, openChatOptions{telemetry: root})
	store.enabled.Store(true)
	spy.enable()
	first := must(conversation.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")}))
	if err := reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	second := must(conversation.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("second"), RequestId: new("queued")}))
	duplicate := must(conversation.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("ignored"), RequestId: new("queued")}))
	if duplicate.Id() != second.Id() {
		t.Fatalf("duplicate id %d, want %d", duplicate.Id(), second.Id())
	}
	if reads := store.reads.Load(); reads != 0 {
		t.Fatalf("%d Storage.Submission reads, want none", reads)
	}
	rootInjects, nilExtracts := spy.counts()
	if rootInjects != 0 {
		t.Fatalf("%d Inject calls targeting the root, want none", rootInjects)
	}
	if nilExtracts != 0 {
		t.Fatalf("%d Extract calls without a carrier, want none", nilExtracts)
	}
	var results []any
	for _, span := range telemetrySpans(recorder, "pi.durable.submit") {
		results = append(results, span.Attributes.Value("result"))
	}
	if !reflect.DeepEqual(results, []any{"placed", "queued", "queued"}) {
		t.Fatalf("submit results %v", results)
	}
	store.enabled.Store(false)
	gate.resolve()
	must(first.Wait(testContext))
	must(second.Wait(testContext))
	mustClose(t, harness)
}

func TestDurableTelemetryInheritsOmittedOrUndefinedTaskTracesAndStoresNoCarrierForAnExplicitRoot(t *testing.T) {
	// harness-telemetry.test.ts: "inherits omitted or undefined task traces and stores no carrier for an explicit root"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	task := telemetryTask("test.trace", "finish", map[string]telemetryPhase{"finish": finishPhase})
	addTask(t, setup.Registry, task.Erased())
	harness, root := openTracked(t, newControlledStorage(), setup, recorder)
	ids := must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "creator"}, func(span telemetry.TelemetrySpan) ([]durable.TaskId, error) {
		return telCreateTasks(chordctx.WithTelemetry(testContext, span), t, root, task.Erased(),
			durable.TaskOptions{}, durable.TaskOptions{Trace: nil}, durable.TaskOptions{Trace: &durable.TaskTrace{Root: true}}), nil
	}))
	for _, id := range ids {
		must(harness.WaitForTask(testContext, id))
	}
	waitFor(t, telSettled(recorder))
	creator := recorder.GetSpans()[0]
	var traces []telemetry.TelemetryCarrier
	for _, id := range ids {
		record := must(harness.GetTask(testContext, id))
		traces = append(traces, record.Trace)
	}
	if want := []telemetry.TelemetryCarrier{creator.Context, creator.Context, nil}; !reflect.DeepEqual(traces, want) {
		t.Fatalf("task traces %v, want %v", traces, want)
	}
	// Upstream lists the phases in start order, which is task-ID order; a Go invocation runs on its own goroutine (scheduler.go start,
	// D181), so each phase is matched to its task by ID instead.
	phases := telemetrySpans(recorder, "pi.durable.phase")
	if len(phases) != len(ids) {
		t.Fatalf("%d phase spans, want %d", len(phases), len(ids))
	}
	parents := make([]telemetry.TelemetryCarrier, len(ids))
	for _, phase := range phases {
		for i, id := range ids {
			if phase.Attributes.Value("task.id") == numberOf(id) {
				parents[i] = phase.ParentContext
			}
		}
	}
	if want := []telemetry.TelemetryCarrier{creator.Context, creator.Context, nil}; !reflect.DeepEqual(parents, want) {
		t.Fatalf("phase parents %v, want %v", parents, want)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryRecordsCheckpointsOnlyWhenTheRunningPhaseChanges(t *testing.T) {
	// harness-telemetry.test.ts: "records checkpoints only when the running phase changes"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	task := telemetryTask("test.checkpoint", "call", map[string]telemetryPhase{
		"call": func(ctx context.Context, running telemetryRunning, runtime telemetryRuntime) error {
			for _, step := range []struct {
				phase string
				count int
			}{{"call", 1}, {"call", 2}, {"finish", 2}} {
				if err := telRunning(step.phase, step.count)(ctx, running, runtime); err != nil {
					return err
				}
			}
			return nil
		},
		"finish": finishPhase,
	})
	addTask(t, setup.Registry, task.Erased())
	harness, root := openTracked(t, newControlledStorage(), setup, recorder)
	id := telCreateTasks(testContext, t, root, task.Erased(), durable.TaskOptions{})[0]
	must(harness.WaitForTask(testContext, id))
	waitFor(t, telSettled(recorder))
	var checkpoints []telemetry.RecordedTelemetryEvent
	for _, event := range spanEvents(recorder.GetSpans()) {
		if event.Name == "pi.durable.checkpoint" {
			checkpoints = append(checkpoints, event)
		}
	}
	want := []telemetry.RecordedTelemetryEvent{{Name: "pi.durable.checkpoint", Attributes: telemetry.SpanAttributes{telemetry.Attr("phase", "finish")}}}
	if !reflect.DeepEqual(checkpoints, want) {
		t.Fatalf("checkpoint events %+v, want %+v", checkpoints, want)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryStartsEachQueuedInputsRunInItsAdmissionTraceAndLinksInputRootsToTheirCaller(t *testing.T) {
	// harness-telemetry.test.ts: "starts each queued input's run in its admission trace and links input roots to their caller"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	gate, reached := deferred(), deferred()
	setup.Faux.SetResponses([]ai.FauxResponseStep{telHeldAnswer(gate, reached, "first"), fauxAnswer("second")})
	harness, root := openTracked(t, newControlledStorage(), setup, recorder)
	must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (struct{}, error) {
		ctx := chordctx.WithTelemetry(testContext, caller)
		first := must(root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")}))
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		second := must(root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("second")}))
		if status := must(second.Status(ctx)).Status; status != durable.SubmissionQueued {
			t.Fatalf("second status %q, want queued", status)
		}
		gate.resolve()
		must(first.Wait(ctx))
		must(second.Wait(ctx))
		return struct{}{}, nil
	}))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, telSettled(recorder))
	spans := recorder.GetSpans()
	caller := telSpanByName(t, spans, "caller")
	submits := telemetrySpans(recorder, "pi.durable.submit")
	if len(submits) != 2 {
		t.Fatalf("%d submit spans, want 2", len(submits))
	}
	for _, submit := range submits {
		if submit.ParentID != nil {
			t.Fatalf("submit %+v has a parent", submit)
		}
		if !reflect.DeepEqual(submit.Links, telLinks(caller.Context)) {
			t.Fatalf("submit links %+v, want a link to the caller %v", submit.Links, caller.Context)
		}
		found := false
		for _, span := range spans {
			found = found || span.Name == "pi.durable.phase" && span.Attributes.Value("phase") == "prepare" && reflect.DeepEqual(span.ParentContext, submit.Context)
		}
		if !found {
			t.Fatalf("no prepare phase parented to the submit %v", submit.Context)
		}
	}
	if reflect.DeepEqual(submits[0].Context, submits[1].Context) {
		t.Fatalf("both submits share the context %v", submits[0].Context)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryLinksTheOtherQueuedInputsToTheFirstInvocationOfTheirSharedRun(t *testing.T) {
	// harness-telemetry.test.ts: "links the other queued inputs to the first invocation of their shared run"
	setup := chatSetup(t)
	setup.SetSettings(func(settings *HarnessSettings) { settings.FollowUpMode = durable.QueueAll })
	recorder := telemetry.NewInMemoryTelemetryContext()
	gate, reached := deferred(), deferred()
	setup.Faux.SetResponses([]ai.FauxResponseStep{telHeldAnswer(gate, reached, "first"), fauxAnswer("shared")})
	harness, root := openTracked(t, newControlledStorage(), setup, recorder)
	first := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")}))
	if err := reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	second := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("second")}))
	third := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("third")}))
	secondTrace := must(second.Status(testContext)).Trace
	thirdTrace := must(third.Status(testContext)).Trace
	gate.resolve()
	must(first.Wait(testContext))
	must(second.Wait(testContext))
	must(third.Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, telSettled(recorder))
	tasks := telScanTasks(t, harness, "pi.generation")
	if len(tasks) < 2 {
		t.Fatalf("%d generation tasks, want at least 2", len(tasks))
	}
	if !reflect.DeepEqual(tasks[1].Trace, secondTrace) || !reflect.DeepEqual(tasks[1].Links, []telemetry.TelemetryCarrier{thirdTrace}) {
		t.Fatalf("second generation trace %v links %v, want trace %v links [%v]", tasks[1].Trace, tasks[1].Links, secondTrace, thirdTrace)
	}
	var phases []string
	for _, phase := range telemetrySpans(recorder, "pi.durable.phase") {
		if phase.Attributes.Value("task.id") != numberOf(tasks[1].Id) {
			continue
		}
		phases = append(phases, phase.Attributes.Value("phase").(string))
		if !reflect.DeepEqual(phase.Links, telLinks(thirdTrace)) {
			t.Fatalf("phase %v links %+v, want a link to %v", phase.Attributes.Value("phase"), phase.Links, thirdTrace)
		}
	}
	if !reflect.DeepEqual(phases, []string{"prepare", "request"}) {
		t.Fatalf("phases %v, want [prepare request]", phases)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryLinksASteerAdmissionToTheCurrentRunWithoutMovingItsWorkIntoTheSteersTrace(t *testing.T) {
	// harness-telemetry.test.ts: "links a steer admission to the current run without moving its work into the steer's trace"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	gate, reached := deferred(), deferred()
	addTool(t, setup.Registry, ownTool("hold", "hold", map[string]any{"type": "object", "properties": map[string]any{}}, func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		reached.resolve()
		if err := gate.wait(ctx); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{toolReply("hold", nil), fauxAnswer("answer")})
	harness, root := openTracked(t, newControlledStorage(), setup, recorder)
	first := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")}))
	if err := reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	var run durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for _, task := range must(harness.Inspect(testContext)).Tasks {
		if task.Record.Kind == "pi.generation" {
			run = task.Record
			break
		}
	}
	steer := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("steer"), WhenBusy: durable.WhenBusySteer}))
	steerTrace := must(steer.Status(testContext)).Trace
	var steerSpan *telemetry.RecordedTelemetrySpan
	for _, span := range recorder.GetSpans() {
		if span.Attributes.Value("submission.id") == numberOf(steer.Id()) {
			steerSpan = &span
			break
		}
	}
	if steerSpan == nil {
		t.Fatal("no span for the steer submission")
	}
	if steerSpan.ParentID != nil || !reflect.DeepEqual(steerSpan.Links, telLinks(run.Trace)) {
		t.Fatalf("steer span parent %v links %+v, want no parent and a link to %v", steerSpan.ParentID, steerSpan.Links, run.Trace)
	}
	gate.resolve()
	must(first.Wait(testContext))
	must(steer.Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, telSettled(recorder))
	for _, phase := range telemetrySpans(recorder, "pi.durable.phase") {
		if reflect.DeepEqual(phase.ParentContext, steerTrace) {
			t.Fatalf("phase %+v is parented to the steer's trace", phase)
		}
	}
	mustClose(t, harness)
}

func TestDurableTelemetryKeepsAPublicWriteAdmissionOpenThroughCommitAsAChildOfItsCaller(t *testing.T) {
	// harness-telemetry.test.ts: "keeps a public write admission open through commit as a child of its caller"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := newControlledStorage()
	harness, root := openTracked(t, store, setup, recorder)
	must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (struct{}, error) {
		held := store.holdCommits()
		type admitted struct {
			submission durable.Submission
			err        error
		}
		writing := make(chan admitted, 1)
		go func() {
			submission, err := root.Submit(chordctx.WithTelemetry(testContext, caller), durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "app.note", Data: "private-note"}})
			writing <- admitted{submission, err}
		}()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		inFlight := telSpanByName(t, recorder.GetSpans(), "pi.durable.submit")
		if inFlight.Settled || len(inFlight.Events) != 0 {
			t.Fatalf("in-flight submit span %+v, want unsettled without events", inFlight)
		}
		held.release()
		result := <-writing
		if result.err != nil {
			t.Fatal(result.err)
		}
		record := must(result.submission.Status(testContext))
		spans := recorder.GetSpans()
		span := telSpanByName(t, spans, "pi.durable.submit")
		if !reflect.DeepEqual(span.ParentContext, spans[0].Context) || !span.Settled || span.Attributes.Value("result") != "done" {
			t.Fatalf("submit span %+v, want a settled child of the caller %v with result done", span, spans[0].Context)
		}
		if !reflect.DeepEqual(record.Trace, span.Context) {
			t.Fatalf("record trace %v, want %v", record.Trace, span.Context)
		}
		var names []string
		for _, event := range span.Events {
			names = append(names, event.Name)
		}
		if !reflect.DeepEqual(names, []string{"pi.durable.entry", "pi.durable.submission"}) {
			t.Fatalf("submit events %v", names)
		}
		return struct{}{}, nil
	}))
	mustClose(t, harness)
}

func TestDurableTelemetryLinksAnInputSubmittedInsideATurnToTheCallingPhaseWithoutParentingIt(t *testing.T) {
	// harness-telemetry.test.ts: "links an input submitted inside a turn to the calling phase without parenting it"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	addTool(t, setup.Registry, ownTool("delegate", "delegate", map[string]any{"type": "object", "properties": map[string]any{}}, func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
		})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		conversation, err := api.Conversation(ctx, created.(durable.ConversationRecord).Id)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		submission, err := conversation.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("child")})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if _, err := submission.Wait(ctx); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{toolReply("delegate", nil), fauxAnswer("child answer"), fauxAnswer("parent answer")})
	harness, root := openTracked(t, newControlledStorage(), setup, recorder)
	must(must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("parent")})).Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, telSettled(recorder))
	spans := recorder.GetSpans()
	var tool *telemetry.RecordedTelemetrySpan
	for _, span := range spans {
		if span.Attributes.Value("tool.name") == "delegate" {
			tool = &span
			break
		}
	}
	if tool == nil {
		t.Fatal("no span for the delegate tool")
	}
	submits := telemetrySpans(recorder, "pi.durable.submit")
	if len(submits) < 2 {
		t.Fatalf("%d submit spans, want at least 2", len(submits))
	}
	child := submits[1]
	if child.ParentID != nil || !reflect.DeepEqual(child.Links, telLinks(tool.Context)) {
		t.Fatalf("child submit parent %v links %+v, want no parent and a link to %v", child.ParentID, child.Links, tool.Context)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryParentsBackgroundCompactionToThePreparePhaseThatCreatesIt(t *testing.T) {
	// harness-telemetry.test.ts: "parents background compaction to the prepare phase that creates it"
	setup := chatSetup(t, ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 1000, MaxTokens: 100}}})
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Compaction = &CompactionPolicyPatch{Enabled: new(true), ReserveTokens: new(300), KeepRecentTokens: new(150), BackgroundTokens: new(1000)}
	})
	recorder := telemetry.NewInMemoryTelemetryContext()
	addHooks(t, setup.Registry, CompactionTask.Erased(), &CompactionHooks{BeforeCompact: func(context.Context, CompactionRequest, HookApi) (*CompactionDecision, error) {
		return &CompactionDecision{Decline: true}, nil
	}})
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	commitValueCtx(testContext, t, root, func(tx durable.Tx) (struct{}, error) {
		if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "pi.user", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(strings.Repeat("x", 400)), Timestamp: 1}}}); err != nil {
			return struct{}{}, err
		}
		_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "pi.assistant", Model: []ai.Message{assistant(strings.Repeat("y", 400))}})
		return struct{}{}, err
	})
	must(must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(strings.Repeat("z", 400))})).Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, span := range recorder.GetSpans() {
			if span.Attributes.Value("task.name") == "pi.compaction" && span.Settled {
				return true
			}
		}
		return false
	})
	spans := recorder.GetSpans()
	var prepare, compaction *telemetry.RecordedTelemetrySpan
	for _, span := range spans {
		if prepare == nil && span.Attributes.Value("task.name") == "pi.generation" && span.Attributes.Value("phase") == "prepare" {
			prepare = &span
		}
		if compaction == nil && span.Attributes.Value("task.name") == "pi.compaction" {
			compaction = &span
		}
	}
	if prepare == nil || compaction == nil {
		t.Fatalf("prepare %v compaction %v", prepare, compaction)
	}
	if !reflect.DeepEqual(compaction.ParentContext, prepare.Context) {
		t.Fatalf("compaction parent %v, want the prepare phase %v", compaction.ParentContext, prepare.Context)
	}
	tasks := telScanTasks(t, harness, "pi.compaction")
	if len(tasks) == 0 || !tasks[0].Background || !reflect.DeepEqual(tasks[0].Trace, prepare.Context) {
		t.Fatalf("compaction tasks %+v, want a background task with trace %v", tasks, prepare.Context)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryMarksOnlyTheFirstInvocationAfterReopenResumedRetainingItsStoredParent(t *testing.T) {
	// harness-telemetry.test.ts: "marks only the first invocation after reopen resumed, retaining its stored parent"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := newControlledStorage()
	var recover atomic.Bool
	reached := deferred()
	task := telemetryTask("test.resume", "hold", map[string]telemetryPhase{
		"hold": func(ctx context.Context, running telemetryRunning, runtime telemetryRuntime) error {
			if !recover.Load() {
				reached.resolve()
				return abortedBy(runtime.Signal())
			}
			return telRunning("finish", 0)(ctx, running, runtime)
		},
		"finish": finishPhase,
	})
	addTask(t, setup.Registry, task.Erased())
	harness, root := openTracked(t, store, setup, recorder)
	id := must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "creator"}, func(span telemetry.TelemetrySpan) (durable.TaskId, error) {
		return telCreateTasks(chordctx.WithTelemetry(testContext, span), t, root, task.Erased(), durable.TaskOptions{})[0], nil
	}))
	harness.Resume()
	if err := reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	before := must(harness.GetTask(testContext, id))
	mustClose(t, harness)
	recover.Store(true)
	reopenedTelemetry := telemetry.NewInMemoryTelemetryContext()
	store.Reopen()
	harness, _ = openTracked(t, store, setup, reopenedTelemetry)
	must(harness.WaitForTask(testContext, id))
	waitFor(t, telSettled(reopenedTelemetry))
	phases := append(telemetrySpans(recorder, "pi.durable.phase"), telemetrySpans(reopenedTelemetry, "pi.durable.phase")...)
	var resumed []any
	for _, phase := range phases {
		resumed = append(resumed, phase.Attributes.Value("resumed"))
	}
	if !reflect.DeepEqual(resumed, []any{false, true, false}) {
		t.Fatalf("resumed attributes %v, want [false true false]", resumed)
	}
	if phases[0].Attributes.Value("outcome") != "aborted" {
		t.Fatalf("first phase outcome %v, want aborted", phases[0].Attributes.Value("outcome"))
	}
	if phases[0].Status != (telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK}) {
		t.Fatalf("first phase status %+v, want ok", phases[0].Status)
	}
	for _, phase := range phases {
		if !reflect.DeepEqual(phase.ParentContext, before.Trace) {
			t.Fatalf("phase parent %v, want the stored trace %v", phase.ParentContext, before.Trace)
		}
	}
	mustClose(t, harness)
	var active []float64
	for _, recorded := range []*telemetry.InMemoryTelemetryContext{recorder, reopenedTelemetry} {
		for _, measurement := range measurements(recorded, "pi.durable.tasks.active") {
			active = append(active, measurement.Value)
		}
	}
	if !reflect.DeepEqual(active, []float64{1, -1, 1, -1}) {
		t.Fatalf("pi.durable.tasks.active values %v, want [1 -1 1 -1]", active)
	}
}

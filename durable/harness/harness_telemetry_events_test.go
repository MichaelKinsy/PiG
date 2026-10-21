// Ports the event, outcome, and report cases of packages/durable/test/harness-telemetry.test.ts (Pi 2ec55d80).

package harness

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

func spanAttr(spans []telemetry.RecordedTelemetrySpan, name string, key string, value any) *telemetry.RecordedTelemetrySpan {
	for index := range spans {
		if spans[index].Name == name && spans[index].Attributes.Value(key) == value {
			return &spans[index]
		}
	}
	return nil
}

func reportAttributes(recorder *telemetry.InMemoryTelemetryContext) []telemetry.SpanAttributes {
	attributes := []telemetry.SpanAttributes{}
	for _, measurement := range measurements(recorder, "pi.durable.reports") {
		attributes = append(attributes, measurement.Attributes)
	}
	return attributes
}

func TestDurableTelemetryEmitsEntryAndSettlementEventsOnlyAfterSuccessfulStorageCommits(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := newControlledStorage()
	harness, root := openTracked(t, store, setup, recorder)
	err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) error {
		ctx := chordctx.WithTelemetry(testContext, span)
		_, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.rolled-back", Data: "private-rolled-back"}); err != nil {
				return nil, err
			}
			return nil, errors.New("rollback")
		})
		expectError(t, err, "rollback")
		if events := recorder.GetSpans()[0].Events; len(events) != 0 {
			t.Fatalf("events after a rolled back commit: %+v", events)
		}
		held := store.holdCommits()
		type written struct {
			entry durable.EntryRecord
			err   error
		}
		writing := make(chan written, 1)
		go func() {
			entry, err := durable.Commit(ctx, root, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.note", Data: "private-content"})
			})
			writing <- written{entry, err}
		}()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if events := recorder.GetSpans()[0].Events; len(events) != 0 {
			t.Fatalf("events while the commit is held: %+v", events)
		}
		held.release()
		result := <-writing
		if result.err != nil {
			t.Fatal(result.err)
		}
		submission, err := durable.Commit(ctx, root, func(tx durable.Tx) (durable.SubmissionRecord, error) {
			entryId := result.entry.Id
			return tx.CreateSubmission(durable.SubmissionCreate{ConversationId: root.Id(), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &entryId})
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []telemetry.RecordedTelemetryEvent{
			{Name: "pi.durable.entry", Attributes: telemetry.SpanAttributes{telemetry.Attr("entry.id", numberOf(result.entry.Id)), telemetry.Attr("entry.kind", "app.note")}},
			{Name: "pi.durable.submission", Attributes: telemetry.SpanAttributes{telemetry.Attr("submission.id", numberOf(submission.Id)), telemetry.Attr("status", "done")}},
		}
		if events := recorder.GetSpans()[0].Events; !reflect.DeepEqual(events, want) {
			t.Fatalf("events %+v, want %+v", events, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustClose(t, harness)
}

func TestDurableTelemetryRecordsInternalCompactionAdmissionsAsPhaseEventsWithoutASubmitSpan(t *testing.T) {
	setup := chatSetup(t)
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Compaction = &CompactionPolicyPatch{KeepRecentTokens: new(1)}
	})
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := newControlledStorage()
	var held atomic.Pointer[gate]
	addHooks(t, setup.Registry, CompactionTask.Erased(), &CompactionHooks{BeforeCompact: func(context.Context, CompactionRequest, HookApi) (*CompactionDecision, error) {
		g := store.holdCommits()
		held.Store(&g)
		return &CompactionDecision{Summary: new("private-summary")}, nil
	}})
	harness, root := openTracked(t, store, setup, recorder)
	if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
		if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "pi.user", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("old"), Timestamp: 1}}}); err != nil {
			return nil, err
		}
		_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "pi.assistant", Model: []ai.Message{assistant("answer")}})
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	id := must(root.Compact(testContext, nil))
	waitFor(t, func() bool { return held.Load() != nil })
	if err := held.Load().entered.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if len(telemetrySpans(recorder, "pi.durable.submit")) != 0 {
		t.Fatal("a submit span was recorded for the internal admission")
	}
	phase := spanAttr(recorder.GetSpans(), "pi.durable.phase", "task.name", "pi.compaction")
	if phase == nil || phase.Settled || len(phase.Events) != 0 {
		t.Fatalf("compaction phase %+v", phase)
	}
	held.Load().release()
	must(harness.WaitForTask(testContext, id))
	waitFor(t, func() bool { return allSettled(recorder) })
	spans := recorder.GetSpans()
	phase = spanAttr(spans, "pi.durable.phase", "task.name", "pi.compaction")
	if len(telemetrySpans(recorder, "pi.durable.submit")) != 0 {
		t.Fatal("a submit span was recorded for the internal admission")
	}
	hasEntry, hasDone := false, false
	for _, event := range phase.Events {
		hasEntry = hasEntry || event.Name == "pi.durable.entry"
		hasDone = hasDone || (event.Name == "pi.durable.submission" && event.Attributes.Value("status") == "done")
	}
	if !hasEntry || !hasDone {
		t.Fatalf("compaction phase events %+v", phase.Events)
	}
	if strings.Contains(jsonText(t, spans), "private-summary") {
		t.Fatal("the summary was recorded")
	}
	mustClose(t, harness)
}

func TestDurableTelemetryRecordsAFixedCategoryAndABoundedDiagnostic(t *testing.T) {
	for _, status := range []string{"faulted", "failed", "orphaned"} {
		t.Run(status, func(t *testing.T) {
			setup := chatSetup(t)
			recorder := telemetry.NewInMemoryTelemetryContext()
			message := strings.Repeat("diagnostic ", 80)
			task := telemetryTask("test.fault", "fail", map[string]telemetryPhase{
				"fail": func(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
					if status == "faulted" {
						return errors.New(message)
					}
					return runtime.Commit(ctx, func(durable.Tx, telemetryRunning) (*durable.NextTaskState[telemetryTaskState, durable.JsonValue], error) {
						outcome := durable.TaskOutcome[durable.JsonValue]{Status: durable.TaskOutcomeStatus(status)}
						if status == "orphaned" {
							outcome.Reason = &message
						} else {
							outcome.Error = &durable.TaskOutcomeError{Message: message, Detail: "private-error-detail"}
						}
						return &durable.NextTaskState[telemetryTaskState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &outcome}, nil
					})
				},
			})
			addTask(t, setup.Registry, task.Erased())
			harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
			id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
				return tx.CreateTaskErased(task.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned, Trace: &durable.TaskTrace{Carrier: telemetry.TelemetryCarrier{"unknown": "carrier"}}})
			}))
			record := must(harness.WaitForTask(testContext, id))
			outcome := record.State.Outcome
			if string(outcome.Status) != status {
				t.Fatalf("outcome %+v, want %s", outcome, status)
			}
			if status == "orphaned" && (outcome.Reason == nil || *outcome.Reason != message) || status != "orphaned" && (outcome.Error == nil || outcome.Error.Message != message) {
				t.Fatalf("outcome %+v does not keep the full message", outcome)
			}
			waitFor(t, func() bool { return allSettled(recorder) })
			span := telemetrySpans(recorder, "pi.durable.phase")[0]
			if span.ParentID != nil || span.Attributes.Value("outcome") != "faulted" {
				t.Fatalf("phase %+v", span)
			}
			want := telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "task_" + status, Message: message[:500]}}
			if !reflect.DeepEqual(span.Status, want) {
				t.Fatalf("status %+v, want %+v", span.Status, want)
			}
			if status == "faulted" && !hasEvent(span.Events, "pi.durable.report", telemetry.SpanAttributes{telemetry.Attr("type", "task_faulted"), telemetry.Attr("reason", "threw"), telemetry.Attr("extension", "task:test.fault"), telemetry.Attr("task.id", numberOf(id))}) {
				t.Fatalf("events %+v", span.Events)
			}
			text := jsonText(t, span)
			if strings.Contains(text, "private-exception-name") || strings.Contains(text, "private-error-detail") {
				t.Fatalf("span %s", text)
			}
			if content := recordedText(t, []telemetry.RecordedTelemetrySpan{span}); strings.Contains(content, "diagnostic") {
				t.Fatalf("attributes and events %s", content)
			}
			mustClose(t, harness)
		})
	}
}

func TestDurableTelemetryKeepsBothInterruptedRunsAndExplicitAbortHandlersAtStatusOk(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	reached := deferred()
	task := telemetryTask("test.abort", "hold", map[string]telemetryPhase{
		"hold": func(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
			reached.resolve()
			return abortedBy(runtime.Signal())
		},
	}, func(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, telemetryRunning) (*durable.NextTaskState[telemetryTaskState, durable.JsonValue], error) {
			return abortedWith[telemetryTaskState, durable.JsonValue]("private-abort-reason"), nil
		})
	})
	addTask(t, setup.Registry, task.Erased())
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned})
	}))
	harness.Resume()
	if err := reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	must(harness.AbortTask(testContext, id))
	must(harness.WaitForTask(testContext, id))
	waitFor(t, func() bool { return allSettled(recorder) })
	phases := telemetrySpans(recorder, "pi.durable.phase")
	if len(phases) != 2 || phases[0].Attributes.Value("kind") != "run" || phases[1].Attributes.Value("kind") != "abort" {
		t.Fatalf("phases %+v", phases)
	}
	for _, span := range phases {
		if span.Attributes.Value("outcome") != "aborted" || !reflect.DeepEqual(span.Status, telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK}) {
			t.Fatalf("phase %+v", span)
		}
	}
	if strings.Contains(jsonText(t, phases), "private-abort-reason") {
		t.Fatal("the abort reason was recorded")
	}
	mustClose(t, harness)
}

func TestDurableTelemetryMirrorsRuntimeReportsAsFixedFieldPhaseEventsAndTypeOnlyCounters(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	failure := errors.New("private-report-payload")
	task := telemetryTask("test.report", "report", map[string]telemetryPhase{
		"report": func(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
			runtime.Report(failure)
			runtime.Report(failure, durable.ReportFrom{Type: durable.ReportFromExtension, Extension: "reporting-extension"})
			runtime.Report(failure, durable.ReportFrom{Type: durable.ReportFromHost, Callback: durable.HostCallbackModels})
			return finishPhase(ctx, telemetryRunning{}, runtime)
		},
	})
	addTask(t, setup.Registry, task.Erased())
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned})
	}))
	must(harness.WaitForTask(testContext, id))
	waitFor(t, func() bool { return allSettled(recorder) })
	span := telemetrySpans(recorder, "pi.durable.phase")[0]
	want := []telemetry.RecordedTelemetryEvent{
		{Name: "pi.durable.report", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "task_error"), telemetry.Attr("extension", "task:test.report"), telemetry.Attr("task.id", numberOf(id))}},
		{Name: "pi.durable.report", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "extension_error"), telemetry.Attr("extension", "reporting-extension"), telemetry.Attr("task.id", numberOf(id))}},
		{Name: "pi.durable.report", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "host_error"), telemetry.Attr("task.id", numberOf(id))}},
	}
	if !reflect.DeepEqual(span.Events, want) {
		t.Fatalf("events %+v, want %+v", span.Events, want)
	}
	if reports := setup.Reports.all(); !reflect.DeepEqual(reports, []error{failure, failure, failure}) {
		t.Fatalf("reports %v", reports)
	}
	if len(telemetrySpans(recorder, "pi.durable.report")) != 0 {
		t.Fatal("a report span was created")
	}
	wantMetrics := []telemetry.SpanAttributes{{telemetry.Attr("type", "task_error")}, {telemetry.Attr("type", "extension_error")}, {telemetry.Attr("type", "host_error")}}
	if got := reportAttributes(recorder); !reflect.DeepEqual(got, wantMetrics) {
		t.Fatalf("report counters %+v, want %+v", got, wantMetrics)
	}
	for _, measurement := range measurements(recorder, "pi.durable.reports") {
		if measurement.Value != 1 {
			t.Fatalf("report counter %+v", measurement)
		}
	}
	if strings.Contains(jsonText(t, recorder.GetSpans()), "private-report-payload") {
		t.Fatal("the report payload was recorded")
	}
	mustClose(t, harness)
}

func TestDurableTelemetryReportsHookFailuresOnThePhaseAndClockFailuresWithoutAPhaseContext(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	hookError := errors.New("private-hook-error")
	mustInstall(t, setup.Registry, new(durable.Extension{Name: "reporting-hooks", Hooks: []durable.HookRegistration{{Task: "pi.generation", Handlers: &GenerationHooks{
		BeforeRequest: func(context.Context, GenerationRequest, HookApi) (*GenerationRequest, error) { return nil, hookError },
	}}}}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	must(must(root.Submit(testContext, ownInput("input"))).Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allSettled(recorder) })
	request := spanAttr(recorder.GetSpans(), "pi.durable.phase", "phase", "request")
	if request == nil {
		t.Fatal("no request phase span")
	}
	wantReport := telemetry.RecordedTelemetryEvent{Name: "pi.durable.report", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "extension_error"), telemetry.Attr("extension", "reporting-hooks"), telemetry.Attr("task.id", request.Attributes.Value("task.id"))}}
	count := 0
	for _, event := range request.Events {
		if event.Name == "pi.durable.report" {
			count++
			if !reflect.DeepEqual(event, wantReport) {
				t.Fatalf("report event %+v, want %+v", event, wantReport)
			}
		}
	}
	if count != 1 {
		t.Fatalf("request phase has %d pi.durable.report events, want 1: %+v", count, request.Events)
	}
	clockError := errors.New("private-clock-error")
	task := telemetryTask("test.clock", "clock", map[string]telemetryPhase{
		"clock": func(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
			setup.SetNow(func() float64 { panic(clockError) })
			runtime.Now()
			setup.SetNow(func() float64 { return 1 })
			return finishPhase(ctx, telemetryRunning{}, runtime)
		},
	})
	addTask(t, setup.Registry, task.Erased())
	id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned})
	}))
	must(harness.WaitForTask(testContext, id))
	waitFor(t, func() bool { return allSettled(recorder) })
	clock := spanAttr(recorder.GetSpans(), "pi.durable.phase", "task.name", "test.clock")
	if len(clock.Events) != 0 {
		t.Fatalf("clock phase events %+v", clock.Events)
	}
	if len(telemetrySpans(recorder, "pi.durable.report")) != 0 {
		t.Fatal("a report span was created")
	}
	if got, want := reportAttributes(recorder), []telemetry.SpanAttributes{{telemetry.Attr("type", "extension_error")}, {telemetry.Attr("type", "host_error")}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("report counters %+v, want %+v", got, want)
	}
	if reports := setup.Reports.all(); len(reports) != 2 || !errors.Is(reports[0], hookError) || reports[1].Error() != clockError.Error() {
		t.Fatalf("reports %v", reports)
	}
	text := jsonText(t, recorder.GetSpans())
	if strings.Contains(text, "private-hook-error") || strings.Contains(text, "private-clock-error") {
		t.Fatal("a report payload was recorded")
	}
	mustClose(t, harness)
}

func TestDurableTelemetryCountsAnOutsideSpanSessionFailureWithoutStartingASpan(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := newControlledStorage()
	harness, root := openTracked(t, store, setup, recorder)
	failure := errors.New("private-storage-error")
	store.failNextCommit(failure)
	_, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.failed"})
	})
	if !errors.Is(err, failure) {
		t.Fatalf("commit error %v, want %v", err, failure)
	}
	<-harness.Closed()
	waitFor(t, func() bool { return allSettled(recorder) })
	if reports := setup.Reports.all(); len(reports) != 1 || !errors.Is(reports[0], failure) {
		t.Fatalf("reports %v", reports)
	}
	if spans := recorder.GetSpans(); len(spans) != 0 {
		t.Fatalf("spans %+v", spans)
	}
	if strings.Contains(jsonText(t, recorder.GetMetrics()), "private-storage-error") {
		t.Fatal("the storage error was recorded")
	}
	if got, want := reportAttributes(recorder), []telemetry.SpanAttributes{{telemetry.Attr("type", "session_failed")}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("report counters %+v, want %+v", got, want)
	}
}

func TestDurableTelemetryCountsReportsRaisedWithANoopSpanWithoutCreatingARoot(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	failure := errors.New("private-noop-report")
	unsubscribe := harness.SubscribeCommits(func(context.Context, durable.CommitPublication) { panic(failure) })
	if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.note"})
	}); err != nil {
		t.Fatal(err)
	}
	if reports := setup.Reports.all(); len(reports) != 1 || reports[0].Error() != failure.Error() {
		t.Fatalf("reports %v", reports)
	}
	if spans := recorder.GetSpans(); len(spans) != 0 {
		t.Fatalf("spans %+v", spans)
	}
	if got, want := reportAttributes(recorder), []telemetry.SpanAttributes{{telemetry.Attr("type", "listener_error")}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("report counters %+v, want %+v", got, want)
	}
	unsubscribe()
	mustClose(t, harness)
}

func TestDurableTelemetryAttachesSynchronousCommitListenerReportsToTheActiveSubmissionSpan(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	failure := errors.New("private-listener-error")
	unsubscribe := harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		for _, change := range publication.Changes {
			if write, ok := change.(durable.EntryWrite); ok && write.Value.Kind == "pi.user" {
				panic(failure)
			}
		}
	})
	must(must(root.Submit(testContext, ownInput("input"))).Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allSettled(recorder) })
	if reports := setup.Reports.all(); len(reports) != 1 || reports[0].Error() != failure.Error() {
		t.Fatalf("reports %v", reports)
	}
	span := telemetrySpans(recorder, "pi.durable.submit")[0]
	if !hasEvent(span.Events, "pi.durable.report", telemetry.SpanAttributes{telemetry.Attr("type", "listener_error")}) {
		t.Fatalf("submit events %+v", span.Events)
	}
	if strings.Contains(jsonText(t, recorder.GetSpans()), "private-listener-error") {
		t.Fatal("the listener error was recorded")
	}
	unsubscribe()
	mustClose(t, harness)
}

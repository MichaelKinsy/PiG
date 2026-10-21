// Ports packages/durable/test/harness-telemetry.test.ts (Pi 2ec55d80).

package harness

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

func TestDurableTelemetryRecordsContentFreeSubmitAndPhaseSpans(t *testing.T) {
	// harness-telemetry.test.ts: "records content-free submit and phase spans, nested parents, checkpoints, entry events, and metrics"
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	secret := map[string]any{"type": "object", "properties": map[string]any{"secret": map[string]any{"type": "string"}}}
	addTool(t, setup.Registry, ownTool("inner", "inner", secret, func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return ownText("private-result"), nil
	}))
	addTool(t, setup.Registry, ownTool("outer", "outer", map[string]any{"type": "object", "properties": map[string]any{}}, func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if _, err := api.ExecuteTool(ctx, "inner", map[string]any{"secret": "private-argument"}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return ownText("private-output"), nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{toolReply("outer", nil), fauxAnswer("private-answer")})
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	submitted := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("private-input"), RequestId: new("dedupe")}))
	must(submitted.Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allSettled(recorder) })
	record := must(submitted.Status(testContext))
	again := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("another-private-input"), RequestId: new("dedupe")}))
	if againRecord := must(again.Status(testContext)); !reflect.DeepEqual(againRecord, record) {
		t.Fatalf("duplicate record %+v, want %+v", againRecord, record)
	}
	spans := recorder.GetSpans()
	submit := telemetrySpans(recorder, "pi.durable.submit")[0]
	if submit.ParentID != nil || submit.Attributes.Value("submission.id") != numberOf(submitted.Id()) || submit.Attributes.Value("result") != "placed" {
		t.Fatalf("submit span %+v", submit)
	}
	if !reflect.DeepEqual(record.Trace, submit.Context) {
		t.Fatalf("record trace %v, want %v", record.Trace, submit.Context)
	}
	phases := telemetrySpans(recorder, "pi.durable.phase")
	if len(phases) <= 4 {
		t.Fatalf("%d phase spans", len(phases))
	}
	requests := telemetrySpans(recorder, "pi.ai.request")
	if len(requests) != 2 {
		t.Fatalf("%d pi.ai.request spans", len(requests))
	}
	for _, request := range requests {
		found := false
		for _, phase := range phases {
			if request.ParentID != nil && phase.ID == *request.ParentID && phase.Attributes.Value("phase") == "request" {
				found = true
			}
		}
		if !found {
			t.Fatalf("request span %+v is not a child of a request phase", request)
		}
	}
	first := phases[0]
	if !reflect.DeepEqual(first.ParentContext, submit.Context) || first.Attributes.Value("phase") != "prepare" || first.Attributes.Value("resumed") != false || first.Attributes.Value("outcome") != "next=request" {
		t.Fatalf("first phase %+v", first)
	}
	var outer, inner telemetry.RecordedTelemetrySpan
	for _, phase := range phases {
		switch phase.Attributes.Value("tool.name") {
		case "outer":
			outer = phase
		case "inner":
			inner = phase
		}
	}
	if !reflect.DeepEqual(inner.ParentContext, outer.Context) {
		t.Fatalf("inner parent %v, want outer %v", inner.ParentContext, outer.Context)
	}
	if outer.Attributes.Value("phase") != "call" || outer.Attributes.Value("outcome") != "done" || outer.Attributes.Value("tool.call_id") != "call-1" {
		t.Fatalf("outer %+v", outer.Attributes)
	}
	if !hasEvent(outer.Events, "pi.durable.checkpoint", telemetry.SpanAttributes{telemetry.Attr("phase", "execute")}) {
		t.Fatalf("outer events %+v", outer.Events)
	}
	waiting := false
	for _, phase := range phases {
		waiting = waiting || phase.Attributes.Value("outcome") == "waiting"
	}
	if !waiting {
		t.Fatal("no phase ended waiting")
	}
	if !hasEvent(submit.Events, "pi.durable.entry", telemetry.SpanAttributes{telemetry.Attr("entry.id", numberOf(*record.Entry)), telemetry.Attr("entry.kind", "pi.user")}) {
		t.Fatalf("submit events %+v", submit.Events)
	}
	if !hasEvent(spanEvents(phases), "pi.durable.submission", telemetry.SpanAttributes{telemetry.Attr("submission.id", numberOf(submitted.Id())), telemetry.Attr("status", "done"), telemetry.Attr("answer.entry_id", numberOf(*record.Answer))}) {
		t.Fatalf("phase events %+v", spanEvents(phases))
	}
	recorded := recordedText(t, spans)
	for _, content := range []string{"private-input", "private-answer", "private-output", "private-result", "private-argument", "secret", "gen_ai"} {
		if strings.Contains(recorded, content) {
			t.Fatalf("recorded telemetry contains %q: %s", content, recorded)
		}
	}
	mustClose(t, harness)
	if got := len(measurements(recorder, "pi.durable.phase.duration")); got != len(phases) {
		t.Fatalf("%d duration measurements, want %d", got, len(phases))
	}
	sum := 0.0
	for _, measurement := range measurements(recorder, "pi.durable.tasks.active") {
		sum += measurement.Value
	}
	if sum != 0 {
		t.Fatalf("active tasks sum %v, want 0", sum)
	}
	for _, measurement := range recorder.GetMetrics() {
		if !strings.HasPrefix(measurement.Name, "pi.durable.") {
			continue
		}
		for _, attribute := range measurement.Attributes {
			if strings.Contains(attribute.Name, "id") {
				t.Fatalf("metric %s has attribute %s", measurement.Name, attribute.Name)
			}
		}
	}
	if reports := setup.Reports.all(); len(reports) != 0 {
		t.Fatalf("reports %v", reports)
	}
}

var _ = chordctx.Telemetry

// Attributes keep Pi's insertion order end to end: the start literal, then each name in the order it was first set
// (scheduler.ts #phase, submissions.ts submit, session.ts publish).
func TestDurableTelemetryRecordsAttributesInPisKeyOrder(t *testing.T) {
	setup := chatSetup(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	addTool(t, setup.Registry, ownTool("outer", "outer", map[string]any{"type": "object", "properties": map[string]any{}}, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return ownText("output"), nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{toolReply("outer", nil), fauxAnswer("answer")})
	harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
	submitted := must(root.Submit(testContext, ownInput("input")))
	must(submitted.Wait(testContext))
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allSettled(recorder) })
	submit := telemetrySpans(recorder, "pi.durable.submit")[0]
	if got, want := attributeNames(submit.Attributes), []string{"submission.type", "conversation.id", "submission.id", "result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("submit attributes %v, want %v", got, want)
	}
	phases := telemetrySpans(recorder, "pi.durable.phase")
	if got, want := attributeNames(phases[0].Attributes), []string{"task.id", "task.name", "phase", "kind", "resumed", "attempt", "outcome"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first phase attributes %v, want %v", got, want)
	}
	for _, phase := range phases {
		if phase.Attributes.Value("tool.name") == nil {
			continue
		}
		if got, want := attributeNames(phase.Attributes), []string{"task.id", "task.name", "phase", "kind", "resumed", "tool.name", "tool.call_id", "outcome"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("tool phase attributes %v, want %v", got, want)
		}
	}
	for _, event := range submit.Events {
		want := map[string][]string{"pi.durable.entry": {"entry.id", "entry.kind"}, "pi.durable.submission": {"submission.id", "status", "answer.entry_id"}}[event.Name]
		if got := attributeNames(event.Attributes); want != nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("%s attributes %v, want %v", event.Name, got, want)
		}
	}
	for _, event := range spanEvents(phases) {
		if event.Name == "pi.durable.submission" {
			if got, want := attributeNames(event.Attributes), []string{"submission.id", "status", "answer.entry_id"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("submission event attributes %v, want %v", got, want)
			}
		}
	}
	for _, measurement := range measurements(recorder, "pi.durable.phase.duration") {
		if got, want := attributeNames(measurement.Attributes), []string{"task", "phase", "outcome"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("duration metric attributes %v, want %v", got, want)
		}
	}
	mustClose(t, harness)
}

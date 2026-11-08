// Pi's case "preserves async listener error containment and report counting"
// (packages/durable/test/harness-telemetry.test.ts) in PiG's own form. Pi's listener returns a promise that rejects after every span
// has settled. A Go commit listener returns nothing from Advance and fails by panic, which the Session contains, so the late failure is a
// listener that fails on a commit made after the submission's spans settled. The property is the same: one listener_error report, one
// type-only pi.durable.reports counter, and no error text in any span.

package harness

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

func TestDurableTelemetryCountsAListenerFailureThatArrivesAfterEverySpanSettled(t *testing.T) {
	late := func(publication durable.CommitPublication) bool {
		for _, change := range publication.Changes {
			if write, ok := change.(durable.EntryWrite); ok && write.Value.Kind == "app.late" {
				return true
			}
		}
		return false
	}
	appendLate := func(root Conversation) {
		t.Helper()
		if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.late"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	// start runs one answered submission to the end of every span, with the failure still to come.
	start := func(t *testing.T) (*chatState, *telemetry.InMemoryTelemetryContext, Harness, Conversation) {
		setup := chatSetup(t)
		recorder := telemetry.NewInMemoryTelemetryContext()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
		return setup, recorder, harness, root
	}
	settle := func(t *testing.T, recorder *telemetry.InMemoryTelemetryContext, harness Harness, root Conversation) {
		t.Helper()
		must(must(root.Submit(testContext, ownInput("input"))).Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return allSettled(recorder) })
	}
	check := func(t *testing.T, setup *chatState, recorder *telemetry.InMemoryTelemetryContext, failure error) {
		t.Helper()
		if reports := setup.Reports.all(); len(reports) != 1 || reports[0].Error() != failure.Error() {
			t.Fatalf("reports %v, want the listener's error once", reports)
		}
		if got, want := reportAttributes(recorder), []telemetry.SpanAttributes{{telemetry.Attr("type", "listener_error")}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("report counters %+v, want %+v", got, want)
		}
		if !allSettled(recorder) {
			t.Fatal("a span is open after the late failure")
		}
		for _, span := range recorder.GetSpans() {
			if hasEvent(span.Events, "pi.durable.report", telemetry.SpanAttributes{telemetry.Attr("type", "listener_error")}) {
				t.Fatalf("a settled span %q received the late report: %+v", span.Name, span.Events)
			}
		}
		if strings.Contains(jsonText(t, recorder.GetSpans()), failure.Error()) {
			t.Fatal("the listener error was recorded in a span")
		}
	}

	t.Run("a commit listener that fails on a commit after the spans settled", func(t *testing.T) {
		setup, recorder, harness, root := start(t)
		failure := errors.New("private-async-listener-error")
		observation := observeStored(harness, func(_ context.Context, publication durable.CommitPublication) {
			if late(publication) {
				panic(failure)
			}
		})
		settle(t, recorder, harness, root)
		if reports := setup.Reports.all(); len(reports) != 0 {
			t.Fatalf("reports %v before the failure", reports)
		}
		appendLate(root)
		check(t, setup, recorder, failure)
		observation.Detach()
		mustClose(t, harness)
	})
}

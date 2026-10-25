package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi reports a failing watch listener through the Session: every CommittedWatch is built with the Session's report
// (session.ts watchDoc, view.ts watch, task-graph.ts watch), which also ends the watch. The Harness over the core reports
// them the same way.

var stateDoc = defineDoc("state.state", 1, func() obj { return delta.JsonObjectOf("value", 0, "retained", delta.JsonObjectOf("label", "stable")) })

func openReportingCoreHarness(t *testing.T) (harness.Harness, harness.Conversation, <-chan error) {
	t.Helper()
	reports := make(chan error, 16)
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{
		Models: ai.CreateModels(), Registry: harness.CreateRegistry(), OnReport: func(err error, _ durable.ReportInfo) { reports <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, root, reports
}

func expectReport(t *testing.T, reports <-chan error, want error) {
	t.Helper()
	select {
	case got := <-reports:
		if !errors.Is(got, want) {
			t.Fatalf("report %v, want %v", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no report of %v", want)
	}
}

func appendCoreNote(t *testing.T, root harness.Conversation) {
	t.Helper()
	if _, err := root.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCoreReportsAFailingWatchListenerAndEndsTheWatch(t *testing.T) {
	t.Run("of a conversation's view", func(t *testing.T) {
		_, root, reports := openReportingCoreHarness(t)
		watch, err := root.Watch(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("view watch listener failed")
		watch.Start(func(context.Context, harness.ConversationView, []durable.Op) error { return failure })
		appendCoreNote(t, root)
		expectWatchListenerError(t, watch.Closed(), watch.End, failure)
		expectReport(t, reports, failure)
	})

	t.Run("of a document", func(t *testing.T) {
		h, _, reports := openReportingCoreHarness(t)
		commitCoreDoc(t, h, touchStateDoc)
		watch, err := h.WatchDocErased(t.Context(), stateDoc)
		if err != nil || watch == nil {
			t.Fatalf("watch: %v %v", watch, err)
		}
		failure := errors.New("document watch listener failed")
		watch.Start(func(context.Context, durable.JsonObject, []durable.Op) error { return failure })
		commitCoreDoc(t, h, increment(stateDoc))
		expectWatchListenerError(t, watch.Closed(), watch.End, failure)
		expectReport(t, reports, failure)
	})
}

func TestCoreReportsAFailingAgentEventListenerAndEndsTheStream(t *testing.T) {
	// Pi events.ts watchEvents builds its CommittedWatch with `(error) => views.report(error)`.
	reports := make(chan error, 16)
	h, root, submit := nestedCoreOpen(t, &durable.Extension{Name: "nested", Tools: []*durable.ToolRegistration{
		nestedCoreTool("batch", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return nestedCoreText("done"), nil
		}),
	}}, func(err error, _ durable.ReportInfo) { reports <- err })
	stream, err := harness.WatchEvents(t.Context(), h, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("event listener failed")
	stream.Start(func(context.Context, []harness.AgentEvent) error { return failure })
	submit()
	expectWatchListenerError(t, stream.Closed(), stream.End, failure)
	expectReport(t, reports, failure)
}

func touchStateDoc(tx durable.Tx) error { return touch(tx, stateDoc) }

func commitCoreDoc(t *testing.T, h harness.Harness, change func(tx durable.Tx) error) {
	t.Helper()
	if _, err := h.Commit(t.Context(), func(tx durable.Tx) (any, error) { return nil, change(tx) }); err != nil {
		t.Fatal(err)
	}
}

func expectWatchListenerError(t *testing.T, closed <-chan struct{}, end func() durable.WatchEnd, failure error) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not end after its listener failed")
	}
	if got := end(); got.Reason != durable.WatchListenerError || !errors.Is(got.Error, failure) {
		t.Fatalf("the watch ended %+v, want listener_error with the listener's error", got)
	}
}

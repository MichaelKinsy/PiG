// Ports the last case of packages/durable/test/harness-telemetry.test.ts (Pi 2ec55d80). It lives here because the code mode
// tool this package builds imports the harness package.

package codemode

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

func TestDurableTelemetryEmitsReplayForACodemodeJournalResultWithoutRerunningItsNestedTool(t *testing.T) {
	f := codemodeChat(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := storage.NewMemoryStorage()
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`await tools.echo({ text: "once" }); await tools.hold({});`), done()})
	open := func() harness.Harness {
		opened, err := harness.OpenHarness(testContext, store, harness.HarnessOptions{Models: f.setup.models, Registry: f.setup.registry, Telemetry: recorder})
		if err != nil {
			t.Fatal(err)
		}
		return opened
	}
	opened := open()
	root, err := opened.Root(testContext, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	id := submitGo(t, root).Id()
	mustWait(t, f.holds.reached)
	mustClose(t, opened)
	f.holds.block.Store(false)
	store.Reopen()
	opened = open()
	if settled := must(must(opened.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %s, want done", settled.Status)
	}
	if err := opened.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		for _, span := range recorder.GetSpans() {
			if !span.Settled {
				return false
			}
		}
		return true
	})
	if got := f.echoCounts()["once"]; got != 1 {
		t.Fatalf("once ran %d times, want 1", got)
	}
	replayed := false
	for _, span := range recorder.GetSpans() {
		for _, event := range span.Events {
			if event.Name == "pi.durable.replay" && reflect.DeepEqual(event.Attributes, telemetry.SpanAttributes{telemetry.Attr("tool.name", "echo")}) {
				replayed = true
			}
		}
	}
	if !replayed {
		t.Fatal("no pi.durable.replay event for the stored echo result")
	}
	mustClose(t, opened)
}

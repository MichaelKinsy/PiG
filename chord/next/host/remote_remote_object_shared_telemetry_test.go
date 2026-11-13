package host

import (
	"reflect"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "telemetry" of 'references sharing one wire hold (decision "References share one wire hold")' of
// packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

// rsSpansNamed are meter's spans named name, in recording order.
func rsSpansNamed(meter *telemetry.InMemoryTelemetryContext, name string) []telemetry.RecordedTelemetrySpan {
	var named []telemetry.RecordedTelemetrySpan
	for _, span := range meter.GetSpans() {
		if span.Name == name {
			named = append(named, span)
		}
	}
	return named
}

// rsLinksOf are the carriers span links to.
func rsLinksOf(span telemetry.RecordedTelemetrySpan) []telemetry.TelemetryCarrier {
	carriers := []telemetry.TelemetryCarrier{}
	for _, each := range span.Links {
		carriers = append(carriers, each.Context)
	}
	return carriers
}

// rsLinksTo fails the test unless span links to exactly want.
func rsLinksTo(t testing.TB, what string, span telemetry.RecordedTelemetrySpan, want ...telemetry.TelemetryCarrier) {
	t.Helper()
	if want == nil {
		want = []telemetry.TelemetryCarrier{}
	}
	if got := rsLinksOf(span); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s links to %s, want %s", what, rhJSON(t, got), rhJSON(t, want))
	}
}

func TestReferencesSharingOneWireHoldTelemetry(t *testing.T) {
	// holdIn holds Notes of session on client inside a span named name of meter (Pi's `telemetry.startSpan({ name },
	// async (span) => { client.hold(..., withTelemetry(span, context)) })`).
	holdIn := func(t *testing.T, meter *telemetry.InMemoryTelemetryContext, name string, client Host, session string) {
		t.Helper()
		must(t, meter.StartSpan(telemetry.SpanOptions{Name: name}, func(span telemetry.TelemetrySpan) error {
			_, err := client.Hold(chordctx.WithTelemetry(background, span), Notes, rhNotes(session), GetOptions{})
			return err
		}))
	}

	// remote-object.test.ts: references sharing one wire hold › telemetry › the shared hold is a root linked to its first getter; a joining getter links to it while open; an untraced first getter gives a root without a link
	t.Run("the shared hold is a root linked to its first getter; a joining getter links to it while open; an untraced first getter gives a root without a link", func(t *testing.T) {
		meter := telemetry.NewInMemoryTelemetryContext()
		rig := rawProvider(t, rawLinkedOptions{Telemetry: meter})
		holdIn(t, meter, "first", rig.Client, "S1")
		holdIn(t, meter, "second", rig.Client, "S1")
		rhHoldRef(t, rig.Client, Notes, rhNotes("S2"))
		holdIn(t, meter, "third", rig.Client, "S2")
		bindNext(t, rig.Peer)
		bindNext(t, rig.Peer)
		rhSync(t, rig.Peer)
		holds := rsSpansNamed(meter, "chord.hold")
		if len(holds) != 2 {
			t.Fatalf("chord.hold spans = %d, want 2", len(holds))
		}
		s1, s2 := holds[0], holds[1]
		first, second, third := rsSpansNamed(meter, "first")[0], rsSpansNamed(meter, "second")[0], rsSpansNamed(meter, "third")[0]
		if s1.ParentID != nil || s2.ParentID != nil {
			t.Fatalf("the holds' parents = [%v %v], want none", s1.ParentID, s2.ParentID)
		}
		rsLinksTo(t, "the first hold", s1, first.Context)
		rsLinksTo(t, "the untraced getter's hold", s2)
		rsLinksTo(t, "the second getter", second, s1.Context)
		rsLinksTo(t, "the third getter", third, s2.Context)
	})

	// remote-object.test.ts: references sharing one wire hold › telemetry › a re-hold is a fresh root linked to the first hold's, also when the first getter was untraced; the NOOP adapter records none
	t.Run("a re-hold is a fresh root linked to the first hold's, also when the first getter was untraced; the NOOP adapter records none", func(t *testing.T) {
		meter := telemetry.NewInMemoryTelemetryContext()
		rig := rawProvider(t, rawLinkedOptions{Telemetry: meter})
		held := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		bindNext(t, rig.Peer)
		waitLive(t, held)
		rig.Peer.Close()
		next := rhNextParent(t, rig.Client)
		bindNext(t, next)
		rhSync(t, next)
		holds := rsSpansNamed(meter, "chord.hold")
		if len(holds) != 2 {
			t.Fatalf("chord.hold spans = %d, want 2", len(holds))
		}
		first, again := holds[0], holds[1]
		if first.ParentID != nil || again.ParentID != nil {
			t.Fatalf("the holds' parents = [%v %v], want none", first.ParentID, again.ParentID)
		}
		rsLinksTo(t, "the re-hold", again, first.Context)
		// Without an adapter (NOOP) no hold span is made, whatever the getter's context: its hold carries no carrier.
		untraced := rhProvider(t)
		holdIn(t, meter, "getter", untraced.Client, "S1")
		if hold := untraced.Peer.Next("hold").(*link.HoldMessage); hold.Trace != nil {
			t.Fatalf("the untraced client's hold carries the trace %v, want none", hold.Trace)
		}
	})
}

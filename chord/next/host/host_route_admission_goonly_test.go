package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Go-only cases of the route's admission: branches of Pi's route-admission.ts that no case of Pi's route-admission.test.ts
// reaches (Pi's cases fail a source only after an allow, and read no span event), each written from Pi's code path.

// Go-only: Pi's #moved `waiting` arm and #apply `again`. The decision's FIRST verdict is a failed source (Pi's cases 9 and 10 fail
// a source only after an allow, so the holds they tell are forwarded already: `again` is skipped for them). The hold held back
// is told `failed` once; its source changing and failing so again tells it nothing new (`again`: a hold held back is not told the
// same failure twice); the source healing into an allow settles the waiting decision again (`waiting`), which forwards the hold.
func TestRouteAdmissionASourceThatFailsBeforeTheFirstAllowTellsItsHoldOnceNothingNewWhenItFailsAgainAndAnAllowForwardsIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		raw := newRawWorkers(t, rawWorkerOptions{})
		g := routeGated(t, relayedOptions{Resolve: raw.Resolve}, raw)
		defer closeHosts(t, g.Server)
		g.set("throw")
		owner := g.rawTab(ownerPrincipal())
		holdOfPlaced(t, owner, "S1", "S1", PlacedNotes.Name, 0)
		g.settled()
		if got := owner.Kinds(); !kindsEqual(got, "hello", "failed") {
			t.Fatalf("owner kinds = %v, want hello, failed", got)
		}
		// Its source changes (another principal's mode) and the admission still throws: nothing new for the hold held back.
		g.set("yes", "guest")
		g.settled()
		if got := owner.Kinds(); !kindsEqual(got, "hello", "failed") {
			t.Fatalf("owner kinds after the same failure again = %v, want hello, failed", got)
		}
		if resolves := g.Resolves(); len(resolves) != 0 {
			t.Fatalf("resolves = %v, want none", resolves)
		}
		// It heals into an allow: the decision settles again and the hold is forwarded: `loading` downstream, the hold at the worker.
		g.set("yes")
		g.settled()
		if peers := len(raw.Peers()); peers != 1 {
			t.Fatalf("workers resolved = %d, want 1 for the healed allow", peers)
		}
		worker := raw.At(t, 0)
		g.settled()
		if got := since(owner, 2); !kindsEqual(got, "loading") {
			t.Fatalf("owner kinds since the failure = %v, want loading", got)
		}
		if holds := holdsOf(worker); len(holds) != 1 {
			t.Fatalf("worker holds = %d, want 1", len(holds))
		}
	})
}

// Go-only: the `chord.admit { outcome }` event of Pi's #apply (route-admission.ts), which no Pi test reads (the inventory names
// it; slice-14-design.md §2.3: a route's decision is an event of the hold's `chord.relay` span). A hold the decision allows shows
// `allow` on its relay span, one it denies `deny`: the event comes before the denial ends the span (relayedHold.withheld).
func TestRouteAdmissionTheDecisionIsAnEventOfTheHoldsRelaySpanWithItsOutcome(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		options := relayedOptions{}
		options.Telemetry.Server = recorder
		g := routeGated(t, options, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		owner := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		wantKind(t, owner, RefStateLive)
		guest := mustHold(t, g.tabOf(delta.JsonObjectOf("kind", "guest", "session", "S1")), PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		wantDenied(t, guest)
		relays := spansNamed(recorder, "chord.relay")
		if len(relays) != 2 {
			t.Fatalf("chord.relay spans = %d, want 2", len(relays))
		}
		for index, want := range []string{"allow", "deny"} {
			var outcomes []any
			for _, event := range relays[index].Events {
				if event.Name != "chord.admit" {
					continue
				}
				outcome, _ := event.Attributes.Get("outcome")
				outcomes = append(outcomes, outcome)
			}
			if len(outcomes) != 1 || outcomes[0] != want {
				t.Fatalf("chord.admit outcomes of relay span %d = %v, want [%s]", index, outcomes, want)
			}
		}
	})
}

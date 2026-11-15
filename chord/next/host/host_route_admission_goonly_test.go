package host

import (
	"context"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
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

// Go-only: Pi's #apply `if (verdict === before) return` (route-admission.ts:234). Forwarding is idempotent and a denial of a held
// back hold changes nothing, so the event is its only effect: `chord.admit { outcome }` is added to the relay span of each hold the
// decision tells, and a hold's relay span lives only until it is held upstream. A hold allowed whose resolve is pending (Pi's
// "the source reloaded while a resolve is pending", Pi's case 21 twin, which reads no span) keeps its span open across the
// reload: the source's plugin reloaded to an equal gate moves it, reads the verdict it has (`decide` keeps it) and tells nobody,
// so the span holds its one `allow` event, not two.
func TestRouteAdmissionASourceReloadedToTheSameVerdictWhileAResolveIsPendingAddsNoSecondAdmitEventToTheHoldsRelaySpan(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		pending := make(chan struct{})
		options := relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			_, _ = chordctx.Receive(ctx, pending)
			return worker(), nil
		}}
		options.Telemetry.Server = recorder
		g := routeGated(t, options, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		_, err := g.Server.Reload(againFacets(serveGate), background, ReloadOptions{})
		must(t, err)
		g.settled()
		if got := kindOf(notes); got != RefStateLoading {
			t.Fatalf("state = %s, want loading (the resolve is pending)", got)
		}
		close(pending)
		g.settled()
		wantKind(t, notes, RefStateLive)
		relays := spansNamed(recorder, "chord.relay")
		if len(relays) != 1 {
			t.Fatalf("chord.relay spans = %d, want 1", len(relays))
		}
		var outcomes []any
		for _, event := range relays[0].Events {
			if event.Name != "chord.admit" {
				continue
			}
			outcome, _ := event.Attributes.Get("outcome")
			outcomes = append(outcomes, outcome)
		}
		if len(outcomes) != 1 || outcomes[0] != "allow" {
			t.Fatalf("chord.admit outcomes of the relay span after the equal reload = %v, want [allow]", outcomes)
		}
	})
}

// Go-only: Pi's route.ts `#resolveIfHeld` (`path.wanted`, no link, `routed.resolving`, route.ts:233), reached by `RouteHandle.retry`
// (route.ts:159). Pi's case 20 and its retry case never call `retry` while a `resolve` is running; the call must not start a second
// one: the address has one resolve at a time (its link, when it returns, serves every hold), and the retry says it resolved nothing.
func TestRouteAdmissionRouteHandleRetryWhileTheAddressIsResolvingStartsNoSecondResolve(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		pending := make(chan struct{})
		g := routeGated(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			_, _ = chordctx.Receive(ctx, pending)
			return worker(), nil
		}}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		if got := kindOf(notes); got != RefStateLoading {
			t.Fatalf("state = %s, want loading (the resolve is pending)", got)
		}
		address := Session.Address(jsonObject("id", "S1"))
		if g.Route.Retry(address) {
			t.Fatal("retry resolved the address that is resolving")
		}
		g.settled()
		close(pending)
		g.settled()
		wantKind(t, notes, RefStateLive)
		if resolves := g.Resolves(); len(resolves) != 1 || resolves[0] != "session|S1" {
			t.Fatalf("resolves = %v, want one session|S1", resolves)
		}
	})
}

// Go-only: remote/path.ts `disconnected` (`this.#released = undefined`: its fences go with the link). A denial releases the hold on
// its link and marks that an allow forwards it again only behind a fence (release, `ping`, hold after the `pong`). The link that
// is lost takes the mark with it: an allow on the next link, which never saw the denial, holds at once with no `ping` before it.
// Pi's case "the link lost before the pong" allows before the loss and forwards nothing across it, so it does not reach this.
func TestRouteAdmissionADenialLeavesNoFenceBehindWithTheLinkThatIsLostAnAllowOnTheNextLinkHoldsAtOnce(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		f.set("no")
		f.settled()
		f.worker.Close()
		f.settled()
		next := f.raw.At(t, 1)
		f.settled()
		// The guest's hold only: the owner is denied.
		if got := next.Kinds(); !kindsEqual(got, "hello", "hold") {
			t.Fatalf("next kinds = %v, want hello, hold", got)
		}
		f.set("yes")
		f.settled()
		// No fence: the hold of the allowed owner follows at once, nothing waits for a `pong`.
		if got := next.Kinds(); !kindsEqual(got, "hello", "hold", "hold") {
			t.Fatalf("next kinds after the allow = %v, want hello, hold, hold", got)
		}
	})
}

// Go-only: route-admission.ts #apply `forwarded` is a Set (`if (forwarded.has(holder)) continue`): a hold is in it once, however
// many allows say so. Go's list needs the check Pi's Set is: an allow that comes again (a source that failed and healed says a
// new allow, the hold forwarded under the old one all the while) must not put the hold in it twice, or the link lost while the
// source fails again takes it back twice (#unlinked walks the list) and tells it `failed` twice.
func TestRouteAdmissionAHoldIsForwardedOnceHoweverManyAllowsSayAndTakenBackOnceWhenItsLinkIsLostUnderAFailedSource(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		f.set("throw")
		f.settled()
		f.set("yes")
		f.settled()
		f.set("throw")
		f.settled()
		f.worker.Close()
		f.settled()
		if told := peerKindCount(f.owner, "failed"); told != 1 {
			t.Fatalf("owner kinds = %v: told failed %d times, want once", f.owner.Kinds(), told)
		}
	})
}

// Go-only: Pi's `#left` (route-admission.ts:167 `asked.forwarded.delete(holder)`). Pi's Set drops the hold with it: a hold that
// left is not kept by the decision its sibling still shares. Here `forwarded` is a slice, and the line is what lets the left hold
// be collected (no case of Pi's reads it: nothing observes a stale entry but the memory it keeps).
func TestRouteAdmissionAHoldThatLeftIsNotKeptByTheDecisionItsSiblingStillSharesWhenForwarded(t *testing.T) {
	g := routeGated(t, relayedOptions{}, nil)
	defer closeHosts(t, g.Tab, g.Server)
	g.set("yes")
	second := g.tabOf(ownerPrincipal())
	defer closeHosts(t, second)
	a := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
	b := mustGet(t, second, PlacedNotes, jsonObject("session", "S1"))
	g.settled()
	var left weak.Pointer[relayedHold]
	g.Server.(*chordHost).enter(func() {
		for _, asked := range g.route().admission.asked {
			if len(asked.holders) != 2 || len(asked.forwarded) != 2 {
				t.Errorf("holders = %d, forwarded = %d, want 2 and 2", len(asked.holders), len(asked.forwarded))
				return
			}
			left = weak.Make(asked.holders[0].(*relayedHold))
		}
	})
	a.Release()
	g.settled()
	wantKind(t, b, RefStateLive)
	expectCollected(t, left)
}

package host

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Own test of `attempt.activate` (attempt.ts:818, `await awaitWithContext(ending, this.#opening)`) at the host's boundary: an
// attempt that is sealed while it waits for the old attempt's drain never runs its factory when the drain ends right after.
// It is the two-attempt form of Pi's admission case "a decision change while its old attempt drains seals that attempt at
// once" (admission.test.ts), whose `built` is `["a:write"]` where the Go port once had `[a:write a:write]` 3 times in 100
// (6 under one CPU): the successor is sealed in the flush of `set("none")` and the old drain ends within microtasks of it.
// Here a reload makes the successor wait (v2, behind v1's held call) and one section seals it and ends the drain it waits
// for, the settlement after the seal as Pi's microtasks run it. Pi: the abort wins, the reaction of the end of the drain
// finds the wait rejected.

// reloadBuilt records, in order, which instance's factory ran.
type reloadBuilt struct {
	mu    sync.Mutex
	order []string
}

func (b *reloadBuilt) add(label string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.order = append(b.order, label)
}

func (b *reloadBuilt) items() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.order)
}

// reloadServeBuilt serves `Pure` labelled label, noting in built that its factory ran.
func reloadServeBuilt(t testing.TB, h FacetHost, label string, built *reloadBuilt) error {
	return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		built.add(label)
		return reloadPureImplementation(t, self, label, 1, nil, nil), nil
	}, ServeOptions{})
}

// orderedReloadRuns is how many hosts a measurement builds.
const orderedReloadRuns = 100

func TestReloadAnAttemptSealedWhileItWaitsForTheOldDrainNeverRunsItsFactoryWhenTheDrainEndsInTheSameSection(t *testing.T) {
	wrong := 0
	var last []string
	for range orderedReloadRuns {
		held := newReloadGate()
		built := &reloadBuilt{}
		host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, reloadGates(held)) }, loadedOptions{})
		pure := mustGet(t, host, reloadPure, jsonObject())
		old := startReloadCall(pure, "bump", nil)
		held.entered.wait(t)
		// v2 waits for v1's drain (its factory ran once: the reload's rehearsal).
		reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServeBuilt(t, h, "v2", built) }))
		pure.binding.loop.Run(func() {
			object := reloadObjectOf(pure)
			ending := object.host.endingOf(object.identity)
			// The seal of the waiting attempt (what a decision change, or a reload that replaces it, does), then the end of the
			// drain it waits for.
			object.phase.attempt.abandon(activation{kind: activationEnded, err: object.ended()})
			ending.resolve(struct{}{})
		})
		held.open.fire()
		old.wait(t)
		deliveredOn(host)
		if got := built.items(); !slices.Equal(got, []string{"v2"}) {
			wrong++
			last = got
		}
		if errs.Len() != 0 {
			t.Fatalf("errors = %v, want none", errs.All())
		}
		closeHosts(t, host)
	}
	if wrong != 0 {
		t.Fatalf("%d of %d runs ran the factory of a sealed attempt: built = %v, want [v2]", wrong, orderedReloadRuns, last)
	}
}

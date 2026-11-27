package host

import (
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// The start barrier (host.ts:730 `await Promise.all(objects.map(({ firstOutcome }) => firstOutcome))`) ends for a host-lifetime
// object once it has its first outcome: live, or parked with no blocker here still without one (dependencies.ts:106 / :113
// `binding.target?.blocksFirstPass`). A blocker that is a remote type with an admission has its first outcome when its admission
// parks it (admission.ts:336 `firstPass: true`). Go-only: these cases ask the object for `hasFirstOutcome` once the host is quiet,
// so a first outcome that never comes fails by assertion, not by a `load` that never returns.

// admBarrier is a host-lifetime type (empty params) reading the text of the document `a`.
var admBarrier = mustContract(declareLocal("test.barrier", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("shout", contracts.Str())},
}))

// firstOutcomeOf is whether the host's object of type typ had its first outcome, and whether the host has it at all.
func firstOutcomeOf(host Host, typ *contracts.Contract) (had, found bool) {
	h := host.(*chordHost)
	h.enter(func() {
		for _, object := range h.objects {
			if object.served.contract.Name == typ.Name {
				had, found = object.hasFirstOutcome, true
			}
		}
	})
	return had, found
}

// barrierFacet serves admBarrier, whose state reads the text of the document `a`.
func barrierFacet() Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(admBarrier, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			text := self.Get(admDoc, jsonObject("id", "a")).Member("text")
			return contracts.MembersImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"shout": self.Derive([]any{text}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			}}, nil
		}, ServeOptions{})
	})
}

func TestStartBarrierAHostObjectParkedOnADocumentWhoseAdmissionSourceFailedHasItsFirstOutcome(t *testing.T) {
	address := Server.Address(nil)
	host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: func(HostError) {}})
	defer closeHosts(t, host)
	var once sync.Once
	ran := make(chan struct{})
	// Its admission's derive throws: the document parks `failed { waiting }` on its source (admission.ts:336).
	throwing := func(_ *delta.JsonObject, _ data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
		return self.Derive(nil, func([]any) (delta.JsonValue, error) {
			once.Do(func() { close(ran) })
			return nil, errors.New("the rule threw")
		}), nil
	}
	doc := admDocFacet(admDocCode{Admit: throwing}, admDoc, nil)
	loaded := make(chan error, 1)
	go func() {
		_, err := host.Load([]HostFacet{{Name: "@test/doc", Facet: doc}, {Name: "@test/barrier", Facet: barrierFacet()}}, background)
		loaded <- err
	}()
	<-ran
	deliveredOn(host)
	if had, found := firstOutcomeOf(host, admBarrier); !found || !had {
		t.Fatalf("the host-lifetime object parked on the failed admission: found = %v, first outcome = %v, want both: its blocker's first outcome is the failure", found, had)
	}
	must(t, <-loaded)
}

// A document's admission that parks it (denied, or failed on its source) is its first outcome: objects parked on it do not wait
// for it (object.ts:1049 `if (firstPass) this.#settleFirstOutcome()` after admission.ts:336 and attempt.ts:874 `firstPass: true`).
func TestStartBarrierADocumentWhoseAdmissionParksItHasItsFirstOutcome(t *testing.T) {
	for name, mode := range map[string]string{"a denial": "none", "a source that throws": "throw"} {
		t.Run(name, func(t *testing.T) {
			s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{})
			s.set(mode, "server")
			doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
			s.delivered()
			if state := stateOfRef(doc); state.Kind != RefStateFailed {
				t.Fatalf("state = %+v, want failed", state)
			}
			if had, found := firstOutcomeOf(s.h, admDoc); !found || !had {
				t.Fatalf("the parked document: found = %v, first outcome = %v, want both", found, had)
			}
			must(t, s.h.Close(background))
		})
	}
}

// attempt.ts:1560 `{ kind: "parked", shown: pinned, firstPass: true }`: an attempt parked on a key that failed to load has its
// first outcome (reference-keys.test.ts › a key whose first pin still loads › the object fails and stays failed).
func TestStartBarrierAnObjectParkedOnAKeyThatFailedToLoadHasItsFirstOutcome(t *testing.T) {
	c := kmColdServe(t, "a")
	c.hold()
	abort, cancel := chordctx.WithCancel(background)
	viewer, err := c.Host.Hold(background, kmViewer, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	loading := kmGetViewer(c.Host, abort)
	<-c.Gate.entered
	c.fail()
	c.Gate.Release()
	deliveredOn(c.Host)
	if state := stateOfRef(viewer); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("viewer state = %+v, want failed waiting", state)
	}
	if had, found := firstOutcomeOf(c.Host, kmViewer); !found || !had {
		t.Fatalf("the viewer parked on the failed key: found = %v, first outcome = %v, want both", found, had)
	}
	viewer.Release()
	cancel(errors.New("aborted"))
	<-loading
	must(t, c.Host.Close(background))
}

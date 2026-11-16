package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// admission.ts:247 `{ kind: "parked", shown: pinned, firstPass: true, cause: pinned.cause }`: an admission whose pinned key fails to
// load parks its object on that failure, and the object has its first outcome (object.ts:1049). Pi has no case that declares a
// key in `admit` (admission.test.ts has none); reference.test.ts:655 is the twin of the setup for a factory. Go-only.
func TestStartBarrierADocumentWhoseAdmissionPinsAKeyThatFailsToLoadHasItsFirstOutcome(t *testing.T) {
	st := refSeeded(t)
	address := Server.Address(nil)
	host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), Store: st, OnError: func(HostError) {}})
	defer closeHosts(t, host)
	// The stored key's version is older than the memo's: its load migrates, and the migration throws.
	notes := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(refNoteStore, refStoreFactory(refPersistedMemo(2), func(*documents.Keyed) {}), ServeOptions{})
	})
	admit := func(_ *delta.JsonObject, _ data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
		_, err := self.Get(refNoteStore, jsonObject()).Keyed("notes").At("a")
		if err != nil {
			return nil, err
		}
		return self.Derive(nil, func([]any) (delta.JsonValue, error) {
			return Allow(jsonObject("mode", "edit")), nil
		}), nil
	}
	doc := admDocFacet(admDocCode{Built: &admLog{}, Admit: admit}, admDoc, nil)
	_, err := host.Load([]HostFacet{{Name: "@test/notes", Facet: notes}, {Name: "@test/doc", Facet: doc}}, background)
	must(t, err)
	held := mustHold(t, host, admDoc, jsonObject("id", "a"))
	scopeEventually(t, "the document failed", func() bool { return refKind(held) == RefStateFailed })
	deliveredOn(host)
	if state := stateOfRef(held); state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	if had, found := firstOutcomeOf(host, admDoc); !found || !had {
		t.Fatalf("the document whose admission's key failed to load: found = %v, first outcome = %v, want both", found, had)
	}
	held.Release()
}

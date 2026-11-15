package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// Go-only (Pi's tests read no `chord.admit` event: attempt.ts:814 and :873 record it, telemetry.ts declares it): an admitted
// type's attempt records its decision on its activation span as the event `chord.admit` with the attribute `outcome`
// (`deny` while the caller has no access, `allow` once it has).
func TestAdmissionRecordsItsDecisionOnItsActivationSpan(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	errs := &hostErrors{}
	address := Server.Address(nil)
	h := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: errs.push, Telemetry: recorder})
	s := &admServer{t: t, h: h, errors: errs, plugins: map[string]Facet{
		"access": admAccessFacet(admAccessCode{}, admAccess, nil),
		"doc":    admDocFacet(admDocCode{Built: &admLog{}}, admDoc, nil),
	}}
	_, err := h.Load(s.facets(), background)
	must(t, err)
	doc, _ := admHoldStates(t, h, admDoc, jsonObject("id", "a"))
	s.delivered()
	admDenied(t, stateOfRef(doc), "no access")
	s.set("read", "server")
	s.delivered()
	var outcomes []any
	for _, span := range spansNamed(recorder, "chord.activate") {
		for _, event := range span.Events {
			if event.Name != "chord.admit" {
				continue
			}
			for _, attribute := range event.Attributes {
				if attribute.Name == "outcome" {
					outcomes = append(outcomes, attribute.Value)
				}
			}
		}
	}
	if len(outcomes) < 2 || outcomes[0] != "deny" || outcomes[len(outcomes)-1] != "allow" {
		t.Fatalf("chord.admit outcomes on the activation spans = %v, want a deny first and an allow last", outcomes)
	}
	must(t, h.Close(background))
}

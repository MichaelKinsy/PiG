package wiring

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// coveredByScenarios is every wire method the SDK conformance table says it
// exercises, keyed "<kind> <method>".
func coveredByScenarios() map[string]string {
	covered := map[string]string{}
	for _, scenario := range sdkscenarios.All() {
		for _, entry := range scenario.Covers {
			covered[entry] = scenario.Group + "/" + scenario.ID
		}
	}
	return covered
}

// derivedWireMethods derives, from the host and the SDKs' code, every wire
// method a Covers entry may name: the calls an SDK can send and the host
// dispatches, the requests and notifies the host sends, and the provider object
// methods the host invokes or an SDK declares.
func derivedWireMethods(t *testing.T) map[string]bool {
	t.Helper()
	host := goPkg(t, "coding/extension/host/subprocess")
	derived := map[string]bool{}
	handled, _ := host.handledMethods(host.envelopeFieldType(t, "call"))
	for _, method := range handled.sorted() {
		derived["call "+method] = true
	}
	for _, frame := range []string{"request", "notify"} {
		for _, method := range host.sentMethods(host.envelopeFieldType(t, frame)).sorted() {
			derived[frame+" "+method] = true
		}
	}
	for _, method := range hostObjectMethods(t).sorted() {
		derived["object "+method] = true
	}
	for _, sdk := range declaredObjectMethods(t) {
		for _, method := range sdk.set.sorted() {
			derived["object "+method] = true
		}
	}
	return derived
}

// TestScenarioCoversNameRealWireMethods: a conformance scenario claims coverage
// only of a wire method the code still has, so a rename or removal cannot leave
// a scenario vouching for a method that no longer exists.
func TestScenarioCoversNameRealWireMethods(t *testing.T) {
	derived := derivedWireMethods(t)
	l := newLedger(t, "scenario-covers", nil)
	for _, entry := range slices.Sorted(maps.Keys(coveredByScenarios())) {
		if !derived[entry] {
			l.add(entry, fmt.Sprintf("scenario %s covers %q, which is no wire method the host or an SDK has", coveredByScenarios()[entry], entry))
		}
	}
	l.finish()
}

// authObjectMethodsNotExercised are provider auth methods no conformance
// scenario drives, each with the suite that does.
var authObjectMethodsNotExercised = map[string]string{}

// TestEveryAuthObjectMethodIsExercisedByAScenario: every provider object auth
// method the host invokes or an SDK declares (auth.apiKey.*, auth.oauth.*) is
// driven by a conformance scenario through every SDK's driver. The auth methods
// are where #201 hid: declared by every SDK, replaced by host behavior.
func TestEveryAuthObjectMethodIsExercisedByAScenario(t *testing.T) {
	covered := coveredByScenarios()
	l := newLedger(t, "auth-method-exercised", authObjectMethodsNotExercised)
	for entry := range derivedWireMethods(t) {
		method, isObject := strings.CutPrefix(entry, "object ")
		if !isObject || !strings.HasPrefix(method, "auth.") {
			continue
		}
		if _, ok := covered[entry]; !ok {
			l.add(method, fmt.Sprintf("no conformance scenario exercises provider object method %q", method))
		}
	}
	l.finish()
}

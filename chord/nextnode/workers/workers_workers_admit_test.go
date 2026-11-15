package workers

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/node/workers/workers.test.ts "starts on demand › passes admit to its route: a denied principal
// starts no worker, an allowed one does (slice 14 §2.3)" (@7cc21c24f6b5), with the support.ts helper change (fleet's `admit`
// option: fleetOptions.admit, passed as WorkersOptions.Admit: see the patch workers-support.patch).
//
// Covers: workers_workers.go `Admit: options.Admit` in CreateWorkers (mutation: drop the field: the denied fleet starts a worker).
func TestStartsOnDemandPassesAdmitToItsRouteADeniedPrincipalStartsNoWorkerAnAllowedOneDoes(t *testing.T) {
	denied := newFleet(t, fleetOptions{admit: func(data.Principal, contracts.Address, host.AdmitSelf) (any, error) {
		return state.NewConstant(host.Deny("no workers for you")), nil
	}})
	refused := denied.hold(fixtureNotes, "S1")
	// Pi waits for `failed`; a fleet that does not pass admit never fails the hold, which goes live: waiting for the hold to leave
	// `loading` ends either way at once, and the assertion after it fails by value, not by the wait's bound.
	until(t, func() bool { return refKind(refused) != host.RefStateLoading })
	if got := refKind(refused); got != host.RefStateFailed {
		t.Fatalf("state = %s, want failed", got)
	}
	cause := refused.State().Value().Cause
	if cause.Kind != data.FailureDenied || cause.Error.Code != data.CodeForbidden || cause.Error.Message != "no workers for you" {
		t.Fatalf("cause = %s %s %q, want denied forbidden: no workers for you", cause.Kind, cause.Error.Code, cause.Error.Message)
	}
	// Nothing forked, nothing even asked to start.
	if starts, entry := denied.starts.all(), denied.stateOf("S1"); len(starts) != 0 || entry != nil {
		t.Fatalf("starts = %v, state = %+v, want none", starts, entry)
	}
	refused.Release()

	allowed := newFleet(t, fleetOptions{admit: func(data.Principal, contracts.Address, host.AdmitSelf) (any, error) {
		return state.NewConstant(host.Allow(nil)), nil
	}})
	notes := allowed.hold(fixtureNotes, "S1")
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if starts := allowed.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v, want session|S1", starts)
	}
	notes.Release()
}

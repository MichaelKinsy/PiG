package host

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// reload.ts:179-183 and 265-279: after the first prepare pass, a reload sets the gate on changed types and waits,
// under the gate deadline, for activations still in flight: an activation of a changed type that started after the
// first pass settled is prepared once it finishes, and one that does not finish in time aborts the reload with
// "activations in flight did not finish within <deadline> ms".
func TestReloadWaitsForActivationsStillInFlightAfterTheFirstPreparePass(t *testing.T) {
	run := func(t *testing.T, release bool) (ReloadReport, *requestsLatch, Ref, error) {
		host := reloadQuiet(t)
		gate := newRequestsLatch()
		t.Cleanup(gate.open)
		openLate := func(self Self, input any) error {
			id, _ := input.(interface{ Value(string) any }).Value("id").(string)
			late := id == "late"
			_, err := self.Resource("conn", ResourceDeclaration[any]{
				Version: 1,
				Open: func(ResourceOpenOptions) (any, error) {
					if late {
						gate.wait()
					}
					return nil, nil
				},
			})
			return err
		}
		reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "v1", extra: openLate}))
		live := reloadGet(t, host, reloadGreeter, delta.JsonObjectOf("id", "a"))
		if err := readyErr(t, live); err != nil {
			t.Fatal(err)
		}
		var late Ref
		reloadBetweenPreparePasses = func(host *HostImpl) {
			// An activation of the changed type starts after the first pass and before the gate.
			late = reloadGet(t, host, reloadGreeter, delta.JsonObjectOf("id", "late"))
			if release {
				// Opened whenever: the second pass prepares it whether it is still activating or already live.
				gate.open()
			}
		}
		t.Cleanup(func() { reloadBetweenPreparePasses = nil })
		deadline := 40 * time.Millisecond
		if release {
			deadline = 5 * time.Second
		}
		report, err := host.Reload(reloadPlugins(reloadGreeterFacet(reloadGreeterSpec{greeting: "v2", extra: openLate})), ReloadOptions{GateDeadline: &deadline})
		return report, gate, late, err
	}

	t.Run("an activation that finishes is prepared", func(t *testing.T) {
		report, _, late, err := run(t, true)
		if err != nil {
			t.Fatal(err)
		}
		if late == nil {
			t.Fatal("the hook did not run")
		}
		if len(report.Prepared) != 2 || !slices.ContainsFunc(report.Prepared, func(address string) bool { return strings.Contains(address, "late") }) {
			t.Fatalf("prepared = %v", report.Prepared)
		}
		if got := mustCall(t, late, "greet", "x", nil); got != "v2 x" {
			t.Fatalf("late greet = %v", got)
		}
	})

	t.Run("an activation past the deadline aborts the reload", func(t *testing.T) {
		_, gate, _, err := run(t, false)
		var chordError *ChordError
		if !errors.As(err, &chordError) || chordError.Code != CodeAborted || !strings.Contains(chordError.Message, "activations in flight did not finish within 40 ms") {
			t.Fatalf("reload = %v", err)
		}
		gate.open()
	})
}

package subprocess

import (
	"fmt"
	"testing"
)

// The emission a quiesce notification names is the decimal id the Host wrote into the events.dispatch request (hostEventBus.newEmission) and the runtime echoes unchanged. A value that is not such a counter (a corrupt or hostile frame) is not held behind a report: the quiesce goes the unheld way and the emission is judged by its own bookkeeping, never parsed as another number.
func TestQuiesceNamingAnEmissionThatIsNotACounterIsNotHeldBehindAReport(t *testing.T) {
	var bus hostEventBus
	conn := &Conn{}
	conn.emissionsCreated = func() uint64 { return 5 }
	conn.noteReportRead()
	if !conn.reports.pending() {
		t.Fatal("the report was not counted as unapplied")
	}
	held := func(emission string) bool {
		env := quiesceFrame(emission)
		return bus.holdQuiesce(conn, env) && env.held
	}
	if !held(fmt.Sprint(5)) {
		t.Error("the quiesce of an emission created before the report was not held")
	}
	if held(fmt.Sprint(6)) {
		t.Error("the quiesce of an emission created after the report was held")
	}
	for _, emission := range []string{"", "x", "-1", "1.5", "0x5", " 5", "99999999999999999999999"} {
		if held(emission) {
			t.Errorf("the quiesce naming %q was held, want it left unheld", emission)
		}
	}
}

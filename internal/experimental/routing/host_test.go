package routing_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// latestHarness is upstream host.latestHarness(id); a missing harness fails the test.
func latestHarness(t *testing.T, host *routingtest.TestServerHost, id string) *routingtest.TestHarness {
	t.Helper()
	harness, err := host.LatestHarness(id)
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// waitAttachedClients waits until the harness's attachment count equals want; upstream expect.poll.
func waitAttachedClients(t *testing.T, harness *routingtest.TestHarness, want int) {
	t.Helper()
	pollUntil(t, fmt.Sprintf("attachedClients == %d", want), func() bool { return harness.AttachedClients() == want })
}

// openGate resumes a gated operation. Resolving twice is harmless, so cleanup can release a gate a failed barrier left closed.
func openGate(gate *routingtest.OpenGate) { gate.Release.Resolve(struct{}{}) }

// gateNextOpenSession is upstream host.gateNextOpenSession(). A barrier that fails with t.Fatal would otherwise leave the gated open parked, and the server's cleanup Close would wait on it until the test binary times out.
func gateNextOpenSession(t *testing.T, host *routingtest.TestServerHost) *routingtest.OpenGate {
	t.Helper()
	gate := host.GateNextOpenSession()
	t.Cleanup(func() { openGate(gate) })
	return gate
}

// gateNextServiceCall is upstream harness.gateNextServiceCall() with the same cleanup release.
func gateNextServiceCall(t *testing.T, harness *routingtest.TestHarness) *routingtest.OpenGate {
	t.Helper()
	gate := harness.GateNextServiceCall()
	t.Cleanup(func() { openGate(gate) })
	return gate
}

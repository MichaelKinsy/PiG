//go:build !race

package sdk

import "testing"

// toolRequestAllocBudget is the allocations one empty tool_call round trip may make on the in-process socket: the SDK's side (decode the request, arm it, dispatch, run the handler, encode the state and the response) plus the peer's decode of both answers. Lower it when a change removes allocations; raising it needs a reason.
const toolRequestAllocBudget = 70

// The budget depends on sync.Pool reuse, and the race detector drops pool items at random (86-88 allocations under -race), so it is checked only in non-race builds, as the tui allocation budgets are.
func TestToolRequestAllocBudget(t *testing.T) {
	call, stop := serveToolRequests(t, 0)
	defer stop()
	call()
	allocs := testing.AllocsPerRun(200, func() { call() })
	t.Logf("allocs per tool_call round trip: %v", allocs)
	switch {
	case allocs > toolRequestAllocBudget:
		t.Fatalf("a tool_call round trip makes %v allocations, budget %d", allocs, toolRequestAllocBudget)
	case allocs <= toolRequestAllocBudget-2:
		// The budget only ratchets down, as the host's connection budgets do (conn_framing_test.go TestConnAllocationBudgets).
		t.Fatalf("a tool_call round trip makes %v allocations, two or more under its budget of %d: lower the budget", allocs, toolRequestAllocBudget)
	}
}

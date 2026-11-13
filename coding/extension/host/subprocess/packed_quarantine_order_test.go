package subprocess

import (
	"slices"
	"testing"
	"time"
)

// TestQuarantineReportsMembersInLoadOrder: the end of a shared process is one sequence of notices, the same on every run: its members in
// load order, as a recovery reports them. Eight members whose load order is not their names' order, four times over.
func TestQuarantineReportsMembersInLoadOrder(t *testing.T) {
	loadOrder := []string{"m5", "m2", "m7", "m0", "m3", "m6", "m1", "m4"}
	for range 4 {
		h := NewHost(t.TempDir())
		process := &packedProcessState{key: "cell"}
		for rank, name := range loadOrder {
			h.exts[name] = &managedExt{config: ExtConfig{Name: name}, packedCellKey: "cell", packedProcess: process}
			h.loadOrder[name] = rank
		}
		var reported []string
		h.SetCrashHandler(func(name string, _ time.Duration, _ bool, _ string) { reported = append(reported, name) })
		h.quarantinePackedCell("cell", "packed process exited")
		if !slices.Equal(reported, loadOrder) {
			t.Fatalf("the members of a quarantined cell were reported as %v, want the load order %v", reported, loadOrder)
		}
	}
}

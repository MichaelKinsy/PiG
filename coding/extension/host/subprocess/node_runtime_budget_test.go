//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"testing"
)

// The reviewed archive budget is independent of machine speed and allocator scheduling. It allows headroom above the approximately 9.3 MB measured bundle; raising it requires new binary-size and startup evidence.
func TestNodeRuntimeArchiveSizeBudget(t *testing.T) {
	data, err := os.ReadFile("../../../../automation/perf/startup-budgets.json")
	if err != nil {
		t.Fatal(err)
	}
	var budget struct {
		RuntimeArchiveMaxBytes int `json:"runtime_archive_max_bytes"`
	}
	if err := json.Unmarshal(data, &budget); err != nil {
		t.Fatal(err)
	}
	if budget.RuntimeArchiveMaxBytes <= 0 {
		t.Fatal("missing archive budget")
	}
	if len(nodeRuntimeArchive) > budget.RuntimeArchiveMaxBytes {
		t.Fatalf("embedded runtime is %d bytes, exceeds reviewed budget %d; measure before changing the budget", len(nodeRuntimeArchive), budget.RuntimeArchiveMaxBytes)
	}
}

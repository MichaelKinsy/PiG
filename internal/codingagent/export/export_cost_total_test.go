package export

import (
	"encoding/json"
	"math"
	"os/exec"
	"testing"
)

// pi-mono #9980 (commit 83ca8020, packages/coding-agent/test/export-html-cost.test.ts): the exported header uses the recorded
// cost.total, which can differ from the catalog-estimated components, and falls back to the component sum for sessions written
// before cost.total was recorded.
func TestExportHTMLCostUsesRecordedTotals(t *testing.T) {
	entries := `[
		{"type":"message","message":{"role":"assistant","content":[],"usage":{"cost":{"input":0.1,"output":0.2,"cacheRead":0,"cacheWrite":0,"total":0.9}}}},
		{"type":"message","message":{"role":"assistant","content":[],"usage":{"cost":{"input":0.4,"output":0.1,"cacheRead":0,"cacheWrite":0}}}}
	]`
	out, err := exec.CommandContext(t.Context(), "node", "testdata/compute_stats_cost.cjs", "assets/template.js", entries).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var cost struct{ Input, Output, Total float64 }
	if err := json.Unmarshal(out, &cost); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for name, got := range map[string][2]float64{"input": {cost.Input, 0.5}, "output": {cost.Output, 0.3}, "total": {cost.Total, 1.4}} {
		if math.Abs(got[0]-got[1]) > 1e-9 {
			t.Errorf("cost.%s = %v, want %v (%s)", name, got[0], got[1], out)
		}
	}
}

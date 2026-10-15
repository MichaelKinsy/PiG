package codingagent

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// /cost lists the tools of one timing snapshot in the same order every time. Go randomizes map iteration, so the same snapshot printed in a different order on each call.
func TestFormatCostSummaryListsToolsInStableNameOrder(t *testing.T) {
	snap := agent.TimingSnapshot{ToolTotals: map[string]time.Duration{}, ToolCounts: map[string]int{}}
	var want []string
	for i := range 24 {
		name := fmt.Sprintf("tool-%02d", (i*7)%24)
		snap.ToolTotals[name] = time.Duration(i+1) * time.Millisecond
		snap.ToolCounts[name] = i + 1
	}
	for name := range snap.ToolTotals {
		want = append(want, name)
	}
	slices.Sort(want)
	for run := range 20 {
		var got []string
		for line := range strings.SplitSeq(formatCostSummary(snap, nil), "\n") {
			if name, _, ok := strings.Cut(strings.TrimPrefix(line, "    "), ":"); ok && strings.HasPrefix(line, "    tool-") {
				got = append(got, name)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("run %d: tool order %v, want %v", run, got, want)
		}
	}
}

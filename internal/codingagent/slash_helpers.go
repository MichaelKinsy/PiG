package codingagent

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// formatCostSummary builds the /cost output. Computes USD using the
// model's per-1M token rates (zero if unset) against the latest
// assistant Usage stats. Also prints turn/tool latency aggregates.
func formatCostSummary(snap agent.TimingSnapshot, model *ai.Model) string {
	var b strings.Builder
	b.WriteString("**Tokens**\n")
	fmt.Fprintf(&b, "  Input: %d\n", snap.TotalIn)
	fmt.Fprintf(&b, "  Output: %d\n", snap.TotalOut)
	if snap.CacheRead > 0 {
		fmt.Fprintf(&b, "  Cache Read: %d\n", snap.CacheRead)
	}
	if snap.CacheWrite > 0 {
		fmt.Fprintf(&b, "  Cache Write: %d\n", snap.CacheWrite)
	}
	fmt.Fprintf(&b, "  Total: %d\n", snap.TotalIn+snap.TotalOut)

	// Cost. Uses the per-turn accumulated cost (tier/cache-aware) rather than
	// recomputing from aggregate tokens. Mirrors upstream usage-totals.cost.
	if model != nil && (model.Capabilities.InputCostPer1M > 0 || model.Capabilities.OutputCostPer1M > 0) {
		b.WriteString("\n**Cost**\n")
		fmt.Fprintf(&b, "  Total: $%.4f\n", snap.Cost)
	}

	b.WriteString("\n**Timings**\n")
	if model != nil {
		fmt.Fprintf(&b, "  Model: %s\n", model.DisplayName)
	}
	fmt.Fprintf(&b, "  Wall-clock: %s\n", snap.Total.Round(time.Millisecond))
	fmt.Fprintf(&b, "  Turns: %d\n", len(snap.Turns))
	if len(snap.ToolTotals) > 0 {
		b.WriteString("  Tools:\n")
		for name, total := range snap.ToolTotals {
			fmt.Fprintf(&b, "    %s: %d call(s), %s\n", name, snap.ToolCounts[name], total.Round(time.Millisecond))
		}
	}
	return b.String()
}

package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// latencyStats are the percentiles of one open-loop run, in milliseconds.
type latencyStats struct {
	SDK      string  `json:"sdk"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	P50      float64 `json:"p50_ms"`
	P90      float64 `json:"p90_ms"`
	P99      float64 `json:"p99_ms"`
	P999     float64 `json:"p99_9_ms"`
	Max      float64 `json:"max_ms"`
}

// openLoop issues rate calls per second for duration on a fixed schedule: call
// i starts at start+i/rate whether or not earlier calls finished, and its
// latency runs from that intended start, so a stall shows in every call it
// delayed (no coordinated omission). Latency of a call that returned an error
// is not counted.
func openLoop(ctx context.Context, rate int, duration time.Duration, call func(context.Context) error) (latencies []time.Duration, failed int) {
	interval := time.Second / time.Duration(rate)
	total := int(duration / interval)
	results := make([]time.Duration, total)
	errs := make([]bool, total)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range total {
		intended := start.Add(time.Duration(i) * interval)
		if wait := time.Until(intended); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				total = i
			}
			if ctx.Err() != nil {
				break
			}
		}
		wg.Go(func() {
			if err := call(ctx); err != nil {
				errs[i] = true
				return
			}
			results[i] = time.Since(intended)
		})
	}
	wg.Wait()
	for i := range total {
		if errs[i] {
			failed++
			continue
		}
		latencies = append(latencies, results[i])
	}
	return latencies, failed
}

func percentile(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := min(len(sorted)-1, int(p*float64(len(sorted))))
	return float64(sorted[index]) / float64(time.Millisecond)
}

func summarize(sdk string, latencies []time.Duration, failed int) latencyStats {
	sorted := slices.Clone(latencies)
	slices.Sort(sorted)
	return latencyStats{
		SDK: sdk, Requests: len(latencies) + failed, Errors: failed,
		P50: percentile(sorted, 0.50), P90: percentile(sorted, 0.90), P99: percentile(sorted, 0.99), P999: percentile(sorted, 0.999),
		Max: percentile(sorted, 1),
	}
}

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

// Round trip budgets, in milliseconds, of an open-loop run of the scenario
// drivers' noop command at roundTripRate calls per second. Budgets only ratchet
// down.
//
// The p50 budget holds on every machine: a shared build host that is far past
// its core count still answers a noop command in about 2 ms. The p99 of such a
// host is dominated by scheduling stalls of the host, not the SDK, so by default
// only a gross p99 bound applies (an SDK that serializes or blocks requests
// misses it by orders of magnitude). On dedicated hardware PIG_EXT_RT_STRICT=1
// holds the tight per-SDK p99 budget and fails a run more than ratchetSlack under
// it until the budget is lowered, so a latency win is kept.
var (
	roundTripP50BudgetMS = map[sdkscenarios.SDK]float64{
		sdkscenarios.Go: 10, sdkscenarios.Node: 10, sdkscenarios.Python: 10, sdkscenarios.Rust: 10,
	}
	roundTripP99GrossMS  = 1000.0
	roundTripP99StrictMS = map[sdkscenarios.SDK]float64{
		sdkscenarios.Go: 25, sdkscenarios.Node: 25, sdkscenarios.Python: 25, sdkscenarios.Rust: 25,
	}
)

const (
	roundTripRate = 100
	// ratchetSlack is how far under its strict budget a measurement may fall before the budget must be lowered.
	ratchetSlack = 0.2
)

// TestExtensionRoundTripLatencyOpenLoop measures, for every SDK through the real
// host, the latency of one host-to-extension request (the command request a
// slash command makes) under a fixed-rate open-loop schedule, and holds the p99
// to its budgets. PIG_EXT_RT_RATE, PIG_EXT_RT_SECONDS and PIG_EXT_RT_REPORT (a
// directory for the JSON of each run) tune a longer or recorded run.
func TestExtensionRoundTripLatencyOpenLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	rate := envInt("PIG_EXT_RT_RATE", roundTripRate)
	seconds := envInt("PIG_EXT_RT_SECONDS", 4)
	strict := os.Getenv("PIG_EXT_RT_STRICT") == "1"
	spec := sdkscenarios.Spec{Commands: []sdkscenarios.Command{{Name: "noop", Description: "Return at once", Op: sdkscenarios.CommandNoop}}}
	for _, sdk := range sdkscenarios.SDKs {
		t.Run(string(sdk), func(t *testing.T) {
			rig := newScenarioRig(t, sdk, sdkscenarios.Scenario{Spec: spec})
			if len(rig.loadFailure) != 0 {
				t.Fatalf("load: %v", rig.loadFailure)
			}
			command := rig.primary().Commands["noop"]
			call := func(ctx context.Context) error { return command.Handler(ctx, "") }
			// Warm the runtime: the first calls pay JIT, import and allocation costs a steady state does not.
			if _, failed := openLoop(t.Context(), rate, time.Second, call); failed != 0 {
				t.Fatalf("%d warm-up calls failed", failed)
			}
			latencies, failed := openLoop(t.Context(), rate, time.Duration(seconds)*time.Second, call)
			stats := summarize(string(sdk), latencies, failed)
			t.Logf("%s: %d requests at %d/s: p50 %.2f ms, p90 %.2f ms, p99 %.2f ms, p99.9 %.2f ms, max %.2f ms, %d errors",
				sdk, stats.Requests, rate, stats.P50, stats.P90, stats.P99, stats.P999, stats.Max, stats.Errors)
			if dir := os.Getenv("PIG_EXT_RT_REPORT"); dir != "" {
				body, _ := json.MarshalIndent(stats, "", "  ")
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("roundtrip-%s.json", sdk)), body, 0o600); err != nil {
					t.Error(err)
				}
			}
			if failed != 0 {
				t.Errorf("%d of %d calls failed", failed, stats.Requests)
			}
			budget := roundTripP50BudgetMS[sdk]
			testenv.TimingGate(t, stats.P50 > budget, "p50 round trip %.2f ms exceeds the %.0f ms budget", stats.P50, budget)
			testenv.TimingGate(t, stats.P99 > roundTripP99GrossMS, "p99 round trip %.2f ms exceeds the gross %.0f ms bound: the SDK serializes or blocks requests", stats.P99, roundTripP99GrossMS)
			if !strict {
				return
			}
			budget = roundTripP99StrictMS[sdk]
			testenv.TimingGate(t, stats.P99 > budget, "p99 round trip %.2f ms exceeds the %.0f ms strict budget", stats.P99, budget)
			testenv.TimingGate(t, stats.P99 <= budget && stats.P99 < budget*(1-ratchetSlack), "p99 round trip %.2f ms is under %.0f%% of the %.0f ms strict budget: lower roundTripP99StrictMS[%s]", stats.P99, 100*(1-ratchetSlack), budget, sdk)
		})
	}
}

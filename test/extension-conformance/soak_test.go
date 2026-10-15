package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// The soak test drives random extension traffic through the real host into every
// SDK's extension process and holds leak and latency budgets. It is the short
// form of a long run: PIG_EXT_SOAK_SECONDS sets the duration per SDK (default
// 6, so the default run is a smoke test), PIG_EXT_SOAK_SEED replays a run and
// PIG_EXT_SOAK_WORKERS sets the concurrent callers (default 4). A failure names
// the seed. For an hours-long run:
//
//	PIG_EXT_SOAK_SECONDS=7200 go test ./test/extension-conformance -run TestExtensionSoak -timeout 0 -v
const (
	soakDefaultSeconds = 6
	soakDefaultWorkers = 4
	// soakMaxPayload is the largest echo text: a frame large enough to exercise chunked writes without pinning memory.
	soakMaxPayload = 64 * 1024
	// soakCallGrace bounds one call: a cancelled cooperative tool settles at once, and the abort grace is three seconds.
	soakCallGrace = 10 * time.Second
	// soakP99GrossMS is the p99 bound of every operation class under the soak's concurrent load on a shared host; it catches a serialized or blocked SDK, not a scheduling stall.
	soakP99GrossMS = 2000.0
	// soakGoroutineGrowth is how many goroutines the host may hold after the traffic beyond what it held before it.
	soakGoroutineGrowth = 8
	// soakHeapGrowthBytes is how much live host heap the traffic may leave behind.
	soakHeapGrowthBytes = 32 << 20
	// soakRSSGrowthBytes and soakRSSGrowthFraction bound the extension process memory: its resident size in the last quarter of the run may exceed its size in the third quarter by the larger of the two.
	soakRSSGrowthBytes    = 24 << 20
	soakRSSGrowthFraction = 0.35
)

type soakOp struct {
	name   string
	weight int
	run    func(ctx context.Context, w *soakWorker) error
}

// soakWorker is one concurrent caller with its own seeded generator.
type soakWorker struct {
	rig *scenarioRig
	rng *rand.Rand
	dir string
	n   int
}

func (w *soakWorker) command(name string) extension.RegisteredCommand {
	return w.rig.primary().Commands[name]
}

func (w *soakWorker) tool(name string) extension.RegisteredTool { return w.rig.primary().Tools[name] }

// result runs a command with a private result file and returns its text.
func (w *soakWorker) result(ctx context.Context, command string) (string, error) {
	w.n++
	path := filepath.Join(w.dir, fmt.Sprintf("result-%d", w.n))
	if err := w.command(command).Handler(ctx, path); err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	_ = os.Remove(path)
	return string(body), err
}

func soakText(rng *rand.Rand, size int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789 <>&\"\\é\u2028"
	runes := []rune(alphabet)
	var b strings.Builder
	for b.Len() < size {
		b.WriteRune(runes[rng.IntN(len(runes))])
	}
	return b.String()
}

func soakOps() []soakOp {
	toolText := func(r extension.AgentToolResult) string { return r.Text() }
	return []soakOp{
		{"noop", 30, func(ctx context.Context, w *soakWorker) error {
			return w.command("noop").Handler(ctx, "")
		}},
		{"echo", 25, func(ctx context.Context, w *soakWorker) error {
			size := w.rng.IntN(64)
			if w.rng.IntN(8) == 0 {
				size = w.rng.IntN(soakMaxPayload)
			}
			text := soakText(w.rng, size)
			params, _ := json.Marshal(map[string]string{"text": text})
			result, err := w.tool("echo").Definition.Execute(ctx, "call", params, nil)
			if err != nil {
				return err
			}
			if got := toolText(result); got != "echo:"+text {
				return fmt.Errorf("echo of %d bytes came back as %d bytes", len(text), len(got))
			}
			return nil
		}},
		{"query", 15, func(ctx context.Context, w *soakWorker) error {
			query := []string{"q-idle", "q-prompt", "q-tools"}[w.rng.IntN(3)]
			want := map[string]string{"q-idle": "false", "q-prompt": `"` + rigSystemPrompt + `"`}
			got, err := w.result(ctx, query)
			if err != nil {
				return err
			}
			if expected, ok := want[query]; ok && got != expected {
				return fmt.Errorf("%s answered %s, want %s", query, got, expected)
			}
			return nil
		}},
		{"tool-error", 10, func(ctx context.Context, w *soakWorker) error {
			_, err := w.tool("fail").Definition.Execute(ctx, "call", json.RawMessage(`{}`), nil)
			if err == nil {
				return errors.New("the failing tool returned no error")
			}
			if !strings.Contains(err.Error(), "boom") {
				return fmt.Errorf("the failing tool returned %w, want boom", err)
			}
			return nil
		}},
		{"cancel", 10, func(ctx context.Context, w *soakWorker) error {
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			time.AfterFunc(time.Duration(1+w.rng.IntN(15))*time.Millisecond, cancel)
			_, err := w.tool("cooperative").Definition.Execute(callCtx, "call", json.RawMessage(`{}`), nil)
			if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "cancel") && !strings.Contains(err.Error(), "aborted") {
				return fmt.Errorf("a cancelled cooperative tool returned %w", err)
			}
			return nil
		}},
		{"ping", 10, func(ctx context.Context, w *soakWorker) error {
			got, err := w.result(ctx, "ping")
			if err == nil && got != "pong" {
				err = fmt.Errorf("ping answered %q", got)
			}
			return err
		}},
	}
}

func soakSpec() sdkscenarios.Spec {
	spec := sdkscenarios.Spec{
		Tools: []sdkscenarios.Tool{
			{Name: "echo", Description: "Echo the text", Behavior: sdkscenarios.ToolEcho},
			{Name: "fail", Description: "Always fails", Behavior: sdkscenarios.ToolFail},
			{Name: "cooperative", Description: "Waits for abort", Behavior: sdkscenarios.ToolCooperative},
		},
		Commands: []sdkscenarios.Command{
			{Name: "noop", Description: "Return at once", Op: sdkscenarios.CommandNoop},
			{Name: "ping", Description: "Answer pong", Op: sdkscenarios.CommandPing},
		},
	}
	for name, arg := range map[string]string{"q-idle": "idle", "q-prompt": "system-prompt", "q-tools": "all-tools"} {
		spec.Commands = append(spec.Commands, sdkscenarios.Command{Name: name, Description: "Query " + arg, Op: sdkscenarios.CommandQuery, Arg: arg})
	}
	return spec
}

// descendantRSS is the resident memory, in bytes, of every process below this
// one, and their number. It is 0, 0 where /proc is unavailable.
func descendantRSS() (bytes int64, processes int) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, 0
	}
	parent := map[int]int{}
	rss := map[int]int64{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		// The command name is parenthesized and may hold spaces: fields after the last ")".
		_, rest, ok := strings.Cut(string(stat), ") ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 22 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		pages, _ := strconv.ParseInt(fields[21], 10, 64)
		parent[pid] = ppid
		rss[pid] = pages * int64(os.Getpagesize())
	}
	self := os.Getpid()
	for pid := range parent {
		for p, hops := pid, 0; hops < 64; hops++ {
			p = parent[p]
			if p == self {
				bytes += rss[pid]
				processes++
				break
			}
			if p == 0 {
				break
			}
		}
	}
	return bytes, processes
}

func mean(samples []int64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum int64
	for _, s := range samples {
		sum += s
	}
	return float64(sum) / float64(len(samples))
}

// TestExtensionSoak is described at the top of this file. Per SDK it asserts: no
// call fails or returns a wrong answer, the p99 of every operation class stays
// under soakP99GrossMS, the host's goroutines and live heap return to where they
// started, the extension process does not grow over the second half of the run, and after shutdown no
// descendant process remains.
func TestExtensionSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	seconds := envInt("PIG_EXT_SOAK_SECONDS", soakDefaultSeconds)
	workers := envInt("PIG_EXT_SOAK_WORKERS", soakDefaultWorkers)
	seed := uint64(envInt("PIG_EXT_SOAK_SEED", int(time.Now().UnixNano()&0xffffff)))
	t.Logf("soak seed %d (replay with PIG_EXT_SOAK_SEED=%d), %d s per SDK, %d workers", seed, seed, seconds, workers)
	ops := soakOps()
	totalWeight := 0
	for _, op := range ops {
		totalWeight += op.weight
	}
	for _, sdk := range sdkscenarios.SDKs {
		t.Run(string(sdk), func(t *testing.T) {
			rig := newScenarioRig(t, sdk, sdkscenarios.Scenario{Spec: soakSpec()})
			if len(rig.loadFailure) != 0 {
				t.Fatalf("load: %v", rig.loadFailure)
			}
			// Warm every operation once so the baseline holds the runtime's lazy state.
			warm := &soakWorker{rig: rig, rng: rand.New(rand.NewPCG(seed, 0)), dir: t.TempDir()}
			for _, op := range ops {
				if err := op.run(t.Context(), warm); err != nil {
					t.Fatalf("warm-up %s: %v", op.name, err)
				}
			}
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			goroutinesBefore := runtime.NumGoroutine()

			deadline := time.Now().Add(time.Duration(seconds) * time.Second)
			var mu sync.Mutex
			latencies := map[string][]time.Duration{}
			var failures []string
			var calls atomic.Int64
			var wg sync.WaitGroup
			for worker := range workers {
				wg.Go(func() {
					w := &soakWorker{rig: rig, rng: rand.New(rand.NewPCG(seed, uint64(worker+1))), dir: t.TempDir()}
					for time.Now().Before(deadline) {
						pick := w.rng.IntN(totalWeight)
						var op soakOp
						for _, candidate := range ops {
							if pick < candidate.weight {
								op = candidate
								break
							}
							pick -= candidate.weight
						}
						ctx, cancel := context.WithTimeout(t.Context(), soakCallGrace)
						start := time.Now()
						err := op.run(ctx, w)
						elapsed := time.Since(start)
						cancel()
						calls.Add(1)
						mu.Lock()
						latencies[op.name] = append(latencies[op.name], elapsed)
						if err != nil && len(failures) < 10 {
							failures = append(failures, fmt.Sprintf("worker %d %s: %v", worker, op.name, err))
						}
						mu.Unlock()
					}
				})
			}
			// Sample the extension process memory through the run.
			var samples []int64
			sampler := make(chan struct{})
			go func() {
				defer close(sampler)
				ticker := time.NewTicker(250 * time.Millisecond)
				defer ticker.Stop()
				for time.Now().Before(deadline) {
					<-ticker.C
					if rss, _ := descendantRSS(); rss > 0 {
						samples = append(samples, rss)
					}
				}
			}()
			wg.Wait()
			<-sampler

			if len(failures) != 0 {
				t.Errorf("failed calls (replay with PIG_EXT_SOAK_SEED=%d):\n  %s", seed, strings.Join(failures, "\n  "))
			}
			t.Logf("%d calls, %d workers", calls.Load(), workers)
			for _, op := range ops {
				sorted := slices.Clone(latencies[op.name])
				slices.Sort(sorted)
				p50, p99 := percentile(sorted, 0.50), percentile(sorted, 0.99)
				t.Logf("%-10s %6d calls: p50 %7.2f ms, p99 %8.2f ms", op.name, len(sorted), p50, p99)
				testenv.TimingGate(t, p99 > soakP99GrossMS, "%s p99 %.2f ms exceeds %.0f ms (seed %d)", op.name, p99, soakP99GrossMS, seed)
			}

			// The traffic is over: the host's goroutines and live heap must return to where they were.
			var goroutinesAfter int
			for range 50 {
				runtime.GC()
				if goroutinesAfter = runtime.NumGoroutine(); goroutinesAfter <= goroutinesBefore+soakGoroutineGrowth {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if goroutinesAfter > goroutinesBefore+soakGoroutineGrowth {
				t.Errorf("goroutines grew from %d to %d (seed %d)", goroutinesBefore, goroutinesAfter, seed)
			}
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			if growth := int64(after.HeapAlloc) - int64(before.HeapAlloc); growth > soakHeapGrowthBytes {
				t.Errorf("live host heap grew by %d bytes (limit %d, seed %d)", growth, soakHeapGrowthBytes, seed)
			}
			var series []string
			for i := 0; i < len(samples); i += max(1, len(samples)/12) {
				series = append(series, fmt.Sprintf("%.0f", float64(samples[i])/(1<<20)))
			}
			t.Logf("extension process resident MiB over the run: %s", strings.Join(series, " "))
			if len(samples) >= 8 {
				// A runtime's heap ramps up while it warms (Node's takes minutes), so the slope is read over the second half: the third quarter of the samples against the last.
				quarter := len(samples) / 4
				mid, late := mean(samples[2*quarter:3*quarter]), mean(samples[len(samples)-quarter:])
				limit := max(float64(soakRSSGrowthBytes), mid*soakRSSGrowthFraction)
				t.Logf("extension process resident size: %.1f MiB in the third quarter, %.1f MiB in the last", mid/(1<<20), late/(1<<20))
				if late-mid > limit {
					t.Errorf("extension process grew from %.1f to %.1f MiB over the second half (limit +%.1f MiB, seed %d)", mid/(1<<20), late/(1<<20), limit/(1<<20), seed)
				}
			}

			rig.host.Shutdown("soak done")
			var leftover int
			for range 100 {
				if _, leftover = descendantRSS(); leftover == 0 {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if leftover != 0 {
				t.Errorf("%d descendant process(es) remain after shutdown (seed %d)", leftover, seed)
			}
		})
	}
}

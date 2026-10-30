package extensionconformance

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// soakSampleInterval is how often a soak run samples the extension process.
const soakSampleInterval = 250 * time.Millisecond

// soakMemory watches the memory of the extension process through a soak run and judges whether it grew.
type soakMemory struct {
	// samples is the resident size, in bytes, of the extension process at each sample.
	samples []int64
}

// watch samples the extension process until deadline.
func (m *soakMemory) watch(deadline time.Time) {
	ticker := time.NewTicker(soakSampleInterval)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		<-ticker.C
		if rss, _ := descendantRSS(); rss > 0 {
			m.samples = append(m.samples, rss)
		}
	}
}

// verdict logs what the run observed and returns the failure text when the extension process grew over the second half of the run, or "".
func (m *soakMemory) verdict(t *testing.T, seed uint64) string {
	t.Helper()
	var series []string
	for i := 0; i < len(m.samples); i += max(1, len(m.samples)/12) {
		series = append(series, fmt.Sprintf("%.0f", float64(m.samples[i])/(1<<20)))
	}
	t.Logf("extension process resident MiB over the run: %s", strings.Join(series, " "))
	if len(m.samples) < 8 {
		return ""
	}
	// A runtime's heap ramps up while it warms (Node's takes minutes), so the slope is read over the second half: the third quarter of the samples against the last.
	quarter := len(m.samples) / 4
	mid, late := mean(m.samples[2*quarter:3*quarter]), mean(m.samples[len(m.samples)-quarter:])
	limit := max(float64(soakRSSGrowthBytes), mid*soakRSSGrowthFraction)
	t.Logf("extension process resident size: %.1f MiB in the third quarter, %.1f MiB in the last", mid/(1<<20), late/(1<<20))
	if late-mid > limit {
		return fmt.Sprintf("extension process grew from %.1f to %.1f MiB over the second half (limit +%.1f MiB, seed %d)", mid/(1<<20), late/(1<<20), limit/(1<<20), seed)
	}
	return ""
}

// soakRetainedBytes is what the leaking extension of the gate's own test keeps of every echo call (the Node driver's ToolRetain), a quarter of the soak's calls. It is sized for a host that is starved of CPU: a run of 150 calls still leaves more than the gate's limit behind, and a run of 5000 calls under load leaves 5 to 15 MB, far below the tens of MB by which the resident size of a Node process moves with nothing retained.
const soakRetainedBytes = 64 << 10

// The soak's extension-memory gate must see an extension that keeps memory of every call: the Node driver's retaining echo tool holds soakRetainedBytes of every call it serves, and a soak run of the Node SDK over it must report growth.
func TestExtensionSoakMemoryGateSeesAnExtensionThatRetainsPerCall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	spec := soakSpec()
	for i := range spec.Tools {
		if spec.Tools[i].Name == "echo" {
			spec.Tools[i].Behavior = sdkscenarios.ToolRetain
		}
	}
	rig := newScenarioRig(t, sdkscenarios.Node, sdkscenarios.Scenario{Spec: spec})
	if len(rig.loadFailure) != 0 {
		t.Fatalf("load: %v", rig.loadFailure)
	}
	ops := soakOps()
	totalWeight := 0
	for _, op := range ops {
		totalWeight += op.weight
	}
	const seed = 1
	// The same warm-up and traffic as the soak, and the same watch of the extension process.
	warm := &soakWorker{rig: rig, rng: rand.New(rand.NewPCG(seed, 0)), dir: t.TempDir()}
	for _, op := range ops {
		if err := op.run(t.Context(), warm); err != nil {
			t.Fatalf("warm-up %s: %v", op.name, err)
		}
	}
	deadline := time.Now().Add(soakDefaultSeconds * time.Second)
	var memory soakMemory
	var wg sync.WaitGroup
	var calls atomic.Int64
	wg.Go(func() { memory.watch(deadline) })
	for worker := range soakDefaultWorkers {
		wg.Go(func() {
			w := &soakWorker{rig: rig, rng: rand.New(rand.NewPCG(seed, uint64(worker+1))), dir: t.TempDir()}
			for time.Now().Before(deadline) {
				ctx, cancel := context.WithTimeout(t.Context(), soakCallGrace)
				op := soakPick(w.rng, ops, totalWeight)
				if err := op.run(ctx, w); err != nil {
					t.Errorf("%s: %v", op.name, err)
				}
				cancel()
				calls.Add(1)
			}
		})
	}
	wg.Wait()
	t.Logf("%d calls, %d bytes retained by each echo", calls.Load(), soakRetainedBytes)
	if growth := memory.verdict(t, seed); growth == "" {
		t.Fatalf("the soak's extension-memory gate saw nothing grow in an extension that kept %d bytes of each echo call, a quarter of its %d calls", soakRetainedBytes, calls.Load())
	}
}

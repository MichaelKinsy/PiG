package extensionconformance

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// soakSampleInterval is how often a soak run samples the extension process.
const soakSampleInterval = 250 * time.Millisecond

// soakMemory watches the memory of the extension process through a soak run and judges whether it grew over the second half.
//
// What the gate reads depends on what the SDK's process can tell. A Node extension reports its live memory after a full collection (the live-memory command, sdkscenarios.CommandLiveMemory), at the middle of the run and when the traffic has ended: that number moves only with what the extension keeps, so a leak of a few hundred bytes per call shows and the schedule of V8's collector does not. The resident size of the same process cannot be read that way: V8 keeps garbage until its heuristics collect it and does not give the pages back, so over the second half of a 6 s run the process grew by 0 to 29 MiB with nothing retained, and once in 4000 loaded runs by 46 MiB, past the limit. The other SDKs' processes are read by resident size, which their runtimes keep flat.
type soakMemory struct {
	// live asks the extension for its live memory; nil for an SDK whose driver does not report it.
	live *soakWorker
	// samples is the resident size, in bytes, of the extension process at each sample.
	samples []int64
	// midLive is the live memory at the middle of the run, and midOK whether the run got that far.
	midLive int64
	midOK   bool
	midErr  error
	// marked is true once the middle reading is taken, for a caller that counts what the extension served after it.
	marked atomic.Bool
}

// newSoakMemory watches rig's primary extension; dir is where its live-memory answers are written.
func newSoakMemory(rig *scenarioRig, dir string) *soakMemory {
	m := &soakMemory{}
	if _, ok := rig.primary().Commands[soakLiveMemoryCommand]; ok {
		m.live = &soakWorker{rig: rig, rng: rand.New(rand.NewPCG(0, 0)), dir: dir}
	}
	return m
}

// liveMemory is the bytes of live memory the extension holds after a full collection.
func (m *soakMemory) liveMemory(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, soakCallGrace)
	defer cancel()
	text, err := m.live.result(ctx, soakLiveMemoryCommand)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}

// watch samples the extension process until deadline, and reads its live memory once, at the middle of the run.
func (m *soakMemory) watch(ctx context.Context, deadline time.Time) {
	middle := time.Now().Add(time.Until(deadline) / 2)
	ticker := time.NewTicker(soakSampleInterval)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		<-ticker.C
		if rss, _ := descendantRSS(); rss > 0 {
			m.samples = append(m.samples, rss)
		}
		if m.live != nil && !m.midOK && m.midErr == nil && !time.Now().Before(middle) {
			if m.midLive, m.midErr = m.liveMemory(ctx); m.midErr == nil {
				m.midOK = true
				m.marked.Store(true)
			}
		}
	}
}

// verdict logs what the run observed and returns the failure text when the extension process grew over the second half of the run, or "".
func (m *soakMemory) verdict(ctx context.Context, t *testing.T, seed uint64) string {
	t.Helper()
	var series []string
	for i := 0; i < len(m.samples); i += max(1, len(m.samples)/12) {
		series = append(series, fmt.Sprintf("%.0f", float64(m.samples[i])/(1<<20)))
	}
	t.Logf("extension process resident MiB over the run: %s", strings.Join(series, " "))
	// A runtime's heap ramps up while it warms, so the slope is read over the second half: the third quarter of the samples against the last.
	var grewResident string
	if len(m.samples) >= 8 {
		quarter := len(m.samples) / 4
		mid, late := mean(m.samples[2*quarter:3*quarter]), mean(m.samples[len(m.samples)-quarter:])
		limit := max(float64(soakRSSGrowthBytes), mid*soakRSSGrowthFraction)
		t.Logf("extension process resident size: %.1f MiB in the third quarter, %.1f MiB in the last", mid/(1<<20), late/(1<<20))
		if late-mid > limit {
			grewResident = fmt.Sprintf("extension process grew from %.1f to %.1f MiB over the second half (limit +%.1f MiB, seed %d)", mid/(1<<20), late/(1<<20), limit/(1<<20), seed)
		}
	}
	if m.live != nil {
		return m.liveVerdict(ctx, t, seed)
	}
	return grewResident
}

// liveVerdict judges the live memory at the end of the traffic against the middle of the run.
func (m *soakMemory) liveVerdict(ctx context.Context, t *testing.T, seed uint64) string {
	t.Helper()
	if m.midErr != nil {
		return fmt.Sprintf("reading the extension's live memory at the middle of the run: %v (seed %d)", m.midErr, seed)
	}
	if !m.midOK {
		return ""
	}
	end, err := m.liveMemory(ctx)
	if err != nil {
		return fmt.Sprintf("reading the extension's live memory at the end of the run: %v (seed %d)", err, seed)
	}
	growth := end - m.midLive
	t.Logf("extension process live memory after a full collection: %.2f MiB in the middle of the run, %.2f MiB at the end", float64(m.midLive)/(1<<20), float64(end)/(1<<20))
	if growth > soakLiveGrowthBytes {
		return fmt.Sprintf("extension process live memory grew from %.2f to %.2f MiB over the second half (limit +%.2f MiB, seed %d)", float64(m.midLive)/(1<<20), float64(end)/(1<<20), float64(soakLiveGrowthBytes)/(1<<20), seed)
	}
	return ""
}

// soakRetainedBytes is what the leaking extension of the gate's own test keeps of every echo call (the Node driver's ToolRetain), a quarter of the soak's calls.
const soakRetainedBytes = 16 << 10

// soakEchoesAfterMark is how many retaining echo calls the gate's own test serves after the middle reading of the extension's live memory. They leave 4 MiB behind, four times the gate's limit, however slowly the host serves them: the test waits for them (at most soakControlLimit) instead of leaving them to the speed of the host, where a run of 150 calls in 6 s leaves less than the limit and is not the gate's failure.
const soakEchoesAfterMark = 256

// soakControlLimit bounds the run of the gate's own test.
const soakControlLimit = 3 * time.Minute

// The soak's extension-memory gate must see an extension that keeps memory of every call: the Node driver's retaining echo tool holds soakRetainedBytes of every call it serves, and a soak run of the Node SDK over it must report growth.
func TestExtensionSoakMemoryGateSeesAnExtensionThatRetainsPerCall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	spec := soakSpecFor(sdkscenarios.Node)
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
	giveUp := time.Now().Add(soakControlLimit)
	memory := newSoakMemory(rig, t.TempDir())
	var wg sync.WaitGroup
	var calls, echoesAfterMark atomic.Int64
	wg.Go(func() { memory.watch(t.Context(), deadline) })
	for worker := range soakDefaultWorkers {
		wg.Go(func() {
			w := &soakWorker{rig: rig, rng: rand.New(rand.NewPCG(seed, uint64(worker+1))), dir: t.TempDir()}
			for time.Now().Before(deadline) || (echoesAfterMark.Load() < soakEchoesAfterMark && time.Now().Before(giveUp)) {
				ctx, cancel := context.WithTimeout(t.Context(), soakCallGrace)
				op := soakPick(w.rng, ops, totalWeight)
				if err := op.run(ctx, w); err != nil {
					t.Errorf("%s: %v", op.name, err)
				}
				cancel()
				calls.Add(1)
				if op.name == "echo" && memory.marked.Load() {
					echoesAfterMark.Add(1)
				}
			}
		})
	}
	wg.Wait()
	t.Logf("%d calls, %d echo calls after the middle reading, %d bytes retained by each echo", calls.Load(), echoesAfterMark.Load(), soakRetainedBytes)
	if echoesAfterMark.Load() < soakEchoesAfterMark {
		t.Fatalf("the host served %d echo calls after the middle reading of the extension's live memory in %v, want %d", echoesAfterMark.Load(), soakControlLimit, soakEchoesAfterMark)
	}
	if growth := memory.verdict(t.Context(), t, seed); growth == "" {
		t.Fatalf("the soak's extension-memory gate saw nothing grow in an extension that kept %d bytes of each of %d echo calls after the middle of the run", soakRetainedBytes, echoesAfterMark.Load())
	}
}

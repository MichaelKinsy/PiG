package harness

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// liveScalingMallocs runs one turn whose tool round calls "echo" k times in parallel and returns the heap allocations
// from the moment all k calls are running to the turn's end: the k completions, each a commit of its slot to pi.live
// (slot done), and the closing turn. A per-commit cost that grows with the size of pi.live shows as more allocations per
// call at a larger k.
//
// The count starts at a barrier that every call waits at, not when the first call starts. The process-wide count is the
// only one the runtime offers, and the first call starts at a point of the round that the scheduler decides: how many of
// the other calls' slot-running commits are already behind it changes the round's work inside the window by 40% at k=64
// (1324 allocations per call in CI against 2116 stand-alone), and the same round at k=256 does not move that much, so
// the ratio between the two sizes was a measure of the host's timing: a first call that starts 50 ms late at k=64 alone
// gives 1328 and 2646 (1.99x, CI's 1.98x). After the barrier every slot is running and every
// call is released at once, whatever the order the calls started in or how late each started; the count is then 1313 to
// 1330 allocations per call at k=64 and 1548 to 1557 at k=256 whether the host is idle, busy, has four CPUs or has one, and
// whether other tests ran before it. The answer holding the k calls and the slot-running commits are not measured: the
// work of both overlaps and moves with the scheduler (2100 to 2900 allocations per call at k=64, 4700 to 6600 at k=256).
func liveScalingMallocs(t *testing.T, k int) uint64 {
	setup := chatSetup(t)
	budget := testbudget.Wait(t)
	var before runtime.MemStats
	var started atomic.Int32
	allStarted := make(chan struct{})
	addTool(t, setup.Registry, tlTool("echo", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if int(started.Add(1)) == k {
			runtime.ReadMemStats(&before)
			close(allStarted)
		}
		select {
		case <-allStarted:
			return durable.ToolExecutionResult{}, nil
		case <-ctx.Done():
			return durable.ToolExecutionResult{}, ctx.Err()
		case <-time.After(budget):
			err := fmt.Errorf("%d of %d parallel calls started within %v; the count needs them all running at once", started.Load(), k, budget)
			t.Error(err)
			return durable.ToolExecutionResult{}, err
		}
	}))
	calls := make([]tlCall, k)
	for i := range calls {
		calls[i] = tlCall{"echo", map[string]any{"text": "x"}, fmt.Sprintf("c%d", i)}
	}
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(calls...), tlDone()})
	_, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := ownSubmit(t, root, ownInput("go"))
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.Mallocs - before.Mallocs
}

// A tool round of k parallel calls commits per call; the cost of each commit must not grow with k, which it did while
// every commit decoded the whole pi.live document to evaluate its checkpoint predicate.
func TestParallelToolCallsCostDoesNotGrowWithTheRoundSize(t *testing.T) {
	small, large := liveScalingMallocs(t, 64), liveScalingMallocs(t, 256)
	perSmall, perLarge := float64(small)/64, float64(large)/256
	t.Logf("mallocs per call: k=64 %.0f, k=256 %.0f (%.2fx)", perSmall, perLarge, perLarge/perSmall)
	if perLarge > perSmall*1.3*1.3 {
		t.Fatalf("allocations per call grew %.2fx from k=64 to k=256, want at most 1.69x", perLarge/perSmall)
	}
}

// liveScalingNestedMallocs is liveScalingMallocs for one tool that makes k nested calls in parallel.
func liveScalingNestedMallocs(t *testing.T, k int) uint64 {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		calls := make([]func(), k)
		for i := range calls {
			calls[i] = func() { _, _ = api.ExecuteTool(ctx, "echo", map[string]any{"text": "x"}) }
		}
		promiseAll(calls...)
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	_, root := openChat(t, storage.NewMemoryStorage(), setup)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	submission := ownSubmit(t, root, ownInput("go"))
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	return after.Mallocs - before.Mallocs
}

// A tool making k nested calls commits to pi.live and its own task record per call; neither may cost more per call as k
// grows.
func TestNestedToolCallsCostDoesNotGrowWithTheCallCount(t *testing.T) {
	small, large := liveScalingNestedMallocs(t, 64), liveScalingNestedMallocs(t, 256)
	perSmall, perLarge := float64(small)/64, float64(large)/256
	t.Logf("mallocs per nested call: k=64 %.0f, k=256 %.0f (%.2fx)", perSmall, perLarge, perLarge/perSmall)
	if perLarge > perSmall*1.3*1.3 {
		t.Fatalf("allocations per nested call grew %.2fx from k=64 to k=256, want at most 1.69x", perLarge/perSmall)
	}
}

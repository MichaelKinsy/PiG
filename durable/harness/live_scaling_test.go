package harness

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// liveScalingMallocs runs one turn whose tool round calls "echo" k times in parallel and returns the heap
// allocations from the first call's start to the turn's end. Each call commits to pi.live (slot running, slot done), so a per-commit cost that
// grows with the size of pi.live shows as more allocations per call at a larger k.
func liveScalingMallocs(t *testing.T, k int) uint64 {
	setup := chatSetup(t)
	// The model's streamed answer and the closing turn are not what is measured, so the count starts when the first call
	// starts, after the answer holding all k calls is complete.
	var before runtime.MemStats
	var started sync.Once
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		started.Do(func() { runtime.ReadMemStats(&before) })
		return durable.ToolExecutionResult{}, nil
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

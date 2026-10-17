//go:build !race && unix

package coding

import (
	"runtime"
	"testing"
)

// A coroutine switch per streamed delta is executor overhead, not order: Pi runs the provider, its decoder generators and the consumer on one thread, where each await is a microtask, not a context switch. A pausing body runs, on its own stack, the reactions its driver would run before resuming it (ai continuationTurn.settle), and a provider's unobservable awaits are made in one row at its next observable step (deferContinuation), so the switches left are the hand-offs between the provider's body and the consumer's: four per delta, two each way through the serving goroutine. A body read that waits for the socket adds two pairs (the source's waitBody goroutine resumes the read coroutine and it yields back), as often as the machine's socket is empty: countTraceWakes counts those apart (waitSwitches, switches with the goroutine running observedBodyReadiness.waitBody), so the four is exact for the HTTP provider too and the pairs are bounded by one wait per delta. At b02c2ceabc the text pipeline switched 9.33 times per delta and the openai-completions one 23.96 (E17: 12 pauses, 7 in the provider and 5 in the consumer). The count is the marginal number between 1k and 2k deltas at GOMAXPROCS=1, with the lw-guards definition of a switch (countTraceWakes).
func TestStreamSwitchesPerDelta(t *testing.T) {
	procs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(procs) })
	const n = 1_000
	for _, mode := range []struct {
		name     string
		provider pipelineProvider
		ceiling  float64
	}{{"text", pipelineFaux, 4}, {"openai_text", pipelineOpenAI, 4}} {
		t.Run(mode.name, func(t *testing.T) {
			small := pipelineWakes(t, mode.provider, n, 0)
			large := pipelineWakes(t, mode.provider, 2*n, 0)
			switches := float64(large.switches-small.switches) / n
			waits := float64(large.waitSwitches-small.waitSwitches) / n
			t.Logf("%.2f coroutine switches, %.2f more in body reads that waited for the socket, %.2f wakes per delta", switches, waits, float64(large.wakes-small.wakes)/n)
			if switches > mode.ceiling {
				t.Errorf("%.2f coroutine switches per delta, want at most %v", switches, mode.ceiling)
			}
			// A wait costs four switches (the read's resume and pending yield, waitBody's resume and the yield back), and a delta's bytes cannot need more than one wait.
			if waits > 4 {
				t.Errorf("%.2f socket-wait switches per delta, want at most 4", waits)
			}
		})
	}
}

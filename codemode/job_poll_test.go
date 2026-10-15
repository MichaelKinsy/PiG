package codemode

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// Promise jobs run one at a time and the interrupt is polled between batches of jobsPerPoll, so a script that only
// chains promise jobs, such as `while (true) await null`, stops at the same points as a synchronous loop
// (packages/codemode/src/runtime/vm.ts, drain and JOBS_PER_POLL; CHANGELOG "Changed" of 748b351f).

// jobChain starts a chain of promise jobs on machine: one synchronous call, then jobs more, each queued by the last.
// `n` counts the calls.
func jobChain(t *testing.T, machine *vm, jobs int) {
	t.Helper()
	handle, x := machine.eval(fmt.Sprintf("globalThis.n = 0; (function f() { if (++n <= %d) Promise.resolve().then(f); })()", jobs), "chain.js")
	if x != nil {
		t.Fatalf("starting the chain: %s", x)
	}
	machine.free(handle)
}

func chainCalls(t *testing.T, machine *vm) int {
	t.Helper()
	handle, x := machine.eval("n", "n.js")
	if x != nil {
		t.Fatalf("reading n: %s", x)
	}
	defer machine.free(handle)
	return int(machine.toNumber(handle))
}

func TestExecutePendingJobsPollsTheInterruptEveryFiftyJobs(t *testing.T) {
	eng, err := newEngine(t.Context(), QuickJSWasm(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.close(t.Context()) })

	for _, tc := range []struct {
		name string
		// jobs is the length of the chain after its synchronous first call.
		jobs        int
		interruptAt int
		wantPolls   int
		wantCalls   int
		wantFailure string
	}{
		{name: "fewer jobs than a batch never poll", jobs: 49, interruptAt: 1, wantPolls: 0, wantCalls: 50},
		{name: "the poll follows the fiftieth job even when the queue is then empty", jobs: 50, interruptAt: 1, wantPolls: 1, wantCalls: 51, wantFailure: "Error: Execution interrupted"},
		{name: "a drain that is not interrupted polls once per batch", jobs: 120, interruptAt: 0, wantPolls: 2, wantCalls: 121},
		{name: "an interrupt stops the drain at the poll that sees it", jobs: 1000, interruptAt: 3, wantPolls: 3, wantCalls: 151, wantFailure: "Error: Execution interrupted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var interrupt atomic.Bool
			machine, err := eng.newVM(t.Context(), vmOptions{shouldInterrupt: interrupt.Load})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(machine.close)
			jobChain(t, machine, tc.jobs)

			polls := 0
			x := machine.executePendingJobs(func() bool {
				polls++
				return tc.interruptAt != 0 && polls == tc.interruptAt
			})
			if tc.wantFailure == "" {
				if x != nil {
					t.Fatalf("executePendingJobs failed: %s", x)
				}
			} else if x == nil || x.String() != tc.wantFailure {
				t.Fatalf("executePendingJobs = %v, want %q", x, tc.wantFailure)
			}
			if polls != tc.wantPolls {
				t.Errorf("polled %d times, want %d", polls, tc.wantPolls)
			}
			if got := chainCalls(t, machine); got != tc.wantCalls {
				t.Errorf("the chain ran %d calls, want %d", got, tc.wantCalls)
			}
		})
	}
}

// A job fails only for an uncatchable error, which is QuickJS's own interrupt here; vm.ts reports it as a plain Error
// that carries no detail of the job.
func TestExecutePendingJobsReportsAJobThatFailsUncatchably(t *testing.T) {
	eng, err := newEngine(t.Context(), QuickJSWasm(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.close(t.Context()) })
	var interrupt atomic.Bool
	machine, err := eng.newVM(t.Context(), vmOptions{shouldInterrupt: interrupt.Load})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.close)
	jobChain(t, machine, 1_000_000)

	interrupt.Store(true)
	polls := 0
	x := machine.executePendingJobs(func() bool {
		polls++
		return false
	})
	if x == nil || x.String() != "Error: Promise job failed" {
		t.Fatalf("executePendingJobs = %v after %d polls, want %q", x, polls, "Error: Promise job failed")
	}
}

// The interrupt flag stops a job chain through the drain's own poll. The test stands for a script that never reaches
// QuickJS's opcode-counting poll by making that poll report nothing, so only the poll between jobs can end the script
// before the grace period closes the instance.
func TestAMicrotaskSpinEndsAtTheJobPollNotTheHardStop(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	ignoreInterrupt.Store(true)
	t.Cleanup(func() { ignoreInterrupt.Store(false) })
	before := hardStops.Load()
	started := time.Now()
	result, err := sandbox.Execute(t.Context(), "while (true) await null", ExecuteOptions{TimeoutMs: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error.Kind != ErrorTimeout {
		t.Fatalf("result = %+v (%+v), want a timeout", result, result.Error)
	}
	// Execute does not wait for the VM, so the VM's exit is what shows the poll stopped it.
	exitsWithin(t, 4*terminationGrace, "microtask spin")
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond+terminationGrace {
		t.Errorf("the VM exited after %v, not before the %v grace ran out", elapsed, terminationGrace)
	}
	if n := hardStops.Load() - before; n != 0 {
		t.Errorf("%d hard stops: the drain did not poll the interrupt flag between jobs", n)
	}
}

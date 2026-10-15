//go:build !tinygo

package codemode

import (
	"context"
	"math"
	"testing"
	"time"
)

// Ports packages/codemode/test/worker-lifetime.test.ts ("settles %s without waiting for worker exit"): a script inside a
// call that never reaches QuickJS's interrupt poll ends its execution as soon as the stop is requested; the grace period
// still halts the instance, so the VM exits within it. The test stands for that script by making the poll report nothing.
func TestCancellationSettlesWithoutWaitingForTheVMToExit(t *testing.T) {
	for _, how := range []string{"abort", "timeout", "close"} {
		t.Run(how, func(t *testing.T) {
			sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: math.Inf(1)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sandbox.Close() })
			if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
				t.Fatalf("warm-up: %+v", r.Error)
			}
			exitsWithin(t, terminationGrace, "warm-up")
			ignoreInterrupt.Store(true)
			t.Cleanup(func() { ignoreInterrupt.Store(false) })
			hardStopsBefore := hardStops.Load()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			options := ExecuteOptions{}
			if how == "timeout" {
				options.TimeoutMs = 100
			}
			type outcome struct {
				result Result
				at     time.Time
			}
			done := make(chan outcome, 1)
			go func() {
				r, _ := sandbox.Execute(ctx, "while (true) {}", options)
				done <- outcome{r, time.Now()}
			}()
			for liveVMs.Load() == 0 {
				time.Sleep(time.Millisecond)
			}
			stopped := time.Now()
			switch how {
			case "abort":
				cancel()
			case "timeout":
				stopped = stopped.Add(100 * time.Millisecond)
			case "close":
				go func() { _ = sandbox.Close() }()
			}
			got := <-done
			wantKind := ErrorAborted
			if how == "timeout" {
				wantKind = ErrorTimeout
			}
			if got.result.OK || got.result.Error.Kind != wantKind {
				t.Fatalf("result = %+v (%+v), want %s", got.result, got.result.Error, wantKind)
			}
			if waited := got.at.Sub(stopped); waited >= terminationGrace/2 {
				t.Fatalf("Execute returned %v after the stop: it waited for the VM to exit", waited)
			}
			if hardStops.Load() != hardStopsBefore {
				t.Fatalf("the instance was halted before Execute returned")
			}
			// The VM still runs; the grace period halts it.
			exitsWithin(t, 4*terminationGrace, how)
			if hardStops.Load() != hardStopsBefore+1 {
				t.Fatalf("hard stops %d -> %d: the instance was not halted under the script", hardStopsBefore, hardStops.Load())
			}
		})
	}
}

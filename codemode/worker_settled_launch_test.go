package codemode

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Ports packages/coding-agent/test/codemode-worker-admission.test.ts "starts no worker for a launch that resumes after its result timed
// out" (#10748, #11184) to the launcher. Pi's host settles a script on its deadline while the module still loads and later takes no worker
// for the launch. PiG's deadline starts once the module is loaded, so the abort of the caller's context is what settles an execution
// during the load; the launch that resumes afterwards must not ask for a worker.
func TestLaunchResumingAfterTheResultSettledTakesNoWorker(t *testing.T) {
	loading := make(chan struct{})
	loadStarted := make(chan struct{})
	var acquired atomic.Int32
	sandbox, err := NewSandbox(SandboxOptions{
		Wasm: func() ([]byte, error) {
			close(loadStarted)
			<-loading
			return QuickJSWasm(), nil
		},
		AcquireWorker: func() (func(), error) {
			acquired.Add(1)
			return func() {}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := sandbox.Execute(ctx, "while (true) {}", ExecuteOptions{})
		done <- outcome{result, err}
	}()
	<-loadStarted
	cancel()
	// The abort settles the execution on its own goroutine; wait until it has before the load resumes.
	deadline := time.Now().Add(5 * time.Second)
	for {
		sandbox.mu.Lock()
		settled := false
		for e := range sandbox.running {
			settled = e.isFinished()
		}
		sandbox.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the abort did not settle the execution")
		}
		time.Sleep(time.Millisecond)
	}
	close(loading)
	got := <-done
	if got.err != nil || got.result.OK || got.result.Error == nil || got.result.Error.Kind != ErrorAborted {
		t.Fatalf("result = %+v (%+v), %v; want the abort", got.result, got.result.Error, got.err)
	}
	if n := acquired.Load(); n != 0 {
		t.Fatalf("a launch resuming after the result settled took %d workers, want 0", n)
	}
}

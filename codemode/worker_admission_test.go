//go:build !tinygo

package codemode

import (
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// Ports packages/codemode/test/worker-lifetime.test.ts (admission and exit reporting through acquireWorker and
// onWorkerStarted).

func TestDeniedAdmissionStartsNoWorker(t *testing.T) {
	var acquired, started atomic.Int32
	sandbox, err := NewSandbox(SandboxOptions{
		AcquireWorker: func() (func(), error) {
			acquired.Add(1)
			return nil, errors.New("worker limit reached")
		},
		OnWorkerStarted: func(<-chan struct{}) error { started.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	r, err := sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Error.Kind != ErrorSandbox || r.Error.Message != "worker limit reached" {
		t.Fatalf("result = %+v (%+v), want the admission error as a sandbox error", r, r.Error)
	}
	if acquired.Load() != 1 || started.Load() != 0 || liveVMs.Load() != 0 {
		t.Fatalf("acquired %d, started %d, live VMs %d; want 1, 0, 0", acquired.Load(), started.Load(), liveVMs.Load())
	}
}

func TestAFailedModuleLoadAcquiresNoAdmission(t *testing.T) {
	var acquired atomic.Int32
	sandbox, err := NewSandbox(SandboxOptions{
		Wasm:          func() ([]byte, error) { return nil, errors.New("wasm unavailable") },
		AcquireWorker: func() (func(), error) { acquired.Add(1); return func() {}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	r, err := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Error.Kind != ErrorSandbox || r.Error.Message != "Failed to load QuickJS: wasm unavailable" {
		t.Fatalf("result = %+v (%+v)", r, r.Error)
	}
	if acquired.Load() != 0 {
		t.Fatalf("acquired %d times, want 0", acquired.Load())
	}
}

func TestReportsTheActualExitOfExecuteAndStartWorkers(t *testing.T) {
	var acquired, released atomic.Int32
	var exits []<-chan struct{}
	sandbox, err := NewSandbox(SandboxOptions{
		AcquireWorker: func() (func(), error) {
			acquired.Add(1)
			return func() { released.Add(1) }, nil
		},
		OnWorkerStarted: func(exited <-chan struct{}) error { exits = append(exits, exited); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK || string(r.Value) != "1" {
		t.Fatalf("execute: %+v (%+v)", r, r.Error)
	}
	run, err := sandbox.Start(t.Context(), "return 2", RunOptions{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r := run.Result(); !r.OK || string(r.Value) != "2" {
		t.Fatalf("start: %+v (%+v)", r, r.Error)
	}
	if !run.Finished() {
		t.Fatal("run not finished after its result")
	}
	if len(exits) != 2 {
		t.Fatalf("OnWorkerStarted called %d times, want 2", len(exits))
	}
	for i, exited := range exits {
		select {
		case <-exited:
		case <-time.After(4 * terminationGrace):
			t.Fatalf("worker %d did not report its exit", i)
		}
	}
	if acquired.Load() != 2 || released.Load() != 2 {
		t.Fatalf("acquired %d, released %d; want 2, 2", acquired.Load(), released.Load())
	}
}

func TestAFailingOnWorkerStartedEndsTheWorker(t *testing.T) {
	var released atomic.Int32
	var exited <-chan struct{}
	sandbox, err := NewSandbox(SandboxOptions{
		TimeoutMs:     math.Inf(1),
		AcquireWorker: func() (func(), error) { return func() { released.Add(1) }, nil },
		OnWorkerStarted: func(ch <-chan struct{}) error {
			exited = ch
			return errors.New("observer failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	r, err := sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Error.Kind != ErrorSandbox || r.Error.Message != "observer failed" {
		t.Fatalf("result = %+v (%+v)", r, r.Error)
	}
	if exited == nil {
		t.Fatal("OnWorkerStarted was not called")
	}
	select {
	case <-exited:
	case <-time.After(4 * terminationGrace):
		t.Fatal("the worker did not exit")
	}
	if released.Load() != 1 {
		t.Fatalf("released %d times, want 1", released.Load())
	}
}

// A cancelled VM keeps its admission until it actually exits, not until its result is returned.
func TestAdmissionIsReleasedOnExitNotOnTheResult(t *testing.T) {
	var released atomic.Int32
	var exited <-chan struct{}
	sandbox, err := NewSandbox(SandboxOptions{
		AcquireWorker:   func() (func(), error) { return func() { released.Add(1) }, nil },
		OnWorkerStarted: func(ch <-chan struct{}) error { exited = ch; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	<-exited
	released.Store(0)
	ignoreInterrupt.Store(true)
	t.Cleanup(func() { ignoreInterrupt.Store(false) })
	r, err := sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{TimeoutMs: 50})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Error.Kind != ErrorTimeout {
		t.Fatalf("result = %+v (%+v), want a timeout", r, r.Error)
	}
	select {
	case <-exited:
		t.Fatal("the worker reported its exit before the grace period halted it")
	default:
	}
	if released.Load() != 0 {
		t.Fatal("admission released with the result")
	}
	select {
	case <-exited:
	case <-time.After(4 * terminationGrace):
		t.Fatal("the worker did not exit")
	}
	if released.Load() != 1 {
		t.Fatalf("released %d times, want 1", released.Load())
	}
}

package codemode

import (
	"context"
	"encoding/json"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Lifetime and cancellation of an execution (docs/specs/builtin-codemode-tool-search.md, "Execution lifetime and
// cancellation"): when Execute returns, its wasm instance is closed and every goroutine it started has exited.

func settled(t *testing.T, baseGoroutines int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if liveVMs.Load() == 0 && runtime.NumGoroutine() <= baseGoroutines {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after Execute: %d live VMs, %d goroutines (baseline %d)", liveVMs.Load(), runtime.NumGoroutine(), baseGoroutines)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitTool(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestExecuteReturnsOnlyAfterTheVMIsClosedForEveryTerminalKind(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{Tools: []Tool{{Name: "wait", Execute: waitTool}}, TimeoutMs: 10_000, MemoryLimitBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	// Warm the compiled module so the goroutine baseline includes wazero's own.
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	base := runtime.NumGoroutine()
	// hardStops is process-wide: TestAScriptThatNeverReachesTheInterruptCheckIsStoppedByClosingTheInstance adds one
	// per run, so -count>1 would see its stops here.
	hardStopsBefore := hardStops.Load()

	kinds := []struct {
		name string
		run  func() (Result, error)
		kind ErrorKind
	}{
		{"ok", func() (Result, error) { return sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}) }, ""},
		{"script error", func() (Result, error) { return sandbox.Execute(t.Context(), "throw new Error('x')", ExecuteOptions{}) }, ErrorScript},
		{"syntax error", func() (Result, error) { return sandbox.Execute(t.Context(), "const = ;", ExecuteOptions{}) }, ErrorScript},
		{"timeout", func() (Result, error) {
			return sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{TimeoutMs: 20})
		}, ErrorTimeout},
		{"microtask spin", func() (Result, error) {
			return sandbox.Execute(t.Context(), "while (true) await null", ExecuteOptions{TimeoutMs: 20})
		}, ErrorTimeout},
		{"abort during a nested call", func() (Result, error) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			return sandbox.Execute(ctx, "await tools.wait()", ExecuteOptions{})
		}, ErrorAborted},
		{"abort before start", func() (Result, error) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return sandbox.Execute(ctx, "return 1", ExecuteOptions{})
		}, ErrorAborted},
		{"out of memory", func() (Result, error) {
			return sandbox.Execute(t.Context(), "const a = []; for (;;) a.push(new Uint8Array(1e6).fill(a.length))", ExecuteOptions{})
		}, ErrorScript},
		{"unawaited call", func() (Result, error) {
			return sandbox.Execute(t.Context(), "tools.wait(); return 1", ExecuteOptions{})
		}, ""},
	}
	for _, k := range kinds {
		for range 25 {
			result, err := k.run()
			if err != nil {
				t.Fatalf("%s: %v", k.name, err)
			}
			if k.kind == "" && !result.OK || k.kind != "" && (result.OK || result.Error.Kind != k.kind) {
				t.Fatalf("%s: result %+v (error %+v)", k.name, result, result.Error)
			}
			// No waiting: the instance must be gone the moment Execute returns.
			if live := liveVMs.Load(); live != 0 {
				t.Fatalf("%s: %d live VMs right after Execute", k.name, live)
			}
		}
		settled(t, base)
	}
	if n := hardStops.Load() - hardStopsBefore; n != 0 {
		t.Errorf("%d executions needed the instance closed under them; the interrupt flag should stop these scripts", n)
	}
}

func TestCloseAbortsEveryRunningExecutionAndWaitsForTheirVMs(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: math.Inf(1)})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	base := runtime.NumGoroutine()
	const n = 8
	results := make([]Result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { results[i], _ = sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{}) })
	}
	for liveVMs.Load() < n {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	if live := liveVMs.Load(); live != 0 {
		t.Fatalf("%d live VMs after Close returned", live)
	}
	wg.Wait()
	for i, r := range results {
		if r.OK || r.Error.Kind != ErrorAborted || r.Error.Message != "Sandbox closed" {
			t.Errorf("execution %d: %+v", i, r)
		}
	}
	settled(t, base)
}

// A script inside a native call never reaches QuickJS's interrupt poll, so the flag cannot stop it; the grace period
// closes the instance under the script, so a script cannot hold Execute forever. The test stands for that script by making
// the poll report nothing.
func TestAScriptThatNeverReachesTheInterruptCheckIsStoppedByClosingTheInstance(t *testing.T) {
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
	result, err := sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{TimeoutMs: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error.Kind != ErrorTimeout {
		t.Fatalf("result = %+v (%+v) after %v, want a timeout", result, result.Error, time.Since(started))
	}
	if hardStops.Load() != before+1 {
		t.Fatalf("hard stops %d -> %d: the instance was not closed under the script", before, hardStops.Load())
	}
	if elapsed := time.Since(started); elapsed < terminationGrace {
		t.Fatalf("Execute returned after %v, before the %v grace: the flag alone stopped a script that ignores it", elapsed, terminationGrace)
	}
	if live := liveVMs.Load(); live != 0 {
		t.Fatalf("%d live VMs after Execute", live)
	}
}

// A nested call that is still running when the script ends is cancelled, and Execute waits for it: no goroutine of
// the execution outlives the call, and its late result is discarded.
func TestExecuteWaitsForANestedCallThatFinishesAfterTheScriptEnded(t *testing.T) {
	var finished, sawCancel bool
	slow := Tool{Name: "slow", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		sawCancel = true
		time.Sleep(100 * time.Millisecond)
		finished = true
		return json.RawMessage(`"late"`), nil
	}}
	sandbox, err := NewSandbox(SandboxOptions{Tools: []Tool{slow}, TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(t.Context(), "tools.slow(); return 'early'", ExecuteOptions{})
	if err != nil || !result.OK || string(result.Value) != `"early"` {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if !sawCancel || !finished {
		t.Fatalf("Execute returned before the nested call finished (cancelled %v, finished %v)", sawCancel, finished)
	}
	if len(result.Calls) != 1 || result.Calls[0].Status != CallCancelled {
		t.Fatalf("calls = %+v: a late result changed the recorded status", result.Calls)
	}
}

// The module compiles once per process and is not the script's time: a deadline shorter than the load still lets a trivial
// script finish. The load is a slow Wasm source, so the test does not depend on how fast the host compiles or starts a VM.
func TestCompilingTheModuleDoesNotCountAgainstTheDeadline(t *testing.T) {
	const load = 6 * time.Second
	sandbox, err := NewSandbox(SandboxOptions{Wasm: func() ([]byte, error) { time.Sleep(load); return QuickJSWasm(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{TimeoutMs: 3000})
	if err != nil || !result.OK {
		t.Fatalf("result = %+v (%+v), %v", result, result.Error, err)
	}
}

package codemode

import "testing"

// wasm.ts:14-35 with SandboxOptions.wasm (types.ts:115-119): sandboxes that use the one module share its compilation. Two sandboxes loading the same module bytes run on the same engine, closing one
// leaves the other running, and a different heap limit is a different engine.
func TestSandboxesLoadingOneModuleShareOneCompilation(t *testing.T) {
	open := func(memoryLimit uint32) *Sandbox {
		sandbox, err := NewSandbox(SandboxOptions{Wasm: func() ([]byte, error) { return QuickJSWasm(), nil }, MemoryLimitBytes: memoryLimit})
		if err != nil {
			t.Fatal(err)
		}
		return sandbox
	}
	run := func(sandbox *Sandbox) {
		t.Helper()
		result, err := sandbox.Execute(t.Context(), "return 1 + 1", ExecuteOptions{})
		if err != nil || !result.OK || string(result.Value) != "2" {
			t.Fatalf("result = %+v (%+v), %v", result, result.Error, err)
		}
	}
	a, b, small := open(0), open(0), open(64<<20)
	run(a)
	run(b)
	run(small)
	if a.loaded == nil || a.loaded != b.loaded {
		t.Fatalf("sandboxes using one module compiled it separately: %p, %p", a.loaded, b.loaded)
	}
	if small.loaded == a.loaded {
		t.Fatal("a different heap limit shares the engine sized for another")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	run(b)
	_ = b.Close()
	_ = small.Close()
}

// wasm.ts:30-33: a failed load is not kept. A module that does not compile fails every sandbox that loads it and leaves nothing in the shared set.
func TestAFailedCompileIsNotKept(t *testing.T) {
	customEnginesMu.Lock()
	before := len(customEngines)
	customEnginesMu.Unlock()
	for range 2 {
		sandbox, err := NewSandbox(SandboxOptions{Wasm: func() ([]byte, error) { return []byte("not wasm"), nil }})
		if err != nil {
			t.Fatal(err)
		}
		result, err := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{})
		if err != nil || result.OK {
			t.Fatalf("result = %+v, %v; want a load failure", result, err)
		}
		_ = sandbox.Close()
	}
	customEnginesMu.Lock()
	after := len(customEngines)
	customEnginesMu.Unlock()
	if after != before {
		t.Fatalf("shared engines went from %d to %d after a failed compile", before, after)
	}
}

package codemode

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// A hand-assembled module with the exports `spin` (an endless loop) and `count` (a loop that adds one until 1000
// and returns the total). Neither has a global section.
var haltTestModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// type section: () -> () and () -> i32
	0x01, 0x08, 0x02, 0x60, 0x00, 0x00, 0x60, 0x00, 0x01, 0x7f,
	// function section: spin, count
	0x03, 0x03, 0x02, 0x00, 0x01,
	// export section
	0x07, 0x10, 0x02,
	0x04, 's', 'p', 'i', 'n', 0x00, 0x00,
	0x05, 'c', 'o', 'u', 'n', 't', 0x00, 0x01,
	// code section
	0x0a, 0x20, 0x02,
	// spin: loop; br 0; end; end
	0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b,
	// count: one i32 local; loop; local.get 0; i32.const 1; i32.add; local.tee 0; i32.const 1000; i32.lt_u; br_if 0; end; local.get 0; end
	0x16, 0x01, 0x01, 0x7f,
	0x03, 0x40, 0x20, 0x00, 0x41, 0x01, 0x6a, 0x22, 0x00, 0x41, 0xe8, 0x07, 0x49, 0x0d, 0x00, 0x0b, 0x20, 0x00, 0x0b,
}

func instantiateWithoutContextChecks(t *testing.T, wasm []byte) api.Module {
	t.Helper()
	runtime := wazero.NewRuntimeWithConfig(t.Context(), wazero.NewRuntimeConfigCompiler())
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	mod, err := runtime.Instantiate(t.Context(), wasm)
	if err != nil {
		t.Fatal(err)
	}
	return mod
}

// A loop with the halt global set traps on its next iteration; with it clear, a loop runs unchanged.
func TestInstrumentHaltTrapsALoopWhenTheGlobalIsSet(t *testing.T) {
	instrumented, err := instrumentHalt(haltTestModule)
	if err != nil {
		t.Fatal(err)
	}
	mod := instantiateWithoutContextChecks(t, instrumented)
	halt, ok := mod.ExportedGlobal(haltExport).(api.MutableGlobal)
	if !ok {
		t.Fatalf("%s is not an exported mutable global", haltExport)
	}

	out, err := mod.ExportedFunction("count").Call(t.Context())
	if err != nil || len(out) != 1 || out[0] != 1000 {
		t.Fatalf("count() = %v, %v with the halt global clear, want 1000", out, err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := mod.ExportedFunction("spin").Call(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("spin() returned before the halt: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	halt.Set(1)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("spin() = %v, want an unreachable trap", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("spin() still running 10s after the halt global was set")
	}
}

func TestInstrumentHaltLeavesALoopFreeModuleAlone(t *testing.T) {
	// type () -> i32, one function returning 7, exported as `seven`.
	module := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x09, 0x01, 0x05, 's', 'e', 'v', 'e', 'n', 0x00, 0x00,
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x07, 0x0b,
	}
	instrumented, err := instrumentHalt(module)
	if err != nil {
		t.Fatal(err)
	}
	mod := instantiateWithoutContextChecks(t, instrumented)
	out, err := mod.ExportedFunction("seven").Call(t.Context())
	if err != nil || len(out) != 1 || out[0] != 7 {
		t.Fatalf("seven() = %v, %v", out, err)
	}
	if bytes.Count(instrumented, []byte{0x03, 0x40}) != 0 {
		t.Error("a loop-free module gained a loop")
	}
}

func TestInstrumentHaltRejectsWhatItCannotRewrite(t *testing.T) {
	// A code section whose only instruction is the SIMD prefix 0xfd: the decoder cannot size its immediates.
	simd := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x05, 0x01, 0x01, 'f', 0x00, 0x00,
		0x0a, 0x06, 0x01, 0x04, 0x00, 0xfd, 0x00, 0x0b,
	}
	for name, wasm := range map[string][]byte{
		"not wasm":         []byte("not wasm"),
		"empty":            nil,
		"truncated":        haltTestModule[:len(haltTestModule)-3],
		"unsupported SIMD": simd,
	} {
		if out, err := instrumentHalt(wasm); err == nil {
			t.Errorf("%s: no error, got %d bytes", name, len(out))
		}
	}
}

// The embedded module is instrumented with the real decoder: every function stays valid, and an instance of it stops
// a script inside a loop that ignores the interrupt flag.
func TestHaltStopsAGuestThatNeverReachesTheInterruptCheck(t *testing.T) {
	eng, err := newEngine(t.Context(), QuickJSWasm(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.close(context.Background()) })
	var interrupt atomic.Bool
	machine, err := eng.newVM(t.Context(), 0, &interrupt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.close)

	ignoreInterrupt.Store(true)
	t.Cleanup(func() { ignoreInterrupt.Store(false) })
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		machine.eval("while (true) {}", "spin.js")
	}()
	select {
	case r := <-done:
		t.Fatalf("the script ended before the halt: %v", r)
	case <-time.After(100 * time.Millisecond):
	}
	machine.halt()
	select {
	case r := <-done:
		if _, ok := r.(vmTrap); !ok {
			t.Fatalf("recovered %#v, want a vmTrap", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the script still runs 10s after halt")
	}
}

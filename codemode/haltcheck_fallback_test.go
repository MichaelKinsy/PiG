package codemode

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func wasmSectionOf(id byte, payload ...byte) []byte {
	return append(appendU32([]byte{id}, uint32(len(payload))), payload...)
}

// haltFallbackModule is a module instrumentHalt cannot rewrite (one function uses the SIMD v128.const) that wazero runs:
// stub VM setup exports and `spin`, an endless loop.
func haltFallbackModule() []byte {
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// types: () -> (), () -> i32, (i32) -> ()
	wasm = append(wasm, wasmSectionOf(1, 0x03, 0x60, 0x00, 0x00, 0x60, 0x00, 0x01, 0x7f, 0x60, 0x01, 0x7f, 0x00)...)
	// functions: qjs_init, qjs_set_max_stack_size, qjs_set_interrupt_handler, qjs_get_undefined, spin, simd
	wasm = append(wasm, wasmSectionOf(3, 0x06, 0x00, 0x02, 0x02, 0x01, 0x00, 0x00)...)
	exports := []byte{0x05}
	for i, name := range []string{"qjs_init", "qjs_set_max_stack_size", "qjs_set_interrupt_handler", "qjs_get_undefined", "spin"} {
		exports = appendU32(exports, uint32(len(name)))
		exports = append(exports, name...)
		exports = append(exports, 0x00, byte(i))
	}
	wasm = append(wasm, wasmSectionOf(7, exports...)...)
	code := []byte{0x06}
	for _, body := range [][]byte{
		{0x00, 0x0b},
		{0x00, 0x0b},
		{0x00, 0x0b},
		{0x00, 0x41, 0x00, 0x0b},
		{0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b},
		append(append([]byte{0x00, 0xfd, 0x0c}, make([]byte, 16)...), 0x1a, 0x0b),
	} {
		code = appendU32(code, uint32(len(body)))
		code = append(code, body...)
	}
	return append(wasm, wasmSectionOf(10, code...)...)
}

// A custom module the halt check cannot be added to still loads, as it did before the check existed, and halt still
// stops a guest inside a loop: the engine falls back to wazero's context-done check for it.
func TestHaltFallsBackToClosingAModuleItCannotInstrument(t *testing.T) {
	module := haltFallbackModule()
	if _, err := instrumentHalt(module); err == nil {
		t.Fatal("instrumentHalt rewrote the SIMD module; the test needs one it cannot rewrite")
	}
	eng, err := newEngine(t.Context(), module, "", 0)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.close(context.Background()) })
	var interrupt atomic.Bool
	machine, err := eng.newVM(t.Context(), 0, &interrupt)
	if err != nil {
		t.Fatalf("newVM: %v", err)
	}
	t.Cleanup(machine.close)

	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		machine.call("spin")
	}()
	select {
	case r := <-done:
		t.Fatalf("spin ended before the halt: %v", r)
	case <-time.After(100 * time.Millisecond):
	}
	machine.halt()
	select {
	case r := <-done:
		if _, ok := r.(vmTrap); !ok {
			t.Fatalf("recovered %#v, want a vmTrap", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("spin still runs 10s after halt")
	}
}

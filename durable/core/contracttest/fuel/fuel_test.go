// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package fuel

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// sumModule is (func (export "sum") (param i32) (result i32) (local i32): the sum of n, n-1, ... 1 by a loop.
//
//	loop; local.get 1; local.get 0; i32.add; local.set 1; local.get 0; i32.const 1; i32.sub; local.tee 0; br_if 0; end
//	local.get 1
var sumModule = []byte{
	0, 'a', 's', 'm', 1, 0, 0, 0,
	1, 6, 1, 0x60, 1, 0x7f, 1, 0x7f, // type: (i32) -> i32
	3, 2, 1, 0, // function
	7, 7, 1, 3, 's', 'u', 'm', 0, 0, // export
	10, 27, 1, 25, // code: one body of 25 bytes
	1, 1, 0x7f, // one i32 local
	0x03, 0x40, // loop
	0x20, 1, 0x20, 0, 0x6a, 0x21, 1, // local.get 1; local.get 0; i32.add; local.set 1
	0x20, 0, 0x41, 1, 0x6b, 0x22, 0, // local.get 0; i32.const 1; i32.sub; local.tee 0
	0x0d, 0, // br_if 0
	0x0b,    // end
	0x20, 1, // local.get 1
	0x0b, // end
}

func instantiate(t *testing.T, wasm []byte) (api.Module, context.Context) {
	t.Helper()
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(ctx) })
	mod, err := rt.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	return mod, ctx
}

// TestInstrumentCountsEveryBlock runs a loop n times: the entry block is the loop opcode (1), the body is 9 instructions
// per iteration, then the end of the loop (1) and the tail with its end (2): 9n+4.
func TestInstrumentCountsEveryBlock(t *testing.T) {
	metered, err := Instrument(sumModule)
	if err != nil {
		t.Fatal(err)
	}
	mod, ctx := instantiate(t, metered)
	for _, n := range []uint64{1, 2, 10, 1000} {
		before, err := Read(mod)
		if err != nil {
			t.Fatal(err)
		}
		res, err := mod.ExportedFunction("sum").Call(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		after, _ := Read(mod)
		if want := n * (n + 1) / 2; res[0] != want {
			t.Errorf("sum(%d) = %d, want %d: instrumenting changed the result", n, res[0], want)
		}
		if got, want := after-before, 9*n+4; got != want {
			t.Errorf("sum(%d) counted %d instructions, want %d", n, got, want)
		}
	}
}

// TestInstrumentLeavesPlainModuleAlone proves the original module has no counter, so Read names the mistake.
func TestInstrumentLeavesPlainModuleAlone(t *testing.T) {
	mod, _ := instantiate(t, sumModule)
	if _, err := Read(mod); err == nil {
		t.Fatal("Read succeeded on a module that was not instrumented")
	}
}

func TestInstrumentRefusesUnsupported(t *testing.T) {
	for name, body := range map[string][]byte{
		"simd":   {0xFD, 0x0C},
		"atomic": {0xFE, 0x00},
		"tail":   {0x12, 0x00},
	} {
		if _, err := instrumentBody(append([]byte{0}, body...), 0); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestLEBRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 63, 64, -64, -65, 1 << 40, -(1 << 40), 1<<62 + 5} {
		b := appendSLEB(nil, v)
		got, at, err := sleb(b, 0)
		if err != nil || got != v || at != len(b) {
			t.Errorf("sleb round trip of %d: got %d, at %d of %d, err %v", v, got, at, len(b), err)
		}
	}
	for _, v := range []uint64{0, 1, 127, 128, 1 << 35, 1<<63 + 1} {
		b := appendULEB(nil, v)
		got, at, err := uleb(b, 0)
		if err != nil || got != v || at != len(b) {
			t.Errorf("uleb round trip of %d: got %d, err %v", v, got, err)
		}
	}
}

// skipModule is (func (export "f") (param i32) (result i32)): block; local.get 0; br_if 0; nop; nop; nop; end; i32.const 7.
// A taken branch skips the nops, so the count depends on the path: every block entry, and the instruction after an end, must
// start a counted block of its own.
var skipModule = []byte{
	0, 'a', 's', 'm', 1, 0, 0, 0,
	1, 6, 1, 0x60, 1, 0x7f, 1, 0x7f,
	3, 2, 1, 0,
	7, 5, 1, 1, 'f', 0, 0,
	10, 16, 1, 14,
	0,
	0x02, 0x40,
	0x20, 0, 0x0d, 0,
	0x01, 0x01, 0x01,
	0x0b,
	0x41, 7,
	0x0b,
}

func TestInstrumentCountsEachPath(t *testing.T) {
	metered, err := Instrument(skipModule)
	if err != nil {
		t.Fatal(err)
	}
	mod, ctx := instantiate(t, metered)
	for _, c := range []struct{ arg, want uint64 }{
		{1, 1 + 2 + 2},     // block; local.get, br_if (taken); the const and the end after the block's end
		{0, 1 + 2 + 4 + 2}, // the same, and the three nops and the block's end between
	} {
		before, _ := Read(mod)
		res, err := mod.ExportedFunction("f").Call(ctx, c.arg)
		if err != nil || res[0] != 7 {
			t.Fatalf("f(%d) = %v, %v", c.arg, res, err)
		}
		after, _ := Read(mod)
		if got := after - before; got != c.want {
			t.Errorf("f(%d) counted %d instructions, want %d", c.arg, got, c.want)
		}
	}
}

// ifElseModule is (func (export "f") (param i32) (result i32)): local.get 0; if (result i32) i32.const 1 else i32.const 2; nop
// end; end. Each arm is a basic block of its own: else ends the then-arm and starts the else-arm.
var ifElseModule = []byte{
	0, 'a', 's', 'm', 1, 0, 0, 0,
	1, 6, 1, 0x60, 1, 0x7f, 1, 0x7f,
	3, 2, 1, 0,
	7, 5, 1, 1, 'f', 0, 0,
	10, 15, 1, 13,
	0,
	0x20, 0, 0x04, 0x7f,
	0x41, 1,
	0x05,
	0x41, 2, 0x01,
	0x0b,
	0x0b,
}

// brModule is (func (export "f") (param i32) (result i32)): block; local.get 0; br_if 0; br 0; nop; nop; end; i32.const 7;
// end. An unconditional branch ends a basic block: the nops after it never run, and are never counted.
var brModule = []byte{
	0, 'a', 's', 'm', 1, 0, 0, 0,
	1, 6, 1, 0x60, 1, 0x7f, 1, 0x7f,
	3, 2, 1, 0,
	7, 5, 1, 1, 'f', 0, 0,
	10, 17, 1, 15,
	0,
	0x02, 0x40,
	0x20, 0, 0x0d, 0,
	0x0c, 0,
	0x01, 0x01,
	0x0b,
	0x41, 7,
	0x0b,
}

func TestInstrumentCountsArmsAndBranches(t *testing.T) {
	for _, m := range []struct {
		name  string
		wasm  []byte
		cases []struct{ arg, res, want uint64 }
	}{
		{"if/else", ifElseModule, []struct{ arg, res, want uint64 }{
			{1, 1, 2 + 2 + 1}, // local.get, if; the then-arm's const and else; the function's end
			{0, 2, 2 + 3 + 1}, // local.get, if; the else-arm's const, nop and end; the function's end
		}},
		{"br", brModule, []struct{ arg, res, want uint64 }{
			{1, 7, 1 + 2 + 2},     // block; local.get, br_if (taken); the const and the function's end
			{0, 7, 1 + 2 + 1 + 2}, // the same with br_if not taken and the br, never the nops or the block's end after it
		}},
	} {
		metered, err := Instrument(m.wasm)
		if err != nil {
			t.Fatal(err)
		}
		mod, ctx := instantiate(t, metered)
		for _, c := range m.cases {
			before, _ := Read(mod)
			res, err := mod.ExportedFunction("f").Call(ctx, c.arg)
			if err != nil || res[0] != c.res {
				t.Fatalf("%s: f(%d) = %v, %v; want %d", m.name, c.arg, res, err, c.res)
			}
			after, _ := Read(mod)
			if got := after - before; got != c.want {
				t.Errorf("%s: f(%d) counted %d instructions, want %d", m.name, c.arg, got, c.want)
			}
		}
	}
}

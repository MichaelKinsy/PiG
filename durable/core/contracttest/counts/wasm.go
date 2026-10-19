// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package counts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest/fuel"
)

// WasmOptions choose what one pass over a Wasm core measures. The passes are separate because each perturbs the run: a listener
// on every allocation slows it, mem_stats collects garbage, and an instrumented module is a different binary.
type WasmOptions struct {
	// Build names the scope of the metrics: "tinygo" or "go".
	Build string
	// Allocs counts calls of the runtime's allocator by name (TinyGo keeps the name section: runtime.alloc,
	// runtime.growHeap and runtime.runGC, which every collection cycle runs, the allocator's own and an explicit one). A module without the names reports no allocation counts.
	Allocs bool
	// Live asks the core for mem_stats at the end of every interval: its live heap after a full collection.
	Live bool
	// Fuel instruments the module (fuel.Instrument) and reports instructions per interval.
	Fuel bool
}

// allocCounter is the listener of the allocator functions of a Wasm runtime.
type allocCounter struct {
	allocs, bytes, grows, collections int64
}

func (a *allocCounter) NewFunctionListener(def api.FunctionDefinition) experimental.FunctionListener {
	switch def.Name() {
	case "runtime.alloc":
		return experimental.FunctionListenerFunc(func(_ context.Context, _ api.Module, _ api.FunctionDefinition, params []uint64, _ experimental.StackIterator) {
			a.allocs++
			a.bytes += int64(uint32(params[0]))
		})
	case "runtime.runGC":
		return experimental.FunctionListenerFunc(func(context.Context, api.Module, api.FunctionDefinition, []uint64, experimental.StackIterator) {
			a.collections++
		})
	case "runtime.growHeap":
		return experimental.FunctionListenerFunc(func(context.Context, api.Module, api.FunctionDefinition, []uint64, experimental.StackIterator) {
			a.grows++
		})
	}
	return nil
}

// wasmCore is one instance of a core module and the state of the recorded entry it is replaying.
type wasmCore struct {
	ctx     context.Context
	mod     api.Module
	mem     api.Memory
	handle  uint64
	inPtr   uint32
	inCap   uint32
	cur     *Entry
	reads   int
	uuids   int
	spill   []byte // an answer longer than the core's buffer, kept for its second call
	failure string

	calls    int64 // exports called
	imports  int64 // imports called
	highMark uint32
}

func (w *wasmCore) call(name string, args ...uint64) (uint64, error) {
	w.calls++
	res, err := w.mod.ExportedFunction(name).Call(w.ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

// step delivers one event the way the Cloudflare host does: reserve an input buffer when the payload outgrows the last one,
// write the payload, call step. It returns the bytes of the step.
func (w *wasmCore) step(e *Entry) ([]byte, error) {
	n := uint32(len(e.Wire))
	if n > w.inCap {
		want := max(n, 65536, w.inCap*2)
		p, err := w.call("in_reserve", uint64(want))
		if err != nil {
			return nil, err
		}
		w.inPtr, w.inCap = uint32(p), want
	}
	if n > 0 && !w.mem.Write(w.inPtr, e.Wire) {
		return nil, errors.New("the input buffer is outside linear memory")
	}
	w.cur, w.reads, w.uuids, w.spill, w.failure = e, 0, 0, nil, ""
	// A recording carries no origins (ABI section 5.4): every event runs in none.
	ptr, err := w.call("step", w.handle, uint64(e.Kind), uint64(n), api.EncodeF64(e.Now), 0)
	if w.failure != "" {
		return nil, errors.New(w.failure)
	}
	if err != nil {
		return nil, err
	}
	if size := w.mem.Size(); size > w.highMark {
		w.highMark = size
	}
	return w.stepBytes(uint32(ptr))
}

// stepBytes joins the parts of the step whose part list is at ptr (ABI section 4.1, step).
func (w *wasmCore) stepBytes(ptr uint32) ([]byte, error) {
	head, ok := w.mem.Read(ptr, 4)
	if !ok {
		return nil, errors.New("the step's part list is outside linear memory")
	}
	n := binary.LittleEndian.Uint32(head)
	var out []byte
	for i := range n {
		part, ok := w.mem.Read(ptr+4+8*i, 8)
		if !ok {
			return nil, errors.New("the step's part list is outside linear memory")
		}
		b, ok := w.mem.Read(binary.LittleEndian.Uint32(part), binary.LittleEndian.Uint32(part[4:]))
		if !ok {
			return nil, errors.New("a part of the step is outside linear memory")
		}
		out = append(out, b...)
	}
	return out, nil
}

// pigRead is the pig.read import (ABI section 4.2): the recorded answer to the core's next read, after a check that the core
// asked what the recorded run asked.
func (w *wasmCore) pigRead(_ context.Context, m api.Module, req, reqLen, dst, capacity uint32) uint32 {
	w.imports++
	answer := w.spill
	w.spill = nil
	if answer == nil {
		if w.reads >= len(w.cur.Reads) {
			w.failure = "the core made a pig.read call the recorded run did not make"
			panic(w.failure)
		}
		got, ok := m.Memory().Read(req, reqLen)
		if !ok || !bytes.Equal(got, w.cur.Reads[w.reads].Request) {
			w.failure = "the core asked a different pig.read than the recorded run"
			panic(w.failure)
		}
		answer = w.cur.Reads[w.reads].Answer
		w.reads++
	}
	if uint32(len(answer)) > capacity {
		w.spill = answer
		return uint32(len(answer))
	}
	m.Memory().Write(dst, answer)
	return uint32(len(answer))
}

func (w *wasmCore) pigUUID(_ context.Context, m api.Module, dst uint32) {
	w.imports++
	if w.uuids >= len(w.cur.UUIDs) {
		w.failure = "the core took a uuid the recorded run did not take"
		panic(w.failure)
	}
	m.Memory().Write(dst, []byte(w.cur.UUIDs[w.uuids]))
	w.uuids++
}

// ReplayWasm drives one Wasm core through the recording under wazero and returns its counts per interval. Every step is
// compared with the step of the recorded run.
func ReplayWasm(ctx context.Context, rec *Recording, wasm []byte, o WasmOptions) ([]Interval, error) {
	if o.Fuel {
		metered, err := fuel.Instrument(wasm)
		if err != nil {
			return nil, err
		}
		wasm = metered
	}
	allocs := &allocCounter{}
	if o.Allocs {
		ctx = experimental.WithFunctionListenerFactory(ctx, allocs)
	}
	rt := wazero.NewRuntime(ctx)
	defer func() { _ = rt.Close(ctx) }()
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	w := &wasmCore{ctx: ctx}
	if _, err := rt.NewHostModuleBuilder("pig").
		NewFunctionBuilder().WithFunc(w.pigUUID).Export("uuidv7").
		NewFunctionBuilder().WithFunc(w.pigRead).Export("read").
		Instantiate(ctx); err != nil {
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return nil, err
	}
	// The Go and TinyGo runtimes read random_get for their hash seeds; a fixed stream makes a map's layout, and with it the
	// instruction count, the same on every run.
	seed := rand.NewChaCha8([32]byte{'d', 'u', 'r', 'a', 'b', 'l', 'e', ' ', 'c', 'o', 'u', 'n', 't', 's'})
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStartFunctions("_initialize").WithRandSource(seed).WithName(""))
	if err != nil {
		return nil, err
	}
	w.mod, w.mem = mod, mod.Memory()
	id, err := w.call("abi_id")
	if err != nil {
		return nil, err
	}
	got, _ := w.mem.Read(uint32(id), 64)
	if string(got) != rec.ABI {
		return nil, fmt.Errorf("counts: the %s core speaks abi_id %s, the recording %s", o.Build, got, rec.ABI)
	}
	if w.handle, err = w.call("session_new"); err != nil {
		return nil, err
	}

	ivs := intervalsOf(rec)
	out := make([]Interval, 0, len(ivs))
	var lastAllocs, lastBytes, lastGrows, lastGCs, lastFuel int64
	var lastCalls int64
	for _, iv := range ivs {
		m := Metrics{}
		for i := iv.from; i < iv.to; i++ {
			e := &rec.Entries[i]
			stepWire, err := w.step(e)
			if err != nil {
				return nil, &ReplayError{o.Build, i, e.Kind, err.Error()}
			}
			if e.Verified && sha256.Sum256(stepWire) != e.Digest {
				return nil, &ReplayError{o.Build, i, e.Kind, "the step differs from the recorded step"}
			}
			if w.reads != len(e.Reads) || w.uuids != len(e.UUIDs) {
				return nil, &ReplayError{o.Build, i, e.Kind, fmt.Sprintf("the core made %d reads and took %d uuids, the recorded run %d and %d", w.reads, w.uuids, len(e.Reads), len(e.UUIDs))}
			}
		}
		b := o.Build + "."
		m[b+"linearBytes"] = int64(w.mem.Size())
		m[b+"highWaterBytes"] = int64(w.highMark)
		m[b+"calls"] = w.calls + w.imports - lastCalls
		lastCalls = w.calls + w.imports
		if o.Allocs {
			m[b+"allocRequestedBytes"], m[b+"allocCalls"], m[b+"growHeapCalls"] = allocs.bytes-lastBytes, allocs.allocs-lastAllocs, allocs.grows-lastGrows
			m[b+"gcCycles"] = allocs.collections - lastGCs
			lastBytes, lastAllocs, lastGrows, lastGCs = allocs.bytes, allocs.allocs, allocs.grows, allocs.collections
		}
		if o.Fuel {
			f, err := fuel.Read(mod)
			if err != nil {
				return nil, err
			}
			m[b+"fuel"] = int64(f) - lastFuel
			lastFuel = int64(f)
		}
		if o.Live {
			ptr, err := w.call("mem_stats", w.handle)
			if err != nil {
				return nil, err
			}
			stats, ok := w.mem.Read(uint32(ptr), 64)
			if !ok {
				return nil, errors.New("mem_stats is outside linear memory")
			}
			m[b+"liveBytes"] = int64(binary.LittleEndian.Uint64(stats[48:]))
			// The mem_stats call is the gate's, not the workload's: it is not a crossing of the next interval.
			lastCalls = w.calls + w.imports
		}
		out = append(out, Interval{iv.name, m})
	}
	return out, nil
}

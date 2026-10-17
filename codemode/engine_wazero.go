//go:build !tinygo

package codemode

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	wasmPageSize = 64 * 1024
	// linearMemoryHeadroom is what the instance needs beyond the QuickJS heap limit: the wasm data and stack, the
	// allocator's bookkeeping and the interpreter's own allocations.
	linearMemoryHeadroom = 64 << 20
)

// engine is one wazero runtime holding the compiled QuickJS module. It is shared by every execution that uses the
// same module, like upstream's per-path module cache (packages/codemode/src/wasm.ts).
type engine struct {
	runtime wazero.Runtime
	module  wazero.CompiledModule
	// haltable reports that the module carries instrumentHalt's halt global; otherwise an instance is halted by
	// cancelling the context of its calls.
	haltable bool
	// vms maps a running instance to its VM, for the host functions the instance calls.
	vms sync.Map
}

// newEngine compiles wasm. cacheDir, when set, holds wazero's on-disk compilation cache; a cache that cannot be
// opened is ignored and the module compiles in memory.
func newEngine(ctx context.Context, wasm []byte, cacheDir string, memoryLimit uint32) (*engine, error) {
	// A running instance is stopped through its halt global (vm.halt). WithCloseOnContextDone would also stop it, but
	// wazero compiles that as a call into Go on every loop iteration, so it is used only for a custom module (Wasm)
	// that instrumentHalt cannot rewrite, which wazero runs all the same.
	instrumented, err := instrumentHalt(wasm)
	haltable := err == nil
	if haltable {
		wasm = instrumented
	}
	config := wazero.NewRuntimeConfigCompiler().WithCloseOnContextDone(!haltable)
	if memoryLimit != 0 {
		// The heap limit bounds the instance's linear memory (plus the engine's own data). The memory is not reserved
		// at that size: wazero's slice grows by doubling on demand, so an idle session costs its live size, not the
		// limit (issue #211). Growth past the limit still fails inside the guest as before.
		pages := (uint64(memoryLimit) + linearMemoryHeadroom + wasmPageSize - 1) / wasmPageSize
		config = config.WithMemoryLimitPages(uint32(min(pages, 65536))).WithMemoryCapacityFromMax(false)
	}
	if cacheDir != "" {
		if cache, err := wazero.NewCompilationCacheWithDir(cacheDir); err == nil {
			config = config.WithCompilationCache(cache)
		}
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, config)
	e := &engine{runtime: runtime, haltable: haltable}
	fail := func(err error) (*engine, error) {
		_ = runtime.Close(ctx)
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return fail(err)
	}
	if err := e.instantiateHost(ctx); err != nil {
		return fail(err)
	}
	module, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		return fail(err)
	}
	e.module = module
	return e, nil
}

func (e *engine) close(ctx context.Context) error { return e.runtime.Close(ctx) }

// instantiateHost registers the `env` imports of quickjs-wasi. The guest reaches the host only through host_call
// (the one bridge function), host_interrupt (the interrupt flag) and inert stubs.
func (e *engine) instantiateHost(ctx context.Context) error {
	env := e.runtime.NewHostModuleBuilder("env")
	env.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, namePtr, nameLen, this, argc, argvPtr uint32) uint32 {
		vm := e.vmFor(m)
		if vm == nil {
			return 0
		}
		return vm.hostCall(namePtr, nameLen, this, argc, argvPtr)
	}).Export("host_call")
	env.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module) uint32 {
		if vm := e.vmFor(m); vm != nil && vm.shouldInterrupt() && !ignoreInterrupt.Load() {
			return 1
		}
		return 0
	}).Export("host_interrupt")
	env.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, promise, reason, _ uint32) {
		// No rejection handler is installed (qjs_set_promise_rejection_handler is never called), so the guest does not
		// reach here; free both values as quickjs-wasi does without a handler.
		if vm := e.vmFor(m); vm != nil {
			vm.free(promise)
			vm.free(reason)
		}
	}).Export("host_promise_rejection")
	env.NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_module_normalize")
	env.NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_module_load")
	env.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, hi, lo uint32) uint32 {
		// A deterministic VM runs in UTC (vm.ts: `timezoneOffset: 0`).
		if vm := e.vmFor(m); vm != nil && vm.clock != nil {
			return 0
		}
		return hostTimezoneOffset(hi, lo)
	}).Export("host_get_timezone_offset")
	_, err := env.Instantiate(ctx)
	return err
}

// hostTimezoneOffset is quickjs-wasi's default `timezoneOffset: "host"`, which upstream's worker keeps: the host's UTC
// offset in seconds east of UTC at the given time, which arrives as the high and low halves of a signed 64-bit count
// of seconds.
//
// upstream: quickjs-wasi 3.6.2 dist/index.js (hostGetTimezoneOffset, the default timezoneOffsetHandler).
func hostTimezoneOffset(hi, lo uint32) uint32 {
	_, offset := time.Unix(int64(int32(hi))<<32|int64(lo), 0).Zone()
	return uint32(int32(offset))
}

// hostMillis is the host's wall clock in whole milliseconds, which quickjs-wasi's WASI shim reports for both the
// realtime and the monotonic clock (`BigInt(Date.now()) * 1000000n`), so `Date.now()`, `performance.now()` and the
// seed of `Math.random()` follow the host clock as in Pi.
//
// upstream: quickjs-wasi 3.6.2 dist/wasi-shim.js (clock_time_get).
func hostMillis() int64 { return time.Now().UnixMilli() }

func hostWalltime() (sec int64, nsec int32) {
	ms := hostMillis()
	return ms / 1000, int32(ms%1000) * int32(time.Millisecond)
}

func hostNanotime() int64 { return hostMillis() * int64(time.Millisecond) }

// moduleConfig gives each instance the system access quickjs-wasi's WASI shim gives it: the host clock and a
// cryptographic random source (upstream: dist/wasi-shim.js clock_time_get and random_get). Standard output and error
// stay discarded (worker.ts discardOutput) and there is no file system.
//
// A deterministic VM (clock set) reads the virtual clock and a seeded PRNG instead (vm.ts: deterministicWasi).
func moduleConfig(clock *virtualClock, random *randomSource) wazero.ModuleConfig {
	// An empty name lets many instances of one module coexist.
	config := wazero.NewModuleConfig().WithStartFunctions("_initialize").WithName("")
	if clock != nil {
		return config.
			WithWalltime(func() (int64, int32) { n := clock.realtimeNanos(); return n / 1e9, int32(n % 1e9) }, sys.ClockResolution(time.Millisecond)).
			WithNanotime(clock.monotonicNanos, sys.ClockResolution(time.Millisecond)).
			WithRandSource(random)
	}
	return config.
		WithWalltime(hostWalltime, sys.ClockResolution(time.Millisecond)).
		WithNanotime(hostNanotime, sys.ClockResolution(time.Millisecond)).
		WithRandSource(rand.Reader)
}

func (e *engine) vmFor(m api.Module) *vm {
	if v, ok := e.vms.Load(m); ok {
		return v.(*vm)
	}
	return nil
}

// ignoreInterrupt makes host_interrupt report no interrupt, which stands for a script inside a native call that never
// reaches an interrupt poll. Only the lifetime tests set it.
var ignoreInterrupt atomic.Bool

// liveVMs counts open wasm instances and hardStops counts instances halted under a running script because the
// interrupt flag did not stop it in time. They let the lifetime tests observe what a result cannot show.
var liveVMs, hardStops atomic.Int64

// wazeroInstance is the wazero module instance of one vm.
type wazeroInstance struct {
	engine *engine
	ctx    context.Context
	mod    api.Module
	halted api.MutableGlobal
	cancel context.CancelFunc
	closed bool
	// idle holds, per export, the api.Function values no call is running. wazero builds a new call engine, stack included, on every
	// ExportedFunction, and a script's host calls reach several exports each, so a call reuses an idle one. A call made while another of
	// the same export runs (a host function the export called re-entering it) takes another value, because one value's stack serves
	// one call at a time.
	idleMu sync.Mutex
	idle   map[string][]api.Function
}

// newVM instantiates the module, applies the limits and installs the interrupt handler.
// ctx is passed to every call into the instance; vm.halt stops a script the interrupt handler cannot reach.
func (e *engine) newVM(ctx context.Context, options vmOptions) (result *vm, err error) {
	v := &vm{shouldInterrupt: options.shouldInterrupt, clock: options.clock, callbacks: map[string]func(uint32, []uint32) uint32{}}
	inst := &wazeroInstance{engine: e}
	v.inst = inst
	if !e.haltable {
		ctx, inst.cancel = context.WithCancel(ctx)
	}
	inst.ctx = ctx
	defer func() {
		if r := recover(); r != nil {
			trap, ok := r.(vmTrap)
			if !ok {
				panic(r)
			}
			v.close()
			result, err = nil, trap
		}
	}()
	var random *randomSource
	if options.clock != nil {
		random = newRandomSource(options.clock.seed)
	}
	mod, err := e.runtime.InstantiateModule(ctx, e.module, moduleConfig(options.clock, random))
	if err != nil {
		v.close()
		return nil, err
	}
	inst.mod = mod
	liveVMs.Add(1)
	e.vms.Store(mod, v)
	if e.haltable {
		halted, ok := mod.ExportedGlobal(haltExport).(api.MutableGlobal)
		if !ok {
			v.close()
			return nil, errors.New("the module does not export its halt global")
		}
		inst.halted = halted
	}
	v.configure(options)
	return v, nil
}

// clockChanged does nothing: the instance's clock functions read the virtual clock themselves.
func (i *wazeroInstance) clockChanged(*virtualClock) {}

// halt makes the instance trap at its next loop iteration, whatever native code the script is in. An instance of a
// module without the halt global is closed by wazero at its next loop iteration instead.
func (i *wazeroInstance) halt() {
	if i.halted != nil {
		i.halted.Set(1)
		return
	}
	i.cancel()
}

func (i *wazeroInstance) close() {
	if i.cancel != nil {
		defer i.cancel()
	}
	if i.closed || i.mod == nil {
		return
	}
	i.closed = true
	liveVMs.Add(-1)
	i.engine.vms.Delete(i.mod)
	//portlint:allow ctxbackground releasing the module instance must finish whatever cancelled the run; wazero's Close takes a context only to run close hooks
	_ = i.mod.Close(context.Background())
}

func (i *wazeroInstance) call(name string, args ...uint64) (uint64, error) {
	i.idleMu.Lock()
	var function api.Function
	if idle := i.idle[name]; len(idle) > 0 {
		function, i.idle[name] = idle[len(idle)-1], idle[:len(idle)-1]
	}
	i.idleMu.Unlock()
	if function == nil {
		function = i.mod.ExportedFunction(name)
	}
	out, err := function.Call(i.ctx, args...)
	var result uint64
	if err == nil && len(out) > 0 {
		result = out[0]
	}
	i.idleMu.Lock()
	if i.idle == nil {
		i.idle = map[string][]api.Function{}
	}
	i.idle[name] = append(i.idle[name], function)
	i.idleMu.Unlock()
	return result, err
}

func (i *wazeroInstance) read(offset, n uint32) ([]byte, bool) { return i.mod.Memory().Read(offset, n) }

func (i *wazeroInstance) readString(offset, n uint32) (string, bool) {
	data, ok := i.mod.Memory().Read(offset, n)
	return string(data), ok
}

func (i *wazeroInstance) write(offset uint32, data []byte) bool {
	return i.mod.Memory().Write(offset, data)
}

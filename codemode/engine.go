package codemode

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// maxStackSize is the VM stack guard; without it deep recursion overflows the wasm stack and traps instead of
// throwing a catchable RangeError.
//
// Ports quickjs-wasi (MAX_STACK_SIZE) as used by packages/codemode/src/runtime/worker.ts.
const maxStackSize = 512 * 1024

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
		// The heap limit bounds the instance, so its linear memory is reserved once at that size (plus the engine's own
		// data): growing it a page at a time would copy the whole memory on every step.
		pages := (uint64(memoryLimit) + linearMemoryHeadroom + wasmPageSize - 1) / wasmPageSize
		config = config.WithMemoryLimitPages(uint32(min(pages, 65536))).WithMemoryCapacityFromMax(true)
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
		if vm := e.vmFor(m); vm != nil && vm.interrupt.Load() && !ignoreInterrupt.Load() {
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
	env.NewFunctionBuilder().WithFunc(hostTimezoneOffset).Export("host_get_timezone_offset")
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
func moduleConfig() wazero.ModuleConfig {
	// An empty name lets many instances of one module coexist.
	return wazero.NewModuleConfig().WithStartFunctions("_initialize").WithName("").
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

// vmTrap is the panic value that carries a failed call into the wasm instance up to the execution that owns the VM.
type vmTrap struct{ err error }

func (t vmTrap) Error() string { return t.err.Error() }

// vm is one QuickJS VM: one wasm instance, used by one goroutine at a time.
type vm struct {
	engine    *engine
	ctx       context.Context
	mod       api.Module
	halted    api.MutableGlobal
	cancel    context.CancelFunc
	interrupt *atomic.Bool
	callbacks map[string]func(this uint32, args []uint32) uint32
	undefined uint32
	closed    bool
}

// newVM instantiates the module, applies the limits and installs the interrupt handler. memoryLimit zero means none.
// ctx is passed to every call into the instance; vm.halt stops a script the interrupt handler cannot reach.
func (e *engine) newVM(ctx context.Context, memoryLimit uint32, interrupt *atomic.Bool) (result *vm, err error) {
	v := &vm{engine: e, interrupt: interrupt, callbacks: map[string]func(uint32, []uint32) uint32{}}
	if !e.haltable {
		ctx, v.cancel = context.WithCancel(ctx)
	}
	v.ctx = ctx
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
	mod, err := e.runtime.InstantiateModule(ctx, e.module, moduleConfig())
	if err != nil {
		v.close()
		return nil, err
	}
	v.mod = mod
	liveVMs.Add(1)
	e.vms.Store(mod, v)
	if e.haltable {
		halted, ok := mod.ExportedGlobal(haltExport).(api.MutableGlobal)
		if !ok {
			v.close()
			return nil, errors.New("the module does not export its halt global")
		}
		v.halted = halted
	}
	v.call("qjs_init")
	if memoryLimit != 0 {
		v.call("qjs_set_memory_limit", uint64(memoryLimit))
	}
	v.call("qjs_set_max_stack_size", maxStackSize)
	v.call("qjs_set_interrupt_handler", 1)
	v.undefined = uint32(v.call("qjs_get_undefined"))
	return v, nil
}

// halt makes the instance trap at its next loop iteration, whatever native code the script is in. An instance of a
// module without the halt global is closed by wazero at its next loop iteration instead.
func (v *vm) halt() {
	if v.halted != nil {
		v.halted.Set(1)
		return
	}
	v.cancel()
}

func (v *vm) close() {
	if v.cancel != nil {
		defer v.cancel()
	}
	if v.closed || v.mod == nil {
		return
	}
	v.closed = true
	liveVMs.Add(-1)
	v.engine.vms.Delete(v.mod)
	_ = v.mod.Close(context.Background())
}

func (v *vm) call(name string, args ...uint64) uint64 {
	out, err := v.mod.ExportedFunction(name).Call(v.ctx, args...)
	if err != nil {
		panic(vmTrap{fmt.Errorf("%s: %w", name, err)})
	}
	if len(out) == 0 {
		return 0
	}
	return out[0]
}

func (v *vm) free(handle uint32) { v.call("qjs_free_value", uint64(handle)) }

func (v *vm) malloc(n int) uint32 {
	p := uint32(v.call("wasm_malloc", uint64(n)))
	if p == 0 {
		panic(vmTrap{fmt.Errorf("wasm_malloc(%d) failed", n)})
	}
	return p
}

func (v *vm) write(offset uint32, data []byte) {
	if !v.mod.Memory().Write(offset, data) {
		panic(vmTrap{fmt.Errorf("memory write at %d out of range", offset)})
	}
}

// str copies s into guest memory as a NUL-terminated string; the caller frees the pointer.
func (v *vm) str(s string) (ptr, n uint32) {
	p := v.malloc(len(s) + 1)
	v.write(p, append([]byte(s), 0))
	return p, uint32(len(s))
}

func (v *vm) newString(s string) uint32 {
	p, n := v.str(s)
	handle := uint32(v.call("qjs_new_string", uint64(p), uint64(n)))
	v.call("wasm_free", uint64(p))
	return handle
}

func (v *vm) newNumber(f float64) uint32 {
	return uint32(v.call("qjs_new_number", math.Float64bits(f)))
}

func (v *vm) toNumber(handle uint32) float64 {
	return math.Float64frombits(v.call("qjs_get_float64", uint64(handle)))
}

func (v *vm) isUndefined(handle uint32) bool { return v.call("qjs_is_undefined", uint64(handle)) != 0 }

func (v *vm) toBool(handle uint32) bool { return v.call("qjs_get_bool", uint64(handle)) != 0 }

// toString reads a QuickJS string. The engine stores unpaired surrogates as three-byte WTF-8 sequences, which Go
// strings cannot hold, so each becomes U+FFFD.
func (v *vm) toString(handle uint32) string {
	lenPtr := v.malloc(4)
	defer v.call("wasm_free", uint64(lenPtr))
	p := uint32(v.call("qjs_get_string_len", uint64(handle), uint64(lenPtr)))
	if p == 0 {
		return "<null>"
	}
	size, ok := v.mod.Memory().ReadUint32Le(lenPtr)
	if !ok {
		panic(vmTrap{fmt.Errorf("string length read out of range")})
	}
	data, ok := v.mod.Memory().Read(p, size)
	if !ok {
		panic(vmTrap{fmt.Errorf("string read out of range")})
	}
	return strings.ToValidUTF8(string(data), "\uFFFD")
}

func (v *vm) getProp(handle uint32, name string) uint32 {
	p, _ := v.str(name)
	prop := uint32(v.call("qjs_get_prop_string", uint64(handle), uint64(p)))
	v.call("wasm_free", uint64(p))
	return prop
}

// jsException is a script exception read out of the VM: JSException in quickjs-wasi.
type jsException struct{ name, message, stack string }

// takeException reads and frees the pending exception.
func (v *vm) takeException() jsException {
	exception := uint32(v.call("qjs_get_exception"))
	defer v.free(exception)
	prop := func(name string) (string, bool) {
		h := v.getProp(exception, name)
		defer v.free(h)
		if v.isUndefined(h) {
			return "", false
		}
		return v.toString(h), true
	}
	name, ok := prop("name")
	if !ok {
		name = "Error"
	}
	message, ok := prop("message")
	if !ok {
		message = v.toString(exception)
	}
	stack, _ := prop("stack")
	return jsException{name: name, message: message, stack: stack}
}

// checked returns the handle, or the pending exception if the handle is one (throwIfException).
func (v *vm) checked(handle uint32) (uint32, *jsException) {
	if v.call("qjs_is_exception", uint64(handle)) == 0 {
		return handle, nil
	}
	v.free(handle)
	exception := v.takeException()
	return 0, &exception
}

func (v *vm) eval(code, filename string) (uint32, *jsException) {
	codePtr, codeLen := v.str(code)
	filePtr, _ := v.str(filename)
	handle := uint32(v.call("qjs_eval", uint64(codePtr), uint64(codeLen), uint64(filePtr), 0))
	v.call("wasm_free", uint64(codePtr))
	v.call("wasm_free", uint64(filePtr))
	return v.checked(handle)
}

// callFunction calls fn; the caller owns and frees the result.
func (v *vm) callFunction(fn, this uint32, args ...uint32) (uint32, *jsException) {
	var argv uint32
	if len(args) > 0 {
		argv = v.malloc(4 * len(args))
		buf := make([]byte, 4*len(args))
		for i, a := range args {
			binary.LittleEndian.PutUint32(buf[4*i:], a)
		}
		v.write(argv, buf)
	}
	handle := uint32(v.call("qjs_call", uint64(fn), uint64(this), uint64(len(args)), uint64(argv)))
	if argv != 0 {
		v.call("wasm_free", uint64(argv))
	}
	return v.checked(handle)
}

// newFunction creates a guest function that calls fn. fn's handles are borrowed: it must not free them.
func (v *vm) newFunction(name string, fn func(this uint32, args []uint32) uint32) uint32 {
	v.callbacks[name] = fn
	p, n := v.str(name)
	handle := uint32(v.call("qjs_new_host_function", uint64(p), uint64(n), 0))
	v.call("wasm_free", uint64(p))
	return handle
}

// executePendingJobs runs the promise job queue; a failing job is reported like quickjs-wasi's executePendingJobs.
func (v *vm) executePendingJobs() *jsException {
	for v.call("qjs_is_job_pending") != 0 {
		if int32(v.call("qjs_execute_pending_job")) < 0 {
			exception := v.takeException()
			return &jsException{name: "Error", message: "Job execution error: " + exception.String()}
		}
	}
	return nil
}

// String is JSException's toString: the exception as `name: message`, or the name alone.
func (e jsException) String() string {
	if e.message == "" {
		return e.name
	}
	return e.name + ": " + e.message
}

// hostCall dispatches the guest's call of a function made by newFunction.
func (v *vm) hostCall(namePtr, nameLen, this, argc, argvPtr uint32) uint32 {
	mem := v.mod.Memory()
	name, _ := mem.Read(namePtr, nameLen)
	callback := v.callbacks[string(name)]
	args := make([]uint32, argc)
	for i := range args {
		args[i], _ = mem.ReadUint32Le(argvPtr + uint32(i)*4)
	}
	if callback == nil {
		return 0
	}
	return uint32(v.call("qjs_dup_value", uint64(callback(this, args))))
}

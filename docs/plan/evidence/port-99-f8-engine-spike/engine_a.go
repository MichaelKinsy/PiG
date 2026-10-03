package main

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed quickjs-wasi-3.6.2.wasm
var quickjsWasm []byte

const maxStackSize = 512 * 1024 // quickjs-wasi index.js:71 MAX_STACK_SIZE

type engineA struct {
	rt   wazero.Runtime
	code wazero.CompiledModule
	vms  sync.Map // api.Module -> *vmA
}

func (e *engineA) vmFor(m api.Module) *vmA {
	if v, ok := e.vms.Load(m); ok {
		return v.(*vmA)
	}
	return nil
}

func (e *engineA) hostModule(ctx context.Context) error {
	env := e.rt.NewHostModuleBuilder("env")
	env.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, namePtr, nameLen, this, argc, argvPtr uint32) uint32 {
		v := e.vmFor(m)
		nameB, _ := m.Memory().Read(namePtr, nameLen)
		cb := v.callbacks[string(nameB)]
		args := make([]uint32, argc)
		for i := range args {
			args[i], _ = m.Memory().ReadUint32Le(argvPtr + uint32(i)*4)
		}
		return uint32(v.call("qjs_dup_value", uint64(cb(this, args))))
	}).Export("host_call")
	env.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module) uint32 {
		if v := e.vmFor(m); v != nil && v.interrupt.Load() {
			return 1
		}
		return 0
	}).Export("host_interrupt")
	env.NewFunctionBuilder().WithFunc(func(uint32, uint32, uint32) {}).Export("host_promise_rejection")
	env.NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_module_normalize")
	env.NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_module_load")
	env.NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_get_timezone_offset")
	_, err := env.Instantiate(ctx)
	return err
}

func newEngineA(ctx context.Context, cacheDir string) (*engineA, error) {
	cfg := wazero.NewRuntimeConfigCompiler()
	if cacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return nil, err
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, err
	}
	e := &engineA{rt: rt}
	if err := e.hostModule(ctx); err != nil {
		return nil, err
	}
	code, err := rt.CompileModule(ctx, quickjsWasm)
	if err != nil {
		return nil, err
	}
	e.code = code
	return e, nil
}

// vmA is one QuickJS VM: one wasm instance per execution, as worker.ts creates one per worker.
type vmA struct {
	mod       api.Module
	interrupt atomic.Bool
	callbacks map[string]func(this uint32, args []uint32) uint32
	undefined uint32
}

func (v *vmA) call(name string, args ...uint64) uint64 {
	out, err := v.mod.ExportedFunction(name).Call(context.Background(), args...)
	if err != nil {
		panic(fmt.Errorf("%s: %w", name, err))
	}
	if len(out) == 0 {
		return 0
	}
	return out[0]
}

func (v *vmA) str(s string) (ptr uint32, n uint32) {
	p := uint32(v.call("wasm_malloc", uint64(len(s)+1)))
	v.mod.Memory().Write(p, append([]byte(s), 0))
	return p, uint32(len(s))
}

func (v *vmA) newString(s string) uint32 {
	p, n := v.str(s)
	r := uint32(v.call("qjs_new_string", uint64(p), uint64(n)))
	v.call("wasm_free", uint64(p))
	return r
}

func (v *vmA) toString(h uint32) string {
	lp := uint32(v.call("wasm_malloc", 4))
	defer v.call("wasm_free", uint64(lp))
	sp := uint32(v.call("qjs_get_string_len", uint64(h), uint64(lp)))
	if sp == 0 {
		return "<null>"
	}
	n, _ := v.mod.Memory().ReadUint32Le(lp)
	b, _ := v.mod.Memory().Read(sp, n)
	return string(b)
}

func (v *vmA) getProp(h uint32, name string) uint32 {
	p, _ := v.str(name)
	r := uint32(v.call("qjs_get_prop_string", uint64(h), uint64(p)))
	v.call("wasm_free", uint64(p))
	return r
}

func (v *vmA) exception() string {
	exc := uint32(v.call("qjs_get_exception"))
	name, msg, stack := v.getProp(exc, "name"), v.getProp(exc, "message"), v.getProp(exc, "stack")
	return fmt.Sprintf("%s: %s", v.toString(name), v.toString(msg)) + "|" + v.toString(stack)
}

func (v *vmA) checked(h uint32) (uint32, error) {
	if v.call("qjs_is_exception", uint64(h)) != 0 {
		return 0, fmt.Errorf("%s", v.exception())
	}
	return h, nil
}

func (v *vmA) eval(code, file string) (uint32, error) {
	cp, cn := v.str(code)
	fp, _ := v.str(file)
	r := uint32(v.call("qjs_eval", uint64(cp), uint64(cn), uint64(fp), 0))
	v.call("wasm_free", uint64(cp))
	v.call("wasm_free", uint64(fp))
	return v.checked(r)
}

func (v *vmA) callFn(fn, this uint32, args ...uint32) (uint32, error) {
	var argv uint32
	if len(args) > 0 {
		argv = uint32(v.call("wasm_malloc", uint64(4*len(args))))
		buf := make([]byte, 4*len(args))
		for i, a := range args {
			binary.LittleEndian.PutUint32(buf[4*i:], a)
		}
		v.mod.Memory().Write(argv, buf)
	}
	r := uint32(v.call("qjs_call", uint64(fn), uint64(this), uint64(len(args)), uint64(argv)))
	if argv != 0 {
		v.call("wasm_free", uint64(argv))
	}
	return v.checked(r)
}

func (v *vmA) newFunction(name string, fn func(this uint32, args []uint32) uint32) uint32 {
	v.callbacks[name] = fn
	p, n := v.str(name)
	r := uint32(v.call("qjs_new_host_function", uint64(p), uint64(n), 0))
	v.call("wasm_free", uint64(p))
	return r
}

func (v *vmA) drain() error {
	for v.call("qjs_is_job_pending") != 0 {
		if int32(v.call("qjs_execute_pending_job")) < 0 {
			return fmt.Errorf("job: %s", v.exception())
		}
	}
	return nil
}

func (e *engineA) newVM(ctx context.Context, memoryLimit uint32) (*vmA, error) {
	v := &vmA{callbacks: map[string]func(uint32, []uint32) uint32{}}
	mod, err := e.rt.InstantiateModule(ctx, e.code, wazero.NewModuleConfig().WithStartFunctions("_initialize").WithName(""))
	if err != nil {
		return nil, err
	}
	v.mod = mod
	e.vms.Store(mod, v)
	v.call("qjs_init")
	v.call("qjs_set_memory_limit", uint64(memoryLimit))
	v.call("qjs_set_max_stack_size", maxStackSize)
	v.call("qjs_set_interrupt_handler", 1)
	v.undefined = uint32(v.call("qjs_get_undefined"))
	return v, nil
}

func (e *engineA) closeVM(ctx context.Context, v *vmA) { e.vms.Delete(v.mod); v.mod.Close(ctx) }

var _ = time.Now

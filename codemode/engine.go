package codemode

import (
	"encoding/binary"
	"fmt"
	"math"
)

// maxStackSize is the VM stack guard; without it deep recursion overflows the wasm stack and traps instead of
// throwing a catchable RangeError.
//
// Ports quickjs-wasi (MAX_STACK_SIZE) as used by packages/codemode/src/runtime/worker.ts.
const maxStackSize = 512 * 1024

// instance is the module instance one vm drives: calls of the module's exports and its linear memory. The wazero
// instance of the process (engine_wazero.go) and the Worker host's QuickJS instance (engine_hostimport.go) implement it.
type instance interface {
	// call runs the export with raw 64-bit arguments (a double travels as its bits) and returns the raw result, zero
	// for none. An error is a trap.
	call(name string, args ...uint64) (uint64, error)
	// read copies n bytes at offset out of linear memory.
	read(offset, n uint32) ([]byte, bool)
	// readString is read as a string, for an instance that can hand over the bytes it copied without copying them again.
	readString(offset, n uint32) (string, bool)
	write(offset uint32, data []byte) bool
	// clockChanged tells an instance whose clock the host reads (the Worker host's QuickJS) that the virtual clock of a
	// deterministic VM changed; an instance that reads the clock itself ignores it.
	clockChanged(c *virtualClock)
	// halt makes the instance trap at its next loop iteration.
	halt()
	// close releases the instance; it may be called more than once and on a partly built instance.
	close()
}

// vmTrap is the panic value that carries a failed call into the wasm instance up to the execution that owns the VM.
type vmTrap struct{ err error }

func (t vmTrap) Error() string { return t.err.Error() }

// vm is one QuickJS VM: one module instance, used by one goroutine at a time.
type vm struct {
	inst instance
	// shouldInterrupt is polled by QuickJS while a script runs and by the job drain between batches of jobs; true raises an
	// uncatchable error in the script.
	shouldInterrupt func() bool
	callbacks       map[string]func(this uint32, args []uint32) uint32
	undefined       uint32
	// clock is the virtual clock of a deterministic VM.
	clock *virtualClock
}

// vmOptions are the limits and the interrupt of one VM.
//
// Ports packages/codemode/src/runtime/vm.ts (StartVmOptions: maxStackSize, shouldInterrupt) and VmStartData.memoryLimitBytes.
type vmOptions struct {
	// memoryLimit is the QuickJS heap limit; zero means none.
	memoryLimit uint32
	// maxStackSize is QuickJS's guard on its own stack, which turns deep recursion into a catchable RangeError. The wasm
	// frames also use the host's native stack, so the guard must trip before that stack runs out. Zero means maxStackSize.
	maxStackSize uint32
	// shouldInterrupt is polled while a script runs.
	shouldInterrupt func() bool
	// clock, when set, makes the VM deterministic: its WASI clocks and randomness are virtual, its timezone is UTC and it has no
	// WeakRef (deterministic.go).
	clock *virtualClock
}

// configure applies the limits and installs the interrupt handler of a fresh instance.
func (v *vm) configure(options vmOptions) {
	if options.clock != nil {
		v.call("qjs_init2", uint64(deterministicIntrinsics))
	} else {
		v.call("qjs_init")
	}
	if options.memoryLimit != 0 {
		v.call("qjs_set_memory_limit", uint64(options.memoryLimit))
	}
	stack := options.maxStackSize
	if stack == 0 {
		stack = maxStackSize
	}
	v.call("qjs_set_max_stack_size", uint64(stack))
	v.call("qjs_set_interrupt_handler", 1)
	v.undefined = uint32(v.call("qjs_get_undefined"))
}

// syncClock tells the instance the virtual clock's current readings; a VM without a clock has nothing to tell.
func (v *vm) syncClock() {
	if v.clock != nil {
		v.inst.clockChanged(v.clock)
	}
}

// quickjsVersion is the QuickJS engine version the module reports, a NUL-terminated string in its memory.
func (v *vm) quickjsVersion() string {
	ptr := uint32(v.call("qjs_get_quickjs_version"))
	var out []byte
	for {
		chunk, ok := v.inst.read(ptr+uint32(len(out)), 1)
		if !ok || len(chunk) == 0 || chunk[0] == 0 {
			return string(out)
		}
		out = append(out, chunk[0])
	}
}

// halt makes the instance trap at its next loop iteration, whatever native code the script is in.
func (v *vm) halt() { v.inst.halt() }

// close releases the instance. A vm whose instance was never made has nothing to release.
func (v *vm) close() {
	if v.inst != nil {
		v.inst.close()
	}
}

func (v *vm) call(name string, args ...uint64) uint64 {
	out, err := v.inst.call(name, args...)
	if err != nil {
		panic(vmTrap{fmt.Errorf("%s: %w", name, err)})
	}
	return out
}

// readUint32 reads a little-endian uint32 from linear memory.
func (v *vm) readUint32(offset uint32) (uint32, bool) {
	b, ok := v.inst.read(offset, 4)
	if !ok || len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
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
	if !v.inst.write(offset, data) {
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

// toString reads a QuickJS string. The engine stores unpaired surrogates as three-byte WTF-8 sequences, which the
// returned Go string keeps, as the JavaScript string keeps a lone unit.
func (v *vm) toString(handle uint32) string {
	lenPtr := v.malloc(4)
	defer v.call("wasm_free", uint64(lenPtr))
	p := uint32(v.call("qjs_get_string_len", uint64(handle), uint64(lenPtr)))
	if p == 0 {
		return "<null>"
	}
	size, ok := v.readUint32(lenPtr)
	if !ok {
		panic(vmTrap{fmt.Errorf("string length read out of range")})
	}
	text, ok := v.inst.readString(p, size)
	if !ok {
		panic(vmTrap{fmt.Errorf("string read out of range")})
	}
	return text
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

// jobsPerPoll is how many promise jobs run between two polls of the interrupt while the queue drains.
//
// Ports packages/codemode/src/runtime/vm.ts (JOBS_PER_POLL).
const jobsPerPoll = 50

// executePendingJobs runs the promise job queue one job at a time and polls shouldInterrupt after every jobsPerPoll
// jobs. A script such as `while (true) await null` is a chain of jobs that each run a few opcodes, so QuickJS's own
// poll, which counts opcodes, would let the queue drain for a long time between polls. A job fails only for an
// uncatchable error such as an interrupt.
//
// Ports packages/codemode/src/runtime/vm.ts (startVm, drain).
func (v *vm) executePendingJobs(shouldInterrupt func() bool) *jsException {
	jobs := 0
	for v.call("qjs_is_job_pending") != 0 {
		if int32(v.call("qjs_execute_pending_job")) < 0 {
			return &jsException{name: "Error", message: "Promise job failed"}
		}
		if jobs++; jobs%jobsPerPoll == 0 && shouldInterrupt() {
			return &jsException{name: "Error", message: "Execution interrupted"}
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
	name, _ := v.inst.read(namePtr, nameLen)
	callback := v.callbacks[string(name)]
	args := make([]uint32, argc)
	for i := range args {
		args[i], _ = v.readUint32(argvPtr + uint32(i)*4)
	}
	if callback == nil {
		return 0
	}
	return uint32(v.call("qjs_dup_value", uint64(callback(this, args))))
}

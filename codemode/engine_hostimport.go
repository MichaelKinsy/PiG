//go:build tinygo

package codemode

import (
	"context"
	"errors"
	"unsafe"
)

// The imports of the codemode module: QuickJS runs in the Worker host (durable/core/host/cf/src/codemode/quickjs.ts),
// which is the only thing the module can reach. Every import is synchronous, as TinyGo's -scheduler=none requires; a
// failure of the host (a trap in QuickJS, an access outside its memory) is a JavaScript exception that unwinds this
// module's frames, so no import reports an error.

// qjsCall calls the QuickJS export at index fn of qjsExports with argc raw 64-bit arguments and returns the raw result.
//
//go:wasmimport qjs call
func qjsCall(fn, argc uint32, a0, a1, a2, a3 uint64) uint64

// qjsMemRead copies n bytes of QuickJS memory at ptr to dst.
//
//go:wasmimport qjs mem_read
func qjsMemRead(ptr, n uint32, dst unsafe.Pointer)

// qjsMemWrite copies n bytes at src to QuickJS memory at ptr.
//
//go:wasmimport qjs mem_write
func qjsMemWrite(ptr uint32, src unsafe.Pointer, n uint32)

// qjsSetClock gives the host's QuickJS instance the readings of a deterministic VM's virtual clock, in nanoseconds: the
// realtime clock (WASI id 0), every other clock, and whether it reads them (virtual) or the host's clock.
//
//go:wasmimport qjs set_clock
func qjsSetClock(realtime, monotonic int64, virtual uint32)

// qjsSetRandomSeed makes the host's QuickJS instance draw random_get from the PRNG that starts at seed (deterministic.go: prng).
//
//go:wasmimport qjs set_random_seed
func qjsSetRandomSeed(seed uint32)

// maxQJSArgs is the most arguments a QuickJS export takes (qjs_eval, qjs_call).
const maxQJSArgs = 4

var qjsExportIndex = func() map[string]uint32 {
	index := make(map[string]uint32, len(qjsExports))
	for i, name := range qjsExports {
		index[name] = uint32(i)
	}
	return index
}()

// engine stands for the QuickJS module the host holds: there is one instance per module instance of this program, made by
// the host before it enters.
type engine struct{}

// hostEngine is the engine of the one VM of this program, the target of the host's callbacks.
var (
	hostEngine = &engine{}
	// current is the VM the host's callbacks (host_call, host_interrupt) belong to.
	current *vm
)

// hostInstance is the host's QuickJS instance of one vm.
type hostInstance struct{ closed bool }

// newVM binds a vm to the host's QuickJS instance and applies the limits. A program runs one execution, so one vm at a time.
func (e *engine) newVM(_ context.Context, options vmOptions) (*vm, error) {
	if current != nil {
		return nil, errors.New("the QuickJS host holds one VM")
	}
	v := &vm{inst: &hostInstance{}, shouldInterrupt: options.shouldInterrupt, clock: options.clock, callbacks: map[string]func(uint32, []uint32) uint32{}}
	current = v
	if options.clock != nil {
		qjsSetRandomSeed(options.clock.seed ^ randomSeedMask)
		v.syncClock()
	}
	v.configure(options)
	return v, nil
}

// clockChanged hands the host's QuickJS instance the clock's current readings.
func (i *hostInstance) clockChanged(c *virtualClock) {
	qjsSetClock(c.realtimeNanos(), c.monotonicNanos(), 1)
}

func (i *hostInstance) call(name string, args ...uint64) (uint64, error) {
	index, ok := qjsExportIndex[name]
	if !ok {
		return 0, errors.New("not an export the QuickJS host serves")
	}
	if len(args) > maxQJSArgs {
		return 0, errors.New("too many arguments")
	}
	var a [maxQJSArgs]uint64
	copy(a[:], args)
	return qjsCall(index, uint32(len(args)), a[0], a[1], a[2], a[3]), nil
}

func (i *hostInstance) read(offset, n uint32) ([]byte, bool) {
	out := make([]byte, n)
	if n > 0 {
		qjsMemRead(offset, n, unsafe.Pointer(&out[0])) //nolint:gosec // G103: the import copies into this buffer
	}
	return out, true
}

// readString hands the copied bytes over as the string: the copy is the only one, which matters for a script's megabytes of output in a
// module with a 96 MiB heap.
func (i *hostInstance) readString(offset, n uint32) (string, bool) {
	if n == 0 {
		return "", true
	}
	data, _ := i.read(offset, n)
	return unsafe.String(&data[0], len(data)), true //nolint:gosec // G103: the slice was just made here and is never written again; the string takes it over
}

func (i *hostInstance) write(offset uint32, data []byte) bool {
	if len(data) > 0 {
		qjsMemWrite(offset, unsafe.Pointer(&data[0]), uint32(len(data))) //nolint:gosec // G103: the import copies out of this buffer
	}
	return true
}

// halt does nothing: the host drops the instance, which stops it, when it stops an execution it cannot interrupt.
func (i *hostInstance) halt() {}

func (i *hostInstance) close() {
	if !i.closed {
		i.closed = true
		current = nil
	}
}

// hostCallOf and hostInterruptOf are what the exports of the module (serve_tinygo.go) forward the host's callbacks to.
func hostCallOf(namePtr, nameLen, this, argc, argvPtr uint32) uint32 {
	if current == nil {
		return 0
	}
	return current.hostCall(namePtr, nameLen, this, argc, argvPtr)
}

func hostInterruptOf() uint32 {
	if current != nil && current.shouldInterrupt != nil && current.shouldInterrupt() {
		return 1
	}
	return 0
}

// engineLoader returns the module for VMs with the given heap limit; the host's instance is the only one.
type engineLoader func(ctx context.Context, memoryLimit uint32) (*engine, error)

//go:build tinygo.wasm

package runtime

import (
	"unsafe"
)

const GOARCH = "wasm"

// The bitness of the CPU (e.g. 8, 32, 64).
const TargetBits = 32

// zeroSizedAlloc a sentinel that gets returned when allocating 0 bytes.
// Using this instead of a constant value since I can't easily find a memory
// location that is definitely not going to end up as a valid pointer.
var zeroSizedAlloc uint8

var zeroSizeAllocPtr = &zeroSizedAlloc

const deferExtraRegs = 0

const callInstSize = 1 // unknown and irrelevant (llvm.returnaddress doesn't work), so make something up

//go:extern __heap_base
var heapStartSymbol [0]byte

//go:extern __global_base
var globalsStartSymbol [0]byte

const (
	// wasmMemoryIndex is always zero until the multi-memory feature is used.
	//
	// See https://github.com/WebAssembly/multi-memory
	wasmMemoryIndex = 0

	// wasmPageSize is the size of a page in WebAssembly's 32-bit memory. This
	// is also its only unit of change.
	//
	// See https://www.w3.org/TR/wasm-core-1/#page-size
	wasmPageSize = 64 * 1024
)

// wasm_memory_size invokes the "memory.size" instruction, which returns the
// current size to the memory at the given index (always wasmMemoryIndex), in
// pages.
//
//export llvm.wasm.memory.size.i32
func wasm_memory_size(index int32) int32

// wasm_memory_grow invokes the "memory.grow" instruction, which attempts to
// increase the size of the memory at the given index (always wasmMemoryIndex),
// by the delta (in pages). This returns the previous size on success of -1 on
// failure.
//
//export llvm.wasm.memory.grow.i32
func wasm_memory_grow(index int32, delta int32) int32

var (
	// heapStart is the current memory offset which starts the heap. The heap
	// extends from this offset until heapEnd (exclusive).
	heapStart = uintptr(unsafe.Pointer(&heapStartSymbol))

	// heapEnd is the current memory length in bytes.
	heapEnd = uintptr(wasm_memory_size(wasmMemoryIndex) * wasmPageSize)

	globalsStart = uintptr(unsafe.Pointer(&globalsStartSymbol))
	globalsEnd   = uintptr(unsafe.Pointer(&heapStartSymbol))

	stackTop = uintptr(unsafe.Pointer(&globalsStartSymbol))
)

func align(ptr uintptr) uintptr {
	// Align to 16, which is the alignment of max_align_t:
	// https://godbolt.org/z/dYqTsWrGq
	const heapAlign = 16
	return (ptr + heapAlign - 1) &^ (heapAlign - 1)
}

//export tinygo_getCurrentStackPointer
func getCurrentStackPointer() uintptr

// savedStackPointer is used to save the system stack pointer while in a goroutine.
var savedStackPointer uintptr

// saveStackPointer is called by internal/task.state.Resume() to save the stack pointer before entering a goroutine from the system stack.
func saveStackPointer() {
	savedStackPointer = getCurrentStackPointer()
}

// growHeap tries to grow the heap size. It returns true if it succeeds, false
// otherwise.
//
// PiG Durable core fork: stock TinyGo doubles the heap on every call, so the heap
// can only reach a ceiling that is its initial size times a power of two, and the
// last step can reserve as much memory as the program has used in total. This
// version grows by a fixed step, growHeapStepKiB (-ldflags "-X
// runtime.growHeapStepKiB=<KiB>", rounded up to whole Wasm pages; 4 MiB without
// it). A step that passes the module's maximum memory halves until it fits, so the
// heap ends within one minimum step (below) of the maximum; when none fits growHeap
// returns false and the allocator stops with runtimeFatal("out of memory"), a trap.
//
// Two rules keep the allocator correct with a step smaller than the heap:
//
//   - setHeapEnd copies the old GC metadata to its new place with memcpy, which needs
//     the two to be disjoint: a step is at least 1/64 of the heap (the metadata is
//     1/65 of it), and a step below that is not taken.
//   - the allocator does not join the free range a step adds to the free range before
//     it, so an allocation larger than the step is never satisfied by repeated steps.
//     The calls made for one allocation (gcMallocs unchanged) double the step each
//     time until the range is big enough.
func growHeap() bool {
	const pageSize = wasmPageSize

	if gcMallocs == growHeapMallocs {
		if growHeapStreak < 16 {
			growHeapStreak++
		}
	} else {
		growHeapStreak = 0
	}
	growHeapMallocs = gcMallocs

	want := (parseKiB(growHeapStepKiB)*1024 + pageSize - 1) / pageSize
	if want == 0 {
		want = 1
	}
	if memoryPages := uint64(wasm_memory_size(wasmMemoryIndex)); want > memoryPages {
		want = memoryPages // A heap smaller than the step doubles until it reaches the step.
	}
	want <<= growHeapStreak
	floor := (uint64(heapEnd-heapStart)/64 + 1 + pageSize - 1) / pageSize
	if want < floor {
		want = floor
	}
	if want > 65536 {
		want = 65536 // The whole 32-bit address space.
	}
	for ; want >= floor; want /= 2 {
		if wasm_memory_grow(wasmMemoryIndex, int32(want)) != -1 {
			setHeapEnd(uintptr(wasm_memory_size(wasmMemoryIndex) * wasmPageSize))
			return true
		}
	}
	return false
}

var (
	growHeapStepKiB = "4096"

	// heapMaxKiB is the module's maximum memory (-ldflags "-X
	// runtime.heapMaxKiB=<KiB>", set by build-wasm.sh from CORE_MAX_MEMORY); 0
	// without it, and the allocator then never keeps large ranges.
	heapMaxKiB = "0"

	// growHeapMallocs is gcMallocs at the last growHeap. Like gcMallocs it is
	// a count that passes through the heap's addresses, so patchdata leaves it
	// out of the root scan (gc_globals.go).
	growHeapMallocs uint64
	growHeapStreak  uint

	// heapKeepNot is ^heapKeepFloor(), 0 until it is read. It is stored
	// inverted because the root scan reads every global word as a possible
	// pointer, and the floor itself is an address inside the heap.
	heapKeepNot uintptr
)

// heapKeepFloor is the heap size from which the allocator keeps large free
// ranges (gc_blocks.go, keepLargeRanges): two thirds of the maximum memory.
func heapKeepFloor() uintptr {
	if heapKeepNot == 0 {
		floor := ^uintptr(0) >> 1
		if max := parseKiB(heapMaxKiB) * 1024; max != 0 {
			floor = uintptr(max / 3 * 2)
		}
		heapKeepNot = ^floor
	}
	return ^heapKeepNot
}

// parseKiB reads a decimal -X value. The runtime cannot import strconv.
func parseKiB(s string) uint64 {
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			runtimeFatal("bad growHeap setting")
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}

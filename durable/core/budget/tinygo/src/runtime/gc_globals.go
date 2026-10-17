//go:build (baremetal || tinygo.wasm) && !uefi

package runtime

import "runtime/volatile"

// This file implements findGlobals for all systems where the start and end of
// the globals section can be found through linker-defined symbols.

// pigDataStart holds an 8-byte marker and, after it, the address of the first
// byte of .data, then up to pigSkips [start, end) ranges of .data and .bss that
// hold no heap pointers, ascending, unused pairs ^0. The wasm linker puts .rodata
// between __global_base and .data, and wasm-ld defines no symbol for the start
// of .data, so PiG's build (durable/core/budget/build-wasm.sh) writes the
// address and the ranges into the linked module from the linker map. A zero
// address means the build did not patch it.
var pigDataStart = [3 + 2*pigSkips]uint32{0x44476950, 0x41544144, 0, // "PiGDDATA", then the address
	^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0),
	^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0),
	^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0)}

// pigSkips is the number of ranges pigDataStart holds after the address.
const pigSkips = 12

// findGlobals finds all globals (which are reachable by definition) and calls
// the callback for them.
//
// This implementation marks all globals conservatively and assumes it can use
// linker-defined symbols for the start and end of the .data section. On wasm
// it starts at .data when the build recorded where .data starts: .rodata holds
// no pointers to the heap, but its string bytes and constant tables contain
// words that look like heap addresses and would keep heap objects alive. It
// leaves out the ranges the build recorded after the address: constant tables
// that TinyGo's package initializer placed in .data (strconv's isPrint and
// isGraphic tables are pairs of uint16 code points, 0x037700a1 is 58 MB),
// random state (wasi-libc's arc4random state in .bss, xorshift32State) and
// counters of the allocator (gcMallocs, gcTotalAlloc, growHeapMallocs, and
// the copies the core's mem_stats and alloc_stats answers hold), whose words
// would pin whatever heap object they land in.
func findGlobals(found func(start, end uintptr)) {
	start := globalsStart
	// The volatile loads keep LLVM from folding the unpatched initializer.
	if s := uintptr(volatile.LoadUint32(&pigDataStart[2])); s > globalsStart && s <= globalsEnd {
		start = s
		for i := range pigSkips {
			from := uintptr(volatile.LoadUint32(&pigDataStart[3+2*i]))
			to := uintptr(volatile.LoadUint32(&pigDataStart[4+2*i]))
			if from < start || to < from || to > globalsEnd {
				break // An unused pair (^0), or one patchdata would not write.
			}
			if from > start {
				found(start, from)
			}
			start = to
		}
	}
	if start < globalsEnd {
		found(start, globalsEnd)
	}
}

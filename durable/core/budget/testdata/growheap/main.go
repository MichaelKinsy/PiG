// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build wasip1

// Command growheap is the probe module of growheap.test.mjs: a WASI reactor built with build-wasm.sh that allocates and keeps
// what the test asks for, so the test can watch how TinyGo's heap grows and what happens at the memory ceiling.
package main

import (
	"runtime"
	"unsafe"
)

var kept [][]byte

func main() {}

// alloc keeps one allocation of n bytes, filled with a byte that depends on its position in kept, and returns its address.
//
//go:wasmexport alloc
func alloc(n uint32) uint32 {
	b := make([]byte, n)
	fill := byte(len(kept)%251 + 1)
	for i := range b {
		b[i] = fill
	}
	kept = append(kept, b)
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
}

// damaged is the number of kept allocations whose bytes are not what alloc wrote.
//
//go:wasmexport damaged
func damaged() uint32 {
	var n uint32
	for i, b := range kept {
		for _, c := range b {
			if c != byte(i%251+1) {
				n++
				break
			}
		}
	}
	return n
}

// release drops everything alloc kept.
//
//go:wasmexport release
func release() { kept = nil }

// num_gc is the number of completed collection cycles.
//
//go:wasmexport num_gc
func numGC() uint32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.NumGC
}

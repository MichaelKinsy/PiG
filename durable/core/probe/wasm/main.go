// SPDX-License-Identifier: MIT

//go:build wasip1

// Command wasm is the probe core as a WASI reactor (ABI.md section 4): build it with
// GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared.
package main

import (
	"encoding/binary"
	"runtime"
	"unsafe"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/probe"
)

var (
	sessions        = map[uint32]*probe.Session{}
	nextID   uint32 = 1
	input    []byte
	output   []byte
	partList []byte
	idText   = []byte(abi.ID)
	sqlTable = abi.SQLTable(probe.SQL)
	// stats and allocs are the mem_stats and alloc_stats answers: counters, never pointers. The TinyGo build leaves
	// them out of the collector's root scan (budget/tinygo/patchdata).
	stats  [64]byte
	allocs [24]byte
)

func main() {}

//go:wasmexport abi_id
func abiID() unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(idText)) }

//go:wasmexport sql_table
func sqlTableExport() unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(sqlTable)) }

//go:wasmexport session_new
func sessionNew() uint32 {
	id := nextID
	nextID++
	sessions[id] = probe.NewSession(hostUUID)
	return id
}

//go:wasmexport session_free
func sessionFree(h uint32) { delete(sessions, h) }

//go:wasmexport in_reserve
func inReserve(n uint32) unsafe.Pointer {
	if uint32(cap(input)) < n {
		input = make([]byte, n)
	}
	return unsafe.Pointer(unsafe.SliceData(input))
}

// step runs one event and returns the address of its answer: the list of the parts the step is the concatenation of, here always the one part that is the step (ABI section 4.1 and the layout in section 5).
//
//go:wasmexport step
func step(h, kind, n uint32, now float64, origin uint32) unsafe.Pointer {
	s := sessions[h]
	if s == nil {
		return answer(&abi.Step{Status: abi.StatusFatal, Err: []byte(`{"name":"UnknownHandle","message":"no such session"}`)})
	}
	ev, err := abi.DecodeEvent(abi.EventKind(kind), now, input[:n])
	if err != nil {
		return answer(&abi.Step{Status: abi.StatusRejected, Err: []byte(`{"name":"InvalidEvent","message":"` + err.Error() + `"}`)})
	}
	ev.Origin = origin
	return answer(s.Step(ev))
}

// hop is step for a hopped event: the probe never answers the integer forms, so the host always gets the step (ABI section 4.1).
//
//go:wasmexport hop
func hop(h, effectID uint32, now float64) unsafe.Pointer {
	s := sessions[h]
	if s == nil {
		return answer(&abi.Step{Status: abi.StatusFatal, Err: []byte(`{"name":"UnknownHandle","message":"no such session"}`)})
	}
	return answer(s.Step(abi.Event{Kind: abi.EventHopped, ID: effectID, Now: now}))
}

// answer returns the address of the step's part list (ABI section 3): the probe's step is one part.
func answer(out *abi.Step) unsafe.Pointer {
	output = abi.AppendStep(output[:0], out)
	partList = binary.LittleEndian.AppendUint32(partList[:0], 1)
	partList = binary.LittleEndian.AppendUint32(partList, uint32(uintptr(unsafe.Pointer(unsafe.SliceData(output)))))
	partList = binary.LittleEndian.AppendUint32(partList, uint32(len(output)))
	return unsafe.Pointer(unsafe.SliceData(partList))
}

//go:wasmexport mem_stats
func memStats(h uint32) unsafe.Pointer {
	if s := sessions[h]; s != nil {
		for i, v := range s.MemStats() {
			binary.LittleEndian.PutUint64(stats[i*8:], v)
		}
	}
	return unsafe.Pointer(&stats[0])
}

// allocStats is the alloc_stats export (ABI section 4.1): the instance's bytes allocated, allocations and completed collections, no collection.
//
//go:wasmexport alloc_stats
func allocStats() unsafe.Pointer {
	var rs runtime.MemStats
	runtime.ReadMemStats(&rs)
	binary.LittleEndian.PutUint64(allocs[0:], rs.TotalAlloc)
	binary.LittleEndian.PutUint64(allocs[8:], rs.Mallocs)
	binary.LittleEndian.PutUint64(allocs[16:], uint64(rs.NumGC))
	return unsafe.Pointer(&allocs[0])
}

//go:wasmimport pig uuidv7
func hostUUIDv7(dst unsafe.Pointer)

// hostUUID asks the host for one pi-ai uuidv7() value (36 ASCII bytes).
func hostUUID() string {
	buf := make([]byte, 36)
	hostUUIDv7(unsafe.Pointer(unsafe.SliceData(buf)))
	return string(buf)
}

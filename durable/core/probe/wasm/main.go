// SPDX-License-Identifier: MIT

//go:build wasip1

// Command wasm is the probe core as a WASI reactor (ABI.md section 4): build it with
// GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared.
package main

import (
	"encoding/binary"
	"unsafe"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/probe"
)

var (
	sessions        = map[uint32]*probe.Session{}
	nextID   uint32 = 1
	input    []byte
	output   []byte
	idText   = []byte(abi.ID)
	sqlTable = abi.SQLTable(probe.SQL)
	stats    [64]byte
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

//go:wasmexport step
func step(h, kind, n uint32, now float64) uint32 {
	s := sessions[h]
	if s == nil {
		output = abi.AppendStep(output[:0], &abi.Step{Status: abi.StatusFatal, Err: []byte(`{"name":"UnknownHandle","message":"no such session"}`)})
		return uint32(len(output))
	}
	ev, err := abi.DecodeEvent(abi.EventKind(kind), now, input[:n])
	var out *abi.Step
	if err != nil {
		out = &abi.Step{Status: abi.StatusRejected, Err: []byte(`{"name":"InvalidEvent","message":"` + err.Error() + `"}`)}
	} else {
		out = s.Step(ev)
	}
	output = abi.AppendStep(output[:0], out)
	return uint32(len(output))
}

//go:wasmexport out_ptr
func outPtr() unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(output)) }

//go:wasmexport mem_stats
func memStats(h uint32) unsafe.Pointer {
	if s := sessions[h]; s != nil {
		for i, v := range s.MemStats() {
			binary.LittleEndian.PutUint64(stats[i*8:], v)
		}
	}
	return unsafe.Pointer(&stats[0])
}

//go:wasmimport pig uuidv7
func hostUUIDv7(dst unsafe.Pointer)

// hostUUID asks the host for one pi-ai uuidv7() value (36 ASCII bytes).
func hostUUID() string {
	buf := make([]byte, 36)
	hostUUIDv7(unsafe.Pointer(unsafe.SliceData(buf)))
	return string(buf)
}

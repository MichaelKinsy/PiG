// SPDX-License-Identifier: MIT

//go:build wasip1

// Command corewasm is the Durable core as a WASI reactor (ABI section 4). The same source builds with
//
//	tinygo build -target=wasip1 -buildmode=c-shared -scheduler=none -no-debug -o core.wasm ./durable/core/cmd/corewasm
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath -ldflags=-s\ -w -o core.wasm ./durable/core/cmd/corewasm
//
// This binding is the only place with package-level state: the handle table and the shared input and output buffers
// one instance serves all its sessions with (ADR D10).
package main

import (
	"encoding/binary"
	"unsafe"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

var (
	sessions        = map[uint32]*core.Session{}
	nextID   uint32 = 1
	input    []byte
	output   []byte
	idText   = []byte(core.ABIID)
	sqlTable []byte
	stats    [64]byte
)

func main() {}

//go:wasmexport abi_id
func abiID() unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(idText)) }

//go:wasmexport sql_table
func sqlTableExport() unsafe.Pointer {
	if sqlTable == nil {
		sqlTable = abi.SQLTable(core.SQLTable())
	}
	return unsafe.Pointer(unsafe.SliceData(sqlTable))
}

//go:wasmexport session_new
func sessionNew() uint32 {
	id := nextID
	nextID++
	sessions[id] = core.NewSession(hostUUID)
	return id
}

//go:wasmexport session_free
func sessionFree(h uint32) { delete(sessions, h) }

//go:wasmexport in_reserve
func inReserve(n uint32) unsafe.Pointer {
	if uint32(cap(input)) < n {
		input = make([]byte, n)
	}
	return unsafe.Pointer(unsafe.SliceData(input[:cap(input)]))
}

//go:wasmexport step
func step(h, kind, n uint32, now float64) uint32 {
	s := sessions[h]
	if s == nil {
		output = abi.AppendStep(output[:0], &abi.Step{Status: abi.StatusFatal, Err: abi.ErrorJSON("UnknownHandle", "no such session")})
		return uint32(len(output))
	}
	ev, err := abi.DecodeEvent(abi.EventKind(kind), now, input[:n])
	if err != nil {
		output = abi.AppendStep(output[:0], &abi.Step{Status: abi.StatusRejected, Err: abi.ErrorJSON("InvalidEvent", err.Error())})
		return uint32(len(output))
	}
	output = abi.AppendStep(output[:0], s.Step(ev))
	return uint32(len(output))
}

//go:wasmexport out_ptr
func outPtr() unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(output)) }

//go:wasmexport mem_stats
func memStats(h uint32) unsafe.Pointer {
	if s := sessions[h]; s != nil {
		// Slot 7, linear memory size, stays 0: the host reads memory.buffer.byteLength.
		m := s.MemStats()
		for i, v := range m {
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

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
	"runtime"
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
	// stats and allocs are the mem_stats and alloc_stats answers: counters, never pointers. The TinyGo build leaves
	// them out of the collector's root scan (budget/tinygo/patchdata).
	stats  [64]byte
	allocs [24]byte
	// readReq and readAns are the request and answer buffers of the pig.read import.
	readReq, readAns []byte
	// parts are the pieces of the last step and partList their addresses, the step export's answer; parts keep the
	// payloads they alias alive until the host has read them.
	parts    []abi.Part
	partList []byte
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
	sessions[id].SetReader(hostRead)
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

// step runs one event and returns the address of its answer: the list of the parts the step is the concatenation of,
// whose first four bytes are its size (ABI section 4.1 and the layout in section 5). origin tags the host's async context of
// the call (ABI section 5.4). The answer stays valid until the next export call.
//
//go:wasmexport step
func step(h, kind, n uint32, now float64, origin uint32) unsafe.Pointer {
	clear(parts) // the host has read the last step: its payloads may go
	s := sessions[h]
	if s == nil {
		return answer(&abi.Step{Status: abi.StatusFatal, Err: abi.ErrorJSON("UnknownHandle", "no such session")})
	}
	ev, err := abi.DecodeEvent(abi.EventKind(kind), now, input[:n])
	if err != nil {
		return answer(&abi.Step{Status: abi.StatusRejected, Err: abi.ErrorJSON("InvalidEvent", err.Error())})
	}
	ev.Origin = origin
	return answer(s.Step(ev))
}

// hop runs the hopped event for hops effect effectId, which carries no payload to copy, and answers without encoding a
// step the host would not decode (ABI section 4.1). 0: the step has no commit, read, notice, effect or error. Otherwise the
// address of a part list: a step as for step, or, with zero parts, the step whose only output is one hops effect, as
// `u32 0, u32 effectId, u32 n` (a plain chain: no commits on it). Valid until the next export call.
//
//go:wasmexport hop
func hop(h, effectID uint32, now float64) unsafe.Pointer {
	clear(parts)
	s := sessions[h]
	if s == nil {
		return answer(&abi.Step{Status: abi.StatusFatal, Err: abi.ErrorJSON("UnknownHandle", "no such session")})
	}
	out := s.Step(abi.Event{Kind: abi.EventHopped, ID: effectID, Now: now})
	if out.Status == abi.StatusOK && len(out.Commits) == 0 && len(out.Reads) == 0 && len(out.Notices) == 0 {
		switch len(out.Effects) {
		case 0:
			return nil
		case 1:
			if e := out.Effects[0]; e.Kind == abi.EffectHops {
				// The core encoded this payload itself: eight bytes with `at` 0 are `n` and no commit. A chain that carries
				// commits is not decoded here (its commits would be copied to the heap only to be discarded): it goes out as a step.
				if len(e.Payload) == 8 && binary.LittleEndian.Uint32(e.Payload[4:]) == 0 {
					binary.LittleEndian.PutUint32(hopAnswer[4:], e.ID)
					copy(hopAnswer[8:], e.Payload[:4])
					return unsafe.Pointer(&hopAnswer[0])
				}
			}
		}
	}
	return answer(out)
}

// hopAnswer is the thin answer of hop: zero parts, the effect ID and the hop count.
var hopAnswer [12]byte

// outputKeep bounds the output buffer kept between steps: a step that carried large notices or effects leaves a buffer
// of that size, which the next step drops rather than holding it for the life of the instance.
const outputKeep = 256 << 10

// partBig is the payload size from which a notice or effect goes to the host from where the core built it, a part of its
// own, rather than through a copy in the output buffer: a model request is the whole transcript, and its copy would
// double the peak of the step that sends it.
const partBig = 64 << 10

// answer encodes a step into the shared output buffer, its payloads of partBig bytes or more as parts of their own, and
// returns the address of the part list. The host has decoded the previous step before this call, so its buffer is free.
// The buffer is sized once: growing it by appending would leave each smaller copy for the collector.
func answer(out *abi.Step) unsafe.Pointer {
	n := abi.StepSizeInline(out, partBig)
	if cap(output) < n || cap(output) > max(outputKeep, n) {
		output = make([]byte, 0, max(n, 4096))
	}
	output, parts = abi.AppendStepParts(output[:0], out, partBig, parts[:0])
	partList = binary.LittleEndian.AppendUint32(partList[:0], uint32(len(parts)))
	for _, p := range parts {
		b := p.Ext
		if b == nil {
			b = output[p.Off:p.End]
		}
		partList = binary.LittleEndian.AppendUint32(partList, uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b)))))
		partList = binary.LittleEndian.AppendUint32(partList, uint32(len(b)))
	}
	return unsafe.Pointer(unsafe.SliceData(partList))
}

// memStats fills slot 6, heap in use, from the runtime after a full collection, so it is what the instance retains rather
// than garbage the collector has not reached yet. Slot 7, linear memory size, stays 0: the host reads
// memory.buffer.byteLength. The session counters are 0 for a handle that does not exist; the heap is the instance's.
//
//go:wasmexport mem_stats
func memStats(h uint32) unsafe.Pointer {
	var m [8]uint64
	if s := sessions[h]; s != nil {
		m = s.MemStats()
	}
	runtime.GC()
	var rs runtime.MemStats
	runtime.ReadMemStats(&rs)
	m[6] = rs.HeapAlloc
	for i, v := range m {
		binary.LittleEndian.PutUint64(stats[i*8:], v)
	}
	return unsafe.Pointer(&stats[0])
}

// allocStats answers the alloc_stats export: the instance's bytes allocated, allocations and completed collections since
// it started, three u64, read without a collection, so a host can count them per step (ABI section 4.1).
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

// readKeep bounds the read answer buffer kept between reads. A page of up to store.PageRows records (~400 KB of entries
// at 3,500 turns) fits, so paging a transcript reuses one buffer: a buffer dropped per page stays alive until a
// collection that TinyGo's conservative roots may never make good on.
const readKeep = 1 << 20

//go:wasmimport pig read
func hostReadImport(req unsafe.Pointer, reqLen uint32, dst unsafe.Pointer, capacity uint32) uint32

// hostRead runs one read statement through the host at once (ABI section 4.2, pig.read). An answer longer than the buffer
// comes back as its length only, and the call repeats with a buffer of that length.
func hostRead(sql uint16, params []abi.Value) ([][]abi.Value, error) {
	readReq = abi.AppendReadRequest(readReq[:0], sql, params)
	if cap(readAns) < 4096 {
		readAns = make([]byte, 4096)
	}
	for {
		readAns = readAns[:cap(readAns)]
		n := hostReadImport(unsafe.Pointer(unsafe.SliceData(readReq)), uint32(len(readReq)), unsafe.Pointer(unsafe.SliceData(readAns)), uint32(len(readAns)))
		if int(n) <= len(readAns) {
			rows, err := abi.DecodeAnswer(readAns[:n])
			if cap(readAns) > readKeep {
				readAns = nil // the rows are copies; an answer of a few huge records does not stay for the life of the instance
			}
			return rows, err
		}
		readAns = make([]byte, max(int(n), min(2*cap(readAns), readKeep))) // pages of similar size reuse it
	}
}

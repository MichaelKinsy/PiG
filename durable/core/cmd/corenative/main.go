// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build !wasip1

// Command corenative serves the Durable core, built natively, to a host in another process over two FIFOs: the same
// exports as the WASI reactor (cmd/corewasm), one request and one response at a time. The Node host
// (host/cf/src/native.ts) uses it to run the conformance gate's native build through the same facade as the Wasm builds.
//
//	corenative <requests fifo> <responses fifo>
//
// A request is u8 op, u32 handle, u8 kind, f64 now, u32 origin (the step export's origin argument, ABI section 5.4), u32 length, then length bytes. Ops: 1 abi_id, 2 sql_table,
// 3 session_new, 4 step, 5 session_free, 6 mem_stats, 9 alloc_stats, 10 heap_threads (the count gate's heap net of the runtime's
// threads). A response is u8 type, u32 length, then length bytes: type 0 is
// the op's answer; type 1 asks the host for one pi-ai uuidv7() (the host answers with a request of op 7 whose payload is
// the 36 ASCII bytes); type 2 asks the host to run one read statement (the payload is the pig.read request of ABI section
// 4.2; the host answers with a request of op 8 whose payload is the read's answer). The response to the original op
// follows them.
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest/guard"
)

// maxPayload bounds one request's payload: a corrupt length must not allocate before the read fails.
const maxPayload = 1 << 30

type server struct {
	in       *bufio.Reader
	out      *bufio.Writer
	sessions map[uint32]*core.Session
	next     uint32
	// perThread is the heap bytes one more runtime thread holds, measured by the first heap_threads request; 0 before.
	perThread uint64
}

type request struct {
	op      uint8
	handle  uint32
	kind    uint8
	now     float64
	origin  uint32
	payload []byte
}

func (s *server) read() (request, error) {
	var head [22]byte
	if _, err := io.ReadFull(s.in, head[:]); err != nil {
		return request{}, err
	}
	n := binary.LittleEndian.Uint32(head[18:])
	if n > maxPayload {
		return request{}, fmt.Errorf("request payload of %d bytes is over the %d byte bound", n, maxPayload)
	}
	r := request{op: head[0], handle: binary.LittleEndian.Uint32(head[1:]), kind: head[5],
		now: math.Float64frombits(binary.LittleEndian.Uint64(head[6:])), origin: binary.LittleEndian.Uint32(head[14:]), payload: make([]byte, n)}
	_, err := io.ReadFull(s.in, r.payload)
	return r, err
}

func (s *server) write(typ uint8, body []byte) error {
	var head [5]byte
	head[0] = typ
	binary.LittleEndian.PutUint32(head[1:], uint32(len(body)))
	if _, err := s.out.Write(head[:]); err != nil {
		return err
	}
	if _, err := s.out.Write(body); err != nil {
		return err
	}
	return s.out.Flush()
}

// uuid asks the host for one uuidv7() in the middle of a step.
func (s *server) uuid() string {
	if err := s.write(1, nil); err != nil {
		panic(err)
	}
	r, err := s.read()
	if err != nil || r.op != 7 || len(r.payload) != 36 {
		panic(fmt.Sprintf("corenative: bad uuid answer (op %d, %d bytes, %v)", r.op, len(r.payload), err))
	}
	return string(r.payload)
}

// readRows asks the host to run one read statement in the middle of a step (the pig.read import).
func (s *server) readRows(sql uint16, params []abi.Value) ([][]abi.Value, error) {
	if err := s.write(2, abi.AppendReadRequest(nil, sql, params)); err != nil {
		return nil, err
	}
	r, err := s.read()
	if err != nil {
		return nil, err
	}
	if r.op != 8 {
		return nil, fmt.Errorf("corenative: a read was answered with op %d", r.op)
	}
	return abi.DecodeAnswer(r.payload)
}

func (s *server) serve(r request) []byte {
	switch r.op {
	case 1:
		return []byte(core.ABIID)
	case 2:
		return abi.SQLTable(core.SQLTable())
	case 3:
		id := s.next
		s.next++
		s.sessions[id] = core.NewSession(s.uuid)
		s.sessions[id].SetReader(s.readRows)
		return binary.LittleEndian.AppendUint32(nil, id)
	case 4:
		sess := s.sessions[r.handle]
		if sess == nil {
			return abi.AppendStep(nil, &abi.Step{Status: abi.StatusFatal, Err: abi.ErrorJSON("UnknownHandle", "no such session")})
		}
		ev, err := abi.DecodeEvent(abi.EventKind(r.kind), r.now, r.payload)
		if err != nil {
			return abi.AppendStep(nil, &abi.Step{Status: abi.StatusRejected, Err: abi.ErrorJSON("InvalidEvent", err.Error())})
		}
		ev.Origin = r.origin
		return abi.AppendStep(nil, sess.Step(ev))
	case 5:
		delete(s.sessions, r.handle)
		return nil
	case 6:
		return memStats(s.sessions[r.handle])
	case 9:
		return allocStats()
	case 10:
		return s.heapThreads()
	}
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: corenative <requests fifo> <responses fifo>")
		os.Exit(2)
	}
	in, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := os.OpenFile(os.Args[2], os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// A step that never returns never lets the parent time out; the guard ends this process on a runaway step or heap, or when the
	// parent is gone, and says which request it was.
	g := guard.Start(guard.FromEnv("corenative"))
	s := &server{in: bufio.NewReaderSize(in, 1<<16), out: bufio.NewWriterSize(out, 1<<16), sessions: map[uint32]*core.Session{}, next: 1}
	for {
		r, err := s.read()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "corenative:", err)
			os.Exit(1)
		}
		g.Begin(fmt.Sprintf("op %d handle %d kind %d (%d payload bytes)", r.op, r.handle, r.kind, len(r.payload)))
		answer := s.serve(r)
		g.End()
		if err := s.write(0, answer); err != nil {
			fmt.Fprintln(os.Stderr, "corenative:", err)
			os.Exit(1)
		}
	}
}

// allocStats answers op 9 with the process's bytes allocated, allocations and completed collections since it started (alloc_stats, ABI section
// 4.1), without a collection: the process serves only the core.
func allocStats() []byte {
	var rs runtime.MemStats
	runtime.ReadMemStats(&rs)
	b := binary.LittleEndian.AppendUint64(nil, rs.TotalAlloc)
	b = binary.LittleEndian.AppendUint64(b, rs.Mallocs)
	return binary.LittleEndian.AppendUint64(b, uint64(rs.NumGC))
}

// heapThreads answers op 10 for the count gate with four u64: the live heap after two full collections, the Go runtime's
// live threads (/sched/threads/total) at that reading, the heap bytes one more thread holds (perThread), and the threads at
// the request's first reading, before the first request measures perThread. The runtime keeps a fixed set of heap objects
// for every OS thread it has started (its m, g0 and signal goroutine), and it starts one whenever a goroutine blocks while
// no idle thread is left, which follows the host's scheduling: under CPU contention about one run in eight has one thread
// more and reads 5,368 B more heap. The gate's figure is heap - threads * perThread. A process that cannot read its thread
// count, or whose calibration started no thread, answers perThread 0 and says why on stderr.
func (s *server) heapThreads() []byte {
	_, before, ok := settledHeap()
	if ok && s.perThread == 0 {
		p, err := measurePerThread(calibrationGoroutines)
		if err != nil {
			fmt.Fprintln(os.Stderr, "corenative:", err)
		}
		s.perThread = p
	}
	heap, threads, _ := settledHeap()
	b := binary.LittleEndian.AppendUint64(nil, heap)
	b = binary.LittleEndian.AppendUint64(b, threads)
	b = binary.LittleEndian.AppendUint64(b, s.perThread)
	return binary.LittleEndian.AppendUint64(b, before)
}

// perThreadSteps is how many threads measurePerThread takes the median of (odd), after the first.
const perThreadSteps = 5

// calibrationGoroutines bounds measurePerThread: each pins one thread, so it starts its 1 + perThreadSteps threads past up
// to that many minus 6 idle ones.
const calibrationGoroutines = 24

var threadsSample = []metrics.Sample{{Name: "/sched/threads/total:threads"}}

// settledHeap is the live heap after two full collections (settledMemStats) and the runtime's live threads; ok is false when
// the runtime does not report its threads.
func settledHeap() (heap, threads uint64, ok bool) {
	rs := settledMemStats()
	metrics.Read(threadsSample)
	if threadsSample[0].Value.Kind() != metrics.KindUint64 {
		return rs.HeapAlloc, 0, false
	}
	return rs.HeapAlloc, threadsSample[0].Value.Uint64(), true
}

// measurePerThread makes the runtime start 1 + perThreadSteps more threads, one at a time, and returns the median heap the
// later ones added each. The first pays for what only the process's first locked thread or first wait creates (the
// runtime's template thread, a cached waiter record), so only the later ones are a thread's own cost; one of those
// sometimes also holds a 48 B object of the scheduler's, which the median leaves out. The n goroutines start before every
// reading, so their own runtime objects are in all of them. Each in turn locks its thread and blocks, which moves this
// goroutine to an idle thread or, when none is left, to a new one. The goroutines then unlock and exit; their threads stay,
// idle, so later readings count them.
func measurePerThread(n int) (uint64, error) {
	start := make([]chan struct{}, n)
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	for i := range start {
		start[i] = make(chan struct{})
		//portlint:allow gorecover the measurement goroutines only lock a thread and signal channels; they run no input-derived code that could panic
		go func(start <-chan struct{}) {
			if _, lock := <-start; lock {
				runtime.LockOSThread()
				locked <- struct{}{}
				<-release
				runtime.UnlockOSThread()
			}
			done <- struct{}{}
		}(start[i])
	}
	pinned := 0
	// pinNewThread pins goroutines until the runtime has more than `above` threads.
	pinNewThread := func(above uint64) {
		for pinned < n {
			start[pinned] <- struct{}{}
			<-locked
			pinned++
			if metrics.Read(threadsSample); threadsSample[0].Value.Uint64() > above {
				return
			}
		}
	}
	var steps []uint64
	var err error
	_, t, _ := settledHeap()
	pinNewThread(t)
	h, t1, _ := settledHeap()
	for t1 > t && len(steps) < perThreadSteps {
		t = t1
		pinNewThread(t)
		h1, t2, _ := settledHeap()
		if t2 > t && h1 >= h {
			steps = append(steps, (h1-h)/(t2-t))
		} else if t2 > t {
			err = fmt.Errorf("heap_threads: the heap fell from %d to %d B while %d thread(s) started", h, h1, t2-t)
			break
		}
		h, t1 = h1, t2
	}
	close(release)
	for i := pinned; i < n; i++ {
		close(start[i])
	}
	for range n {
		<-done
	}
	if err != nil {
		return 0, err
	}
	if len(steps) < perThreadSteps {
		return 0, fmt.Errorf("heap_threads: %d locked goroutines started %d of the %d threads to measure", pinned, len(steps), perThreadSteps+1)
	}
	slices.Sort(steps)
	return steps[len(steps)/2], nil
}

// settledMemStats reads the runtime's memory statistics after two full collections: the first moves every sync.Pool's
// objects to its victim cache and only the second frees them. mem_stats (op 6) and heap_threads (op 10) both read the heap so.
func settledMemStats() runtime.MemStats {
	runtime.GC()
	runtime.GC()
	var rs runtime.MemStats
	runtime.ReadMemStats(&rs)
	return rs
}

// memStats answers op 6 with the eight mem_stats counters (ABI section 4.1). Slot 6, heap in use, is the process's live heap
// after two full collections (settledMemStats); slot 7 is what the Go runtime obtained from the OS (Sys), which, like a Wasm
// build's linear memory, never shrinks. The process serves only the core, so both measure the core. A handle that does not
// exist reports 0 for the session counters.
func memStats(sess *core.Session) []byte {
	var m [8]uint64
	if sess != nil {
		m = sess.MemStats()
	}
	rs := settledMemStats()
	m[6], m[7] = rs.HeapAlloc, rs.Sys
	out := make([]byte, 0, len(m)*8)
	for _, v := range m {
		out = binary.LittleEndian.AppendUint64(out, v)
	}
	return out
}

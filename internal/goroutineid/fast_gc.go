//go:build gc && (linux || darwin) && (amd64 || arm64)

package goroutineid

import (
	"slices"
	"sync"
	"sync/atomic"
)

// offsetState is where the goroutine ID lives in the runtime's goroutine record: proving (0) until proofGoroutines distinct
// goroutines have called Current, slowOffset when the proof found no single word, and the proved offset plus offsetBias otherwise.
// While it proves, Current returns the stack header's ID, so every call is right before and after the proof.
var offsetState atomic.Int64

// proof is the proof of the offset while offsetState is proving. It runs on the goroutines that call Current and starts none of
// its own: a goroutine the probe started would count in every process's goroutine total (TestPrintReadToolsProcessGate).
var proof offsetProof

const (
	slowOffset = 1
	offsetBias = 2
)

func current() uint64 {
	state := offsetState.Load()
	if state >= offsetBias {
		return goidAt(uintptr(state - offsetBias))
	}
	id := stackID()
	if state == 0 {
		if decided := proof.observe(id, goidAt); decided != 0 {
			offsetState.CompareAndSwap(0, decided)
		}
	}
	return id
}

// goidAt returns the word at offset in the calling goroutine's runtime record (goroutineid_amd64.s, goroutineid_arm64.s). An
// offset beyond the record reads memory that is not the record's, so only the proof's scan and the offset it proves reach it.
func goidAt(offset uintptr) uint64

// probeScanLimit bounds the scan to the first words of the record, which is larger than any word it reads.
const probeScanLimit = 256

// proofGoroutines is how many distinct goroutines must agree with their stack headers before an offset is trusted. The runtime
// never reuses a goroutine ID, so their IDs are distinct, and a word that equals the ID on all of them is the ID: a word that
// only coincides with an ID on a few goroutines is ruled out by the others.
const proofGoroutines = 17

// offsetProof narrows the offsets of the record whose word equals the calling goroutine's stack-header ID, one distinct goroutine
// at a time. The zero value scans the first probeScanLimit bytes.
type offsetProof struct {
	mu sync.Mutex
	// words is how many words of the record the scan covers; zero means probeScanLimit/8.
	words    int
	ruledOut []bool
	seen     [proofGoroutines]uint64
	n        int
	decided  int64
}

// observe narrows the candidates with the calling goroutine, whose stack-header ID is id; read loads the word at an offset of the
// calling goroutine's record. It returns 0 until proofGoroutines distinct goroutines have been observed, then the offset plus
// offsetBias when exactly one word equalled the ID on every one of them, and slowOffset otherwise. A goroutine observed before
// changes nothing.
func (p *offsetProof) observe(id uint64, read func(offset uintptr) uint64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.decided != 0 || slices.Contains(p.seen[:p.n], id) {
		return p.decided
	}
	if p.ruledOut == nil {
		words := p.words
		if words == 0 {
			words = probeScanLimit / 8
		}
		p.ruledOut = make([]bool, words)
	}
	left := 0
	for i := range p.ruledOut {
		if !p.ruledOut[i] && read(uintptr(i*8)) != id {
			p.ruledOut[i] = true
		}
		if !p.ruledOut[i] {
			left++
		}
	}
	p.seen[p.n] = id
	p.n++
	switch {
	case left == 0:
		p.decided = slowOffset
	case p.n == proofGoroutines:
		p.decided = slowOffset
		if left == 1 {
			p.decided = int64(slices.Index(p.ruledOut, false)*8) + offsetBias
		}
	}
	return p.decided
}

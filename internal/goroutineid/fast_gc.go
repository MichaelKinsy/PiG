//go:build gc && (linux || darwin) && (amd64 || arm64)

package goroutineid

import (
	"sync"
	"sync/atomic"
)

// offsetState is where the goroutine ID lives in the runtime's goroutine record: unprobed (0) until the first call of Current
// runs the probe, slowOffset when it proved no offset, and the proved offset plus offsetBias otherwise. The probe starts
// goroutines and formats stack headers, so it runs on first use and not at process start, which every process pays.
var offsetState atomic.Int64

// probeOnce runs the probe once, however many goroutines make the first call together.
var probeOnce sync.Once

const (
	slowOffset = 1
	offsetBias = 2
)

func current() uint64 {
	state := offsetState.Load()
	if state == 0 {
		probeOnce.Do(func() {
			if offset := probeOffset(probeScanLimit, goidAt); offset >= 0 {
				offsetState.Store(int64(offset) + offsetBias)
			} else {
				offsetState.Store(slowOffset)
			}
		})
		state = offsetState.Load()
	}
	if state >= offsetBias {
		return goidAt(uintptr(state - offsetBias))
	}
	return stackID()
}

// goidAt returns the word at offset in the calling goroutine's runtime record (goroutineid_amd64.s, goroutineid_arm64.s). An
// offset beyond the record reads memory that is not the record's, so only probeOffset's scan and the offset it proves reach it.
func goidAt(offset uintptr) uint64

// probeScanLimit bounds the scan to the first words of the record, which is larger than any word it reads.
const probeScanLimit = 256

// probeRounds is how many goroutines must agree with the stack header before an offset is trusted. A word that only coincides
// with an ID on a few goroutines is not the ID.
const probeRounds = 16

// probeOffset finds, among the first limit bytes of the record, the offset whose word equals the stack header's ID on the calling
// goroutine and on probeRounds others, each started while the previous ones are still alive and so has an ID of its own. It
// returns -1 unless exactly one offset passes. read loads the word at an offset of the calling goroutine's record (goidAt).
func probeOffset(limit int, read func(offset uintptr) uint64) int {
	candidates := make([]bool, limit/8)
	for i := range candidates {
		candidates[i] = true
	}
	narrow := func() {
		want := stackID()
		for i := range candidates {
			if candidates[i] && read(uintptr(i*8)) != want {
				candidates[i] = false
			}
		}
	}
	narrow()
	var live sync.WaitGroup
	release := make(chan struct{})
	for range probeRounds {
		started := make(chan struct{})
		live.Go(func() {
			narrow()
			close(started)
			<-release
		})
		<-started
	}
	close(release)
	live.Wait()
	found := -1
	for i, ok := range candidates {
		if !ok {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = i * 8
	}
	return found
}

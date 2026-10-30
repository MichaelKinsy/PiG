package host

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// Ports packages/chord/src/next/host/uuid.ts. UUIDv7 (§3, code copied from pi-ai's `uuidv7`): time-ordered ids, minted for
// host incarnations.

const maxSequence = int64(1)<<41 - 1

var uuidState = struct {
	sync.Mutex
	// lastTimestamp is the last timestamp used: a clock going backwards keeps the ids ordered.
	lastTimestamp int64
	// sequence is a random start, then incremented per id, so ids within one millisecond stay ordered and unique;
	// started says it has its start.
	sequence int64
	started  bool
	now      func() int64
}{lastTimestamp: -1, now: func() int64 { return time.Now().UnixMilli() }}

// uuidv7 is a time-ordered UUIDv7. It panics with the RangeError text of Pi when the sequence is exhausted.
func uuidv7() string {
	state := &uuidState
	state.Lock()
	defer state.Unlock()
	timestamp := max(state.now(), state.lastTimestamp)
	state.lastTimestamp = timestamp
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	if !state.started {
		state.sequence = int64(bytes[1])<<32 | int64(bytes[2])<<24 | int64(bytes[3])<<16 | int64(bytes[4])<<8 | int64(bytes[5])
		state.started = true
	} else {
		if state.sequence == maxSequence {
			panic("UUIDv7 generator sequence exhausted")
		}
		state.sequence++
	}
	sequence := state.sequence
	for index := 5; index >= 0; index-- {
		bytes[index] = byte(timestamp >> uint((5-index)*8))
	}
	bytes[6] = 0x70 | byte((sequence>>37)&0x0f)
	bytes[7] = byte((sequence >> 29) & 0xff)
	bytes[8] = 0x80 | byte((sequence>>23)&0x3f)
	bytes[9] = byte((sequence >> 15) & 0xff)
	bytes[10] = byte((sequence >> 7) & 0xff)
	bytes[11] = byte((sequence&0x7f)<<1) | (bytes[11] & 0x01)
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
}

// setUUIDClock replaces the clock uuidv7 reads (tests: Pi's tests fake Date) and returns the restore.
func setUUIDClock(now func() int64) (restore func()) {
	uuidState.Lock()
	defer uuidState.Unlock()
	previous := uuidState.now
	uuidState.now = now
	return func() {
		uuidState.Lock()
		defer uuidState.Unlock()
		uuidState.now = previous
	}
}

// exhaustUUIDSequence puts the generator at its sequence limit (tests) and returns the restore that starts it over.
func exhaustUUIDSequence() (restore func()) {
	uuidState.Lock()
	defer uuidState.Unlock()
	uuidState.sequence, uuidState.started = maxSequence, true
	return func() {
		uuidState.Lock()
		defer uuidState.Unlock()
		uuidState.started = false
	}
}

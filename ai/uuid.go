package ai

// Ports packages/ai/src/utils/uuid.ts.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	maxUUIDv7Timestamp = 0xffffffffffff
	maxUUIDv7Sequence  = 1<<41 - 1
)

// uuidv7Generator holds the process-wide ordering state of uuid.ts: the last ordinary timestamp and a 41-bit sequence seeded from randomness. The clock and the random source are injected so tests control both.
type uuidv7Generator struct {
	mu                    sync.Mutex
	now                   func() int64
	readRandom            func([]byte) error
	lastOrdinaryTimestamp int64
	sequence              uint64
	seeded                bool
}

// newUUIDv7Generator returns a generator over the given clock (Unix milliseconds) and random source. A nil random source reads crypto/rand.
func newUUIDv7Generator(now func() int64, readRandom func([]byte) error) *uuidv7Generator {
	if readRandom == nil {
		readRandom = func(bytes []byte) error { _, err := rand.Read(bytes); return err }
	}
	return &uuidv7Generator{now: now, readRandom: readRandom, lastOrdinaryTimestamp: -1}
}

var defaultUUIDv7Generator = newUUIDv7Generator(func() int64 { return time.Now().UnixMilli() }, nil)

// UUIDv7 returns a time-ordered UUIDv7. A nil timestamp uses the wall clock and never moves backwards; a supplied timestamp is preserved exactly for follower ids. A process-wide 41-bit sequence seeded from randomness keeps ids unique and ordered within one millisecond.
func UUIDv7(timestampMs *int64) (string, error) {
	return defaultUUIDv7Generator.next(timestampMs)
}

func (generator *uuidv7Generator) next(timestampMs *int64) (string, error) {
	requested := generator.now()
	if timestampMs != nil {
		requested = *timestampMs
	}
	if requested < 0 || requested > maxUUIDv7Timestamp {
		return "", fmt.Errorf("UUIDv7 timestamp must be an integer between 0 and %d", int64(maxUUIDv7Timestamp))
	}
	var bytes [16]byte
	if err := generator.readRandom(bytes[:]); err != nil {
		return "", err
	}
	effective, sequence, err := generator.advance(requested, timestampMs == nil, bytes)
	if err != nil {
		return "", err
	}
	for index := 5; index >= 0; index-- {
		bytes[index] = byte(effective >> uint((5-index)*8))
	}
	bytes[6] = 0x70 | byte((sequence>>37)&0x0f)
	bytes[7] = byte(sequence >> 29)
	bytes[8] = 0x80 | byte((sequence>>23)&0x3f)
	bytes[9] = byte(sequence >> 15)
	bytes[10] = byte(sequence >> 7)
	bytes[11] = byte((sequence&0x7f)<<1) | (bytes[11] & 0x01)
	encoded := hex.EncodeToString(bytes[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func (generator *uuidv7Generator) advance(requested int64, ordinary bool, random [16]byte) (int64, uint64, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	effective := requested
	if ordinary {
		effective = max(requested, generator.lastOrdinaryTimestamp)
		generator.lastOrdinaryTimestamp = effective
	}
	if !generator.seeded {
		var seed [8]byte
		copy(seed[3:], random[1:6])
		generator.sequence = binary.BigEndian.Uint64(seed[:])
		generator.seeded = true
		return effective, generator.sequence, nil
	}
	if generator.sequence == maxUUIDv7Sequence {
		return 0, 0, errors.New("UUIDv7 generator sequence exhausted")
	}
	generator.sequence++
	return effective, generator.sequence, nil
}

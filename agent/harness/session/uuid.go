package session

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

var uuidv7State struct {
	sync.Mutex
	lastOrdinaryTimestamp int64
	sequence              uint64
	seeded                bool
}

// UUIDv7 returns a time-ordered UUIDv7. A nil timestamp uses the wall clock and
// never moves backwards; a supplied timestamp is preserved exactly for
// follower ids. A process-wide 41-bit sequence seeded from randomness keeps
// ids unique and ordered within one millisecond.
func UUIDv7(timestampMs *int64) (string, error) {
	requested := time.Now().UnixMilli()
	if timestampMs != nil {
		requested = *timestampMs
	}
	if requested < 0 || requested > maxUUIDv7Timestamp {
		return "", fmt.Errorf("UUIDv7 timestamp must be an integer between 0 and %d", maxUUIDv7Timestamp)
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	effective, sequence, err := nextUUIDv7Sequence(requested, timestampMs == nil, bytes)
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

func nextUUIDv7Sequence(requested int64, ordinary bool, random [16]byte) (int64, uint64, error) {
	uuidv7State.Lock()
	defer uuidv7State.Unlock()
	effective := requested
	if ordinary {
		effective = max(requested, uuidv7State.lastOrdinaryTimestamp)
		uuidv7State.lastOrdinaryTimestamp = effective
	}
	if !uuidv7State.seeded {
		var seed [8]byte
		copy(seed[3:], random[1:6])
		uuidv7State.sequence = binary.BigEndian.Uint64(seed[:])
		uuidv7State.seeded = true
	} else {
		if uuidv7State.sequence == maxUUIDv7Sequence {
			return 0, 0, errors.New("UUIDv7 generator sequence exhausted")
		}
		uuidv7State.sequence++
	}
	return effective, uuidv7State.sequence, nil
}

// IdGenerator mints session-unique ids. A non-nil timestampMs is the exact
// time prefix of the minted id.
type IdGenerator interface {
	Next(timestampMs *int64) string
}

// IdGeneratorFunc adapts a function to IdGenerator.
type IdGeneratorFunc func(timestampMs *int64) string

// Next calls the function.
func (generator IdGeneratorFunc) Next(timestampMs *int64) string { return generator(timestampMs) }

// UUIDv7Generator is the default UUIDv7 IdGenerator. An out-of-range supplied
// timestamp is a trusted-programming defect and panics, like the upstream
// RangeError.
var UUIDv7Generator IdGenerator = IdGeneratorFunc(func(timestampMs *int64) string {
	id, err := UUIDv7(timestampMs)
	if err != nil {
		panic(err)
	}
	return id
})

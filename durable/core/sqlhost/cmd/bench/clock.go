// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/hex"
	"math"
	"os"
	"strconv"
	"sync"
	"time"
)

// The conformance gate's reference runs under detenv (durable/contract/lib/detenv.mjs): a virtual clock fixed at
// DETENV_EPOCH and a seeded Math.random. The only values of that environment a native run must reproduce are the ones
// the reference writes into the store: the clock, and the faux provider's api id `faux:<now>:<Math.random().toString(36).slice(2)>`
// (faux.ts), which is the run's first random draw.

// detenvRNG is detenv's generator: Math.random, crypto.getRandomValues and crypto.randomUUID draw from one stream, so the order
// of draws is part of the run (the faux provider's api id is the first draw; each uuidv7 reads sixteen bytes after it). In
// detenv each draw and each getRandomValues fill is atomic on the event loop; here the faux provider's responses and the
// session can draw from different goroutines, so mu makes each one atomic.
type detenvRNG struct {
	mu   sync.Mutex
	seed uint32
}

// next is one Math.random() draw.
func (r *detenvRNG) next() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.step()
}

// step is next under mu.
func (r *detenvRNG) step() float64 {
	r.seed += 0x6d2b79f5
	t := r.seed
	t = (t ^ t>>15) * (t | 1)
	t ^= t + (t^t>>7)*(t|61)
	return float64(t^t>>14) / 4294967296
}

// fill is crypto.getRandomValues on a byte array: one draw per byte, all of them in one turn.
func (r *detenvRNG) fill(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range b {
		b[i] = byte(math.Floor(r.step() * 256))
	}
}

// uuidv7 is packages/ai/src/utils/uuid.ts uuidv7() with no timestamp argument, over detenv's randomness and the run's clock.
type uuidv7Gen struct {
	rng       *detenvRNG
	last      int64
	sequence  uint64
	hasSeq    bool
	nowMillis func() int64
}

func (g *uuidv7Gen) next() string {
	timestamp := max(g.nowMillis(), g.last)
	g.last = timestamp
	var b [16]byte
	g.rng.fill(b[:])
	if !g.hasSeq {
		g.sequence = uint64(b[1])<<32 | uint64(b[2])<<24 | uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
		g.hasSeq = true
	} else {
		g.sequence++
	}
	for i := 5; i >= 0; i-- {
		b[i] = byte(timestamp >> uint((5-i)*8))
	}
	b[6] = 0x70 | byte((g.sequence>>37)&0x0f)
	b[7] = byte(g.sequence >> 29)
	b[8] = 0x80 | byte((g.sequence>>23)&0x3f)
	b[9] = byte(g.sequence >> 15)
	b[10] = byte(g.sequence >> 7)
	b[11] = byte((g.sequence&0x7f)<<1) | (b[11] & 0x01)
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// base36Fraction is JavaScript's Number.prototype.toString(36).slice(2) for a value in [0, 1): V8's DoubleToRadixCString
// fraction loop, which prints the shortest digit string that rounds back to the double.
func base36Fraction(value float64) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	fraction := value
	delta := math.Max(0.5*(math.Nextafter(value, math.Inf(1))-value), math.SmallestNonzeroFloat64)
	var digits []int
	for {
		// V8's C++ rounds each product before the subtraction and the sum below; Go would fuse them (FMA) on arm64.
		fraction = float64(fraction * 36)
		delta = float64(delta * 36)
		digit := int(fraction)
		digits = append(digits, digit)
		fraction -= float64(digit)
		if fraction > 0.5 || (fraction == 0.5 && digit&1 == 1) {
			if fraction+delta > 1 {
				// Round up, carrying through digits that overflow the radix.
				for len(digits) > 0 {
					last := len(digits) - 1
					if digits[last]+1 < 36 {
						digits[last]++
						break
					}
					digits = digits[:last]
				}
				break
			}
		}
		if fraction < delta {
			break
		}
	}
	out := make([]byte, len(digits))
	for i, d := range digits {
		out[i] = alphabet[d]
	}
	return string(out)
}

// fauxAPI is the api id the reference's faux provider gets: faux.ts randomId over the run's epoch and its first draw.
func fauxAPI(epoch int64, draw float64) string {
	return "faux:" + strconv.FormatInt(epoch, 10) + ":" + base36Fraction(draw)
}

// detenvClock reads the gate's DETENV_EPOCH and DETENV_SEED (defaults as in detenv.mjs) and fixes the run's clock.
func detenvClock() (epoch int64, rng *detenvRNG) {
	seed := uint32(1)
	epoch = epochMs
	if v, err := strconv.ParseInt(os.Getenv("DETENV_EPOCH"), 10, 64); err == nil {
		epoch = v
	}
	if v, err := strconv.ParseUint(os.Getenv("DETENV_SEED"), 10, 32); err == nil {
		seed = uint32(v)
	}
	epochMs = epoch
	clockStart = time.Now()
	return epoch, &detenvRNG{seed: seed}
}

// clockStart is the bubble's time at the start of the run: virtual time is the epoch plus what the bubble clock has advanced.
var clockStart time.Time

// virtualNow is detenv's clock for a native run: the epoch, advanced only by timers (inside a synctest bubble the time package
// moves only when every goroutine is blocked on one).
func virtualNow() float64 {
	return float64(epochMs) + float64(time.Since(clockStart))/float64(time.Millisecond)
}

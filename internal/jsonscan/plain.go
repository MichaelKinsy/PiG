// Package jsonscan finds the bytes a JSON string scan must stop at, eight bytes at a time.
package jsonscan

import "math/bits"

const (
	swarLSB = 0x0101010101010101
	swarMSB = 0x8080808080808080
)

// PlainEnd is the index of the first byte at or after i that a JSON string
// scan must look at (below 0x20, '"', '\\' or from 0x80), or len(s). It reads
// eight bytes at a time with byte loads and shifts (no unsafe, so TinyGo builds
// it the same way): lane k of the word is byte k, and a borrow can mark a lane
// above a marked one falsely, never one below, so the lowest mark is exact.
func PlainEnd(s string, i int) int {
	// Four independent words per branch: a long plain run, such as source text, keeps the ALUs busy and pays one branch
	// per 32 bytes. The lowest marked lane of the first word with one is the stop, as in the word loop below.
	for ; i+32 <= len(s); i += 32 {
		b := s[i : i+32]
		w0 := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 | uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
		w1 := uint64(b[8]) | uint64(b[9])<<8 | uint64(b[10])<<16 | uint64(b[11])<<24 | uint64(b[12])<<32 | uint64(b[13])<<40 | uint64(b[14])<<48 | uint64(b[15])<<56
		w2 := uint64(b[16]) | uint64(b[17])<<8 | uint64(b[18])<<16 | uint64(b[19])<<24 | uint64(b[20])<<32 | uint64(b[21])<<40 | uint64(b[22])<<48 | uint64(b[23])<<56
		w3 := uint64(b[24]) | uint64(b[25])<<8 | uint64(b[26])<<16 | uint64(b[27])<<24 | uint64(b[28])<<32 | uint64(b[29])<<40 | uint64(b[30])<<48 | uint64(b[31])<<56
		s0, s1, s2, s3 := plainStops(w0), plainStops(w1), plainStops(w2), plainStops(w3)
		if s0|s1|s2|s3 != 0 {
			switch {
			case s0 != 0:
				return i + bits.TrailingZeros64(s0)>>3
			case s1 != 0:
				return i + 8 + bits.TrailingZeros64(s1)>>3
			case s2 != 0:
				return i + 16 + bits.TrailingZeros64(s2)>>3
			}
			return i + 24 + bits.TrailingZeros64(s3)>>3
		}
	}
	for ; i+8 <= len(s); i += 8 {
		block := s[i : i+8]
		w := uint64(block[0]) | uint64(block[1])<<8 | uint64(block[2])<<16 | uint64(block[3])<<24 |
			uint64(block[4])<<32 | uint64(block[5])<<40 | uint64(block[6])<<48 | uint64(block[7])<<56
		if stops := plainStops(w); stops != 0 {
			return i + bits.TrailingZeros64(stops)>>3
		}
	}
	for ; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == '"' || c == '\\' || c >= 0x80 {
			return i
		}
	}
	return i
}

// plainStops marks the lanes of w that end a plain run: below 0x20, '"', '\\', or from 0x80.
// (w - 0x20 per lane) &^ w marks a lane below 0x20; w itself marks a lane from 0x80.
func plainStops(w uint64) uint64 {
	return ((w-swarLSB*0x20)&^w | swarZero(w^(swarLSB*'"')) | swarZero(w^(swarLSB*'\\')) | w) & swarMSB
}

// swarZero marks each zero lane of x, and possibly lanes above one.
func swarZero(x uint64) uint64 { return (x - swarLSB) &^ x }

// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import "errors"

// tiffLZWReader is tiff 0.9.1 decoder/stream.rs:111-157 LZWReader over weezl 0.1.8 `Decoder::with_tiff_size_switch(Msb, 8)` (decode.rs):
// the bytes of a TIFF LZW strip, most significant bit first, with the code size growing one code sooner than the GIF rule.
//
// weezl decodes into the caller's buffer through a burst machinery whose result is the plain sequential algorithm this type
// implements: the first code after a start or a clear code needs no predecessor (a code of next_code or more is invalid), every
// later code adds the table entry (previous word + first byte of this word), the entry table stops growing at 4096, and a clear
// code ends the table (without any output) and the end code ends the stream. What the sequential form must keep from weezl is
// when an invalid code is noticed: after a read call has filled its buffer exactly at the end of a word, weezl goes on and reads
// the next code (decode.rs:690-719 loops while a code is available and only stops early when a word is waiting in `buffer`), so
// an invalid next code fails that call although its bytes are all delivered. A stream whose codes run out before the end code
// fails the read that needs more bytes (LZWReader::read, NoProgress: "no lzw end code found"); after the end code a read gives 0.
type tiffLZWReader struct {
	data []byte
	// bitPos is the number of bits of data read.
	bitPos int

	// The code table: entry i is the word of entry prev[i] followed by the byte last[i] (the first 256 are literals). length[i] is the
	// byte count of the word, so words are reconstructed from the back.
	prev   [4096 + 6]uint16
	last   [4096 + 6]byte
	length [4096 + 6]uint16
	// entries is the number of entries of the table (weezl `Table.inner.len()`, bursts of codes past 4096 do not matter: no code
	// refers to them).
	entries  int
	nextCode int
	// codeSize is the current code width in bits.
	codeSize int

	// previous is the code before the one being decoded; hasPrevious is false at the start and after a clear code.
	previous    int
	hasPrevious bool
	started     bool
	ended       bool

	// pending is the word of the last code read that the caller has not taken yet.
	pending    [4096 + 2]byte
	pendingLen int
	pendingOff int
}

var (
	errTIFFLZWInvalidCode = errors.New("photonimage: tiff: lzw: invalid code")
	errTIFFLZWNoEnd       = errors.New("photonimage: tiff: lzw: no lzw end code found")
)

const (
	tiffLZWMinSize   = 8
	tiffLZWClear     = 1 << tiffLZWMinSize
	tiffLZWEnd       = tiffLZWClear + 1
	tiffLZWMaxCode   = 4096
	tiffLZWFirstFree = tiffLZWClear + 2
)

func newTIFFLZWReader(data []byte) *tiffLZWReader {
	z := &tiffLZWReader{data: data, codeSize: tiffLZWMinSize + 1, nextCode: tiffLZWFirstFree}
	return z
}

// initTable is weezl Table::init: the literals and the two special codes.
func (z *tiffLZWReader) initTable() {
	for i := range tiffLZWClear {
		z.prev[i], z.last[i], z.length[i] = 0, byte(i), 1
	}
	z.prev[tiffLZWClear], z.last[tiffLZWClear], z.length[tiffLZWClear] = 0, 0, 0
	z.prev[tiffLZWEnd], z.last[tiffLZWEnd], z.length[tiffLZWEnd] = 0, 0, 0
	z.entries = tiffLZWFirstFree
	z.nextCode = tiffLZWFirstFree
	z.codeSize = tiffLZWMinSize + 1
}

// nextSymbol is MsbBuffer::next_symbol: the next code of the current width, or false when fewer bits remain.
func (z *tiffLZWReader) nextSymbol() (int, bool) {
	if len(z.data)*8-z.bitPos < z.codeSize {
		return 0, false
	}
	code := 0
	for i := range z.codeSize {
		bit := int(z.data[(z.bitPos+i)>>3]>>(7-(z.bitPos+i)&7)) & 1
		code = code<<1 | bit
	}
	z.bitPos += z.codeSize
	return code, true
}

// word fills the pending buffer with the word of code and returns its first byte.
func (z *tiffLZWReader) word(code int) byte {
	n := int(z.length[code])
	cur := code
	for i := n - 1; i >= 0; i-- {
		z.pending[i] = z.last[cur]
		cur = int(z.prev[cur])
	}
	z.pendingLen, z.pendingOff = n, 0
	return z.pending[0]
}

// step reads and applies one code. ok is false when no code is left in the data.
func (z *tiffLZWReader) step() (ok bool, err error) {
	code, ok := z.nextSymbol()
	if !ok {
		return false, nil
	}
	if !z.hasPrevious {
		// decode.rs:629-668: the first code after a start or a clear code.
		switch {
		case code >= z.nextCode:
			return true, errTIFFLZWInvalidCode
		case code == tiffLZWClear:
			z.initTable()
			z.started = true
		case code == tiffLZWEnd:
			z.ended = true
		default:
			if !z.started {
				z.initTable()
				z.started = true
			}
			z.word(code)
			z.previous, z.hasPrevious = code, true
		}
		return true, nil
	}

	switch {
	case code == tiffLZWClear:
		// reset_tables: the entries after the special codes go, the code width is the initial one.
		z.entries = tiffLZWFirstFree
		z.nextCode = tiffLZWFirstFree
		z.codeSize = tiffLZWMinSize + 1
		z.hasPrevious = false
		return true, nil
	case code == tiffLZWEnd:
		z.ended = true
		return true, nil
	case code > z.nextCode:
		return true, errTIFFLZWInvalidCode
	}

	var first byte
	if code == z.nextCode {
		// The word of the previous code followed by its own first byte.
		z.word(z.previous)
		first = z.pending[0]
		z.pending[z.pendingLen] = first
		z.pendingLen++
	} else {
		first = z.word(code)
	}

	if z.entries < tiffLZWMaxCode {
		i := z.entries
		z.prev[i], z.last[i] = uint16(z.previous), first
		z.length[i] = z.length[z.previous] + 1
		z.entries++
		// The early change of TIFF: the width grows when the entry before the last of the width is added.
		maxCode := 1<<z.codeSize - 1
		if z.nextCode == maxCode-1 && z.codeSize < 12 {
			z.codeSize++
		}
		z.nextCode++
	}
	z.previous = code
	return true, nil
}

// read fills p as io.Read of LZWReader: it returns the bytes it could produce (0 after the end code) and an error when the data ends
// without an end code or holds an invalid code. When p is filled up to the end of a word the next code is read and applied too.
func (z *tiffLZWReader) read(p []byte) (int, error) {
	n := 0
	for {
		if z.pendingOff < z.pendingLen {
			c := copy(p[n:], z.pending[z.pendingOff:z.pendingLen])
			n += c
			z.pendingOff += c
			if z.pendingOff < z.pendingLen {
				return n, nil
			}
		}
		if n == len(p) {
			// p is full at the end of a word: weezl reads the next code unless the stream has ended or the last code was a clear
			// code (the call ends after a clear code).
			if !z.ended && z.hasPrevious {
				_, err := z.step()
				return n, err
			}
			return n, nil
		}
		if z.ended {
			return n, nil
		}
		ok, err := z.step()
		if err != nil {
			return n, err
		}
		if !ok {
			if n > 0 {
				return n, nil
			}
			return 0, errTIFFLZWNoEnd
		}
	}
}

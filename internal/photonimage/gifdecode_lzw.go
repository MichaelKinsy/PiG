// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

// gifLZW is weezl 0.1.8 `Decoder::new(BitOrder::Lsb, min_code_size)` (decode.rs:557-955), which the gif crate calls through `decode_bytes`
// with the bytes of a sub-block and the room that is left in the frame. The code table, the code width (it grows when the entry that
// ends a width is added) and the end and clear codes are the plain algorithm; what the port keeps from the library is how its calls end:
//
//   - A word that does not fit in the room that is left is kept whole in the buffer, and the call ends; the next call writes it first.
//   - When a word fills the room exactly the call goes on and reads the next code if its bits are in the bit buffer or the input: a clear
//     code or the end code is applied, an invalid code (above the next code) fails the call although the words were delivered, and another
//     one is added to the table and its word kept in the buffer.
//   - A clear code ends the call (the first code after it starts the next), the first code after a clear code needs no predecessor and
//     a code of the next code or more is invalid there.
//   - The 64-bit bit buffer is filled from the input before each code (the library fills it before each burst of up to six codes), which
//     only decides how many bytes of the input a call reports as consumed.
type gifLZW struct {
	minSize   int
	clearCode int
	endCode   int

	// The code table: entry i is the word of entry prev[i] followed by last[i]; depth is the length of the word.
	prev       [4096]uint16
	last       [4096]byte
	depth      [4096]uint16
	entries    int
	tableEmpty bool
	nextCode   int
	codeSize   int

	bitBuf uint64
	bits   int

	// hasLast is `last` (the code before the next one); lastCode is its code.
	hasLast  bool
	lastCode int
	hasEnded bool

	// buf holds the word of the code that the room did not take whole; readMark..writeMark has not been delivered.
	buf       [4096 + 1]byte
	readMark  int
	writeMark int
}

const gifMaxEntries = 4096

func newGIFLZW(minSize byte) *gifLZW {
	z := &gifLZW{minSize: int(minSize), clearCode: 1 << minSize, endCode: 1<<minSize + 1, tableEmpty: true}
	z.nextCode = z.endCode + 1
	z.codeSize = z.minSize + 1
	return z
}

// initTables is `init_tables`, resetTables `reset_tables` (the literals of the table never change).
func (z *gifLZW) initTables() {
	for i := range z.clearCode {
		z.last[i], z.depth[i] = byte(i), 1
	}
	z.depth[z.clearCode], z.depth[z.endCode] = 0, 0
	z.tableEmpty = false
	z.resetTables()
}

func (z *gifLZW) resetTables() {
	z.codeSize = z.minSize + 1
	z.nextCode = z.endCode + 1
	z.entries = z.nextCode
}

// refill is `LsbBuffer::refill_bits`: as many whole bytes of the input as the 64 bits take.
func (z *gifLZW) refill(inp *[]byte) {
	wish := (64 - z.bits) / 8
	n := min(wish, len(*inp))
	for i := range n {
		z.bitBuf |= uint64((*inp)[i]) << (z.bits + 8*i)
	}
	z.bits += 8 * n
	*inp = (*inp)[n:]
}

// getBits is `LsbBuffer::get_bits`.
func (z *gifLZW) getBits() (int, bool) {
	if z.bits < z.codeSize {
		return 0, false
	}
	code := int(z.bitBuf & (1<<z.codeSize - 1))
	z.bitBuf >>= z.codeSize
	z.bits -= z.codeSize
	return code, true
}

// reconstruct writes the word of code to out, which is as long as the word, and returns its first byte.
func (z *gifLZW) reconstruct(code int, out []byte) byte {
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = z.last[code]
		code = int(z.prev[code])
	}
	return out[0]
}

func (z *gifLZW) fillReconstruct(code int) byte {
	n := int(z.depth[code])
	z.readMark, z.writeMark = 0, n
	return z.reconstruct(code, z.buf[:n])
}

// decode is `Decoder::decode_bytes` (`DecodeState::advance`): it decodes the codes of inp into out and returns how many bytes of inp it
// consumed into its bit buffer and how many bytes it wrote.
func (z *gifLZW) decode(inp, out []byte) (consumed, produced int, err error) {
	if z.hasEnded {
		return 0, 0, nil
	}
	oIn, oOut := len(inp), len(out)
	done := func() (int, int, error) { return oIn - len(inp), oOut - len(out), nil }

	haveLink, code := false, 0
	if !z.hasLast {
		if z.bits < z.codeSize {
			z.refill(&inp)
		}
		c, ok := z.getBits()
		switch {
		case !ok:
			return done()
		case c >= z.nextCode:
			return 0, 0, errGIFLZW
		case c == z.clearCode:
			z.initTables()
		case c == z.endCode:
			z.hasEnded = true
			return done()
		default:
			if z.tableEmpty {
				z.initTables()
			}
			z.fillReconstruct(c)
			haveLink, code = true, c
		}
	} else {
		haveLink, code = true, z.lastCode
		z.hasLast = false
	}

	if haveLink {
		// The word that was kept from the last call (or the first code's).
		remain := z.buf[z.readMark:z.writeMark]
		switch {
		case len(remain) > len(out):
			if len(out) != 0 {
				copy(out, remain)
				z.readMark += len(out)
				out = out[len(out):]
			}
		case len(remain) != 0:
			copy(out, remain)
			z.readMark += len(remain)
			out = out[len(remain):]
		}
	}

	for haveLink {
		if len(out) == 0 && z.readMark < z.writeMark {
			break
		}
		z.refill(&inp)
		c, ok := z.getBits()
		if !ok {
			break
		}
		if c == z.clearCode {
			z.resetTables()
			haveLink = false
			break
		}
		if c == z.endCode {
			z.hasEnded = true
			haveLink = false
			break
		}
		if c > z.nextCode {
			return 0, 0, errGIFLZW
		}
		requiredLen := int(z.depth[c])
		if c == z.nextCode {
			requiredLen = int(z.depth[code]) + 1
		}
		var first byte
		inBuffer := requiredLen > len(out)
		switch {
		case inBuffer && c == z.nextCode:
			n := int(z.depth[code])
			z.reconstruct(code, z.buf[:n])
			z.buf[n] = z.buf[0]
			z.readMark, z.writeMark = 0, n+1
			first = z.buf[0]
		case inBuffer:
			first = z.fillReconstruct(c)
		case c == z.nextCode:
			target := out[:requiredLen]
			out = out[requiredLen:]
			z.reconstruct(code, target[:requiredLen-1])
			target[requiredLen-1] = target[0]
			first = target[0]
		default:
			target := out[:requiredLen]
			out = out[requiredLen:]
			first = z.reconstruct(c, target)
		}
		// Each code that is read adds an entry to the table, until it has 4096.
		if z.entries < gifMaxEntries {
			z.prev[z.entries], z.last[z.entries], z.depth[z.entries] = uint16(code), first, z.depth[code]+1
			z.entries++
			if z.nextCode == 1<<z.codeSize-1 && z.codeSize < 12 {
				z.codeSize++
			}
			z.nextCode++
		}
		code = c
		if inBuffer {
			break
		}
	}
	z.hasLast, z.lastCode = haveLink, code
	return done()
}

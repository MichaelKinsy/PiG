// SPDX-License-Identifier: MIT

package abi

import "encoding/binary"

// ChunkSize is the size of the blocks Chunks starts once a payload outgrows its first block.
const ChunkSize = 64 << 10

// Chunks is a payload built in blocks of at most ChunkSize bytes, or of one item when that is larger; each block grows by
// append. A notice or effect of megabytes (a 20,000-task commit's hops payload is 7 MB, its published notice 1.2 MB) then
// needs no block of its size: the TinyGo core's collector does not move objects, and late in a long run its heap has room
// for such a payload only in smaller free ranges. The payload is the blocks' concatenation; the step encoder hands each
// block to the host as a part of its own.
type Chunks [][]byte

// Tail returns the block the caller appends its next item of n bytes to, and the caller stores the result back with
// Set: the last block while the item keeps it within ChunkSize, else a new, empty block with room for len(tailRoom)
// bytes. The blocks grow by append, so Chunks has no allocation site of its own (budget/allocs.json): the room is
// appended from tailRoom, not made.
func (c *Chunks) Tail(n int) []byte {
	if k := len(*c); k > 0 && len((*c)[k-1])+n <= ChunkSize {
		return (*c)[k-1]
	}
	*c = append(*c, append([]byte(nil), tailRoom[:]...)[:0])
	return (*c)[len(*c)-1]
}

// tailRoom is the room a new block starts with: a published notice of a small commit fits in one allocation. It is
// read-only (all zeros; only copied from). It is 255 bytes, not 256: TinyGo 0.42's sliceGrow rounds an appended capacity up
// to the next power of two (1 << bits.Len(n)), so 256 bytes would allocate 512.
var tailRoom [255]byte

// Set stores b, the block Tail returned with an item appended, as the last block.
func (c *Chunks) Set(b []byte) { (*c)[len(*c)-1] = b }

// Append appends item to the payload, in the last block when it fits (see Tail).
func (c *Chunks) Append(item []byte) { c.Set(append(c.Tail(len(item)), item...)) }

// Len is the payload's length.
func (c Chunks) Len() int {
	n := 0
	for _, b := range c {
		n += len(b)
	}
	return n
}

// AppendHopsChunks appends the wire form of h to c, as AppendHops does, a statement at a time.
func AppendHopsChunks(c *Chunks, h Hops) {
	b := c.Tail(8)
	b = binary.LittleEndian.AppendUint32(b, h.N)
	c.Set(binary.LittleEndian.AppendUint32(b, h.At))
	for i := range h.Commits {
		commit := &h.Commits[i]
		body := 10
		for j := range commit.Stmts {
			body += 2 + paramsSize(commit.Stmts[j].Params)
		}
		b := c.Tail(14)
		b = binary.LittleEndian.AppendUint32(b, uint32(body))
		b = binary.LittleEndian.AppendUint64(b, uint64(commit.Seq))
		c.Set(binary.LittleEndian.AppendUint16(b, uint16(len(commit.Stmts))))
		for j := range commit.Stmts {
			s := &commit.Stmts[j]
			b := c.Tail(2 + paramsSize(s.Params))
			b = binary.LittleEndian.AppendUint16(b, s.SQL)
			c.Set(appendParams(b, s.Params))
		}
	}
}

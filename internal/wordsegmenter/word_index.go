//go:build !pig_strip_word_dictionaries

package wordsegmenter

import "strings"

// indexBlock is the number of words that share one stored offset. It is a power of two, and sixteen length bytes are two uint64 loads.
const (
	indexBlockShift = 4
	indexBlock      = 1 << indexBlockShift
)

// firstBytes is the number of uint24 entries of the first-byte table of a byte-keyed index: entry b is the index of the first word whose first byte is at least b, and entry 256 is the count.
const firstBytes = 257

// wordIndex borrows one embedded dictionary index (README: Regeneration; dictionary_format.py writes it). Lookup narrows a sorted prefix range without allocating a runtime trie. The index holds a uint32 count, the first-byte table when it is byte-keyed, one uint24 offset per block of indexBlock words, one length byte per word, one weight byte per word when weighted, then the words in byte-lexical order. A word's start is its block's offset plus the lengths of the words before it in the block. The fields are the parts of the one embedded string, so a lookup does no offset arithmetic over the whole index.
type wordIndex struct {
	count int
	// heads is the first-byte table, empty when the index is not byte-keyed.
	heads  string
	blocks string
	// lengths runs to the end of the index, so that the sixteen length bytes of the last block are in bounds.
	lengths string
	weights string
	words   string
}

func newWordIndex(data string, weighted, keyed bool) wordIndex {
	count := le32(data)
	blocks := 4
	if keyed {
		blocks += firstBytes * 3
	}
	lengths := blocks + (count+indexBlock-1)>>indexBlockShift*3
	weights := lengths + count
	words := weights
	if weighted {
		words += count
	}
	return wordIndex{count: count, heads: data[4:blocks], blocks: data[blocks:lengths], lengths: data[lengths:], weights: data[weights:words], words: data[words:]}
}

// le32 reads the little-endian uint32 at the start of s.
func le32(s string) int {
	//portlint:allow utf16units offset indexes the embedded little-endian ICU dictionary bytes, not a JS string; Pi's Intl.Segmenter (utils.ts:getWordSegmenter) has no such table
	return int(uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24)
}

// le24 reads the little-endian uint24 at the start of s.
func le24(s string) int {
	//portlint:allow utf16units offset indexes the embedded little-endian ICU dictionary bytes, not a JS string; Pi's Intl.Segmenter (utils.ts:getWordSegmenter) has no such table
	return int(s[0]) | int(s[1])<<8 | int(s[2])<<16
}

func le64(s string) uint64 {
	return uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 | uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
}

// byteRange returns the words whose first byte is c as [low, high) of a byte-keyed index.
func (d *wordIndex) byteRange(c int) (low, high int) {
	return le24(d.heads[c*3:]), le24(d.heads[c*3+3:])
}

// start returns the offset in words of the first word of block.
func (d *wordIndex) start(block int) int {
	return le24(d.blocks[block*3:])
}

// before returns the offset in words of word i: its block's offset plus the lengths of the words before it in the block. The block's sixteen length bytes are summed as 16-bit lanes of two uint64 loads, not one byte at a time. A lane holds at most four lengths of at most 255, and the four lanes of the product sum to at most 4,080, so no lane overflows.
func (d *wordIndex) before(i int) int {
	first := i &^ (indexBlock - 1)
	lengths := d.lengths[first : first+indexBlock]
	n := uint(i - first)
	low, high := le64(lengths), le64(lengths[8:])
	low &= 1<<(8*n) - 1
	high &= 1<<(8*max(n, 8)-64) - 1
	const evens = 0x00ff00ff00ff00ff
	lanes := low&evens + low>>8&evens + high&evens + high>>8&evens
	return d.start(first>>indexBlockShift) + int(lanes*0x0001000100010001>>48)
}

// head returns the first word of block.
func (d *wordIndex) head(block int) string {
	start := d.start(block)
	return d.words[start : start+d.length(block<<indexBlockShift)]
}

// length returns the byte length of word i.
func (d *wordIndex) length(i int) int {
	return int(d.lengths[i])
}

// weight returns the weight of word i of a weighted index.
func (d *wordIndex) weight(i int) uint32 {
	return uint32(d.weights[i])
}

type boundKind uint8

const (
	atLeast     boundKind = iota // word >= key
	unprefixed                   // word does not start with key
	byteAtLeast                  // byte depth of word (-1 when the word is shorter) >= c
	byteAbove                    // byte depth of word (-1 when the word is shorter) > c
)

// bound is a predicate over the sorted words that is false below some index and true from it on.
type bound struct {
	kind  boundKind
	key   string
	depth int
	c     int
}

func (b *bound) holds(word string) bool {
	switch b.kind {
	case atLeast:
		return word >= b.key
	case unprefixed:
		return !strings.HasPrefix(word, b.key)
	}
	at := -1
	if len(word) > b.depth {
		at = int(word[b.depth])
	}
	if b.kind == byteAtLeast {
		return at >= b.c
	}
	return at > b.c
}

// first returns the least index in [lo, hi) whose word satisfies b, or hi when none does. It searches the first word of each block, which costs no length sum, then scans at most one block in order. near says the answer is likely close to lo, so the block search doubles its step from lo before it halves.
func (d *wordIndex) first(lo, hi int, b *bound, near bool) int {
	if lo >= hi {
		return hi
	}
	// The block heads strictly inside (lo, hi) are blocks lowBlock to highBlock. left ends as the first of them that satisfies b, or highBlock+1.
	lowBlock, highBlock := lo>>indexBlockShift+1, (hi-1)>>indexBlockShift
	left, right := lowBlock, highBlock+1
	for step := 1; near && left+step-1 < right; step <<= 1 {
		probe := left + step - 1
		if b.holds(d.head(probe)) {
			right = probe
			break
		}
		left = probe + 1
	}
	for left < right {
		mid := int(uint(left+right) >> 1)
		if b.holds(d.head(mid)) {
			right = mid
		} else {
			left = mid + 1
		}
	}
	// The answer is after the head before left and at or before the head at left.
	from, to := lo, hi
	if left > lowBlock {
		from = (left-1)<<indexBlockShift + 1
	}
	if left <= highBlock {
		to = left << indexBlockShift
	}
	if from >= to {
		return to
	}
	start := d.before(from)
	for i := from; i < to; i++ {
		n := d.length(i)
		if b.holds(d.words[start : start+n]) {
			return i
		}
		start += n
	}
	return to
}

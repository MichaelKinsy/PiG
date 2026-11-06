//go:build !pig_strip_word_dictionaries

package wordsegmenter

import "encoding/binary"

// indexBlock is the number of words that share one stored offset.
const indexBlock = 16

// wordIndex borrows one embedded dictionary index (README: Regeneration; dictionary_format.py writes it). Lookup narrows a sorted prefix range without allocating a runtime trie. The index holds a uint32 count, one uint32 offset per block of indexBlock words, one length byte per word, one weight byte per word when weighted, then the words in byte-lexical order. A word's start is its block's offset plus the lengths of the words before it in the block.
type wordIndex struct {
	data     string
	weighted bool
}

func (d wordIndex) count() int {
	//portlint:allow utf16units offset indexes the embedded little-endian ICU dictionary bytes, not a JS string; Pi's Intl.Segmenter (utils.ts:getWordSegmenter) has no such table
	return int(binary.LittleEndian.Uint32([]byte(d.data[:4])))
}

// word returns word index of an index of count words.
func (d wordIndex) word(index, count int) string {
	lengths := 4 + (count+indexBlock-1)/indexBlock*4
	block := index / indexBlock
	//portlint:allow utf16units offset indexes the embedded little-endian ICU dictionary bytes, not a JS string; Pi's Intl.Segmenter (utils.ts:getWordSegmenter) has no such table
	start := int(binary.LittleEndian.Uint32([]byte(d.data[4+block*4 : 8+block*4])))
	for i := block * indexBlock; i < index; i++ {
		start += int(d.data[lengths+i])
	}
	words := lengths + count
	if d.weighted {
		words += count
	}
	return d.data[words+start : words+start+int(d.data[lengths+index])]
}

// weight returns the weight of word index of a weighted index of count words.
func (d wordIndex) weight(index, count int) uint32 {
	return uint32(d.data[4+(count+indexBlock-1)/indexBlock*4+count+index])
}

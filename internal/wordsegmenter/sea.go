//go:build !pig_strip_word_dictionaries

// © 2016 and later: Unicode, Inc. and others.
// Copyright (C) 2006-2016, International Business Machines Corporation and others.
// ICU DictionaryBreakEngine, PossibleWord and Thai/Lao/Khmer/BurmeseBreakEngine translation.
// See LICENSES/Unicode-3.0.txt and LICENSES/LicenseRef-ICU-SEA.txt.

package wordsegmenter

import (
	_ "embed"
	"encoding/binary"
	"sort"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

//go:embed thai_dictionary.bin
var thaiDictionary string

//go:embed lao_dictionary.bin
var laoDictionary string

//go:embed khmer_dictionary.bin
var khmerDictionary string

//go:embed burmese_dictionary.bin
var burmeseDictionary string

// byteDictionary borrows ICU's offset-transformed word bytes in a sorted index. Lookup narrows the prefix range without allocating a runtime trie.
type byteDictionary string

func (d byteDictionary) uint32At(offset int) int {
	return int(binary.LittleEndian.Uint32([]byte(d[offset : offset+4])))
}
func (d byteDictionary) word(index int) string {
	start := 4 + (d.uint32At(0)+1)*4
	return string(d[start+d.uint32At(4+index*4) : start+d.uint32At(8+index*4)])
}

// possibleWord retains ICU's three-position candidate cache, ascending length order, longest initial choice, mark and backtracking behavior. All four scripts use BMP characters, so these code-point lengths also count UTF-16 units.
type possibleWord struct {
	offset, count, prefix, mark, current int
	lengths                              [20]int
}

func (w *possibleWord) candidates(text []rune, pos *int, dictionary byteDictionary, base rune) int {
	start := *pos
	if start != w.offset {
		w.offset, w.count, w.prefix = start, 0, 0
		low, high := 0, dictionary.uint32At(0)
		for i := start; i < len(text); i++ {
			c := text[i] - base
			switch text[i] {
			case 0x200c:
				c = 0xfe
			case 0x200d:
				c = 0xff
			}
			w.prefix++ // ICU counts the mismatching code point, too.
			if c < 0 || c > 0xfd && text[i] != 0x200c && text[i] != 0x200d {
				break
			}
			depth := i - start
			at := func(index int) int {
				word := dictionary.word(index)
				if len(word) <= depth {
					return -1
				}
				return int(word[depth])
			}
			low += sort.Search(high-low, func(i int) bool { return at(low+i) >= int(c) })
			high = low + sort.Search(high-low, func(i int) bool { return at(low+i) > int(c) })
			if low == high {
				break
			}
			if len(dictionary.word(low)) == depth+1 {
				if w.count < len(w.lengths) {
					w.lengths[w.count] = depth + 1
					w.count++
				}
				if high-low == 1 {
					break
				} // USTRINGTRIE_FINAL_VALUE
			}
		}
	}
	if w.count > 0 {
		*pos = start + w.lengths[w.count-1]
	}
	w.current = w.count - 1
	w.mark = w.current
	return w.count
}
func (w *possibleWord) acceptMarked(pos *int) int {
	length := w.lengths[w.mark]
	*pos = w.offset + length
	return length
}
func (w *possibleWord) backUp(pos *int) bool {
	if w.current <= 0 {
		return false
	}
	w.current--
	*pos = w.offset + w.lengths[w.current]
	return true
}

func seaBeginWord(script rune, r rune) bool {
	switch script {
	case 1:
		return r >= 0x0e01 && r <= 0x0e2e || r >= 0x0e40 && r <= 0x0e44
	case 2:
		return r >= 0x0e81 && r <= 0x0eae || r >= 0x0edc && r <= 0x0edd || r >= 0x0ec0 && r <= 0x0ec4
	case 3:
		return r >= 0x1780 && r <= 0x17b3
	case 4:
		return r >= 0x1000 && r <= 0x102a
	}
	return false
}
func seaEndWord(script rune, r rune) bool {
	if complexContext(r)&7 != script {
		return false
	}
	switch script {
	case 1:
		return r != 0x0e31 && (r < 0x0e40 || r > 0x0e44)
	case 2:
		return r < 0x0ec0 || r > 0x0ec4
	case 3:
		return r != 0x17d2
	}
	return true
}

func seaMarkBest(words *[3]possibleWord, found int, text []rune, pos *int, dictionary byteDictionary, base rune) {
	first, second, third := &words[found%3], &words[(found+1)%3], &words[(found+2)%3]
	if *pos >= len(text) {
		return
	}
	for {
		if second.candidates(text, pos, dictionary, base) > 0 {
			first.mark = first.current
			if *pos >= len(text) {
				return
			}
			for {
				if third.candidates(text, pos, dictionary, base) > 0 {
					first.mark = first.current
					return
				}
				if !second.backUp(pos) {
					break
				}
			}
		}
		if !first.backUp(pos) {
			return
		}
	}
}

// seaDivide ports ICU 78.3's four divideUpDictionaryRange implementations. The engines share three-word lookahead and unknown-word resynchronization; Thai additionally consumes elision/repetition suffixes and requires more than four code points. Returned byte offsets exclude the range end, as in ICU's DictionaryCache.
func seaDivide(text string, script rune, visit func(int)) {
	// pig additive (D92): a Piglet that strips word-dictionaries finds no boundary inside a Thai, Lao, Khmer or Burmese run, as dictionaries_off.go does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.WordDictionaries) {
		return
	}
	chars := []rune(text)
	if len(chars) < 4 || script == 1 && len(chars) == 4 {
		return
	}
	dictionaries := [...]string{"", thaiDictionary, laoDictionary, khmerDictionary, burmeseDictionary}
	bases := [...]rune{0, 0xe00, 0xe80, 0x1780, 0x1000}
	dictionary, base := byteDictionary(dictionaries[script]), bases[script]
	words := [3]possibleWord{{offset: -1}, {offset: -1}, {offset: -1}}
	found, pos := 0, 0
	for pos < len(chars) {
		current, length := pos, 0
		word := &words[found%3]
		candidates := word.candidates(chars, &pos, dictionary, base)
		if candidates > 0 {
			if candidates > 1 {
				seaMarkBest(&words, found, chars, &pos, dictionary, base)
			}
			length = word.acceptMarked(&pos)
			found++
		}
		if pos < len(chars) && length < 3 {
			next := &words[found%3]
			if next.candidates(chars, &pos, dictionary, base) == 0 && (length == 0 || next.prefix < 3) {
				for {
					previous := chars[pos]
					pos++
					if pos >= len(chars) {
						break
					}
					if seaEndWord(script, previous) && seaBeginWord(script, chars[pos]) {
						saved := pos
						count := words[(found+1)%3].candidates(chars, &pos, dictionary, base)
						pos = saved
						if count > 0 {
							break
						}
					}
				}
				if length == 0 {
					found++
				}
				length = pos - current
			} else {
				pos = current + length
			}
		}
		// fMarkSet is Script & LineBreak=SA & M, plus U+0020.
		for pos < len(chars) && (complexContext(chars[pos])&16 != 0 || chars[pos] == ' ') {
			pos++
			length++
		}
		if script == 1 && pos < len(chars) && length > 0 {
			if words[found%3].candidates(chars, &pos, dictionary, base) == 0 {
				suffix := func(r rune) bool { return r == 0xe2f || r == 0xe46 }
				if chars[pos] == 0xe2f && !suffix(chars[pos-1]) {
					pos++
					length++
				}
				if pos < len(chars) && chars[pos] == 0xe46 && chars[pos-1] != 0xe46 {
					pos++
					length++
				}
			} else {
				pos = current + length
			}
		}
		if length > 0 && current+length < len(chars) {
			// All engine-handled code points have three-byte UTF-8 encodings.
			visit((current + length) * 3)
		}
	}
}

//go:build !pig_strip_word_dictionaries

// © 2016 and later: Unicode, Inc. and others.
// Copyright (C) 2006-2016, International Business Machines Corporation and others.
// ICU CjkBreakEngine translation; see LICENSES/Unicode-3.0.txt and LICENSES/LicenseRef-ICU-CJK.txt.

// Ports packages/tui/src/utils.ts (getWordSegmenter: the CJK dictionary engine of Intl.Segmenter)

package wordsegmenter

import (
	_ "embed"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/internal/allocsize"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// The immutable trie is generated from ICU release-78.3 cjdict.txt (dictionary_format.py documents its layout). Lookup borrows its embedded bytes; it does not build a dictionary map or decode anything on the input loop. Its labels are UTF-16 units: every dictionary word is BMP text without U+FFFD, so no supplementary or invalid text can match, as no dictionary word contains one.
//
//go:embed cjk_dictionary.bin
var cjkDictionary string

// cjkTrie is a function, not a package variable: a variable initialised from the embedded string would run at init and link the dictionary into every program that imports the package, including the headless embed that must not carry it.
func cjkTrie() wordTrie { return wordTrie{data: cjkDictionary, width: 2, weighted: true} }

// dictionaryMatches calls visit(length, cost) for each dictionary word that is a prefix of chars[start:], shortest first, as far as the 20-code-point candidate limit allows.
func dictionaryMatches(chars []rune, start int, visit func(length int, cost uint32)) {
	trie := cjkTrie()
	cursor := trie.root()
	for length := 1; length <= 20 && start+length <= len(chars); length++ {
		r := chars[start+length-1]
		if r > 0xffff {
			return
		}
		step, ok := trie.step(&cursor, int(r))
		if !ok {
			return
		}
		if step.terminal {
			visit(length, step.weight)
		}
		if !step.more {
			return
		}
	}
}

func katakana(r rune) bool {
	return r >= 0x30a1 && r <= 0x30fe && r != 0x30fb || r >= 0xff66 && r <= 0xff9f
}

// cjkBoundaries ports ICU 78.3 CjkBreakEngine::divideUpDictionaryRange (word, not phrase, mode): NFKC mapping, weighted shortest path, unknown-character cost and Katakana-run candidates. Returned positions index the original UTF-8 text.
func cjkBoundaries(text string) []int {
	// pig additive (D92): a Piglet that strips word-dictionaries finds no boundary inside a CJK run, as dictionaries_off.go does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.WordDictionaries) {
		return nil
	}
	normalized := text
	var inputMap []int
	if !norm.NFKC.IsNormalString(text) {
		var builder strings.Builder
		var iterator norm.Iter
		iterator.InitString(norm.NFKC, text)
		for !iterator.Done() {
			start := iterator.Pos()
			fragment := iterator.Next()
			builder.Write(fragment)
			for range string(fragment) {
				inputMap = append(inputMap, start)
			}
		}
		inputMap = append(inputMap, len(text))
		normalized = builder.String()
	}
	var offsets []int
	var chars []rune
	for index, r := range normalized {
		offsets = append(offsets, index)
		chars = append(chars, r)
	}
	offsets = append(offsets, len(normalized))
	n := len(chars)
	const unreachable = ^uint32(0)
	best := make([]uint32, allocsize.Add(n, 1))
	previous := make([]int, allocsize.Add(n, 1))
	for i := range n + 1 {
		best[i] = unreachable
		previous[i] = -1
	}
	best[0] = 0
	wasKatakana := false
	for i, r := range chars {
		if best[i] == unreachable {
			continue
		}
		single := false
		consider := func(length int, cost uint32) {
			if length == 1 {
				single = true
			}
			next := i + length
			total := best[i] + cost
			if total < best[next] {
				best[next] = total
				previous[next] = i
			}
		}
		dictionaryMatches(chars, i, consider)
		if !single && (r < 0xac00 || r > 0xd7a3) {
			consider(1, 255)
		}
		isKatakana := katakana(r)
		if !wasKatakana && isKatakana {
			length := 1
			for i+length < n && length < 20 && katakana(chars[i+length]) {
				length++
			}
			if length < 20 {
				costs := [...]uint32{8192, 984, 408, 240, 204, 252, 300, 372, 480}
				cost := uint32(8192)
				if length < len(costs) {
					cost = costs[length]
				}
				consider(length, cost)
			}
		}
		wasKatakana = isKatakana
	}
	var reversed []int
	if best[n] == unreachable {
		reversed = append(reversed, n)
	} else {
		for i := n; i > 0; i = previous[i] {
			reversed = append(reversed, i)
		}
	}
	var boundaries []int
	last := 0
	for _, index := range slices.Backward(reversed) {
		position := offsets[index]
		if inputMap != nil {
			position = inputMap[index]
		}
		if position > last {
			boundaries = append(boundaries, position)
			last = position
		}
	}
	return boundaries
}

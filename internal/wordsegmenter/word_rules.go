// © 2016 and later: Unicode, Inc. and others.
// Copyright (C) 2002-2016, International Business Machines Corporation and others.
// ICU word.txt rule tailoring; see LICENSES/Unicode-3.0.txt.

package wordsegmenter

import (
	"sort"
	"unicode/utf8"
)

const (
	ruleKanaKanji rune = 1 << iota
	ruleHangul
	ruleDictionaryCJK
	ruleExcluded
	ruleExFm
	ruleExtendNumLet
	ruleHebrew
)

func wordRuleClass(r rune) rune {
	i := sort.Search(len(wordRuleRanges), func(i int) bool { return wordRuleRanges[i][1] >= r })
	if i < len(wordRuleRanges) && wordRuleRanges[i][0] <= r {
		return wordRuleRanges[i][2]
	}
	return 0
}

// ICU word.txt adds ComplexContext - Extend - Control to ALetterPlus, removes dictionaryCJK from ALetter, and removes Han from Extend. Same-byte-width representatives feed those exact classes to the UAX rule scanner; emitted text and dictionary input always retain the original spelling.
func tailoredWordRules(text string) string {
	var tailored []byte
	for i, r := range text {
		representative := rune(0)
		if complexContext(r)&8 != 0 {
			representative = 0xa640 // ALetter
			if utf8.RuneLen(r) == 4 {
				representative = 0x10400
			}
		} else if wordRuleClass(r)&ruleExcluded != 0 {
			representative = 0x4e00 // Other
			if utf8.RuneLen(r) == 4 {
				representative = 0x20000
			}
		}
		if representative == 0 {
			continue
		}
		if tailored == nil {
			tailored = []byte(text)
		}
		utf8.EncodeRune(tailored[i:], representative)
	}
	if tailored == nil {
		return text
	}
	return string(tailored)
}

func joinWordRules(left, right string) bool {
	last, _ := utf8.DecodeLastRuneInString(left)
	first, _ := utf8.DecodeRuneInString(right)
	// ICU's two extra chaining rules do not permit intervening ExFm characters.
	return wordRuleClass(last)&wordRuleClass(first)&(ruleKanaKanji|ruleHangul) != 0
}

// DictionaryCache applies the containing rule span's status to every dictionary break. A generic trailing ExFm match can replace a word status with zero; a dictionary match alone does not make a segment word-like.
func ruleWordLike(text string) bool {
	tail := len(text)
	for tail > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:tail])
		if wordRuleClass(r)&ruleExFm == 0 {
			break
		}
		tail -= size
	}
	if tail == 0 {
		return false
	}
	last, size := utf8.DecodeLastRuneInString(text[:tail])
	class := wordRuleClass(last)
	if class&(ruleHangul|ruleExtendNumLet) != 0 {
		return tail == len(text) && (class&ruleHangul != 0 || tail > size)
	}
	if wordLike(text[tail-size : tail]) {
		return true
	}
	if tail < len(text) {
		return false
	}
	previous := tail - size
	for previous > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:previous])
		if wordRuleClass(r)&ruleExFm != 0 {
			previous -= size
			continue
		}
		return last == '\'' && wordRuleClass(r)&ruleHebrew != 0 || previous == tail-utf8.RuneLen(last) && wordRuleClass(r)&class&ruleKanaKanji != 0
	}
	return false
}

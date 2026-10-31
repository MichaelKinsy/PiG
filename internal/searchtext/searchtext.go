// Package searchtext is the tokenizer of tool search, the one `tokenize` of packages/coding-agent/src/extensions/tool-search/tool.ts and packages/durable/src/search.ts, which Pi keeps as two copies:
// Unicode word segmentation with identifier boundaries and English stop words and singular forms. The coding agent's ranker (coding/extension/builtin/toolsearch) and durable's
// (durable/internal/search) both call it.
package searchtext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/wordsegmenter"
)

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true,
	"from": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "with": true,
}

var (
	lowerUpper    = lazyregexp.New(`([a-z0-9])([A-Z])`)
	upperRunUpper = lazyregexp.New(`([A-Z]+)([A-Z][a-z])`)
	pluralEnding  = lazyregexp.New(`(ches|shes|sses|xes|zes)$`)
)

// stem is a naive singular form, so `issues` matches `issue` and `searches` matches `search`. Terms are ASCII.
func stem(term string) string {
	switch {
	case len(term) > 4 && strings.HasSuffix(term, "ies"): //portlint:allow utf16units stem is called for ASCII terms only
		return term[:len(term)-3] + "y"
	case len(term) > 4 && pluralEnding.MatchString(term): //portlint:allow utf16units stem is called for ASCII terms only
		return term[:len(term)-2]
	case len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss"): //portlint:allow utf16units stem is called for ASCII terms only
		return term[:len(term)-1]
	}
	return term
}

// wordText is text with every run of characters that are not letters, marks or numbers (`/[^\p{L}\p{M}\p{N}]+/gu`) replaced by one space. An unpaired surrogate, which WTF-8 spells as
// invalid UTF-8 bytes, is such a character.
func wordText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	separated := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if wordRune := r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r)); !wordRune {
			if !separated {
				out.WriteByte(' ')
				separated = true
			}
		} else {
			out.WriteString(text[i : i+size])
			separated = false
		}
		i += size
	}
	return out.String()
}

func isASCIIAlnum(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }

// isASCIIWordText is `/^[a-z0-9 ]*$/`.
func isASCIIWordText(text string) bool {
	for i := range len(text) { //portlint:allow utf16units the bytes are tested against ASCII classes, so a non-ASCII byte is rejected
		if c := text[i]; c != ' ' && !isASCIIAlnum(c) {
			return false
		}
	}
	return true
}

// isASCIIWord is `/^[a-z0-9]+$/`.
func isASCIIWord(word string) bool {
	if word == "" {
		return false
	}
	for i := range len(word) { //portlint:allow utf16units the bytes are tested against ASCII classes, so a non-ASCII byte is rejected
		if !isASCIIAlnum(word[i]) {
			return false
		}
	}
	return true
}

// Tokenize is Unicode word segmentation with identifier boundaries and English stop words and singular forms: the text is NFC-normalized, split at camelCase boundaries, lowercased, reduced to
// letters, marks and numbers, and split into the word-like segments of the ICU word break rules (internal/wordsegmenter, the Intl.Segmenter of Pi); plain ASCII splits at spaces instead, which
// gives the same words. ASCII words lose their stop words and plural endings.
//
// pig additive (D92): a Piglet Binary built with pig_strip_word_dictionaries has no ICU dictionary data, so each run of Han, Hiragana, Katakana, Thai, Lao, Khmer or Myanmar text is one word, as ICU
// segments it without the dictionaries.
//
// pig divergence (D217): Pi 79006f06 (#11037) pinned Pi lowercases the text and splits it on /[^a-z0-9]+/, which drops every letter outside ASCII, so a query in another script finds nothing.
func Tokenize(text string) []string {
	text = norm.NFC.String(text)
	text = lowerUpper.ReplaceAllString(text, "$1 $2")
	text = upperRunUpper.ReplaceAllString(text, "$1 $2")
	normalized := wordText(jsstring.ToLower(text))
	terms := []string{}
	add := func(word string) {
		switch {
		case word == "" || stopWords[word]:
		case isASCIIWord(word):
			terms = append(terms, stem(word))
		default:
			terms = append(terms, word)
		}
	}
	if isASCIIWordText(normalized) {
		for word := range strings.SplitSeq(normalized, " ") {
			add(word)
		}
		return terms
	}
	for segment := range wordsegmenter.Segments(normalized) {
		if segment.IsWordLike {
			add(segment.Segment)
		}
	}
	return terms
}

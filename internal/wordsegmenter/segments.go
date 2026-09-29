// © 2016 and later: Unicode, Inc. and others.
// Copyright (C) 1999-2016, International Business Machines Corporation and others.
// ICU DictionaryCache::populateDictionary and break-engine selection translation; see LICENSES/Unicode-3.0.txt.

package wordsegmenter

// Ports packages/tui/src/utils.ts

import (
	"iter"
	"sort"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/words"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// SegmentData mirrors the word-granularity fields of Intl.SegmentData. Index counts UTF-16 units.
type SegmentData struct {
	Segment    string
	Index      int
	Input      string
	IsWordLike bool
}

func wordLike(text string) bool {
	for _, r := range text {
		if r < 128 {
			if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				return true
			}
			continue
		}
		i := sort.Search(len(wordLikeRanges), func(i int) bool { return wordLikeRanges[i][1] >= r })
		if i < len(wordLikeRanges) && wordLikeRanges[i][0] <= r {
			return true
		}
	}
	return false
}

// Segments applies Unicode word rules, ICU rule tailoring, and ICU's Chinese/Japanese, Thai, Lao, Khmer and Burmese dictionary engines. Returned slices retain the original text, including normalization variants and UTF-16 surrogate halves.
func Segments(text string) iter.Seq[SegmentData] {
	text = jsstring.Canonical(text)
	return func(yield func(SegmentData) bool) {
		decoded := text
		if !utf8.ValidString(text) {
			decoded = string(jsstring.ToUTF8(text))
		}
		scanner := words.FromString(tailoredWordRules(decoded))
		scanned := 0
		scanValue := func() string {
			end := scanned + len(scanner.Value())
			part := decoded[scanned:end]
			scanned = end
			return part
		}
		var engines dictionaryEngines
		pending := ""
		offset, index := 0, 0
		emit := func(end int, like bool) bool {
			part := text[offset:end]
			result := SegmentData{Segment: part, Index: index, Input: text, IsWordLike: like}
			offset = end
			index += jsstring.Length(part)
			return yield(result)
		}
		for pending != "" || scanner.Next() {
			part := pending
			if part == "" {
				part = scanValue()
			}
			pending = ""
			start, end := offset, offset+len(part)
			for scanner.Next() {
				next := scanValue()
				if !joinWordRules(part, next) {
					pending = next
					break
				}
				end += len(next)
				part = decoded[start:end]
			}
			like, keepGoing := ruleWordLike(part), true
			engines.dictionaryBoundaries(part, func(boundary int) {
				if keepGoing {
					keepGoing = emit(start+boundary, like)
				}
			})
			if !keepGoing || !emit(end, like) {
				return
			}
		}
	}
}

// dictionaryEngines holds the break-engine selection state that ICU keeps on one break iterator for one segment() call. rbbi.cpp getLanguageBreakEngine searches the iterator's engine stack from the top before asking the process factory; UnhandledEngine sits at the bottom. Its handleCharacter applies the Script value of each new character to fHandled, and UnicodeSet::applyIntPropertyValue replaces the set, so it claims only the most recent script. The process factory is modeled after it has loaded every engine.
type dictionaryEngines struct {
	cjkOnStack bool
	unhandled  rune // UnhandledEngine fHandled as an unhandledScriptRanges ID; 0 before its first character
}

func unhandledScript(r rune) rune {
	i := sort.Search(len(unhandledScriptRanges), func(i int) bool { return unhandledScriptRanges[i][1] >= r })
	if i < len(unhandledScriptRanges) && unhandledScriptRanges[i][0] <= r {
		return unhandledScriptRanges[i][2]
	}
	return 0
}

func (e *dictionaryEngines) unhandledHandles(r rune) bool {
	return e.unhandled != 0 && unhandledScript(r) == e.unhandled
}

// engineFor returns 1-4 for the Southeast Asian engines, 5 for CjkBreakEngine, or 0 for UnhandledEngine. Only the CJK set shares Common characters (U+30FC and U+FF70) with a Script value UnhandledEngine can claim, so stack order matters only there.
func (e *dictionaryEngines) engineFor(r rune) rune {
	// pig divergence (D27): select CjkBreakEngine as ICU does once its process-wide engine cache is warm; a fresh Pi process sends a U+30FC/U+FF70 span start to UnhandledEngine first.
	if script := complexContext(r) & 7; script != 0 {
		return script
	}
	if dictionaryCharacter(r) {
		if e.cjkOnStack {
			return 5
		}
		if !e.unhandledHandles(r) {
			e.cjkOnStack = true
			return 5
		}
		return 0
	}
	if !e.unhandledHandles(r) {
		e.unhandled = unhandledScript(r) // UnhandledEngine::handleCharacter
	}
	return 0
}

func (e *dictionaryEngines) handles(engine, r rune) bool {
	switch engine {
	case 0:
		return e.unhandledHandles(r)
	case 5:
		return dictionaryCharacter(r)
	}
	return complexContext(r)&7 == engine
}

func ruleDictionary(r rune) bool {
	return complexContext(r) != 0 || wordRuleClass(r)&(ruleKanaKanji|ruleHangul) != 0
}

// dictionaryBoundaries ports DictionaryCache::populateDictionary over one rule span. A span starts only at a $dictionary character; DictionaryBreakEngine::findBreaks then extends it over the selected engine's set, and UnhandledEngine consumes its claimed script without breaks. Engine range ends are omitted: the containing rule span supplies only its outer endpoints.
func (e *dictionaryEngines) dictionaryBoundaries(text string, visit func(int)) {
	if len(text) <= 3 && jsstring.Length(text) <= 1 { // one UTF-16 unit takes at most three bytes
		return
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !ruleDictionary(r) {
			i += size
			continue
		}
		engine, start := e.engineFor(r), i
		for i < len(text) {
			r, size := utf8.DecodeRuneInString(text[i:])
			if !e.handles(engine, r) {
				break
			}
			i += size
		}
		if i == start {
			i += size // Every engine handles its start character; never stall the input loop on a data gap.
		}
		switch engine {
		case 0:
		case 5:
			for _, boundary := range cjkBoundaries(text[start:i]) {
				if start+boundary < i {
					visit(start + boundary)
				}
			}
		default:
			seaDivide(text[start:i], engine, func(boundary int) { visit(start + boundary) })
		}
	}
}

package jsdiff

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The helpers below are jsdiff's util/string.js for whitespace-only prefixes and suffixes, which are BMP characters, so rune
// positions equal UTF-16 positions.

func longestCommonPrefix(a, b string) string {
	ar, br := []rune(a), []rune(b)
	i := 0
	for i < len(ar) && i < len(br) && ar[i] == br[i] {
		i++
	}
	return string(ar[:i])
}

func longestCommonSuffix(a, b string) string {
	ar, br := []rune(a), []rune(b)
	i := 0
	for i < len(ar) && i < len(br) && ar[len(ar)-1-i] == br[len(br)-1-i] {
		i++
	}
	return string(ar[len(ar)-i:])
}

// replacePrefix panics when value does not start with oldPrefix, as jsdiff throws on that bug.
func replacePrefix(value, oldPrefix, newPrefix string) string {
	if !strings.HasPrefix(value, oldPrefix) {
		panic("jsdiff: string does not start with the prefix to replace")
	}
	return newPrefix + value[len(oldPrefix):]
}

func replaceSuffix(value, oldSuffix, newSuffix string) string {
	if oldSuffix == "" {
		return value + newSuffix
	}
	if !strings.HasSuffix(value, oldSuffix) {
		panic("jsdiff: string does not end with the suffix to replace")
	}
	return value[:len(value)-len(oldSuffix)] + newSuffix
}

func removePrefix(value, oldPrefix string) string { return replacePrefix(value, oldPrefix, "") }
func removeSuffix(value, oldSuffix string) string { return replaceSuffix(value, oldSuffix, "") }

// maximumOverlap is the longest prefix of b that is also a suffix of a.
func maximumOverlap(a, b string) string {
	ar, br := []rune(a), []rune(b)
	for n := min(len(ar), len(br)); n > 0; n-- {
		if string(ar[len(ar)-n:]) == string(br[:n]) {
			return string(br[:n])
		}
	}
	return ""
}

func leadingWs(value string) string {
	return value[:len(value)-len(strings.TrimLeftFunc(value, jsstring.IsSpace))]
}

func trailingWs(value string) string {
	return value[len(strings.TrimRightFunc(value, jsstring.IsSpace)):]
}

func leadingAndTrailingWs(value string) (string, string) { return leadingWs(value), trailingWs(value) }

// postProcess is WordDiff.postProcess: it removes whitespace that one change repeats from its neighbours.
func postProcess(changes []*component) {
	var lastKeep, insertion, deletion *component
	for _, change := range changes {
		switch {
		case change.added:
			insertion = change
		case change.removed:
			deletion = change
		default:
			if insertion != nil || deletion != nil {
				dedupeWhitespace(lastKeep, deletion, insertion, change)
			}
			lastKeep, insertion, deletion = change, nil, nil
		}
	}
	if insertion != nil || deletion != nil {
		dedupeWhitespace(lastKeep, deletion, insertion, nil)
	}
}

// dedupeWhitespace is dedupeWhitespaceInChangeObjects (word.js): tidy the whitespace around a change between two keeps.
func dedupeWhitespace(startKeep, deletion, insertion, endKeep *component) {
	switch {
	case deletion != nil && insertion != nil:
		oldWsPrefix, oldWsSuffix := leadingAndTrailingWs(deletion.value)
		newWsPrefix, newWsSuffix := leadingAndTrailingWs(insertion.value)
		if startKeep != nil {
			commonWsPrefix := longestCommonPrefix(oldWsPrefix, newWsPrefix)
			startKeep.value = replaceSuffix(startKeep.value, newWsPrefix, commonWsPrefix)
			deletion.value = removePrefix(deletion.value, commonWsPrefix)
			insertion.value = removePrefix(insertion.value, commonWsPrefix)
		}
		if endKeep != nil {
			commonWsSuffix := longestCommonSuffix(oldWsSuffix, newWsSuffix)
			endKeep.value = replacePrefix(endKeep.value, newWsSuffix, commonWsSuffix)
			deletion.value = removeSuffix(deletion.value, commonWsSuffix)
			insertion.value = removeSuffix(insertion.value, commonWsSuffix)
		}
	case insertion != nil:
		if startKeep != nil {
			insertion.value = insertion.value[len(leadingWs(insertion.value)):]
		}
		if endKeep != nil {
			endKeep.value = endKeep.value[len(leadingWs(endKeep.value)):]
		}
	case startKeep != nil && endKeep != nil:
		newWsFull := leadingWs(endKeep.value)
		delWsStart, delWsEnd := leadingAndTrailingWs(deletion.value)
		newWsStart := longestCommonPrefix(newWsFull, delWsStart)
		deletion.value = removePrefix(deletion.value, newWsStart)
		newWsEnd := longestCommonSuffix(removePrefix(newWsFull, newWsStart), delWsEnd)
		deletion.value = removeSuffix(deletion.value, newWsEnd)
		endKeep.value = replacePrefix(endKeep.value, newWsFull, newWsEnd)
		startKeep.value = replaceSuffix(startKeep.value, newWsFull, newWsFull[:len(newWsFull)-len(newWsEnd)])
	case endKeep != nil:
		overlap := maximumOverlap(trailingWs(deletion.value), leadingWs(endKeep.value))
		deletion.value = removeSuffix(deletion.value, overlap)
	case startKeep != nil:
		overlap := maximumOverlap(trailingWs(startKeep.value), leadingWs(deletion.value))
		deletion.value = removePrefix(deletion.value, overlap)
	}
}

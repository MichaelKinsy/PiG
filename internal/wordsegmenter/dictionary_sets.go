// © 2016 and later: Unicode, Inc. and others.
// Copyright (C) 2006-2016, International Business Machines Corporation and others.
// ICU CjkBreakEngine and DictionaryBreakEngine character sets; see LICENSES/Unicode-3.0.txt.

// Package wordsegmenter implements word segments used by the terminal editor.
package wordsegmenter

import "sort"

// pig additive (D92): the dictionary engines' character sets stay linked when a Piglet Binary compiles the dictionaries out (cjk.go and sea.go), so engine selection is unchanged.

func dictionaryCharacter(r rune) bool {
	return wordRuleClass(r)&ruleDictionaryCJK != 0
}

func complexContext(r rune) rune {
	i := sort.Search(len(complexContextRanges), func(i int) bool { return complexContextRanges[i][1] >= r })
	if i < len(complexContextRanges) && complexContextRanges[i][0] <= r {
		return complexContextRanges[i][2]
	}
	return 0
}

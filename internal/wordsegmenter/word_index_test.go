//go:build !pig_strip_word_dictionaries

package wordsegmenter

import "testing"

// Every embedded index must hold sorted, distinct words whose lengths and block offsets agree, and whose last word ends at the last byte, so a block offset or length byte that drifted from the words fails here and not as a changed segmentation.
func TestEmbeddedIndexesAreWellFormed(t *testing.T) {
	for _, c := range []struct {
		name  string
		index wordIndex
	}{
		{"cjk", wordIndex{data: cjkDictionary, weighted: true}},
		{"thai", wordIndex{data: thaiDictionary}},
		{"lao", wordIndex{data: laoDictionary}},
		{"khmer", wordIndex{data: khmerDictionary}},
		{"burmese", wordIndex{data: burmeseDictionary}},
	} {
		count := c.index.count()
		if count == 0 {
			t.Fatalf("%s: no words", c.name)
		}
		total := 0
		previous := ""
		for i := range count {
			word := c.index.word(i, count)
			if word == "" || i > 0 && word <= previous {
				t.Fatalf("%s: word %d %q does not follow %q", c.name, i, word, previous)
			}
			previous = word
			total += len(word)
		}
		words := len(c.index.data) - total
		fixed := 4 + (count+indexBlock-1)/indexBlock*4 + count
		if c.index.weighted {
			fixed += count
		}
		if words != fixed {
			t.Fatalf("%s: %d bytes outside the words, want %d", c.name, words, fixed)
		}
		if got := c.index.word(count-1, count); got != previous || c.index.data[len(c.index.data)-len(got):] != got {
			t.Fatalf("%s: last word %q does not end the index", c.name, got)
		}
	}
}

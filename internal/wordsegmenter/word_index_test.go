//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"testing"
)

// word returns word i. Production lookup never builds a word it does not compare, so only the tests and the benchmarks read one whole.
func (d *wordIndex) word(i int) string {
	start := d.before(i)
	return d.words[start : start+d.length(i)]
}

type namedIndex struct {
	name  string
	index wordIndex
	keyed bool
}

func embeddedIndexes() []namedIndex {
	return []namedIndex{
		{"cjk", newWordIndex(cjkDictionary, true, false), false},
		{"thai", newWordIndex(thaiDictionary, false, true), true},
		{"lao", newWordIndex(laoDictionary, false, true), true},
		{"khmer", newWordIndex(khmerDictionary, false, true), true},
		{"burmese", newWordIndex(burmeseDictionary, false, true), true},
	}
}

// Every embedded index must hold sorted, distinct words whose lengths and block offsets agree, and whose last word ends at the last byte, so a block offset or length byte that drifted from the words fails here and not as a changed segmentation.
func TestEmbeddedIndexesAreWellFormed(t *testing.T) {
	for _, c := range embeddedIndexes() {
		d := c.index
		if d.count == 0 {
			t.Fatalf("%s: no words", c.name)
		}
		total := 0
		previous := ""
		for i := range d.count {
			if want := total; d.before(i) != want {
				t.Fatalf("%s: word %d starts at %d, want the sum of the lengths before it, %d", c.name, i, d.before(i), want)
			}
			word := d.word(i)
			if word == "" || i > 0 && word <= previous {
				t.Fatalf("%s: word %d %q does not follow %q", c.name, i, word, previous)
			}
			previous = word
			total += len(word)
		}
		if total != len(d.words) {
			t.Fatalf("%s: the lengths sum to %d bytes, the words hold %d", c.name, total, len(d.words))
		}
		if got := d.word(d.count - 1); got != previous || d.words[len(d.words)-len(got):] != got {
			t.Fatalf("%s: last word %q does not end the index", c.name, got)
		}
		if len(d.blocks) != (d.count+indexBlock-1)/indexBlock*3 {
			t.Fatalf("%s: %d bytes of block offsets for %d words", c.name, len(d.blocks), d.count)
		}
		if (len(d.heads) != 0) != c.keyed || c.keyed && len(d.heads) != firstBytes*3 {
			t.Fatalf("%s: %d bytes of first-byte table", c.name, len(d.heads))
		}
		if weighted := c.name == "cjk"; weighted && len(d.weights) != d.count || !weighted && len(d.weights) != 0 {
			t.Fatalf("%s: %d weight bytes for %d words", c.name, len(d.weights), d.count)
		}
	}
}

// The first-byte table of a byte-keyed index must give, for every byte, exactly the words that start with it.
func TestFirstByteTableMatchesTheWords(t *testing.T) {
	for _, c := range embeddedIndexes() {
		if !c.keyed {
			continue
		}
		d := c.index
		next := 0
		for b := range 256 {
			low, high := d.byteRange(b)
			if low != next || low > high {
				t.Fatalf("%s: byte %d starts at word %d, want %d", c.name, b, low, next)
			}
			for i := low; i < high; i++ {
				if int(d.word(i)[0]) != b {
					t.Fatalf("%s: word %d %q is listed under byte %d", c.name, i, d.word(i), b)
				}
			}
			next = high
		}
		if next != d.count {
			t.Fatalf("%s: the first-byte table ends at word %d of %d", c.name, next, d.count)
		}
	}
}

// reference is the search that first replaces: a binary search of [lo, hi) that reads one whole word per probe.
func reference(d *wordIndex, lo, hi int, holds func(string) bool) int {
	return lo + sort.Search(hi-lo, func(i int) bool { return holds(d.word(lo + i)) })
}

func byteAt(word string, depth int) int {
	if len(word) <= depth {
		return -1
	}
	return int(word[depth])
}

// first must give the least index of any range whose bound is monotone there, from any start and with either block search, across block edges, one-word ranges and the ends of the dictionary.
func TestFirstMatchesAPlainSearch(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	rounds := 20000
	if testing.Short() {
		rounds = 2000
	}
	for _, c := range embeddedIndexes() {
		d := c.index
		check := func(lo, hi int, b bound, holds func(string) bool) {
			t.Helper()
			want := reference(&d, lo, hi, holds)
			for _, near := range []bool{false, true} {
				if got := d.first(lo, hi, &b, near); got != want {
					t.Fatalf("%s: first(%d, %d, %+v, near=%v) = %d, want %d", c.name, lo, hi, b, near, got, want)
				}
			}
		}
		for range rounds {
			word := d.word(rng.IntN(d.count))
			// A range is a random sub-range, or the words that share a prefix of word, or the words from a start to the end.
			lo, hi := 0, d.count
			switch rng.IntN(3) {
			case 0:
				lo = rng.IntN(d.count)
				hi = lo + rng.IntN(min(d.count-lo, 40)+1)
			case 1:
				lo = rng.IntN(d.count)
			}
			// atLeast holds in any range of a sorted index.
			key := word[:rng.IntN(len(word))+1]
			if rng.IntN(3) == 0 {
				key += string(rune(rng.IntN(256)))
			}
			check(lo, hi, bound{kind: atLeast, key: key}, func(w string) bool { return w >= key })
			// unprefixed holds in a range of words that are at least the key, which is where dictionaryMatches calls it.
			from := reference(&d, 0, d.count, func(w string) bool { return w >= key })
			upto := min(d.count, from+rng.IntN(60))
			check(from, upto, bound{kind: unprefixed, key: key}, func(w string) bool { return !strings.HasPrefix(w, key) })
			check(from, d.count, bound{kind: unprefixed, key: key}, func(w string) bool { return !strings.HasPrefix(w, key) })
			// The byte bounds hold in a range of words that share the bytes before depth, which is where the Southeast Asian walk calls them.
			depth := rng.IntN(len(word))
			prefix := word[:depth]
			start := reference(&d, 0, d.count, func(w string) bool { return w >= prefix })
			end := reference(&d, start, d.count, func(w string) bool { return !strings.HasPrefix(w, prefix) })
			b := rng.IntN(256)
			if rng.IntN(2) == 0 {
				b = byteAt(word, depth)
			}
			check(start, end, bound{kind: byteAtLeast, depth: depth, c: b}, func(w string) bool { return byteAt(w, depth) >= b })
			check(start, end, bound{kind: byteAbove, depth: depth, c: b}, func(w string) bool { return byteAt(w, depth) > b })
		}
	}
}

// dictionaryMatches must visit every dictionary word that is a prefix of the text, with the word's weight, as a map of all the words would.
func TestCJKMatchesAreTheDictionaryWordsThatArePrefixes(t *testing.T) {
	index := newWordIndex(cjkDictionary, true, false)
	words := make(map[string]uint32, index.count)
	list := make([]string, index.count)
	for i := range index.count {
		list[i] = index.word(i)
		words[list[i]] = index.weight(i)
	}
	type match struct {
		length int
		cost   uint32
	}
	rng := rand.New(rand.NewPCG(3, 4))
	step := 1
	if testing.Short() {
		step = 50
	}
	check := func(text string) {
		t.Helper()
		var offsets []int
		for i := range text {
			offsets = append(offsets, i)
		}
		offsets = append(offsets, len(text))
		for start := 0; start+1 < len(offsets); start++ {
			var got, want []match
			dictionaryMatches(&index, text, offsets, start, func(length int, cost uint32) { got = append(got, match{length, cost}) })
			for length := 1; length <= 20 && start+length < len(offsets); length++ {
				if cost, ok := words[text[offsets[start]:offsets[start+length]]]; ok {
					want = append(want, match{length, cost})
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("%q from character %d: got %v, want %v", text, start, got, want)
			}
		}
	}
	for i := 0; i < len(list); i += step {
		// Each word alone, then followed by another word and by a character that continues no word.
		check(list[i])
		check(list[i] + list[rng.IntN(len(list))] + "x")
	}
}

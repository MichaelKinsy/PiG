//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"compress/gzip"
	"encoding/binary"
	"io"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// flatIndex reads the flat sorted-array index the package embedded before the trie (dictionary_format.py: encode, decode). It exists only here, as the oracle the trie is compared with: testdata/<name>_flat.bin.gz is the generators' own raw output.
type flatIndex struct {
	data     string
	weighted bool
}

const flatBlock = 16

func (d flatIndex) count() int { return int(binary.LittleEndian.Uint32([]byte(d.data[:4]))) }

func (d flatIndex) word(index, count int) string {
	lengths := 4 + (count+flatBlock-1)/flatBlock*4
	block := index / flatBlock
	start := int(binary.LittleEndian.Uint32([]byte(d.data[4+block*4 : 8+block*4])))
	for i := block * flatBlock; i < index; i++ {
		start += int(d.data[lengths+i])
	}
	words := lengths + count
	if d.weighted {
		words += count
	}
	return d.data[words+start : words+start+int(d.data[lengths+index])]
}

func (d flatIndex) weight(index, count int) uint32 {
	return uint32(d.data[4+(count+flatBlock-1)/flatBlock*4+count+index])
}

// prefix narrows the sorted words to those with prefix, as the previous lookup did.
func (d flatIndex) prefix(prefix string, count int) (low, high int) {
	low = sort.Search(count, func(i int) bool { return d.word(i, count) >= prefix })
	high = low + sort.Search(count-low, func(i int) bool { return !strings.HasPrefix(d.word(low+i, count), prefix) })
	return low, high
}

type dictionaryCase struct {
	name  string
	trie  wordTrie
	runes bool // labels are UTF-16 units of UTF-8 words; otherwise one byte per label
}

func dictionaryCases() []dictionaryCase {
	return []dictionaryCase{
		{"cjk", cjkTrie(), true},
		{"thai", wordTrie{data: thaiDictionary, width: 1}, false},
		{"lao", wordTrie{data: laoDictionary, width: 1}, false},
		{"khmer", wordTrie{data: khmerDictionary, width: 1}, false},
		{"burmese", wordTrie{data: burmeseDictionary, width: 1}, false},
	}
}

func loadFlat(t *testing.T, c dictionaryCase) flatIndex {
	t.Helper()
	file, err := os.Open("testdata/" + c.name + "_flat.bin.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return flatIndex{data: string(data), weighted: c.trie.weighted}
}

func labelsOf(word string, runes bool) []int {
	var labels []int
	if runes {
		for _, r := range word {
			labels = append(labels, int(r))
		}
		return labels
	}
	for i := range len(word) {
		labels = append(labels, int(word[i]))
	}
	return labels
}

func wordOf(labels []int, runes bool) string {
	if !runes {
		b := make([]byte, len(labels))
		for i, l := range labels {
			b[i] = byte(l)
		}
		return string(b)
	}
	var b strings.Builder
	for _, l := range labels {
		b.WriteRune(rune(l))
	}
	return b.String()
}

// enumerate visits every word of the trie in label order, walking its records directly.
func (t wordTrie) enumerate(at int, prefix []int, visit func([]int, uint32)) {
	n := t.node(at)
	if n.terminal {
		visit(prefix, n.weight)
	}
	for i := range n.skipLen {
		prefix = append(prefix, t.label(n.skipAt+i*t.width))
	}
	const none = 1 << 30
	stride := t.width + triePointer
	i, j := 0, 0
	for i < n.leaves || j < n.inner {
		leaf, inner := none, none
		if i < n.leaves {
			leaf = t.label(n.leafAt + i*t.leafStride())
		}
		if j < n.inner {
			inner = t.label(n.innerAt + j*stride)
		}
		if leaf < inner {
			var weight uint32
			if t.weighted {
				weight = uint32(t.data[n.leafAt+i*t.leafStride()+t.width])
			}
			visit(append(prefix, leaf), weight)
			i++
			continue
		}
		at := n.innerAt + j*stride + t.width
		t.enumerate(int(t.data[at])|int(t.data[at+1])<<8|int(t.data[at+2])<<16, append(prefix, inner), visit)
		j++
	}
}

// Every embedded trie must hold exactly the words and weights of the flat index the generators also write: all words in order through the records, every word through step, every prefix of every 97th word and random prefixes through step against the previous narrowing lookup.
func TestEmbeddedTriesMatchTheFlatIndexes(t *testing.T) {
	for _, c := range dictionaryCases() {
		t.Run(c.name, func(t *testing.T) {
			flat := loadFlat(t, c)
			count := flat.count()
			if count == 0 {
				t.Fatal("no words")
			}
			next := 0
			c.trie.enumerate(0, nil, func(labels []int, weight uint32) {
				if next >= count {
					t.Fatalf("trie holds more than %d words", count)
				}
				if word := wordOf(labels, c.runes); word != flat.word(next, count) || c.trie.weighted && weight != flat.weight(next, count) {
					t.Fatalf("word %d is %q weight %d, want %q weight %d", next, word, weight, flat.word(next, count), flat.weight(next, count))
				}
				next++
			})
			if next != count {
				t.Fatalf("trie holds %d words, want %d", next, count)
			}
			// compare walks one label prefix of the walk against the previous lookup; it reports whether the walk may continue.
			compare := func(word string, labels []int, cursor *trieCursor) bool {
				step, ok := c.trie.step(cursor, labels[len(labels)-1])
				prefix := wordOf(labels, c.runes)
				low, high := flat.prefix(prefix, count)
				if ok != (low != high) {
					t.Fatalf("prefix %q of %q: step ok=%v, flat range [%d,%d)", prefix, word, ok, low, high)
				}
				if !ok {
					return false
				}
				exact := flat.word(low, count) == prefix
				extendable := high-low > 1 || !exact
				if step.terminal != exact || step.more != extendable || exact && c.trie.weighted && step.weight != flat.weight(low, count) {
					t.Fatalf("prefix %q of %q: step %+v, flat exact=%v extendable=%v", prefix, word, step, exact, extendable)
				}
				return step.more
			}
			for i := range count {
				word := flat.word(i, count)
				labels := labelsOf(word, c.runes)
				cursor := c.trie.root()
				var step trieStep
				for _, l := range labels {
					var ok bool
					if step, ok = c.trie.step(&cursor, l); !ok {
						t.Fatalf("word %d %q is not found", i, word)
					}
				}
				if !step.terminal || c.trie.weighted && step.weight != flat.weight(i, count) {
					t.Fatalf("word %d %q: step %+v, want weight %d", i, word, step, flat.weight(i, count))
				}
				if i%97 == 0 {
					cursor := c.trie.root()
					for n := 1; n <= len(labels); n++ {
						if !compare(word, labels[:n], &cursor) && n < len(labels) {
							t.Fatalf("word %q stops at %d of %d labels", word, n, len(labels))
						}
					}
				}
			}
			random := rand.New(rand.NewSource(1))
			for range 20000 {
				word := flat.word(random.Intn(count), count)
				labels := labelsOf(word, c.runes)
				labels = labels[:1+random.Intn(len(labels))]
				// Diverge: replace the last label, extend by a random label or leave the prefix alone.
				switch random.Intn(3) {
				case 0:
					labels[len(labels)-1] = randomLabel(random, flat, count, c.runes)
				case 1:
					labels = append(labels, randomLabel(random, flat, count, c.runes))
				}
				cursor := c.trie.root()
				for n := 1; n <= len(labels); n++ {
					if !compare(word, labels[:n], &cursor) {
						break
					}
				}
			}
		})
	}
}

// randomLabel draws a label of the dictionary's alphabet: a label of a random word, so neighbouring and rare labels both occur.
func randomLabel(random *rand.Rand, flat flatIndex, count int, runes bool) int {
	labels := labelsOf(flat.word(random.Intn(count), count), runes)
	return labels[random.Intn(len(labels))]
}

// A dictionary word is BMP text without U+FFFD, so the UTF-16 unit labels of the CJK trie cannot confuse invalid UTF-8 or a supplementary character with a word.
func TestCJKWordsHaveUTF16UnitLabels(t *testing.T) {
	c := dictionaryCases()[0]
	flat := loadFlat(t, c)
	count := flat.count()
	for i := range count {
		word := flat.word(i, count)
		if !utf8.ValidString(word) || strings.ContainsRune(word, utf8.RuneError) {
			t.Fatalf("word %d %q is not valid text without U+FFFD", i, word)
		}
		for _, r := range word {
			if r > 0xffff {
				t.Fatalf("word %d %q holds the supplementary character %U", i, word, r)
			}
		}
	}
}

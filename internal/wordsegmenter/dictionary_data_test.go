//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
)

// The inflated dictionaries are byte for byte the indexes the generators wrote before they were embedded compressed: these are the SHA-256 digests of the former cjk_dictionary.bin, thai_dictionary.bin, lao_dictionary.bin, khmer_dictionary.bin and burmese_dictionary.bin.
func TestInflatedDictionariesAreTheGeneratedIndexes(t *testing.T) {
	for _, c := range []struct {
		name   string
		get    func() string
		sha256 string
		size   int
	}{
		{"cjk", cjkDictionary, "5111be4597ef8370329df83b3a3f1f59628ee7c4eb3b30208df02f56062f0010", 4212243},
		{"thai", thaiDictionary, "946cda1c14e3f25dbcf7997fedfe30e1a347529a813b935380b100e53f9bed6d", 260897},
		{"lao", laoDictionary, "f07d3fa58ae8d9ace3632b2125a30466d636bb23e4cbd834ccb1e9c6132976b9", 335860},
		{"khmer", khmerDictionary, "8ad75ab7e55ea6cbbcce60cb231ba085e5314011319742a67de12efe3e40e1d3", 974071},
		{"burmese", burmeseDictionary, "fa5728b2a3b7cfb4b72307bc1bc45e9876aafe341abb978cf7ea5476d6236901", 537893},
	} {
		data := c.get()
		sum := sha256.Sum256([]byte(data))
		if len(data) != c.size || hex.EncodeToString(sum[:]) != c.sha256 {
			t.Errorf("%s dictionary is %d bytes, sha256 %x; want %d bytes, %s", c.name, len(data), sum, c.size, c.sha256)
		}
	}
}

// Every caller of a dictionary gets the one inflated string, however many ask at once.
func TestLazyDictionaryInflatesOnce(t *testing.T) {
	get := lazyDictionary(thaiDictionaryGzip)
	results := make([]string, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = get() })
	}
	wg.Wait()
	for _, got := range results {
		if len(got) != 260897 {
			t.Fatalf("a caller got %d bytes", len(got))
		}
	}
}

// The first segmentation of a script pays for inflating its dictionary; the benchmark reports that once-per-process cost per script and then the cost of a warm segmentation.
func BenchmarkFirstUseInflate(b *testing.B) {
	for _, c := range []struct {
		name string
		gz   []byte
	}{{"cjk", cjkDictionaryGzip}, {"thai", thaiDictionaryGzip}, {"lao", laoDictionaryGzip}, {"khmer", khmerDictionaryGzip}, {"burmese", burmeseDictionaryGzip}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = lazyDictionary(c.gz)()
			}
		})
	}
}

func BenchmarkSegmentWarm(b *testing.B) {
	for _, text := range []string{"日本語文章を分かち書きする", "สวัสดีครับขอบคุณมาก"} {
		b.Run(text, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				for range Segments(text) {
				}
			}
		})
	}
}

package ai

import (
	"bytes"
	"strings"
	"testing"
)

// A long session re-sends the same message strings on every request. The cache must find each one by the string itself, not
// by hashing and comparing its text again: here the entry is moved to a key no hash of the text gives, so only a lookup by
// identity finds it.
func TestTheQuotedStringCacheFindsARepeatedStringWithoutHashingIt(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	text := strings.Repeat("line with \"quotes\" and <tags>\n", 1000)
	first := cache.quote(text)
	for key, element := range cache.entries {
		delete(cache.entries, key)
		moved := quotedStringKey{key.size, key.hash + 1}
		element.Value.(*quotedString).key = moved
		cache.entries[moved] = element
	}
	if again := cache.quote(text); !bytes.Equal(again, first) || cache.misses != 1 {
		t.Fatalf("the same string was looked up by its hash: misses=%d", cache.misses)
	}
}

// quoteValid finds a repeated string the same way, and an equal copy is found by its hash once and then by its own identity.
func TestTheQuotedStringCacheIndexesTheLatestCopyOfAString(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	text := strings.Repeat("line with \"quotes\" and <tags>\n", 1000)
	first := cache.quote(text)
	if quoted, ok := cache.quoteValid(text); !ok || &quoted[0] != &first[0] || cache.misses != 1 {
		t.Fatalf("quoteValid encoded the same string again: misses=%d", cache.misses)
	}
	copied := strings.Clone(text)
	cache2 := newQuotedStringCache(1 << 30)
	cache2.quote(text)
	cache2.quote(copied)
	if element, ok := cache2.bySource[identityOf(copied)]; !ok || identityOf(element.Value.(*quotedString).source) != identityOf(copied) {
		t.Fatal("a copy found by its hash did not become the entry's source")
	}
	if _, ok := cache2.bySource[identityOf(text)]; ok {
		t.Fatal("the replaced source is still indexed")
	}
}

// quoteValid caches only UTF-8, as the splice walk did when it checked each string itself.
func TestQuoteValidRefusesAStringThatIsNotUTF8(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	invalid := strings.Repeat("a", requestSpliceMinBytes) + "\xff"
	if _, ok := cache.quoteValid(invalid); ok || cache.misses != 0 || len(cache.entries) != 0 {
		t.Fatalf("a string that is not UTF-8 was cached (%d misses)", cache.misses)
	}
}

// Eviction removes the identity index with the entry, so a freed string's address never finds an encoding.
func TestTheQuotedStringCacheUnindexesAnEvictedSource(t *testing.T) {
	cache := newQuotedStringCache(5000)
	a, b, c := strings.Repeat("a", 1000), strings.Repeat("b", 1000), strings.Repeat("c", 1000)
	cache.quote(a)
	cache.quote(b)
	cache.quote(c) // evicts a
	if _, ok := cache.bySource[identityOf(a)]; ok || len(cache.bySource) != len(cache.entries) {
		t.Fatalf("bySource holds %d entries for %d cached strings", len(cache.bySource), len(cache.entries))
	}
}

// SPDX-License-Identifier: MIT

package lazyregexp

import (
	"regexp"
	"testing"
)

func TestCacheCompilesAKeyOnce(t *testing.T) {
	cache := NewCache[string](4)
	compiled := 0
	compile := func(key string) *regexp.Regexp { compiled++; return regexp.MustCompile(key) }
	first := cache.Get("a+b", compile)
	second := cache.Get("a+b", compile)
	if first != second || compiled != 1 {
		t.Fatalf("compiled %d times, same expression %v, want once and the same", compiled, first == second)
	}
	if other := cache.Get("c", compile); other == first || compiled != 2 {
		t.Fatalf("a second key compiled %d times in all, want 2 and its own expression", compiled)
	}
}

func TestCacheHoldsNoMoreThanItsLimit(t *testing.T) {
	cache := NewCache[int](3)
	compiled := 0
	compile := func(int) *regexp.Regexp { compiled++; return regexp.MustCompile("x") }
	for i := range 10 {
		cache.Get(i, compile)
		if len(cache.entries) > 3 {
			t.Fatalf("after key %d the cache holds %d expressions, want at most 3", i, len(cache.entries))
		}
	}
	cache.Get(9, compile)
	if compiled != 10 {
		t.Fatalf("compiled %d times for 10 distinct keys and a repeat of the newest, want 10", compiled)
	}
}

func TestCacheCachesNothingForAPanickingCompile(t *testing.T) {
	cache := NewCache[string](2)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("an invalid pattern did not panic")
			}
		}()
		cache.Get("(", regexp.MustCompile)
	}()
	if len(cache.entries) != 0 {
		t.Fatalf("the cache holds %d expressions after a panic, want none", len(cache.entries))
	}
}

// SPDX-License-Identifier: MIT

package lazyregexp

import (
	"regexp"
	"sync"
)

// Cache holds the compiled expressions of patterns that are built at run time (a search query, a paste marker, a tool pattern of a configuration)
// by the key they are built from, so a pattern that comes back is compiled once. It holds at most the number of entries it was made with: one more
// than that empties it, so a stream of different keys cannot grow it. It is safe for concurrent use. A cache made at process start holds no storage
// for its entries until the first expression is stored, and then grows with the keys it holds.
type Cache[K comparable] struct {
	mu      sync.Mutex
	entries map[K]*regexp.Regexp
	limit   int
}

// NewCache returns a Cache that holds at most limit expressions.
func NewCache[K comparable](limit int) *Cache[K] {
	return &Cache[K]{limit: limit}
}

// Get returns the expression compile makes for key, compiling it when the cache does not hold it. compile may be called more than once for a key
// when callers race, and a panic of compile (regexp.MustCompile on an invalid pattern) reaches the caller and caches nothing.
func (c *Cache[K]) Get(key K, compile func(K) *regexp.Regexp) *regexp.Regexp {
	c.mu.Lock()
	re, ok := c.entries[key]
	c.mu.Unlock()
	if ok {
		return re
	}
	re = compile(key)
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[K]*regexp.Regexp)
	} else if len(c.entries) >= c.limit {
		clear(c.entries)
	}
	c.entries[key] = re
	c.mu.Unlock()
	return re
}

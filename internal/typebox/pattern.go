// SPDX-License-Identifier: MIT

package typebox

import (
	"regexp"
	"sync"
)

// patternCacheLimit bounds the compiled-pattern cache; a full cache starts over.
const patternCacheLimit = 512

// patterns caches CompilePattern's results by pattern text. CompilePattern makes the map on first use.
var patterns struct {
	sync.Mutex
	byText map[string]compiledPattern
}

type compiledPattern struct {
	expression *regexp.Regexp
	err        error
}

// CompilePattern is regexp.Compile for a schema's pattern text (patternProperties keys and the like), cached so a
// schema checked again does not compile its patterns again. A *regexp.Regexp is safe for concurrent use.
func CompilePattern(pattern string) (*regexp.Regexp, error) {
	patterns.Lock()
	defer patterns.Unlock()
	if entry, ok := patterns.byText[pattern]; ok {
		return entry.expression, entry.err
	}
	var entry compiledPattern
	entry.expression, entry.err = regexp.Compile(pattern) //perf:ok the cache fill: once per distinct pattern text
	if patterns.byText == nil {
		patterns.byText = map[string]compiledPattern{}
	} else if len(patterns.byText) >= patternCacheLimit {
		clear(patterns.byText)
	}
	patterns.byText[pattern] = entry
	return entry.expression, entry.err
}

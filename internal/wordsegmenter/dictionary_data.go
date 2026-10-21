//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"strings"
	"sync"
)

// lazyDictionary returns the dictionary a gzip-compressed index inflates to. The first call inflates it, once, on the caller's goroutine; every later call, from any goroutine, returns the same string. The five indexes are 6.3 MB inflated and 2.65 MB compressed, and a session that never segments CJK or Southeast Asian text never inflates one. The embedded bytes are generated, so a malformed index is a build defect and panics.
func lazyDictionary(compressed []byte) func() string {
	return sync.OnceValue(func() string {
		if len(compressed) < 4 {
			panic("wordsegmenter: embedded dictionary is truncated")
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			panic("wordsegmenter: embedded dictionary: " + err.Error())
		}
		var inflated strings.Builder
		inflated.Grow(int(binary.LittleEndian.Uint32(compressed[len(compressed)-4:])))
		if _, err := io.Copy(&inflated, reader); err != nil {
			panic("wordsegmenter: embedded dictionary: " + err.Error())
		}
		return inflated.String()
	})
}

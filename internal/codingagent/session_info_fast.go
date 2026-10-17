package codingagent

import (
	"bytes"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsonvalid"
)

// The listing needs only a count for a tool-result message line, so such a line is recognised without decoding it. The line must still be valid JSON (a malformed line is skipped exactly as the full decode skips it); anything unusual falls back to the full decode.

// isToolResultMessageLine reports whether line is a valid JSON object whose top-level "type" is "message" and whose top-level "message" object has "role" "toolResult". Duplicate keys, escaped keys or any unexpected shape report false.
func isToolResultMessageLine(line []byte) bool {
	line = bytes.TrimFunc(line, isJSWhitespace)
	if len(line) < 2 || line[0] != '{' || !bytes.Contains(line, []byte(`"toolResult"`)) {
		return false
	}
	if !jsonvalid.Valid(line) {
		return false
	}
	var typ, msg []byte
	if !walkTopLevel(line, func(key, val []byte) bool {
		switch string(key) {
		case "type":
			if typ != nil {
				return false
			}
			typ = val
		case "message":
			if msg != nil {
				return false
			}
			msg = val
		}
		return true
	}) || string(typ) != `"message"` || len(msg) == 0 || msg[0] != '{' {
		return false
	}
	var role []byte
	if !walkTopLevel(msg, func(key, val []byte) bool {
		if string(key) == "role" {
			if role != nil {
				return false
			}
			role = val
		}
		return true
	}) {
		return false
	}
	return string(role) == `"toolResult"`
}

// walkTopLevel visits the members of valid JSON object obj with their raw key content and raw value. It reports false when a key holds an escape or visit returns false.
func walkTopLevel(obj []byte, visit func(key, val []byte) bool) bool {
	i := 1
	for {
		i = skipSpace(obj, i)
		if i >= len(obj) {
			return false
		}
		if obj[i] == '}' {
			return true
		}
		if obj[i] == ',' {
			i = skipSpace(obj, i+1)
		}
		if i >= len(obj) || obj[i] != '"' {
			return false
		}
		end := skipString(obj, i)
		key := obj[i+1 : end-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			return false
		}
		i = skipSpace(obj, end)
		if i >= len(obj) || obj[i] != ':' {
			return false
		}
		i = skipSpace(obj, i+1)
		end = skipValue(obj, i)
		if end <= i || !visit(key, obj[i:end]) {
			return false
		}
		i = end
	}
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index after the string that opens at b[i].
func skipString(b []byte, i int) int {
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(b)
}

// skipValue returns the index after the value that starts at b[i]; b is valid JSON.
func skipValue(b []byte, i int) int {
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				j = skipString(b, j) - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1
				}
			}
		}
		return len(b)
	}
	for i < len(b) && b[i] != ',' && b[i] != '}' && b[i] != ']' && b[i] != ' ' && b[i] != '\n' && b[i] != '\t' && b[i] != '\r' {
		i++
	}
	return i
}

// summaryCacheEntry is the in-process fold of the file up to off (just after a newline), valid while the file only grew: the file identity (size, mtime) must match for a reuse, and a grown file must still carry the same bytes around off.
type summaryCacheEntry struct {
	size, mtime, off int64
	sample           uint64
	p                summaryPartial
}

const summaryCacheMax = 512

var summaryCache = struct {
	sync.Mutex
	m map[string]*summaryCacheEntry
}{m: map[string]*summaryCacheEntry{}}

// summarySample hashes the first and last 4 KiB before off, so a rewritten file that happens to be larger is not mistaken for an append.
func summarySample(f *os.File, off int64) (uint64, bool) {
	h := fnv.New64a()
	var buf [4096]byte
	for _, at := range [2]int64{0, max(0, off-int64(len(buf)))} {
		n, err := f.ReadAt(buf[:min(int64(len(buf)), off-at)], at)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, false
		}
		_, _ = h.Write(buf[:n])
	}
	return h.Sum64(), true
}

// summaryCacheCatchUp returns a private copy of the cached fold and the offset to resume scanning from, or nil when nothing valid is cached.
func summaryCacheCatchUp(f *os.File, path string, st os.FileInfo) (*summaryPartial, int64) {
	summaryCache.Lock()
	e := summaryCache.m[path]
	summaryCache.Unlock()
	if e == nil || st.Size() < e.size {
		return nil, 0
	}
	if st.Size() == e.size && st.ModTime().UnixNano() != e.mtime {
		return nil, 0
	}
	if sum, ok := summarySample(f, e.off); !ok || sum != e.sample {
		return nil, 0
	}
	p := e.p.fork()
	return p, e.off
}

func summaryCachePut(f *os.File, path string, st os.FileInfo, p *summaryPartial, off int64) {
	sum, ok := summarySample(f, off)
	if !ok {
		return
	}
	// One joined text keeps a single copy shared with the result; joining it with later texts equals the serial join.
	if len(p.texts) > 1 {
		p.texts = []string{strings.Join(p.texts, " ")}
	}
	e := &summaryCacheEntry{size: st.Size(), mtime: st.ModTime().UnixNano(), off: off, sample: sum, p: *p.fork()}
	summaryCache.Lock()
	defer summaryCache.Unlock()
	if _, ok := summaryCache.m[path]; !ok && len(summaryCache.m) >= summaryCacheMax {
		clear(summaryCache.m)
	}
	summaryCache.m[path] = e
}

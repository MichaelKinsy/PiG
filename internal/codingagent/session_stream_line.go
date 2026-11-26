// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/maphash"
	"io"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// streamLineLimit is the byte threshold above which streamSessionFile avoids
// buffering a single JSONL line whole in memory when it is a valid custom
// object line, keeping peak RSS tiny even for 256 MiB single-line payloads.
// pig additive (D112): Pi holds every entry in memory; streaming validation
// and metadata extraction yields the exact same index slot, hash, and offset.
const streamLineLimit = 4 << 20

// errStreamFallback reports that a streamed oversized line had an unusual JSON
// shape (such as duplicate/escaped top-level keys or a non-custom type) and
// must be indexed via the full-line path from its snapshot bytes.
var errStreamFallback = errors.New("stream fallback")

// forEachStreamLine reads JSONL lines from r, calling onLine for normal lines
// and onStream for oversized "custom" lines (> streamLineLimit) validated and
// hashed in constant memory without retaining the full line on the Go heap.
// If an oversized streamed line turns out to have an unusual shape, onFallback
// is called with the line's byte offset and length so the caller can read it
// from its snapshot (or fail over cleanly).
func forEachStreamLine(
	r io.Reader,
	onLine func(line []byte, off int64) error,
	onStream func(prepared *preparedLine, off int64, lineLen int) error,
	onFallback func(off int64, lineLen int) error,
) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var long []byte
	var off int64
	for {
		chunk, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, chunk...)
			if len(long) >= streamLineLimit && canStreamCustomLine(long) {
				start := off
				prepared, lineLen, consumed, eof, streamErr := streamOversizedCustomLine(long, reader)
				off += consumed
				long = nil
				if streamErr != nil {
					if errors.Is(streamErr, errStreamFallback) {
						if fnErr := onFallback(start, lineLen); fnErr != nil {
							return fnErr
						}
						if eof {
							return nil
						}
						continue
					}
					return fmt.Errorf("session: read: %w", streamErr)
				}
				if prepared != nil {
					if fnErr := onStream(prepared, start, lineLen); fnErr != nil {
						return fnErr
					}
				}
				if eof {
					return nil
				}
				continue
			}
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("session: read: %w", err)
		}
		line := chunk
		if len(long) > 0 {
			long = append(long, chunk...)
			line = long
		}
		start := off
		off += int64(len(line))
		if len(line) > 0 {
			line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
			if fnErr := onLine(line, start); fnErr != nil {
				return fnErr
			}
		}
		if cap(long) > maxRetainedLineBuffer {
			long = nil
		} else {
			long = long[:0]
		}
		if err != nil {
			return nil
		}
	}
}

// canStreamCustomLine reports whether prefix begins a JSON object whose
// leading bytes look like a "custom" entry.
func canStreamCustomLine(prefix []byte) bool {
	i := skipJSONSpace(prefix, 0)
	if i >= len(prefix) || prefix[i] != '{' {
		return false
	}
	head := prefix[:min(len(prefix), 4096)]
	return bytes.Contains(head, []byte(`"type":"custom"`)) ||
		bytes.Contains(head, []byte(`"type": "custom"`))
}

// streamOversizedCustomLine validates, hashes, and extracts base members of a
// single oversized JSONL line whose initial bytes are in prefix and whose
// remainder is in reader (up to and including '\n' or io.EOF).
func streamOversizedCustomLine(prefix []byte, reader *bufio.Reader) (prepared *preparedLine, lineLen int, consumed int64, eof bool, err error) {
	var st streamCustomParser
	st.init()
	var pendingCR bool
	feed := func(p []byte) {
		if len(p) == 0 {
			return
		}
		if pendingCR {
			st.write([]byte{'\r'})
			pendingCR = false
		}
		if p[len(p)-1] == '\r' {
			pendingCR = true
			p = p[:len(p)-1]
		}
		if len(p) > 0 {
			st.write(p)
		}
	}
	feed(prefix)
	consumed = int64(len(prefix))
	for {
		chunk, readErr := reader.ReadSlice('\n')
		consumed += int64(len(chunk))
		if errors.Is(readErr, bufio.ErrBufferFull) {
			feed(chunk)
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, 0, consumed, false, readErr
		}
		eof = errors.Is(readErr, io.EOF)
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			chunk = chunk[:len(chunk)-1]
		}
		if len(chunk) > 0 {
			if pendingCR {
				st.write([]byte{'\r'})
				pendingCR = false
			}
			if chunk[len(chunk)-1] == '\r' {
				chunk = chunk[:len(chunk)-1]
			}
			if len(chunk) > 0 {
				st.write(chunk)
			}
		} else {
			// Line ended with "\r\n" or "\r" at EOF: drop pendingCR to match TrimSuffix("\n") then TrimSuffix("\r").
			pendingCR = false
		}
		break
	}
	if !st.finish() {
		// Invalid JSON line: skipped just like jsonvalid.Valid == false.
		return nil, st.length, consumed, eof, nil
	}
	if !st.usable() {
		return nil, st.length, consumed, eof, errStreamFallback
	}
	base, ok := st.base()
	if !ok || base.Type != typeCustom {
		return nil, st.length, consumed, eof, errStreamFallback
	}
	p := preparedLine{
		state: lineMessage,
		base:  base,
		slot:  baseSlot(base),
		hash:  uint64(uint32(st.hash.Sum64())),
	}
	return &p, st.length, consumed, eof, nil
}

// streamCustomParser is a constant-memory streaming RFC 8259 JSON validator
// (matching internal/jsonvalid's exact grammar and nesting limit of 10,000),
// incremental maphash computer, and top-level base-member extractor for
// oversized "custom" JSONL lines.
type streamCustomParser struct {
	hash   maphash.Hash
	length int

	state     uint8
	failed    bool
	stack     jsonStack
	lit       string
	litPos    int
	uHexLeft  int
	topKeyBuf []byte
	topValBuf []byte
	capturing bool
	curKey    string

	seenMask   uint8
	dupOrWeird bool
	rawType    []byte
	rawID      []byte
	rawParent  []byte
	rawTime    []byte
}

const (
	spExpectVal uint8 = iota
	spAfterVal
	spObjStart
	spArrStart
	spExpectKey
	spKeyStr
	spKeyEsc
	spKeyUHex
	spExpectColon
	spInStr
	spInStrEsc
	spInStrUHex
	spNumNeg
	spNumZero
	spNumInt
	spNumDot
	spNumFrac
	spNumExpSign
	spNumExpDig
	spNumExp
	spLit
	spDone
)

// maxStreamMemberBytes caps the raw JSON slice captured per base metadata field during constant-memory streaming; any longer base value falls back to full-line indexing.
// pig additive (D112): Pi holds every entry in memory; fallback produces identical entries for unusual base values.
const maxStreamMemberBytes = 4096

func (p *streamCustomParser) init() {
	p.hash.SetSeed(recordSeed)
	p.state = spExpectVal
}

func (p *streamCustomParser) write(data []byte) {
	p.length += len(data)
	_, _ = p.hash.Write(data)
	if p.failed {
		return
	}
	i := 0
	n := len(data)
	for i < n {
		// Fast path inside strings (where 99.99% of a large custom payload lives).
		if p.state == spInStr {
			start := i
			for i < n {
				c := data[i]
				if c == '"' || c == '\\' || c < 0x20 {
					break
				}
				i++
			}
			if p.capturing {
				p.appendVal(data[start:i])
			}
			if i >= n {
				return
			}
			c := data[i]
			i++
			if p.capturing {
				p.appendValByte(c)
			}
			switch c {
			case '"':
				p.endValue()
			case '\\':
				p.state = spInStrEsc
			default:
				p.failed = true
				return
			}
			continue
		}

		c := data[i]
		i++
		switch p.state {
		case spExpectVal:
			if isJSONWS(c) {
				continue
			}
			p.startValue(c)
		case spObjStart:
			if isJSONWS(c) {
				continue
			}
			if c == '}' {
				p.stack.pop()
				p.endValue()
				continue
			}
			if c != '"' {
				p.failed = true
				return
			}
			p.beginKey()
		case spArrStart:
			if isJSONWS(c) {
				continue
			}
			if c == ']' {
				p.stack.pop()
				p.endValue()
				continue
			}
			p.startValue(c)
		case spExpectKey:
			if isJSONWS(c) {
				continue
			}
			if c != '"' {
				p.failed = true
				return
			}
			p.beginKey()
		case spKeyStr:
			switch {
			case c == '"':
				if p.stack.len == 1 {
					p.curKey = string(p.topKeyBuf)
				}
				p.state = spExpectColon
			case c == '\\':
				if p.stack.len == 1 {
					p.dupOrWeird = true
				}
				p.state = spKeyEsc
			case c < 0x20:
				p.failed = true
				return
			default:
				if p.stack.len == 1 {
					if len(p.topKeyBuf) < 64 {
						p.topKeyBuf = append(p.topKeyBuf, c)
					} else {
						p.topKeyBuf[0] = 0
					}
				}
			}
		case spKeyEsc:
			switch {
			case isSimpleEscape(c):
				p.state = spKeyStr
			case c == 'u':
				p.uHexLeft = 4
				p.state = spKeyUHex
			default:
				p.failed = true
				return
			}
		case spKeyUHex:
			if !isHexByte(c) {
				p.failed = true
				return
			}
			p.uHexLeft--
			if p.uHexLeft == 0 {
				p.state = spKeyStr
			}
		case spExpectColon:
			if isJSONWS(c) {
				continue
			}
			if c != ':' {
				p.failed = true
				return
			}
			if p.stack.len == 1 {
				p.prepareTopValue()
			}
			p.state = spExpectVal
		case spInStrEsc:
			if p.capturing {
				p.appendValByte(c)
			}
			switch {
			case isSimpleEscape(c):
				p.state = spInStr
			case c == 'u':
				p.uHexLeft = 4
				p.state = spInStrUHex
			default:
				p.failed = true
				return
			}
		case spInStrUHex:
			if p.capturing {
				p.appendValByte(c)
			}
			if !isHexByte(c) {
				p.failed = true
				return
			}
			p.uHexLeft--
			if p.uHexLeft == 0 {
				p.state = spInStr
			}
		case spNumNeg:
			switch {
			case c == '0':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumZero
			case c >= '1' && c <= '9':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumInt
			default:
				p.failed = true
				return
			}
		case spNumZero:
			switch c {
			case '.':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumDot
			case 'e', 'E':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumExpSign
			default:
				p.endValue()
				i--
			}
		case spNumInt:
			switch {
			case c >= '0' && c <= '9':
				if p.capturing {
					p.appendValByte(c)
				}
			case c == '.':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumDot
			case c == 'e' || c == 'E':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumExpSign
			default:
				p.endValue()
				i--
			}
		case spNumDot:
			if c >= '0' && c <= '9' {
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumFrac
			} else {
				p.failed = true
				return
			}
		case spNumFrac:
			switch {
			case c >= '0' && c <= '9':
				if p.capturing {
					p.appendValByte(c)
				}
			case c == 'e' || c == 'E':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumExpSign
			default:
				p.endValue()
				i--
			}
		case spNumExpSign:
			switch {
			case c == '+' || c == '-':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumExpDig
			case c >= '0' && c <= '9':
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumExp
			default:
				p.failed = true
				return
			}
		case spNumExpDig:
			if c >= '0' && c <= '9' {
				if p.capturing {
					p.appendValByte(c)
				}
				p.state = spNumExp
			} else {
				p.failed = true
				return
			}
		case spNumExp:
			if c >= '0' && c <= '9' {
				if p.capturing {
					p.appendValByte(c)
				}
			} else {
				p.endValue()
				i--
			}
		case spLit:
			if p.capturing {
				p.appendValByte(c)
			}
			if c != p.lit[p.litPos] {
				p.failed = true
				return
			}
			p.litPos++
			if p.litPos == len(p.lit) {
				p.endValue()
			}
		case spAfterVal:
			if isJSONWS(c) {
				continue
			}
			if p.stack.len == 0 {
				p.failed = true
				return
			}
			object := p.stack.top()
			switch {
			case c == ',':
				if object {
					p.state = spExpectKey
				} else {
					p.state = spExpectVal
				}
			case object && c == '}', !object && c == ']':
				p.stack.pop()
				p.endValue()
			default:
				p.failed = true
				return
			}
		case spDone:
			if !isJSONWS(c) {
				p.failed = true
				return
			}
		}
	}
}

func (p *streamCustomParser) beginKey() {
	if p.stack.len == 1 {
		p.topKeyBuf = p.topKeyBuf[:0]
	}
	p.state = spKeyStr
}

func (p *streamCustomParser) prepareTopValue() {
	p.capturing = false
	p.topValBuf = p.topValBuf[:0]
	switch p.curKey {
	case "type":
		if p.seenMask&(1<<0) != 0 {
			p.dupOrWeird = true
		}
		p.seenMask |= 1 << 0
		p.capturing = true
	case "id":
		if p.seenMask&(1<<1) != 0 {
			p.dupOrWeird = true
		}
		p.seenMask |= 1 << 1
		p.capturing = true
	case "parentId":
		if p.seenMask&(1<<2) != 0 {
			p.dupOrWeird = true
		}
		p.seenMask |= 1 << 2
		p.capturing = true
	case "timestamp":
		if p.seenMask&(1<<3) != 0 {
			p.dupOrWeird = true
		}
		p.seenMask |= 1 << 3
		p.capturing = true
	default:
		if bytes.EqualFold([]byte(p.curKey), []byte("type")) ||
			bytes.EqualFold([]byte(p.curKey), []byte("id")) ||
			bytes.EqualFold([]byte(p.curKey), []byte("parentId")) ||
			bytes.EqualFold([]byte(p.curKey), []byte("timestamp")) {
			p.dupOrWeird = true
		}
	}
}

func (p *streamCustomParser) startValue(c byte) {
	if p.capturing && p.stack.len == 1 {
		p.appendValByte(c)
	}
	switch {
	case c == '{':
		if p.capturing && p.stack.len == 1 {
			p.dupOrWeird = true
			p.capturing = false
		}
		if !p.stack.push(true) {
			p.failed = true
			return
		}
		p.state = spObjStart
	case c == '[':
		if p.capturing && p.stack.len == 1 {
			p.dupOrWeird = true
			p.capturing = false
		}
		if !p.stack.push(false) {
			p.failed = true
			return
		}
		p.state = spArrStart
	case c == '"':
		p.state = spInStr
	case c == '-':
		p.state = spNumNeg
	case c == '0':
		p.state = spNumZero
	case c >= '1' && c <= '9':
		p.state = spNumInt
	case c == 't':
		p.lit, p.litPos = "true", 1
		p.state = spLit
	case c == 'f':
		p.lit, p.litPos = "false", 1
		p.state = spLit
	case c == 'n':
		p.lit, p.litPos = "null", 1
		p.state = spLit
	default:
		p.failed = true
	}
}

func (p *streamCustomParser) endValue() {
	if p.capturing && p.stack.len == 1 {
		val := bytes.Clone(p.topValBuf)
		switch p.curKey {
		case "type":
			p.rawType = val
		case "id":
			p.rawID = val
		case "parentId":
			p.rawParent = val
		case "timestamp":
			p.rawTime = val
		}
		p.capturing = false
	}
	if p.stack.len == 0 {
		p.state = spDone
	} else {
		p.state = spAfterVal
	}
}

func (p *streamCustomParser) appendVal(b []byte) {
	if len(p.topValBuf)+len(b) > maxStreamMemberBytes {
		p.dupOrWeird = true
		p.capturing = false
		return
	}
	p.topValBuf = append(p.topValBuf, b...)
}

func (p *streamCustomParser) appendValByte(c byte) {
	if len(p.topValBuf)+1 > maxStreamMemberBytes {
		p.dupOrWeird = true
		p.capturing = false
		return
	}
	p.topValBuf = append(p.topValBuf, c)
}

func (p *streamCustomParser) finish() bool {
	if p.failed {
		return false
	}
	switch p.state {
	case spNumZero, spNumInt, spNumFrac, spNumExp:
		p.endValue()
	}
	return !p.failed && p.state == spDone && p.stack.len == 0
}

func (p *streamCustomParser) usable() bool {
	return !p.dupOrWeird
}

func (p *streamCustomParser) base() (SessionEntryBase, bool) {
	var base SessionEntryBase
	if len(p.rawType) == 0 || !probeString(p.rawType, &base.Type, internEntryType) {
		return SessionEntryBase{}, false
	}
	if len(p.rawID) > 0 {
		if !probeString(p.rawID, &base.ID, nil) {
			return SessionEntryBase{}, false
		}
	}
	if len(p.rawParent) > 0 {
		parent, ok := probeOptionalString(p.rawParent, "")
		if !ok {
			return SessionEntryBase{}, false
		}
		base.ParentID = parent
	}
	ts, ok := rawJSONString(p.rawTime)
	if !ok {
		return SessionEntryBase{}, false
	}
	base.Timestamp = ts
	if base.ParentID == nil && len(p.rawParent) == 0 {
		base = sessionentry.WithJSValues(p.miniBaseJSON(), base)
	} else if base.ID == "" && (len(p.rawID) == 0 || !bytes.HasPrefix(p.rawID, []byte(`"`))) {
		base = sessionentry.WithJSValues(p.miniBaseJSON(), base)
	}
	return base, true
}

func (p *streamCustomParser) miniBaseJSON() json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	add := func(k string, v []byte) {
		if len(v) == 0 {
			return
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.WriteByte('"')
		buf.WriteString(k)
		buf.WriteString(`":`)
		buf.Write(v)
	}
	add("id", p.rawID)
	add("parentId", p.rawParent)
	buf.WriteByte('}')
	return buf.Bytes()
}

// jsonStack mirrors internal/jsonvalid's 10,000-level nesting bitstack.
type jsonStack struct {
	len   int
	small [8]uint64
	large []uint64
}

func (s *jsonStack) push(object bool) bool {
	if s.len >= 10000 {
		return false
	}
	word, bit := s.len/64, uint(s.len%64)
	if word >= len(s.small) {
		for len(s.large) <= word-len(s.small) {
			s.large = append(s.large, 0)
		}
		if object {
			s.large[word-len(s.small)] |= 1 << bit
		} else {
			s.large[word-len(s.small)] &^= 1 << bit
		}
	} else {
		if object {
			s.small[word] |= 1 << bit
		} else {
			s.small[word] &^= 1 << bit
		}
	}
	s.len++
	return true
}

func (s *jsonStack) top() bool {
	n := s.len - 1
	word, bit := n/64, uint(n%64)
	if word >= len(s.small) {
		return s.large[word-len(s.small)]>>bit&1 == 1
	}
	return s.small[word]>>bit&1 == 1
}

func (s *jsonStack) pop() { s.len-- }

func isJSONWS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func isSimpleEscape(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}

func isHexByte(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

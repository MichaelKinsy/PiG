// SPDX-License-Identifier: MIT

package history

import "math"

// Token estimation ports pi-ai's src/utils/estimate.ts (estimateMessageTokens, calculateContextTokens), which
// compaction range selection and the threshold checks use. Lengths are JavaScript string lengths: UTF-16 units.

const (
	charsPerTokenTimes2 = 7    // chars / 3.5 == 2 * chars / 7
	estimatedImageChars = 4800 // ESTIMATED_IMAGE_CHARS
)

// ceilTokens is Math.ceil(chars / 3.5) for a non-negative integer chars.
func ceilTokens(chars int) float64 {
	return float64((2*chars + charsPerTokenTimes2 - 1) / charsPerTokenTimes2)
}

// contentChars is the length estimateTextAndImageContentChars counts for a user or tool result content value: a
// string's length, or per block the text's length or the image allowance.
func contentChars(v []byte) int {
	switch valueKind(v) {
	case '"':
		return unitsOfRaw(v[1 : len(v)-1])
	case '[':
		chars := 0
		s := newScanner(v)
		it, err := s.array()
		if err != nil {
			return 0
		}
		for it.next() {
			blk := v[it.vs:it.ve]
			if text, ok := textBlock(blk); ok {
				chars += unitsOfRaw(text)
			} else {
				chars += estimatedImageChars
			}
		}
		return chars
	}
	return 0
}

// textBlock reports whether blk is a {type: "text", text} block and returns the text's raw string bytes.
func textBlock(blk []byte) (raw []byte, isText bool) {
	if valueKind(blk) != '{' {
		return nil, false
	}
	s := newScanner(blk)
	it, err := s.object()
	if err != nil {
		return nil, false
	}
	for it.next() {
		switch {
		case it.keyIs("type"):
			isText = false
			if v := it.value(); valueKind(v) == '"' {
				isText = keyIs(v[1:len(v)-1], "text")
			}
		case it.keyIs("text"):
			raw = nil
			if v := it.value(); valueKind(v) == '"' {
				raw = v[1 : len(v)-1]
			}
		}
	}
	return raw, isText
}

// canonicalUnits is the length of JSON.stringify(value) for the JSON value v.
func canonicalUnits(v []byte) int {
	var buf [256]byte
	out, err := Canonical(buf[:0], v)
	if err != nil {
		return len("[unserializable]")
	}
	return unitsOf(out)
}

// EstimateMessageTokens is pi-ai's estimateMessageTokens for the message JSON msg.
func EstimateMessageTokens(msg []byte) float64 {
	s := newScanner(msg)
	it, err := s.object()
	if err != nil {
		return 0
	}
	var role []byte
	var content, sections, toolsAdded, toolsRemoved []byte
	for it.next() {
		switch {
		case it.keyIs("role"):
			role = nil
			if v := it.value(); valueKind(v) == '"' {
				role = v[1 : len(v)-1]
			}
		case it.keyIs("content"):
			content = it.value()
		case it.keyIs("sections"):
			sections = it.value()
		case it.keyIs("toolsAdded"):
			toolsAdded = it.value()
		case it.keyIs("toolsRemoved"):
			toolsRemoved = it.value()
		}
	}
	switch {
	case keyIs(role, "system"):
		return ceilTokens(systemTextUnits(content, sections)) + toolsTokens(toolsAdded) + toolsTokens(toolsRemoved)
	case keyIs(role, "user"), keyIs(role, "toolResult"):
		return ceilTokens(contentChars(content))
	}
	// Every other role is estimated as an assistant message.
	chars := 0
	if valueKind(content) != '[' {
		return 0
	}
	cs := newScanner(content)
	ait, err := cs.array()
	if err != nil {
		return 0
	}
	for ait.next() {
		blk := content[ait.vs:ait.ve]
		bs := newScanner(blk)
		oit, err := bs.object()
		if err != nil {
			continue
		}
		var typ, text, thinking, name, args []byte
		hasArgs := false
		for oit.next() {
			switch {
			case oit.keyIs("type"):
				typ = nil
				if v := oit.value(); valueKind(v) == '"' {
					typ = v[1 : len(v)-1]
				}
			case oit.keyIs("text"):
				text = nil
				if v := oit.value(); valueKind(v) == '"' {
					text = v[1 : len(v)-1]
				}
			case oit.keyIs("thinking"):
				thinking = nil
				if v := oit.value(); valueKind(v) == '"' {
					thinking = v[1 : len(v)-1]
				}
			case oit.keyIs("name"):
				name = nil
				if v := oit.value(); valueKind(v) == '"' {
					name = v[1 : len(v)-1]
				}
			case oit.keyIs("arguments"):
				args, hasArgs = oit.value(), true
			}
		}
		switch {
		case keyIs(typ, "text"):
			chars += unitsOfRaw(text)
		case keyIs(typ, "thinking"):
			chars += unitsOfRaw(thinking)
		default:
			chars += unitsOfRaw(name)
			if hasArgs {
				chars += canonicalUnits(args)
			} else {
				chars += len("undefined")
			}
		}
	}
	return ceilTokens(chars)
}

// systemTextUnits is the length of getSystemMessageText: the content text and every non-null section, those with
// text joined by a blank line.
func systemTextUnits(content, sections []byte) int {
	total, parts := 0, 0
	add := func(n int) {
		if n > 0 {
			if parts > 0 {
				total += 2
			}
			total += n
			parts++
		}
	}
	switch valueKind(content) {
	case '"':
		add(unitsOfRaw(content[1 : len(content)-1]))
	case '[':
		n, blocks := 0, 0
		s := newScanner(content)
		if it, err := s.array(); err == nil {
			for it.next() {
				if text, ok := textBlock(content[it.vs:it.ve]); ok {
					if blocks > 0 {
						n++
					}
					n += unitsOfRaw(text)
					blocks++
				}
			}
		}
		add(n)
	default:
		add(0)
	}
	if valueKind(sections) == '{' {
		s := newScanner(sections)
		if it, err := s.object(); err == nil {
			for it.next() {
				if v := it.value(); valueKind(v) == '"' {
					add(unitsOfRaw(v[1 : len(v)-1]))
				}
			}
		}
	}
	return total
}

func toolsTokens(v []byte) float64 {
	if valueKind(v) != '[' {
		return 0
	}
	s := newScanner(v)
	it, err := s.array()
	if err != nil || !it.next() {
		return 0
	}
	return ceilTokens(canonicalUnits(v))
}

// numberField returns a numeric field, NaN when it is absent or not a number (JavaScript undefined arithmetic).
func numberField(v []byte) float64 {
	if valueKind(v) != 'n' {
		return math.NaN()
	}
	f, err := parseFloat(v)
	if err != nil {
		return math.NaN()
	}
	return f
}

// CalculateContextTokens is pi-ai's calculateContextTokens for an assistant message's usage: totalTokens when it
// is truthy, else the sum of the four counters (NaN when one is missing).
func CalculateContextTokens(msg []byte) float64 {
	s := newScanner(msg)
	it, err := s.object()
	if err != nil {
		return math.NaN()
	}
	for it.next() {
		if !it.keyIs("usage") {
			continue
		}
		v := it.value()
		if valueKind(v) != '{' {
			return math.NaN()
		}
		us := newScanner(v)
		uit, err := us.object()
		if err != nil {
			return math.NaN()
		}
		total, in, out, cr, cw := math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()
		for uit.next() {
			switch {
			case uit.keyIs("totalTokens"):
				total = numberField(uit.value())
			case uit.keyIs("input"):
				in = numberField(uit.value())
			case uit.keyIs("output"):
				out = numberField(uit.value())
			case uit.keyIs("cacheRead"):
				cr = numberField(uit.value())
			case uit.keyIs("cacheWrite"):
				cw = numberField(uit.value())
			}
		}
		if total != 0 && !math.IsNaN(total) {
			return total
		}
		return in + out + cr + cw
	}
	return math.NaN()
}

package build

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The source map reader build.ts's `freeGlobals` needs: the inline map esbuild appends to a script (stack.ts's
// `inlineSourceMap`) read as Node's `SourceMap` reads it (lib/internal/source_map/source_map.js): the mappings' VLQ
// decoded into a list of mappings in the order the string gives them, and `findEntry` a binary search over it.

// sourceMapEntry is a mapping found by sourceMap.findEntry. Upstream's entry carries `originalSource` only for a mapping
// of more than one field; a mapping of the generated position alone has none (hasSource false), and build.ts skips it.
type sourceMapEntry struct {
	originalSource string
	hasSource      bool
	originalLine   int
	originalColumn int
}

// sourceMapping is one decoded segment of the mappings: its generated position and its entry.
type sourceMapping struct {
	line, column int
	sourceMapEntry
}

// sourceMap is a parsed source map (version 3, by its `sources` and `mappings`).
type sourceMap struct {
	mappings []sourceMapping
}

// inlineSourceMapPattern is the inline source map esbuild appends to a script.
var inlineSourceMapPattern = regexp.MustCompile(`//# sourceMappingURL=data:application/json;base64,([A-Za-z0-9+/=]+)\s*$`)

// inlineSourceMap is the inline source map esbuild appended to a built script: nil when it has none, an error when the
// map is not valid base64 or JSON (upstream's decode throws).
func inlineSourceMap(source string) (*sourceMap, error) {
	match := inlineSourceMapPattern.FindStringSubmatch(source)
	if match == nil {
		return nil, nil
	}
	text, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		return nil, err
	}
	return parseSourceMap(text)
}

// parseSourceMap parses a source map's JSON text.
func parseSourceMap(text []byte) (*sourceMap, error) {
	var raw struct {
		Sources  []string `json:"sources"`
		Mappings string   `json:"mappings"`
	}
	if err := json.Unmarshal(text, &raw); err != nil {
		return nil, err
	}
	m := &sourceMap{}
	if err := m.parse(raw.Sources, raw.Mappings); err != nil {
		return nil, err
	}
	return m, nil
}

// parse reads `mappings` as Node's `#parseMap` does: `;` starts a generated line (its column back to 0), `,` separates the
// segments of a line, a segment is one, four or five VLQ fields (generated column, source index, source line and column,
// name index), each relative to the field before it.
func (m *sourceMap) parse(sources []string, mappings string) error {
	var line, column, sourceIndex, sourceLine, sourceColumn int
	var source string
	var hasSource bool
	if len(sources) > 0 {
		source, hasSource = sources[0], true
	}
	at := 0
	// isSeparator is Node's, with the end of the string one too (a last segment of one field).
	isSeparator := func() bool { return at >= len(mappings) || mappings[at] == ',' || mappings[at] == ';' }
	for {
		if at < len(mappings) && mappings[at] == ',' {
			at++
		} else {
			for at < len(mappings) && mappings[at] == ';' {
				line++
				column = 0
				at++
			}
			if at >= len(mappings) {
				return nil
			}
		}
		delta, err := decodeVLQ(mappings, &at)
		if err != nil {
			return err
		}
		column += delta
		if isSeparator() {
			m.mappings = append(m.mappings, sourceMapping{line: line, column: column})
			continue
		}
		if delta, err = decodeVLQ(mappings, &at); err != nil {
			return err
		}
		if delta != 0 {
			sourceIndex += delta
			hasSource = sourceIndex >= 0 && sourceIndex < len(sources)
			source = ""
			if hasSource {
				source = sources[sourceIndex]
			}
		}
		if delta, err = decodeVLQ(mappings, &at); err != nil {
			return err
		}
		sourceLine += delta
		if delta, err = decodeVLQ(mappings, &at); err != nil {
			return err
		}
		sourceColumn += delta
		if !isSeparator() {
			// The name's index: no use here, but its field is read.
			if _, err = decodeVLQ(mappings, &at); err != nil {
				return err
			}
		}
		m.mappings = append(m.mappings, sourceMapping{line: line, column: column, sourceMapEntry: sourceMapEntry{
			originalSource: source, hasSource: hasSource, originalLine: sourceLine, originalColumn: sourceColumn,
		}})
	}
}

// vlqAlphabet is the base64 digits of a VLQ.
const vlqAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// decodeVLQ reads one base64 VLQ of text at *at: digits of five bits, least significant first, the sixth bit saying more
// follow, the lowest bit of the value its sign.
func decodeVLQ(text string, at *int) (int, error) {
	result, shift := 0, 0
	for {
		if *at >= len(text) {
			return 0, errors.New("source map: mappings end inside a VLQ")
		}
		digit := strings.IndexByte(vlqAlphabet, text[*at])
		if digit < 0 {
			return 0, fmt.Errorf("source map: %q is no base64 digit", text[*at])
		}
		*at++
		result += (digit & 0x1f) << shift
		shift += 5
		if digit&0x20 == 0 {
			break
		}
	}
	negative := result&1 == 1
	result >>= 1
	if negative {
		return -result, nil
	}
	return result, nil
}

// findEntry is Node's `SourceMap.findEntry(line, column)` (both 0-based, generated): the greatest mapping at or before the
// position in the order the mappings were read, found by the same binary search; found is false where Node returns `{}`
// (no mapping at or before the position). An entry without a source has hasSource false.
func (m *sourceMap) findEntry(line, column int) (entry sourceMapEntry, found bool) {
	first, count := 0, len(m.mappings)
	before := func(mapping sourceMapping) bool {
		return line < mapping.line || (line == mapping.line && column < mapping.column)
	}
	for count > 1 {
		step := count >> 1
		middle := first + step
		if before(m.mappings[middle]) {
			count = step
		} else {
			first = middle
			count -= step
		}
	}
	if first >= len(m.mappings) {
		return sourceMapEntry{}, false
	}
	mapping := m.mappings[first]
	if first == 0 && before(mapping) {
		return sourceMapEntry{}, false
	}
	return mapping.sourceMapEntry, true
}

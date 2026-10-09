package yaml12

// Ports yaml 2.9.0 src/errors.ts, src/parse/line-counter.ts and the Directives class of src/doc/directives.ts. Copyright Eemeli Aro, ISC licence (see lexer.go).

import (
	"errors"
	"maps"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// pos is a [start, end] byte range of the source.
type pos [2]int

func posOfToken(t *Token) pos {
	switch t.Type {
	case "document", "block-map", "block-seq", "flow-collection":
		return pos{t.Offset, t.Offset + 1}
	}
	return pos{t.Offset, t.Offset + len(t.Source)}
}

func posOfOffset(n int) pos { return pos{n, n + 1} }

func posOfRange(r [3]int) pos { return pos{r[0], r[1]} }

type yamlError struct {
	code    string
	pos     pos
	message string
}

// errorFn reports a parse error, or a warning when warning is true.
type errorFn func(p pos, code, message string, warning ...bool)

type directives struct {
	explicit       bool
	version        string
	tags           map[string]string
	docStart       bool
	docEnd         bool
	atNextDocument bool
}

func defaultTags() map[string]string { return map[string]string{"!!": "tag:yaml.org,2002:"} }

func newDirectives(version string) *directives {
	return &directives{version: version, tags: defaultTags()}
}

func (d *directives) copy() *directives {
	tags := make(map[string]string, len(d.tags))
	maps.Copy(tags, d.tags)
	return &directives{explicit: d.explicit, version: d.version, tags: tags}
}

func (d *directives) atDocument() *directives {
	res := d.copy()
	switch d.version {
	case "1.1":
		d.atNextDocument = true
	case "1.2":
		d.atNextDocument = false
		d.explicit = false
		d.version = "1.2"
		d.tags = defaultTags()
	}
	return res
}

var (
	jsWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
	versionRE    = lazyregexp.New(`^[0-9]+\.[0-9]+$`)
)

// add reads a directive line; onError receives the offset within the line, the message and whether it is a warning.
func (d *directives) add(line string, onError func(offset int, message string, warning bool)) {
	if d.atNextDocument {
		d.explicit, d.version = false, "1.1"
		d.tags = defaultTags()
		d.atNextDocument = false
	}
	trimmed := strings.Trim(line, jsWhitespace)
	parts := splitBlanks(trimmed)
	name := parts[0]
	parts = parts[1:]
	switch name {
	case "%TAG":
		if len(parts) != 2 {
			onError(0, "%TAG directive should contain exactly two parts", false)
			if len(parts) < 2 {
				return
			}
		}
		d.tags[parts[0]] = parts[1]
	case "%YAML":
		d.explicit = true
		if len(parts) != 1 {
			onError(0, "%YAML directive should contain exactly one part", false)
			return
		}
		version := parts[0]
		if version == "1.1" || version == "1.2" {
			d.version = version
			return
		}
		onError(6, "Unsupported YAML version "+version, versionRE.MatchString(version))
	default:
		onError(0, "Unknown directive "+name, true)
	}
}

// splitBlanks is split(/[ \t]+/).
func splitBlanks(s string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		if s[i] == ' ' || s[i] == '\t' {
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			out = append(out, s[start:i])
			start = j
			i = j
			continue
		}
		i++
	}
	return append(out, s[start:])
}

// tagName resolves a tag token's source against the %TAG handles. It returns "" where the library returns null.
func (d *directives) tagName(source string, onError func(message string)) string {
	if source == "!" {
		return "!"
	}
	if source[0] != '!' {
		onError("Not a valid tag: " + source)
		return ""
	}
	if len(source) > 1 && source[1] == '<' {
		verbatim := ""
		if len(source) > 3 {
			verbatim = source[2 : len(source)-1]
		}
		if verbatim == "!" || verbatim == "!!" {
			onError("Verbatim tags aren't resolved, so " + source + " is invalid.")
			return ""
		}
		if source[len(source)-1] != '>' {
			onError("Verbatim tags must end with a >")
		}
		return verbatim
	}
	i := strings.LastIndexByte(source, '!')
	handle, suffix := source[:i+1], source[i+1:]
	if suffix == "" {
		onError("The " + source + " tag has no suffix")
	}
	if prefix := d.tags[handle]; prefix != "" {
		decoded, err := decodeURIComponent(suffix)
		if err != nil {
			onError("URIError: URI malformed")
			return ""
		}
		return prefix + decoded
	}
	if handle == "!" {
		return source
	}
	onError("Could not resolve tag: " + source)
	return ""
}

// decodeURIComponent mirrors the JavaScript function, including rejecting malformed UTF-8.
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	hexAt := func(i int) (byte, bool) {
		if i+3 > len(s) || s[i] != '%' {
			return 0, false
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil || !isHexByte(s[i+1]) || !isHexByte(s[i+2]) {
			return 0, false
		}
		return byte(v), true
	}
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := hexAt(i)
		if !ok {
			return "", errors.New("URI malformed")
		}
		n := 1
		switch {
		case first < 0x80:
		case first >= 0xC0 && first < 0xE0:
			n = 2
		case first >= 0xE0 && first < 0xF0:
			n = 3
		case first >= 0xF0 && first < 0xF8:
			n = 4
		default:
			return "", errors.New("URI malformed")
		}
		seq := []byte{first}
		for k := 1; k < n; k++ {
			c, ok := hexAt(i + 3*k)
			if !ok || c&0xC0 != 0x80 {
				return "", errors.New("URI malformed")
			}
			seq = append(seq, c)
		}
		if n > 1 {
			r, size := utf8.DecodeRune(seq)
			if r == utf8.RuneError || size != n {
				return "", errors.New("URI malformed")
			}
		}
		b.Write(seq)
		i += 3 * n
	}
	return b.String(), nil
}

func isHexByte(c byte) bool { return isHex(int(c)) }

// jsonQuote is JSON.stringify of a string.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteString(strconv.FormatInt(int64(r)+0x100, 16)[1:])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// prettify appends the line, column and source excerpt the library adds to a parse error message.
func prettify(src string, lineStarts []int, e *yamlError) {
	if e.pos[0] == -1 {
		return
	}
	u := utf16.Encode([]rune(src))
	offsets := utf16Index(src)
	ls := make([]int, len(lineStarts))
	for i, b := range lineStarts {
		ls[i] = offsets[b]
	}
	linePos := func(offset int) (line, col int) {
		low, high := 0, len(ls)
		for low < high {
			mid := (low + high) >> 1
			if ls[mid] < offset {
				low = mid + 1
			} else {
				high = mid
			}
		}
		if low < len(ls) && ls[low] == offset {
			return low + 1, 1
		}
		if low == 0 {
			return 0, offset
		}
		return low, offset - ls[low-1] + 1
	}
	sub := func(a, b int) []uint16 {
		a, b = min(max(a, 0), len(u)), min(max(b, 0), len(u))
		if a > b {
			a, b = b, a
		}
		return u[a:b]
	}
	lineStart := func(line int) int {
		if line >= 0 && line < len(ls) {
			return ls[line]
		}
		return len(u)
	}
	toU16 := func(b int) int {
		if b >= len(offsets) {
			return offsets[len(offsets)-1] + b - (len(offsets) - 1)
		}
		return offsets[b]
	}
	line, col := linePos(toU16(e.pos[0]))
	endLine, endCol := linePos(toU16(e.pos[1]))
	e.message += " at line " + strconv.Itoa(line) + ", column " + strconv.Itoa(col)
	ci := col - 1
	lineStr := sub(lineStart(line-1), lineStart(line))
	for len(lineStr) > 0 && (lineStr[len(lineStr)-1] == '\n' || lineStr[len(lineStr)-1] == '\r') {
		lineStr = lineStr[:len(lineStr)-1]
	}
	ellipsis := []uint16{0x2026}
	if ci >= 60 && len(lineStr) > 80 {
		trimStart := min(ci-39, len(lineStr)-79)
		lineStr = append(append([]uint16{}, ellipsis...), lineStr[min(max(trimStart, 0), len(lineStr)):]...)
		ci -= trimStart - 1
	}
	if len(lineStr) > 80 {
		lineStr = append(append([]uint16{}, lineStr[:79]...), ellipsis...)
	}
	allSpaces := true
	for _, c := range lineStr[:min(max(ci, 0), len(lineStr))] {
		if c != ' ' {
			allSpaces = false
		}
	}
	if line > 1 && allSpaces {
		prev := append([]uint16{}, sub(lineStart(line-2), lineStart(line-1))...)
		if len(prev) > 80 {
			prev = append(append([]uint16{}, prev[:79]...), ellipsis...)
			prev = append(prev, '\n')
		}
		lineStr = append(prev, lineStr...)
	}
	for _, c := range lineStr {
		if c != ' ' {
			count := 1
			if endLine == line && endCol > col {
				count = max(1, min(endCol-col, 80-ci))
			}
			pointer := strings.Repeat(" ", max(ci, 0)) + strings.Repeat("^", count)
			e.message += ":\n\n" + string(utf16.Decode(lineStr)) + "\n" + pointer + "\n"
			return
		}
	}
}

// utf16Index maps each byte offset of src to the offset of the same position in the UTF-16 string the library reads.
func utf16Index(src string) []int {
	out := make([]int, len(src)+1)
	n := 0
	for i, r := range src {
		for j := i; j < len(src) && (j == i || !utf8.RuneStart(src[j])); j++ {
			out[j] = n
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	out[len(src)] = n
	return out
}

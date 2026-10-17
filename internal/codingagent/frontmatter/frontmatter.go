// Package frontmatter parses YAML frontmatter blocks from markdown files.
package frontmatter

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/text"
	"github.com/MichaelKinsy/PiG/internal/yaml12"
)

// Doc is a parsed markdown-with-frontmatter file.
type Doc struct {
	// Frontmatter holds the YAML values from the document header.
	Frontmatter map[string]any
	// Value is what Pi's parseFrontmatter returns as `frontmatter`: the parsed header (`parsed ?? {}`), which is a mapping, or an array, string, number or boolean when the header is not a mapping. Frontmatter is the mapping, or empty.
	Value any
	// Body is the trimmed markdown content following the closing `---`.
	Body string
	// Err reports malformed YAML. The body remains available to callers that
	// intentionally tolerate malformed frontmatter.
	Err error
}

// Parse extracts and parses a YAML frontmatter block plus body. If the file
// does not begin with a closed frontmatter block, the whole input is returned
// as Body and Frontmatter is empty.
func Parse(content string) Doc { return parse(content, false) }

// ParseOrdered is Parse with Value built by yaml12.ParseOrdered: each object in it is a *yaml12.Object that lists its keys in insertion order, which is the
// order a JavaScript object enumerates them in (after its array-index keys). Frontmatter is the top-level mapping's map as in Parse.
func ParseOrdered(content string) Doc { return parse(content, true) }

func parse(content string, ordered bool) Doc {
	s := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text.StripBom(content))
	if !strings.HasPrefix(s, "---") {
		return Doc{Frontmatter: map[string]any{}, Value: map[string]any{}, Body: s}
	}
	end := strings.Index(s[3:], "\n---")
	if end < 0 {
		return Doc{Frontmatter: map[string]any{}, Value: map[string]any{}, Body: s}
	}
	end += 3
	// upstream: packages/coding-agent/src/utils/frontmatter.ts:extractFrontmatter
	body := strings.Trim(s[end+4:], "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
	fields := map[string]any{}
	var value any = fields
	if yamlString := headerText(s, end); yamlString != "" {
		parse := yaml12.Parse
		if ordered {
			parse = yaml12.ParseOrdered
		}
		parsed, err := parse(yamlString)
		if err != nil {
			return Doc{Frontmatter: map[string]any{}, Value: map[string]any{}, Body: body, Err: err}
		}
		// upstream: packages/coding-agent/src/utils/frontmatter.ts:parseFrontmatter reads `parsed ?? {}`; a document that is not a mapping carries no fields.
		if parsedFields, ok := parsed.(map[string]any); ok {
			fields, value = parsedFields, parsedFields
		} else if object, ok := parsed.(*yaml12.Object); ok {
			fields, value = object.Values, object
		} else if parsed != nil {
			value = parsed
		}
	}
	return Doc{Frontmatter: fields, Value: value, Body: body}
}

// headerText is `normalized.slice(4, endIndex)`: the header after "---" and the one UTF-16 code unit that follows it, which is the line break unless the opening line carries more. Slicing at byte 4 would cut a multi-byte character; an astral character leaves its low surrogate, as a JavaScript slice does.
func headerText(s string, end int) string {
	r, size := utf8.DecodeRuneInString(s[3:])
	if r >= 0x10000 {
		low := 0xdc00 + (r-0x10000)&0x3ff
		return string([]byte{0xed, byte(0x80 | (low>>6)&0x3f), byte(0x80 | low&0x3f)}) + s[3+size:end]
	}
	if end <= 3+size {
		return ""
	}
	return s[3+size : end]
}

// String returns the named field as a string, or "" if missing.
func (d Doc) String(key string) string {
	v, ok := d.Frontmatter[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}

// Bool returns the named field as a bool, defaulting to false.
func (d Doc) Bool(key string) bool {
	v, ok := d.Frontmatter[key]
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	if s, ok := v.(string); ok {
		switch s {
		case "true", "yes", "True", "TRUE":
			return true
		}
	}
	return false
}

// Strip is upstream's stripFrontmatter (utils/frontmatter.ts:40): the body of content without its frontmatter. Malformed YAML in the header is an error, as upstream's parse throws.
func Strip(content string) (string, error) {
	doc := Parse(content)
	return doc.Body, doc.Err
}

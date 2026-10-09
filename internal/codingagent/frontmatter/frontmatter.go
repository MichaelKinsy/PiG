// Package frontmatter parses YAML frontmatter blocks from markdown files.
package frontmatter

import (
	"strings"

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
func Parse(content string) Doc {
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
	if end > 3 {
		parsed, err := yaml12.Parse(s[4:end])
		if err != nil {
			return Doc{Frontmatter: map[string]any{}, Value: map[string]any{}, Body: body, Err: err}
		}
		// upstream: packages/coding-agent/src/utils/frontmatter.ts:parseFrontmatter reads `parsed ?? {}`; a document that is not a mapping carries no fields.
		if parsedFields, ok := parsed.(map[string]any); ok {
			fields, value = parsedFields, parsedFields
		} else if parsed != nil {
			value = parsed
		}
	}
	return Doc{Frontmatter: fields, Value: value, Body: body}
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if (strings.HasPrefix(p, `"`) && strings.HasSuffix(p, `"`)) ||
			(strings.HasPrefix(p, `'`) && strings.HasSuffix(p, `'`)) {
			p = p[1 : len(p)-1]
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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

// StringList returns the named field as a []string, accepting either a
// list value or a single comma-separated string.
func (d Doc) StringList(key string) []string {
	v, ok := d.Frontmatter[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, value := range t {
			if value, ok := value.(string); ok {
				out = append(out, value)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return splitList(t)
	}
	return nil
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

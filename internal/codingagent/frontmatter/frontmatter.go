// Package frontmatter parses YAML frontmatter blocks from markdown files.
package frontmatter

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/text"
)

// Doc is a parsed markdown-with-frontmatter file.
type Doc struct {
	// Frontmatter holds the YAML values from the document header.
	Frontmatter map[string]any
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
		return Doc{Frontmatter: map[string]any{}, Body: s}
	}
	end := strings.Index(s[3:], "\n---")
	if end < 0 {
		return Doc{Frontmatter: map[string]any{}, Body: s}
	}
	end += 3
	body := strings.TrimSpace(s[end+4:])
	fields := map[string]any{}
	if end > 3 {
		var parsed any
		if err := yaml.Unmarshal([]byte(s[4:end]), &parsed); err != nil {
			return Doc{Frontmatter: map[string]any{}, Body: body, Err: fmt.Errorf("parse frontmatter: %w", err)}
		}
		if parsedFields, ok := parsed.(map[string]any); ok {
			fields = parsedFields
		}
	}
	return Doc{Frontmatter: fields, Body: body}
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

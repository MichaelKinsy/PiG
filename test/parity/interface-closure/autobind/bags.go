package main

import (
	"regexp"
	"strings"
)

var pickRe = regexp.MustCompile(`(?s)^Pick<\s*[A-Za-z0-9_]+\s*,\s*(.+)>$`)

// bagMembers lists the members of an upstream options-bag parameter type: an inline object literal, a Pick of string literal keys,
// or a named interface whose properties the inventory records. A member without a recorded type has an empty Type.
func (c *checker) bagMembers(typ string) ([]param, bool) {
	typ = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(typ), "| undefined"))
	typ = strings.TrimSpace(typ)
	switch {
	case strings.HasPrefix(typ, "{") && strings.HasSuffix(typ, "}"):
		var out []param
		inner := strings.TrimSuffix(strings.TrimPrefix(typ, "{"), "}")
		for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, mt, ok := strings.Cut(part, ":")
			if !ok {
				return nil, false
			}
			name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "readonly "))
			opt := strings.HasSuffix(name, "?")
			out = append(out, param{Name: strings.TrimSuffix(name, "?"), Type: strings.TrimSpace(mt), Optional: opt})
		}
		return out, len(out) > 0
	case pickRe.MatchString(typ):
		var out []param
		for key := range strings.SplitSeq(pickRe.FindStringSubmatch(typ)[1], "|") {
			key = strings.Trim(strings.TrimSpace(key), `"'`)
			if key == "" {
				return nil, false
			}
			out = append(out, param{Name: key})
		}
		return out, true
	case c.propNames != nil:
		names := c.propNames(typ)
		var out []param
		for _, n := range names {
			out = append(out, param{Name: n, Optional: true})
		}
		return out, len(out) > 0
	}
	return nil, false
}

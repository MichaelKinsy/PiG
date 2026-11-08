// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The shared statement layer: source text to statements, plus the small
// string-reading helpers every grammar leans on. Ported from lovely-mermaid
// statements.ts.

// ---------------------------------------------------------------- whitespace

func trimJS(s string) string      { return strings.TrimFunc(s, jsstring.IsSpace) }
func trimStartJS(s string) string { return strings.TrimLeftFunc(s, jsstring.IsSpace) }
func trimEndJS(s string) string   { return strings.TrimRightFunc(s, jsstring.IsSpace) }

func containsJSSpace(s string) bool { return strings.IndexFunc(s, jsstring.IsSpace) >= 0 }

func words(s string) []string { return strings.FieldsFunc(s, jsstring.IsSpace) }

func firstWord(s string) string {
	w := words(s)
	if len(w) == 0 {
		return ""
	}
	return w[0]
}

// ---------------------------------------------------------------- statements

func flushStatement(cur string, out *[]string) string {
	trimmed := trimJS(cur)
	if trimmed != "" {
		*out = append(*out, trimmed)
	}
	return ""
}

// splitStatements splits one line into statements on ';', stopping at a %%
// comment. Quoted spans are opaque, so a label may contain `;` and `%%`.
func splitStatements(line string, out *[]string) {
	chars := []rune(line)
	var cur strings.Builder
	inQuotes := false
	flush := func() { flushStatement(cur.String(), out); cur.Reset() }
	for i := range chars {
		c := chars[i]
		switch {
		case inQuotes:
			if c == '"' {
				inQuotes = false
			}
			cur.WriteRune(c)
		case c == '"':
			inQuotes = true
			cur.WriteRune(c)
		case c == '%' && charAt(chars, i+1) == '%':
			flush()
			return
		case c == ';':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
}

// frontmatterEnd is the index just past a leading YAML frontmatter block
// (`---` ... `---`), or 0 when there is none. While the block is still
// unterminated everything is frontmatter, so a streamed diagram stays blank
// until it closes.
func frontmatterEnd(lines []string) int {
	i := 0
	for i < len(lines) && trimJS(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || trimJS(lines[i]) != "---" {
		return 0
	}
	i++
	for i < len(lines) && trimJS(lines[i]) != "---" {
		i++
	}
	return i + 1
}

// statementsOf returns all statements in a source block, in order. A leading
// YAML frontmatter block, part of the mermaid grammar since v10, is skipped.
func statementsOf(src string) []string {
	lines := srcLines(src)
	var out []string
	end := min(frontmatterEnd(lines), len(lines))
	for _, line := range lines[end:] {
		splitStatements(line, &out)
	}
	return out
}

// frontmatterTitle is the `title:` of a leading frontmatter block, or ok=false.
// The one frontmatter key with terminal meaning; `config` and friends style
// mermaid's own renderers and are deliberately ignored.
func frontmatterTitle(src string) (string, bool) {
	lines := srcLines(src)
	end := min(frontmatterEnd(lines), len(lines))
	for _, line := range lines[:end] {
		key, value, ok := splitOnce(line, ":")
		// Untrimmed on the left: an indented `title:` is nested under some other
		// key, not the diagram's.
		if !ok || trimEndJS(key) != "title" {
			continue
		}
		t := trimJS(value)
		quoted := len([]rune(t)) > 1 &&
			((strings.HasPrefix(t, "\"") && strings.HasSuffix(t, "\"")) || (strings.HasPrefix(t, "'") && strings.HasSuffix(t, "'")))
		title := t
		if quoted {
			title = t[1 : len(t)-1]
		}
		title = trimJS(title)
		if title == "" {
			return "", false
		}
		return title, true
	}
	return "", false
}

// quoteMask flags, per char, 1 where the char lies inside a double-quoted span
// (quotes included).
func quoteMask(chars []rune) []byte {
	mask := make([]byte, len(chars))
	inQuotes := false
	for i, c := range chars {
		if c == '"' {
			mask[i] = 1
			inQuotes = !inQuotes
		} else if inQuotes {
			mask[i] = 1
		}
	}
	return mask
}

// splitTop splits on separator chars sitting outside double quotes and
// parentheses, dropping empty segments: the one splitter every grammar shares,
// so quote rules cannot drift between them.
func splitTop(s string, isSep func(rune) bool) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	depth := 0
	for _, c := range s {
		if c == '"' {
			inQuotes = !inQuotes
		} else if !inQuotes && c == '(' {
			depth++
		} else if !inQuotes && c == ')' && depth > 0 {
			depth--
		}
		if !inQuotes && depth == 0 && isSep(c) {
			if cur.Len() > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
		} else {
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// splitColon splits `head : rest` at the first label colon, skipping `:::` tag
// runs so `A:::hot : desc` keeps its tag with the id. ok=false when there is no
// colon.
func splitColon(s string) (head, rest string, ok bool) {
	chars := []rune(s)
	for i := 0; i < len(chars); i++ {
		if chars[i] != ':' {
			continue
		}
		run := i
		for run < len(chars) && chars[run] == ':' {
			run++
		}
		if run-i >= 3 {
			i = run - 1
			continue
		}
		return string(chars[:i]), string(chars[i+1:]), true
	}
	return "", "", false
}

// takeTags strips trailing `:::name` tags from an id token: `A:::hot` is id
// `A`, classes `[hot]`.
func takeTags(token string) (id string, classes []string) {
	parts := strings.Split(token, ":::")
	if len(parts) == 1 || parts[0] == "" {
		return token, nil
	}
	for _, c := range parts[1:] {
		if c != "" {
			classes = append(classes, c)
		}
	}
	return parts[0], classes
}

func splitCommaList(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = trimJS(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseClassAssign reads the body of a `class A,B name` statement. The last
// whitespace-separated token is the name list, everything before it the ids, so
// a space after a comma (`class A, B warn`) still reads as two ids.
func parseClassAssign(rest string) (classAssign, bool) {
	body := trimJS(rest)
	ws := strings.LastIndexFunc(body, jsstring.IsSpace)
	if ws == -1 {
		return classAssign{}, false
	}
	return classAssign{ids: splitCommaList(body[:ws]), names: splitCommaList(body[ws:])}, true
}

var hrefURL = regexp.MustCompile(`"([^"]+)"`)

// parseHref reads `A "url" [tooltip]` / `A href "url" ...` into an id and url.
// The callback forms (`call`/`callback`) return ok=false: their quoted string is
// a tooltip.
func parseHref(rest string) (hrefAssign, bool) {
	w := words(rest)
	if len(w) == 0 || (len(w) > 1 && (w[1] == "call" || w[1] == "callback")) {
		return hrefAssign{}, false
	}
	m := hrefURL.FindStringSubmatch(rest)
	if m == nil {
		return hrefAssign{}, false
	}
	return hrefAssign{id: w[0], url: m[1]}, true
}

// splitOnce splits on the first occurrence of sep (Rust's split_once).
func splitOnce(s, sep string) (pre, post string, ok bool) {
	before, after, ok0 := strings.Cut(s, sep)
	if !ok0 {
		return "", "", false
	}
	return before, after, true
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// headerKind returns the diagram kind from the header statement, lowercased.
func headerKind(statements []string) string {
	if len(statements) == 0 {
		return ""
	}
	return asciiLower(firstWord(statements[0]))
}

// parseClassDef parses the body of a `classDef` statement:
// `name[,name2] k1:v1,k2:v2`. Values are kept verbatim; malformed pairs are
// skipped.
func parseClassDef(rest string) (names []string, props map[string]string, ok bool) {
	body := trimJS(rest)
	ws := strings.IndexFunc(body, jsstring.IsSpace)
	if ws == -1 {
		return nil, nil, false
	}
	names = splitCommaList(body[:ws])
	props = map[string]string{}
	// splitTop keeps `rgb(255,0,0)` whole; a bare comma still separates pairs.
	for _, pair := range splitTop(trimJS(body[ws:]), func(c rune) bool { return c == ',' }) {
		k, v, ok := splitOnce(pair, ":")
		if !ok {
			continue
		}
		k, v = trimJS(k), trimJS(v)
		if k != "" && v != "" {
			props[k] = v
		}
	}
	if len(names) == 0 {
		return nil, nil, false
	}
	return names, props, true
}

// charAt returns chars[i] or 0 (mirrors JS out-of-range undefined in compares).
func charAt(chars []rune, i int) rune {
	if i < 0 || i >= len(chars) {
		return 0
	}
	return chars[i]
}

// runesEqualAt reports whether chars starting at i equals the ASCII token.
func runesEqualAt(chars []rune, i int, token string) bool {
	if i < 0 || i+len(token) > len(chars) {
		return false
	}
	for k := 0; k < len(token); k++ {
		if chars[i+k] != rune(token[k]) {
			return false
		}
	}
	return true
}

func lastOrNil(stack []int) *int {
	if len(stack) == 0 {
		return nil
	}
	v := stack[len(stack)-1]
	return &v
}

func itoa(n int) string { return strconv.Itoa(n) }

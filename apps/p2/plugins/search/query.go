package search

import (
	"regexp"
	"strings"
)

// Ports apps/p2/plugins/search/query.ts: a keyword query as an FTS5 MATCH expression, built here and never taken as raw FTS
// syntax (the history tools pass what a model wrote). Every term is required. `"…"` is a phrase; `word*` a prefix; a term with
// punctuation (`tx.draft`, `next/diff.ts`) becomes the phrase of its words (`"tx draft"`), as the index's tokenizer splits it.

// word is the characters that are part of a word for the index's tokenizer (unicode61): letters, digits, and the rest of a
// word.
var word = regexp.MustCompile(`[\p{L}\p{N}\p{M}_]+`)

// jsSpace is JavaScript's `\s`: `\S` below is its complement.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var terms = regexp.MustCompile(`"([^"]*)"|([^` + jsSpace + `]+)`)

// phrase is a quoted FTS5 string (a phrase of parts).
func phrase(parts []string) string {
	return `"` + strings.ReplaceAll(strings.Join(parts, " "), `"`, `""`) + `"`
}

// FTSQuery is the MATCH expression for query, "" when it has no searchable word.
func FTSQuery(query string) string {
	var out []string
	for _, match := range terms.FindAllStringSubmatchIndex(query, -1) {
		if match[2] >= 0 {
			if parts := word.FindAllString(query[match[2]:match[3]], -1); len(parts) > 0 {
				out = append(out, phrase(parts))
			}
			continue
		}
		bare := query[match[4]:match[5]]
		prefix := strings.HasSuffix(bare, "*")
		if prefix {
			bare = bare[:len(bare)-1]
		}
		parts := word.FindAllString(bare, -1)
		if len(parts) == 0 {
			continue
		}
		// A prefix applies to a single word; a punctuated term is a phrase (its last word may still be a prefix).
		if prefix {
			out = append(out, phrase(parts)+"*")
		} else {
			out = append(out, phrase(parts))
		}
	}
	return strings.Join(out, " AND ")
}

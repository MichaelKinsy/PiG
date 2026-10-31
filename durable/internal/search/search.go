// Ports packages/durable/src/search.ts.

// Package search ranks tools for code mode's searchTools() and the tool_search tool with BM25.
package search

import (
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsmath"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Document is a tool as the ranker sees it: its name, its text and the text of its namespace.
//
// pig divergence (D217): Pi c7372af4 (#11631) the name is a field of its own and the namespace text is separate; pinned Pi folds both into the text.
type Document struct {
	Name string
	// Text is the description and the parameters' names and descriptions, not the name.
	Text string
	// NamespaceText is the name, description and instructions of the tool's namespace.
	NamespaceText string
}

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true,
	"from": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "with": true,
}

var (
	lowerUpper     = lazyregexp.New(`([a-z0-9])([A-Z])`)
	upperRunUpper  = lazyregexp.New(`([A-Z]+)([A-Z][a-z])`)
	nonAlphanumRun = lazyregexp.New(`[^a-z0-9]+`)
	pluralEnding   = lazyregexp.New(`(ches|shes|sses|xes|zes)$`)
)

// stem is a naive singular form, so `issues` matches `issue` and `searches` matches `search`. Terms are ASCII.
func stem(term string) string {
	switch {
	case len(term) > 4 && strings.HasSuffix(term, "ies"): //portlint:allow utf16units tokenize splits at [^a-z0-9]+, so a term is ASCII
		return term[:len(term)-3] + "y"
	case len(term) > 4 && pluralEnding.MatchString(term): //portlint:allow utf16units tokenize splits at [^a-z0-9]+, so a term is ASCII
		return term[:len(term)-2]
	case len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss"): //portlint:allow utf16units a term is ASCII ([^a-z0-9]+ split)
		return term[:len(term)-1]
	}
	return term
}

// tokenize lowercases text into terms, splitting at camelCase boundaries and non-alphanumerics, without stop words.
func tokenize(text string) []string {
	text = lowerUpper.ReplaceAllString(text, "$1 $2")
	text = upperRunUpper.ReplaceAllString(text, "$1 $2")
	terms := []string{}
	for _, term := range nonAlphanumRun.Split(jsstring.ToLower(text), -1) {
		if term != "" && !stopWords[term] {
			terms = append(terms, stem(term))
		}
	}
	return terms
}

// normalizeName is search.ts normalizeName: lowercase with every UTF-16 code unit that is not a script identifier character as `_`, so `dev-radius`
// matches `dev_radius`.
func normalizeName(name string) string {
	var normalized strings.Builder
	for _, char := range jsstring.ToLower(jsstring.Trim(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_', char == '$':
			normalized.WriteRune(char)
		case char > 0xFFFF:
			// A character outside the BMP is two UTF-16 code units, each replaced.
			normalized.WriteString("__")
		default:
			normalized.WriteByte('_')
		}
	}
	return normalized.String()
}

// isExactNameQuery is search.ts isExactNameQuery: whether the whole query (already normalized) is the tool's name, by its full name or by a suffix after a `__` separator, so
// `thread_read` and `app__thread_read` both name `mcp__app__thread_read`.
func isExactNameQuery(query, name string) bool {
	normalized := normalizeName(name)
	return normalized == query || strings.HasSuffix(normalized, "__"+query)
}

// indexedDocument is a document split into the fields the ranker weighs differently.
type indexedDocument struct {
	name string
	// nameTerms are the terms of the name, for example `mcp`, `app`, `thread`, `read` for `mcp__app__thread_read`.
	nameTerms map[string]struct{}
	// localNameTerms are the terms of the name after its last `__`, the part models say: `thread`, `read`.
	localNameTerms []string
	textCounts     map[string]int
	textLength     float64
	namespaceTerms map[string]struct{}
}

func indexDocument(document Document) indexedDocument {
	indexed := indexedDocument{name: document.Name, nameTerms: map[string]struct{}{}, textCounts: map[string]int{}, namespaceTerms: map[string]struct{}{}}
	for _, term := range tokenize(document.Text) {
		indexed.textCounts[term]++
		indexed.textLength++
	}
	for _, term := range tokenize(document.Name) {
		indexed.nameTerms[term] = struct{}{}
	}
	segments := strings.Split(document.Name, "__")
	for _, term := range tokenize(segments[len(segments)-1]) {
		if !slices.Contains(indexed.localNameTerms, term) {
			indexed.localNameTerms = append(indexed.localNameTerms, term)
		}
	}
	for _, term := range tokenize(document.NamespaceText) {
		indexed.namespaceTerms[term] = struct{}{}
	}
	return indexed
}

const (
	k1 = 1.2
	b  = 0.75
	// nameWeight is how many times its IDF a query term in the name counts. The text part of a term is at most `k1 + 1` (2.2) times its IDF and about 1 for a single mention, so a name match beats any number of mentions in the text.
	nameWeight = 3
	// namespaceWeight is how many times its IDF a query term in the namespace text counts. Every tool of the namespace shares it, so it helps find the namespace but not the tool in it.
	namespaceWeight = 0.5
)

// RankDocuments is Okapi BM25 (k1 1.2, b 0.75) over the text, with the name and the namespace as separate fields: the names of the best matches, best first; ties keep document order.
//   - A tool that matches more of the query's terms, in any field, ranks above one that matches fewer, whatever their scores: for `screenshot window`, a tool whose description has both words beats `window`, whose name has one.
//   - A query term in the name scores more than any number of mentions in the text.
//   - A tool whose whole local name (after the last `__`) is in the query gets a bonus worth one more match of every query term, so among tools matching as many terms, `peekaboo app` finds `mcp__peekaboo__app` over tools that only mention the words.
//   - A query that is exactly a tool's name, or its name after a `__`, ranks that tool first, before coverage.
//
// Only the text is length-normalized, so a long name or long namespace instructions do not lower a tool's score.
//
// pig divergence (D217): Pi c7372af4 (#11631) pinned Pi scores the name, description and namespace as one bag of words and has no coverage-first order.
func RankDocuments(query string, documents []Document, limit int) []string {
	var queryTerms []string
	for _, term := range tokenize(query) {
		if !slices.Contains(queryTerms, term) {
			queryTerms = append(queryTerms, term)
		}
	}
	if len(queryTerms) == 0 || len(documents) == 0 || limit <= 0 {
		return []string{}
	}
	indexed := make([]indexedDocument, len(documents))
	var total float64
	for i, document := range documents {
		indexed[i] = indexDocument(document)
		total += indexed[i].textLength
	}
	averageLength := total / float64(len(documents))
	if averageLength == 0 {
		averageLength = 1
	}
	idf := make(map[string]float64, len(queryTerms))
	var fullNameBonus float64
	for _, term := range queryTerms {
		frequency := 0
		for _, document := range indexed {
			_, inName := document.nameTerms[term]
			_, inText := document.textCounts[term]
			_, inNamespace := document.namespaceTerms[term]
			if inName || inText || inNamespace {
				frequency++
			}
		}
		idf[term] = jsmath.Log(1 + (float64(len(documents)-frequency)+0.5)/(float64(frequency)+0.5))
		fullNameBonus += idf[term]
	}
	exactQuery := normalizeName(query)
	type match struct {
		name    string
		score   float64
		covered int
		exact   bool
	}
	var matches []match
	for _, document := range indexed {
		// float64() rounds the product, so the compiler cannot fuse it into the addition below as a multiply-add (arm64 FMADD); JavaScript rounds every operation.
		norm := float64(k1 * (1 - b + (b*document.textLength)/averageLength))
		var score float64
		covered := 0
		for _, term := range queryTerms {
			count := document.textCounts[term]
			var weight float64
			if _, ok := document.nameTerms[term]; ok {
				weight = nameWeight
			}
			if _, ok := document.namespaceTerms[term]; ok {
				weight += namespaceWeight
			}
			weight += (float64(count) * (k1 + 1)) / (float64(count) + norm)
			if weight > 0 {
				covered++
			}
			score += float64(idf[term] * weight)
		}
		if score == 0 {
			continue
		}
		if len(document.localNameTerms) > 0 && !slices.ContainsFunc(document.localNameTerms, func(term string) bool { return !slices.Contains(queryTerms, term) }) {
			score += fullNameBonus
		}
		matches = append(matches, match{name: document.name, score: score, covered: covered, exact: isExactNameQuery(exactQuery, document.name)})
	}
	sort.SliceStable(matches, func(a, c int) bool {
		if matches[a].exact != matches[c].exact {
			return matches[a].exact
		}
		if matches[a].covered != matches[c].covered {
			return matches[a].covered > matches[c].covered
		}
		return matches[a].score > matches[c].score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.name
	}
	return names
}

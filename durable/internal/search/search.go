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

// Document is a tool as the ranker sees it: its name and its text.
type Document struct{ Name, Text string }

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

// RankDocuments is Okapi BM25 (k1 1.2, b 0.75): the names of the best matches, best first; ties keep document order.
func RankDocuments(query string, documents []Document, limit int) []string {
	const k1, b = 1.2, 0.75
	var queryTerms []string
	for _, term := range tokenize(query) {
		if !slices.Contains(queryTerms, term) {
			queryTerms = append(queryTerms, term)
		}
	}
	if len(queryTerms) == 0 || len(documents) == 0 || limit <= 0 {
		return []string{}
	}
	termCounts := make([]map[string]int, len(documents))
	lengths := make([]float64, len(documents))
	var total float64
	for i, document := range documents {
		counts := map[string]int{}
		for _, term := range tokenize(document.Text) {
			counts[term]++
			lengths[i]++
		}
		termCounts[i] = counts
		total += lengths[i]
	}
	averageLength := total / float64(len(documents))
	if averageLength == 0 {
		averageLength = 1
	}
	idf := make(map[string]float64, len(queryTerms))
	for _, term := range queryTerms {
		frequency := 0
		for _, counts := range termCounts {
			if _, ok := counts[term]; ok {
				frequency++
			}
		}
		idf[term] = jsmath.Log(1 + (float64(len(documents)-frequency)+0.5)/(float64(frequency)+0.5))
	}
	type match struct {
		name  string
		score float64
	}
	var matches []match
	for i, document := range documents {
		var score float64
		for _, term := range queryTerms {
			count := termCounts[i][term]
			if count == 0 {
				continue
			}
			// float64() rounds the product, so the compiler cannot fuse it into the addition below as a multiply-add (arm64 FMADD); JavaScript rounds every operation.
			norm := float64(k1 * (1 - b + (b*lengths[i])/averageLength))
			score += float64(idf[term] * ((float64(count) * (k1 + 1)) / (float64(count) + norm)))
		}
		if score > 0 {
			matches = append(matches, match{document.Name, score})
		}
	}
	sort.SliceStable(matches, func(a, c int) bool { return matches[a].score > matches[c].score })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.name
	}
	return names
}

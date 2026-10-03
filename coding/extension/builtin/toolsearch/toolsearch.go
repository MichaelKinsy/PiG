// Package toolsearch is the built-in `tool_search` extension: tool discovery with a BM25 ranker over tool metadata,
// shared by `searchTools()` in codemode scripts and the optional `tool_search` tool.
//
// `tool_search` searches tools that are not declared to the model (`codemode` and `deferred` exposure) and loads the
// matches, so they are declared for the next model call. Loading goes through the active tool set, so it is recorded
// in the transcript like any other tool change.
//
// Ports packages/coding-agent/src/extensions/tool-search/tool.ts.
package toolsearch

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// ToolName is the name of the tool.
const ToolName = "tool_search"

// DefaultLimit is the number of tools a search returns unless the caller passes a limit.
const DefaultLimit = 8

// Document is a tool as the ranker sees it: its name and the text built by CreateDocument.
type Document struct {
	Name string
	Text string
}

// Match is one ranked tool.
type Match struct {
	Name  string
	Score float64
}

// Ranker ranks tools for a query. BM25 today; a hybrid ranker with embeddings can replace it.
type Ranker interface {
	Rank(query string, documents []Document, limit int) []Match
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
	case len(term) > 4 && strings.HasSuffix(term, "ies"):
		return term[:len(term)-3] + "y"
	case len(term) > 4 && pluralEnding.MatchString(term):
		return term[:len(term)-2]
	case len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss"):
		return term[:len(term)-1]
	}
	return term
}

// Tokenize lowercases text into terms, splitting at camelCase boundaries and non-alphanumerics, without stop words.
func Tokenize(text string) []string {
	text = lowerUpper.ReplaceAllString(text, "$1 $2")
	text = upperRunUpper.ReplaceAllString(text, "$1 $2")
	terms := []string{}
	for _, term := range nonAlphanumRun.Split(strings.ToLower(text), -1) {
		if term != "" && !stopWords[term] {
			terms = append(terms, stem(term))
		}
	}
	return terms
}

// schemaText collects schema descriptions and property names, recursively.
func schemaText(schema any, parts *[]string) {
	object, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if description, ok := object["description"].(string); ok {
		*parts = append(*parts, description)
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		for _, name := range slices.Sorted(mapsKeys(properties)) {
			*parts = append(*parts, name)
			schemaText(properties[name], parts)
		}
	}
	schemaText(object["items"], parts)
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := object[key].([]any); ok {
			for _, variant := range variants {
				schemaText(variant, parts)
			}
		}
	}
}

func mapsKeys(m map[string]any) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// CreateDocument builds the search text of a tool: the name, the name with `_` as spaces, the description, schema
// descriptions and property names, and the namespace with its description and instructions. BM25 counts terms, so the order of the parts does not matter.
func CreateDocument(tool extension.ToolInfo, namespace *extension.ToolNamespace) Document {
	parts := []string{tool.Name, strings.ReplaceAll(tool.Name, "_", " "), tool.Description}
	var parameters any
	if len(tool.Parameters) > 0 && json.Unmarshal(tool.Parameters, &parameters) == nil {
		schemaText(parameters, &parts)
	}
	if namespace != nil {
		parts = append(parts, namespace.Name, namespace.Description, namespace.Instructions)
	}
	kept := parts[:0]
	for _, part := range parts {
		if jsstring.Trim(part) != "" {
			kept = append(kept, part)
		}
	}
	return Document{Name: tool.Name, Text: strings.Join(kept, " ")}
}

// Bm25Ranker is Okapi BM25 with the usual parameters. Ties keep document order.
type Bm25Ranker struct{ K1, B float64 }

// NewBm25Ranker returns a ranker with k1 1.2 and b 0.75.
func NewBm25Ranker() *Bm25Ranker { return &Bm25Ranker{K1: 1.2, B: 0.75} }

// LimitOf converts a validated positive integer limit to an int. JavaScript's slice(0, limit) accepts any number, so a
// limit beyond the int range keeps every match instead of wrapping around to a negative int.
func LimitOf(limit float64) int { return int(min(limit, math.MaxInt32)) }

// Rank returns at most limit matches with a positive score, best first.
func (r *Bm25Ranker) Rank(query string, documents []Document, limit int) []Match {
	var queryTerms []string
	for _, term := range Tokenize(query) {
		if !slices.Contains(queryTerms, term) {
			queryTerms = append(queryTerms, term)
		}
	}
	if len(queryTerms) == 0 || len(documents) == 0 || limit <= 0 {
		return []Match{}
	}
	termCounts := make([]map[string]int, len(documents))
	lengths := make([]float64, len(documents))
	var total float64
	for i, document := range documents {
		counts := map[string]int{}
		for _, term := range Tokenize(document.Text) {
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
		idf[term] = math.Log(1 + (float64(len(documents)-frequency)+0.5)/(float64(frequency)+0.5))
	}
	matches := []Match{}
	for i, document := range documents {
		var score float64
		for _, term := range queryTerms {
			count := termCounts[i][term]
			if count == 0 {
				continue
			}
			norm := r.K1 * (1 - r.B + (r.B*lengths[i])/averageLength)
			score += idf[term] * ((float64(count) * (r.K1 + 1)) / (float64(count) + norm))
		}
		if score > 0 {
			matches = append(matches, Match{Name: document.Name, Score: score})
		}
	}
	sort.SliceStable(matches, func(a, b int) bool { return matches[a].Score > matches[b].Score })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// ToolSearchDescription is the `tool_search` description. It does not list the searchable tools or their namespaces, so
// it stays the same while tools are registered, for example when MCP servers connect.
//
// Ports packages/coding-agent/src/extensions/tool-search/tool.ts (TOOL_SEARCH_DESCRIPTION).
const ToolSearchDescription = "# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (`" + ToolName + "`) to search for the required tools. For MCP tool discovery, always use `" + ToolName + "`."

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
	"github.com/MichaelKinsy/PiG/internal/jsmath"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// ToolName is the name of the tool.
const ToolName = "tool_search"

// DefaultLimit is the number of tools a search returns unless the caller passes a limit.
const DefaultLimit = 8

// Document is a tool as the ranker sees it: its name and the texts built by CreateDocument.
//
// pig divergence (D217): Pi cc44fa21 (#11331) the name is a field of its own and the namespace text is separate; pinned Pi folds both into Text.
type Document struct {
	Name string
	// Text is the description, schema descriptions and property names.
	Text string
	// NamespaceText is the name, description and instructions of the tool's namespace.
	NamespaceText string
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
	lowerUpper    = lazyregexp.New(`([a-z0-9])([A-Z])`)
	upperRunUpper = lazyregexp.New(`([A-Z]+)([A-Z][a-z])`)
	// termRun is a run of ASCII letters and digits or of CJK-script characters: the characters of the scripts that write no spaces between words.
	termRun      = lazyregexp.New(`[a-z0-9]+|[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]`)
	pluralEnding = lazyregexp.New(`(ches|shes|sses|xes|zes)$`)
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
// pig divergence (D173): Pi's tokenizer splits on /[^a-z0-9]+/ and so drops every Chinese, Japanese and Korean character (Pi #10816); PiG makes each of those characters a term of its own, so a query in those scripts matches a tool whose metadata holds the same characters. Text without such characters tokenizes as in Pi.
func Tokenize(text string) []string {
	text = lowerUpper.ReplaceAllString(text, "$1 $2")
	text = upperRunUpper.ReplaceAllString(text, "$1 $2")
	terms := []string{}
	for _, term := range termRun.FindAllString(jsstring.ToLower(text), -1) {
		if !stopWords[term] {
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

// CreateDocument builds the search texts of a tool: the description with the schema descriptions and property names, and separately the namespace name, description and instructions. The ranker reads the name from Name. BM25 counts terms, so the order of the parts does not matter.
//
// pig divergence (D217): Pi cc44fa21 (#11331) pinned Pi puts the name, the name with `_` as spaces and the namespace into the one text.
func CreateDocument(tool extension.ToolInfo, namespace *extension.ToolNamespace) Document {
	parts := []string{tool.Description}
	var parameters any
	if len(tool.Parameters) > 0 && json.Unmarshal(tool.Parameters, &parameters) == nil {
		schemaText(parameters, &parts)
	}
	var namespaceText string
	if namespace != nil {
		namespaceText = strings.Join([]string{namespace.Name, namespace.Description, namespace.Instructions}, " ")
	}
	kept := parts[:0]
	for _, part := range parts {
		if jsstring.Trim(part) != "" {
			kept = append(kept, part)
		}
	}
	return Document{Name: tool.Name, Text: strings.Join(kept, " "), NamespaceText: namespaceText}
}

// normalizeName is tool.ts normalizeName: lowercase with every UTF-16 code unit that is not a script identifier character as `_`, so `dev-radius`
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

// isExactNameQuery is tool.ts isExactNameQuery: whether the whole query (already normalized) is the tool's name, by its full name or by a suffix after a `__` separator, so
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
	// localNameTerms are the terms of the last `__` segment of the name, the part models say: `thread`, `read`.
	localNameTerms []string
	textCounts     map[string]int
	textLength     float64
	namespaceTerms map[string]struct{}
}

func indexDocument(document Document) indexedDocument {
	indexed := indexedDocument{name: document.Name, nameTerms: map[string]struct{}{}, textCounts: map[string]int{}, namespaceTerms: map[string]struct{}{}}
	for _, term := range Tokenize(document.Text) {
		indexed.textCounts[term]++
		indexed.textLength++
	}
	for _, term := range Tokenize(document.Name) {
		indexed.nameTerms[term] = struct{}{}
	}
	segments := strings.Split(document.Name, "__")
	for _, term := range Tokenize(segments[len(segments)-1]) {
		if !slices.Contains(indexed.localNameTerms, term) {
			indexed.localNameTerms = append(indexed.localNameTerms, term)
		}
	}
	for _, term := range Tokenize(document.NamespaceText) {
		indexed.namespaceTerms[term] = struct{}{}
	}
	return indexed
}

// nameWeight is how many times its IDF a query term in the name counts. The text part of a term is at most `k1 + 1` (2.2) times its IDF and about 1 for a single mention, so a name match beats any number of mentions in the description.
const nameWeight = 3

// namespaceWeight is how many times its IDF a query term in the namespace name, description or instructions counts. They are shared by every tool of the server, so they help find the server but not the tool in it.
const namespaceWeight = 0.5

// Bm25Ranker is Okapi BM25 over the description, with the name and the namespace as separate fields:
//   - a query term in the name scores more than any number of mentions in the description;
//   - a tool whose whole local name is in the query (`app` in `peekaboo app` for `mcp__peekaboo__app`) gets a bonus worth one more match of every query term, so it mostly wins over tools that only mention the words;
//   - a query that is exactly a tool's name ranks that tool first.
//
// The description alone is length-normalized, so a long name or long namespace instructions do not lower a tool's score. Ties keep document order.
//
// pig divergence (D217): Pi cc44fa21 (#11331) pinned Pi scores the name, description and namespace as one bag of words.
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
	type rankedMatch struct {
		Match
		exact bool
	}
	ranked := []rankedMatch{}
	for _, document := range indexed {
		// float64() rounds the product, so the compiler cannot fuse it into the addition below as a multiply-add (arm64 FMADD); JavaScript rounds every operation.
		norm := float64(r.K1 * (1 - r.B + (r.B*document.textLength)/averageLength))
		var score float64
		for _, term := range queryTerms {
			count := document.textCounts[term]
			var weight float64
			if _, ok := document.nameTerms[term]; ok {
				weight = nameWeight
			}
			if _, ok := document.namespaceTerms[term]; ok {
				weight += namespaceWeight
			}
			weight += (float64(count) * (r.K1 + 1)) / (float64(count) + norm)
			score += float64(idf[term] * weight)
		}
		if score == 0 {
			continue
		}
		if len(document.localNameTerms) > 0 && !slices.ContainsFunc(document.localNameTerms, func(term string) bool { return !slices.Contains(queryTerms, term) }) {
			score += fullNameBonus
		}
		ranked = append(ranked, rankedMatch{Match: Match{Name: document.name, Score: score}, exact: isExactNameQuery(exactQuery, document.name)})
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].exact != ranked[b].exact {
			return ranked[a].exact
		}
		return ranked[a].Score > ranked[b].Score
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	matches := make([]Match, len(ranked))
	for i, match := range ranked {
		matches[i] = match.Match
	}
	return matches
}

// ToolSearchDescription is the `tool_search` description. It does not list the searchable tools or their namespaces, so
// it stays the same while tools are registered, for example when MCP servers connect.
//
// Ports packages/coding-agent/src/extensions/tool-search/tool.ts (TOOL_SEARCH_DESCRIPTION).
const ToolSearchDescription = "# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (`" + ToolName + "`) to search for the required tools. For MCP tool discovery, always use `" + ToolName + "`."

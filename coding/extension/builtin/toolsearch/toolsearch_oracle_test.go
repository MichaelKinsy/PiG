package toolsearch

import (
	"encoding/json"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// tokenize, createToolSearchDocument and Bm25Ranker.rank against Pi's tool-search/tool.js over random text (camelCase runs, digits, plural endings, stop words,
// JS-only case mappings such as U+0130, non-ASCII letters, whitespace kinds), tools with nested schemas and namespaces, and random queries and limits. Names, their order
// and the scores must match exactly: the idf goes through jsmath.Log, V8's fdlibm Math.log.
func TestToolSearchMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	words := []string{"issue", "issues", "search", "searches", "boxes", "class", "classes", "status", "the", "and", "of", "GitHub", "getHTTPResponse", "XMLParser", "list_issues", "mcp__server__tool", "A1b2", "ABc", "aBC", "ies", "ries", "dies", "ches", "wishes", "as", "us", "\u0130stanbul", "\u0130x", "caf\u00e9", "\u212a", "日本語", "tab\tsep", "line\nbreak", "\u00a0nbsp", "\ufeffbom", "x-y.z", "UPPER", "lower", "Mixed3Case", "FILEName", "id", "URLs", "Pass", "boss", "gas"}
	text := func(n int) string {
		var parts []string
		for range n {
			parts = append(parts, words[rng.Intn(len(words))])
		}
		return strings.Join(parts, []string{" ", "", "_", " - "}[rng.Intn(4)])
	}
	type schema map[string]any
	var makeSchema func(depth int) schema
	makeSchema = func(depth int) schema {
		s := schema{}
		if rng.Intn(2) == 0 {
			s["description"] = text(1 + rng.Intn(3))
		}
		if depth > 0 {
			if rng.Intn(2) == 0 {
				props := map[string]any{}
				for range 1 + rng.Intn(3) {
					props[text(1)] = makeSchema(depth - 1)
				}
				s["properties"] = props
			}
			if rng.Intn(4) == 0 {
				s["items"] = makeSchema(depth - 1)
			}
			for _, key := range []string{"anyOf", "oneOf", "allOf"} {
				if rng.Intn(5) == 0 {
					s[key] = []any{makeSchema(depth - 1), makeSchema(depth - 1)}
				}
			}
		}
		return s
	}
	type tool struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  schema         `json:"parameters"`
		Namespace   map[string]any `json:"namespace,omitempty"`
	}
	type probe struct {
		Tools []tool   `json:"tools"`
		Query string   `json:"query"`
		Limit int      `json:"limit"`
		Texts []string `json:"texts"`
	}
	var probes []probe
	for range 400 {
		var p probe
		for range 1 + rng.Intn(12) {
			tl := tool{Name: text(1), Description: text(rng.Intn(6)), Parameters: makeSchema(2)}
			if rng.Intn(3) == 0 {
				ns := map[string]any{"name": text(1)}
				if rng.Intn(2) == 0 {
					ns["description"] = text(2)
				}
				if rng.Intn(2) == 0 {
					ns["instructions"] = text(3)
				}
				tl.Namespace = ns
			}
			p.Tools = append(p.Tools, tl)
		}
		p.Query = text(rng.Intn(5))
		p.Limit = []int{-1, 0, 1, 2, 3, 8, 50}[rng.Intn(7)]
		for range 3 {
			p.Texts = append(p.Texts, text(rng.Intn(6)))
		}
		probes = append(probes, spaceOutCJK(t, p))
	}
	type matchOut struct {
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	}
	type result struct {
		Docs   []map[string]string `json:"docs"`
		Ranked []matchOut          `json:"ranked"`
		Tokens [][]string          `json:"tokens"`
	}
	var want []result
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/tool-search/tool.js");
emit(input.map((p) => {
	const docs = p.tools.map((t) => m.createToolSearchDocument({ name: t.name, description: t.description, parameters: t.parameters }, t.namespace ?? undefined));
	return {
		docs,
		ranked: new m.Bm25Ranker().rank(p.query, docs, p.limit),
		tokens: p.texts.concat([p.query]).map((x) => m.tokenize(x)),
	};
}));
`, probes, &want)
	bad := 0
	for i, p := range probes {
		w := want[i]
		var docs []Document
		for j, tl := range p.Tools {
			raw, _ := json.Marshal(tl.Parameters)
			info := extension.ToolInfo{Name: tl.Name, Description: tl.Description, Parameters: raw}
			if tl.Namespace != nil {
				ns := &extension.ToolNamespace{Name: tl.Namespace["name"].(string)}
				ns.Description, _ = tl.Namespace["description"].(string)
				ns.Instructions, _ = tl.Namespace["instructions"].(string)
				info.Namespace = ns
			}
			doc := CreateDocument(info, info.Namespace)
			docs = append(docs, doc)
			// Property order differs by design (BM25 counts terms); compare the token multiset.
			if !sameTerms(Tokenize(doc.Text), Tokenize(w.Docs[j]["text"])) || doc.Name != w.Docs[j]["name"] {
				if bad++; bad <= 8 {
					t.Errorf("probe %d doc %d: got %q, want %q", i, j, doc.Text, w.Docs[j]["text"])
				}
			}
		}
		ranked := NewBm25Ranker().Rank(p.Query, docs, p.Limit)
		if len(ranked) != len(w.Ranked) {
			if bad++; bad <= 8 {
				t.Errorf("probe %d rank %q limit %d: got %v, want %v", i, p.Query, p.Limit, ranked, w.Ranked)
			}
		} else {
			for k := range ranked {
				if ranked[k].Name != w.Ranked[k].Name || ranked[k].Score != w.Ranked[k].Score {
					if bad++; bad <= 8 {
						t.Errorf("probe %d rank %q limit %d: got %v, want %v", i, p.Query, p.Limit, ranked, w.Ranked)
					}
					break
				}
			}
		}
		for k, x := range append(append([]string{}, p.Texts...), p.Query) {
			if got := Tokenize(x); strings.Join(got, "\x00") != strings.Join(w.Tokens[k], "\x00") {
				if bad++; bad <= 8 {
					t.Errorf("tokenize(%q) = %q, want %q", x, got, w.Tokens[k])
				}
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d mismatches", bad)
	}
}

var cjkCharacter = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]`)

// spaceOutCJK replaces every Chinese, Japanese and Korean character of a probe with a space before both sides see it. Pi 1.1.0 drops those characters as it splits
// a term, which is a space; PiG makes each one a term of its own (Pi #10816, D173), so the probes are compared where the two agree.
func spaceOutCJK[T any](t *testing.T, probe T) T {
	t.Helper()
	raw, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(cjkCharacter.ReplaceAll(raw, []byte(" ")), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTerms(a, b []string) bool {
	counts := map[string]int{}
	for _, x := range a {
		counts[x]++
	}
	for _, x := range b {
		counts[x]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// A query that names a tool ranks it first, as Pi's isNameQuery decides it: the full normalized name or a suffix after `__`. Pi's tool.js runs over tools whose names
// hold separators, upper case, the Kelvin sign and U+0130 (JS lowercases them before the replacement), a character outside the BMP (two UTF-16 code units, each
// replaced) and surrounding white space, with the queries that name them in every form, partial names and non-names; names, order and scores must match.
func TestToolSearchNameQueriesMatchPi(t *testing.T) {
	names := []string{
		"mcp__app__queue_read", "mcp__dev-app__thread_read", "thread_read", "app__thread_read", "mcp__app__THREAD_READ", "mcp__a.b__read thread", "\u212aelvin_read",
		"\u0130stanbul_read", "x\U0001F600y_read", "mcp__$dollar__read", "read", "mcp__x__read", "mcp__x____read",
	}
	queries := []string{
		"thread_read", "Thread_Read", "thread read", "dev_app__thread_read", "mcp__dev-app__thread_read", "app__thread_read", " thread_read ", "THREAD-READ", "read", "mcp__x__read",
		"x__read", "__read", "kelvin_read", "\u212aelvin_read", "i_stanbul_read", "\u0130stanbul_read", "x__y_read", "x\U0001F600y_read", "$dollar__read", "mcp__a_b__read_thread", "a.b__read thread", "mcp__app__queue_read",
	}
	type probe struct {
		Names []string `json:"names"`
		Query string   `json:"query"`
		Limit int      `json:"limit"`
	}
	var probes []probe
	for _, query := range queries {
		for _, limit := range []int{1, 3, 50} {
			probes = append(probes, probe{Names: names, Query: query, Limit: limit})
		}
	}
	type matchOut struct {
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	}
	var want [][]matchOut
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/tool-search/tool.js");
emit(input.map((p) => {
	const docs = p.names.map((name, i) => m.createToolSearchDocument({ name, description: i % 2 === 0 ? "Read a thread message." : "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans.", parameters: { type: "object", properties: {} } }));
	return new m.Bm25Ranker().rank(p.query, docs, p.limit);
}));
`, probes, &want)
	for i, p := range probes {
		var docs []Document
		for j, name := range p.Names {
			description := "Read a thread message."
			if j%2 == 1 {
				description = "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans."
			}
			docs = append(docs, CreateDocument(extension.ToolInfo{Name: name, Description: description, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}, nil))
		}
		got := NewBm25Ranker().Rank(p.Query, docs, p.Limit)
		if len(got) != len(want[i]) {
			t.Errorf("query %q limit %d: got %v, want %v", p.Query, p.Limit, got, want[i])
			continue
		}
		for k := range got {
			if got[k].Name != want[i][k].Name || got[k].Score != want[i][k].Score {
				t.Errorf("query %q limit %d: got %v, want %v", p.Query, p.Limit, got, want[i])
				break
			}
		}
	}
}

package toolsearch

import (
	"encoding/json"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// tokenize, createToolSearchDocument and Bm25Ranker.rank against Pi's tool-search/tool.js over random text (camelCase runs, digits, plural endings, stop words,
// JS-only case mappings such as U+0130, non-ASCII letters, whitespace kinds), tools with nested schemas, and random queries and limits. Names, their order
// and the scores must match exactly: the idf goes through jsmath.Log, V8's fdlibm Math.log.
//
// D217 (Pi cc44fa21, #11331): the pinned Pi scores the name and the namespace as part of the one text, PiG weighs them as fields of their own, so the two rank a tool
// with a name term or a namespace differently. The probes are the inputs the two agree on: tools whose names hold only stop words (no name term) and no namespace.
// TestBm25RankerMatchesPiCC44fa21 pins the carried ranking with Pi's own numbers.
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
			tl := tool{Name: stopWordNames[rng.Intn(len(stopWordNames))], Description: text(rng.Intn(6)), Parameters: makeSchema(2)}
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
		// D217 (Pi e033f255, #11632): PiG ranks a tool matching more of the query's distinct terms first, the pinned Pi by score alone. The two agree on the order for a
		// query of one term (every match covers it) and on the scores of every match; a many-term query is compared as the multiset of scores, where the limit keeps them all.
		distinct := map[string]bool{}
		for _, term := range Tokenize(p.Query) {
			distinct[term] = true
		}
		mismatch := len(ranked) != len(w.Ranked)
		if !mismatch && len(distinct) <= 1 {
			for k := range ranked {
				mismatch = mismatch || ranked[k].Name != w.Ranked[k].Name || ranked[k].Score != w.Ranked[k].Score
			}
		} else if !mismatch && len(w.Ranked) < p.Limit {
			gotScores, wantScores := make([]float64, len(ranked)), make([]float64, len(ranked))
			for k := range ranked {
				gotScores[k], wantScores[k] = ranked[k].Score, w.Ranked[k].Score
			}
			slices.Sort(gotScores)
			slices.Sort(wantScores)
			mismatch = !slices.Equal(gotScores, wantScores)
		}
		if mismatch {
			if bad++; bad <= 8 {
				t.Errorf("probe %d rank %q limit %d: got %v, want %v", i, p.Query, p.Limit, ranked, w.Ranked)
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
// replaced) and surrounding white space, with the queries that name them in every form, partial names and non-names.
//
// D217 (Pi cc44fa21, #11331): the scores and the order inside the tools that do not name the query differ from the pinned Pi, which scores the name as part of the text.
// What the two agree on is the name rule: how many tools lead the result as exact names, which ones when all of them fit the limit, and which tools match at all.
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
		exactQuery := normalizeName(p.Query)
		leading := func(count int, name func(int) string) (int, []string) {
			n := 0
			for n < count && isExactNameQuery(exactQuery, name(n)) {
				n++
			}
			lead := make([]string, n)
			for k := range lead {
				lead[k] = name(k)
			}
			slices.Sort(lead)
			return n, lead
		}
		gotLead, gotNames := leading(len(got), func(k int) string { return got[k].Name })
		wantLead, wantNames := leading(len(want[i]), func(k int) string { return want[i][k].Name })
		if gotLead != wantLead || (wantLead < p.Limit && !slices.Equal(gotNames, wantNames)) {
			t.Errorf("query %q limit %d: exact names lead %v, want %v", p.Query, p.Limit, gotNames, wantNames)
		}
		if p.Limit == 50 {
			gotAll, wantAll := make([]string, len(got)), make([]string, len(got))
			for k := range got {
				gotAll[k], wantAll[k] = got[k].Name, want[i][k].Name
			}
			slices.Sort(gotAll)
			slices.Sort(wantAll)
			if !slices.Equal(gotAll, wantAll) {
				t.Errorf("query %q: matches %v, want %v", p.Query, gotAll, wantAll)
			}
		}
	}
}

// stopWordNames are tool names with no term: every part is a stop word.
var stopWordNames = []string{"the", "and", "of", "as", "a", "the_and", "of__the", "an", "to", "A", "The", "of_the__and"}

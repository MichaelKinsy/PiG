package toolsearch_test

// pi: packages/coding-agent/src/extensions/tool-search/tool.ts

import (
	_ "embed"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// Ports tool-search.test.ts "ranks a name match above mentions in descriptions" (Pi cc44fa21, #11331).
func TestBm25RankerRanksANameMatchAboveMentionsInDescriptions(t *testing.T) {
	documents := []toolsearch.Document{
		toolsearch.CreateDocument(info("mcp__ci__status", "Deploy status: last deploy, deploy log, deploy errors.", ""), nil),
		toolsearch.CreateDocument(info("mcp__ci__deploy_release", "Start a release of the current branch to an environment, run the migrations, switch traffic over, and wait until the new version is live and healthy.", ""), nil),
	}
	if got, want := names(toolsearch.NewBm25Ranker().Rank("deploy", documents, 1)), []string{"mcp__ci__deploy_release"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deploy: %q, want %q", got, want)
	}
}

// Ports tool-search.test.ts "ranks a tool first when the query contains its whole name" (Pi cc44fa21, #11331).
func TestBm25RankerRanksAToolFirstWhenTheQueryContainsItsWholeName(t *testing.T) {
	peekaboo := &extension.ToolNamespace{Name: "mcp__peekaboo", Instructions: "Peekaboo controls macOS apps by app name."}
	tools := []extension.ToolInfo{
		info("mcp__peekaboo__see", "Capture the app window of the peekaboo app and describe the app.", ""),
		info("mcp__peekaboo__menu", "Click a menu item of an app.", ""),
		info("mcp__peekaboo__app", "Launch, quit, relaunch, focus, hide, or switch applications. Targets an application by name, bundle identifier, or process ID, and can wait until it is ready.", ""),
		info("mcp__app__queue_read", "Read a queued message of this thread.", ""),
		info("mcp__dev-app__thread_read", "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans.", ""),
	}
	documents := make([]toolsearch.Document, len(tools))
	for i, tool := range tools {
		var namespace *extension.ToolNamespace
		if len(tool.Name) > len("mcp__peekaboo__") && tool.Name[:len("mcp__peekaboo__")] == "mcp__peekaboo__" {
			namespace = peekaboo
		}
		documents[i] = toolsearch.CreateDocument(tool, namespace)
	}
	ranker := toolsearch.NewBm25Ranker()
	for query, want := range map[string]string{
		"peekaboo app":               "mcp__peekaboo__app",
		"open the peekaboo app tool": "mcp__peekaboo__app",
		"read thread_read state":     "mcp__dev-app__thread_read",
		// Without the whole name, mentions in descriptions decide.
		"describe window": "mcp__peekaboo__see",
	} {
		if got := names(ranker.Rank(query, documents, 1)); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%q: %q, want %q", query, got, want)
		}
	}
}

// Ports tool-search.test.ts "ranks a tool matching more query terms first, whatever the scores" (Pi e033f255, #11632).
func TestBm25RankerRanksAToolMatchingMoreQueryTermsFirst(t *testing.T) {
	tools := []extension.ToolInfo{
		info("mcp__peekaboo__app", "Launch, quit, focus, or switch applications by name or bundle identifier.", ""),
		info("mcp__peekaboo__see", "Capture the app window and describe the UI elements of the app.", ""),
		info("mcp__peekaboo__window", "Move, resize, or close a window of an app.", ""),
		info("mcp__peekaboo__image", "Take a screenshot of the screen, an app, or a window.", ""),
	}
	documents := make([]toolsearch.Document, len(tools))
	for i, tool := range tools {
		documents[i] = toolsearch.CreateDocument(tool, nil)
	}
	ranker := toolsearch.NewBm25Ranker()
	for query, want := range map[string]string{
		// `window` and `app` are whole names, but each covers one of the two terms.
		"screenshot window": "mcp__peekaboo__image",
		"describe app":      "mcp__peekaboo__see",
		// An exact name still ranks first.
		"window": "mcp__peekaboo__window",
	} {
		if got := names(ranker.Rank(query, documents, 1)); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%q: %q, want %q", query, got, want)
		}
	}
}

//go:embed testdata/ranker_goldens.json
var rankerGoldens []byte

// maxScoreULPs is how many units in the last place a score may be from Pi's golden. V8's Math.log, which gives the IDF, differs between platforms by up to one unit in the last place: Node's
// arm64 build contracts the multiply-adds of fdlibm's log, its x64 build rounds every operation, and jsmath.Log reproduces each (it equals the Node of the same platform bit for bit,
// 0 differences over 20,000 random rankings on arm64 and on amd64). The goldens are Node on linux/amd64. A score is a sum of at most a handful of IDFs times positive weights, so a one-unit difference in each IDF
// moves it by at most the same relative amount plus the rounding of the few additions it goes through: 2 units over 78,000 scores of random rankings (433 differ, none by more), and 4 leaves room for a
// longer query. The same inputs ranked on both platforms by Node itself never changed an order. Scores inside the bound are interchangeable, so tools whose goldens lie within it may swap places.
const maxScoreULPs = 4

func scoreULPs(a, b float64) uint64 {
	x, y := math.Float64bits(a), math.Float64bits(b)
	if x > y {
		return x - y
	}
	return y - x
}

// sameRanking reports whether got is Pi's ranking up to maxScoreULPs: every score within the bound of the golden of the same name, and the names in the golden's order except between goldens that are
// within the bound of each other.
func sameRanking(got []toolsearch.Match, want []goldenMatch) bool {
	if len(got) != len(want) {
		return false
	}
	for k := range got {
		if got[k].Name == want[k].Name {
			if scoreULPs(got[k].Score, want[k].Score) > maxScoreULPs {
				return false
			}
			continue
		}
		swapped := slices.IndexFunc(want, func(m goldenMatch) bool { return m.Name == got[k].Name })
		if swapped < 0 || scoreULPs(want[k].Score, want[swapped].Score) > maxScoreULPs || scoreULPs(got[k].Score, want[swapped].Score) > maxScoreULPs {
			return false
		}
	}
	return true
}

type goldenMatch struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// The goldens are Pi's own ranking at 79006f06, the newest carried commit of tool.ts (testdata/gen_ranker_goldens.mjs, run with Node on linux/amd64) over random tool sets with namespaces, names that share words with the queries, words of other scripts and
// limits: names and order must match, and scores within maxScoreULPs. The pinned Pi's oracle (toolsearch_oracle_test.go) cannot supply them: it scores the name and the namespace as part
// of the text.
func TestBm25RankerMatchesPiTheLatestCarriedRanker(t *testing.T) {
	var probes []struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Namespace   *struct {
				Name         string `json:"name"`
				Description  string `json:"description"`
				Instructions string `json:"instructions"`
			} `json:"namespace"`
		} `json:"tools"`
		Query  string        `json:"query"`
		Limit  int           `json:"limit"`
		Ranked []goldenMatch `json:"ranked"`
	}
	if err := json.Unmarshal(rankerGoldens, &probes); err != nil {
		t.Fatal(err)
	}
	ranked := 0
	for i, probe := range probes {
		// A Binary without the word dictionaries (D92) keeps dictionary scripts whole, so a probe that holds one is outside what Pi's answer pins there.
		if stripsDictionaries() {
			words := probe.Query
			for _, tool := range probe.Tools {
				words += tool.Name + tool.Description + string(tool.Parameters)
				if tool.Namespace != nil {
					words += tool.Namespace.Name + tool.Namespace.Description + tool.Namespace.Instructions
				}
			}
			if usesDictionary(words) {
				continue
			}
		}
		var documents []toolsearch.Document
		for _, tool := range probe.Tools {
			var namespace *extension.ToolNamespace
			if tool.Namespace != nil {
				namespace = &extension.ToolNamespace{Name: tool.Namespace.Name, Description: tool.Namespace.Description, Instructions: tool.Namespace.Instructions}
			}
			documents = append(documents, toolsearch.CreateDocument(extension.ToolInfo{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}, namespace))
		}
		got := toolsearch.NewBm25Ranker().Rank(probe.Query, documents, probe.Limit)
		ranked += len(got)
		if !sameRanking(got, probe.Ranked) {
			t.Errorf("probe %d %q limit %d: got %v, want %v", i, probe.Query, probe.Limit, got, probe.Ranked)
		}
	}
	if ranked < 100 {
		t.Fatalf("only %d matches over %d probes: the goldens do not exercise the ranker", ranked, len(probes))
	}
}

// The comparison is strict outside the bound: a score further than maxScoreULPs from its golden fails, a tool may take another's place only when their goldens are within the bound, and no
// tool may appear or vanish.
func TestSameRankingKeepsTheOrderStrictOutsideTheScoreBound(t *testing.T) {
	step := func(score float64, ulps int) float64 {
		for range ulps {
			score = math.Nextafter(score, math.Inf(1))
		}
		return score
	}
	want := []goldenMatch{{"a", 3}, {"b", step(3, 1)}, {"c", 2}}
	for _, test := range []struct {
		name string
		got  []toolsearch.Match
		ok   bool
	}{
		{"exact", []toolsearch.Match{{Name: "a", Score: 3}, {Name: "b", Score: step(3, 1)}, {Name: "c", Score: 2}}, true},
		{"scores at the bound", []toolsearch.Match{{Name: "a", Score: step(3, maxScoreULPs)}, {Name: "b", Score: step(3, 1)}, {Name: "c", Score: 2}}, true},
		{"a score past the bound", []toolsearch.Match{{Name: "a", Score: step(3, maxScoreULPs+1)}, {Name: "b", Score: step(3, 1)}, {Name: "c", Score: 2}}, false},
		{"near-tied tools swapped", []toolsearch.Match{{Name: "b", Score: step(3, 1)}, {Name: "a", Score: 3}, {Name: "c", Score: 2}}, true},
		{"tools swapped past the bound", []toolsearch.Match{{Name: "a", Score: 3}, {Name: "c", Score: 2}, {Name: "b", Score: step(3, 1)}}, false},
		{"a tool replaced", []toolsearch.Match{{Name: "a", Score: 3}, {Name: "b", Score: step(3, 1)}, {Name: "d", Score: 2}}, false},
		{"a tool missing", []toolsearch.Match{{Name: "a", Score: 3}, {Name: "b", Score: step(3, 1)}}, false},
	} {
		if got := sameRanking(test.got, want); got != test.ok {
			t.Errorf("%s: sameRanking = %v, want %v", test.name, got, test.ok)
		}
	}
}

package toolsearch_test

// pi: packages/coding-agent/src/extensions/tool-search/tool.ts

import (
	_ "embed"
	"encoding/json"
	"reflect"
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
		"describe app":               "mcp__peekaboo__app",
		"read thread_read state":     "mcp__dev-app__thread_read",
		// Without the whole name, mentions in descriptions decide.
		"describe window": "mcp__peekaboo__see",
	} {
		if got := names(ranker.Rank(query, documents, 1)); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%q: %q, want %q", query, got, want)
		}
	}
}

//go:embed testdata/cc44fa21_goldens.json
var cc44fa21Goldens []byte

// The goldens are Pi's own ranking at cc44fa21 (testdata/gen_cc44fa21_goldens.mjs) over random tool sets with namespaces, names that share words with the queries and
// limits: names, order and scores must match exactly. The pinned Pi's oracle (toolsearch_oracle_test.go) cannot supply them: it scores the name and the namespace as part
// of the text.
func TestBm25RankerMatchesPiCC44fa21(t *testing.T) {
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
		Query  string `json:"query"`
		Limit  int    `json:"limit"`
		Ranked []struct {
			Name  string  `json:"name"`
			Score float64 `json:"score"`
		} `json:"ranked"`
	}
	if err := json.Unmarshal(cc44fa21Goldens, &probes); err != nil {
		t.Fatal(err)
	}
	ranked := 0
	for i, probe := range probes {
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
		if len(got) != len(probe.Ranked) {
			t.Errorf("probe %d %q limit %d: got %v, want %v", i, probe.Query, probe.Limit, got, probe.Ranked)
			continue
		}
		for k := range got {
			if got[k].Name != probe.Ranked[k].Name || got[k].Score != probe.Ranked[k].Score {
				t.Errorf("probe %d %q limit %d: got %v, want %v", i, probe.Query, probe.Limit, got, probe.Ranked)
				break
			}
		}
	}
	if ranked < len(probes) {
		t.Fatalf("only %d matches over %d probes: the goldens do not exercise the ranker", ranked, len(probes))
	}
}

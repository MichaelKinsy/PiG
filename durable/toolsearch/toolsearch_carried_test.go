package toolsearch

// pi: packages/durable/src/tool-search/tool-search.ts

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

//go:embed testdata/tool_search_goldens.json
var toolSearchGoldens []byte

func deferredTools(t *testing.T, raw []json.RawMessage) []*durable.ToolRegistration {
	t.Helper()
	deferred := make([]*durable.ToolRegistration, len(raw))
	for i, tool := range raw {
		var schema ai.ToolSchema
		if err := json.Unmarshal(tool, &schema); err != nil {
			t.Fatal(err)
		}
		deferred[i] = &durable.ToolRegistration{ToolSchema: schema}
	}
	return deferred
}

// Pi c7372af4 (#11631): `thread_read` with limit 1 loads `thread_read`, not `queue_read` whose description mentions the thread; a query that is a tool's name ranks it first.
func TestToolSearchRanksAToolByItsName(t *testing.T) {
	deferred := deferredTools(t, []json.RawMessage{
		json.RawMessage(`{"name":"mcp__app__queue_read","description":"Read a queued message of this thread.","parameters":{"type":"object","properties":{}}}`),
		json.RawMessage(`{"name":"mcp__app__thread_read","description":"Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans. Continue with afterPosition until hasMore is false.","parameters":{"type":"object","properties":{}}}`),
	})
	for _, query := range []string{"thread_read", "mcp__app__thread_read", "Thread_Read"} {
		got, err := CreateTool().Execute(t.Context(), map[string]any{"query": query, "limit": 1.0}, oracleAPI{deferred: deferred})
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"loaded": []any{"mcp__app__thread_read"}}; !reflect.DeepEqual(got.Details, want) {
			t.Errorf("%q: details %v, want %v", query, got.Details, want)
		}
	}
}

// The goldens are Pi's own tool_search at c7372af4 (testdata/gen_tool_search_goldens.mjs) over random deferred tools whose names share words with the queries: the result text, details and
// control must match exactly. The pinned Pi's oracle (toolsearch_pi_oracle_test.go) cannot supply them: it searches the name as part of the text and ranks by score alone.
func TestToolSearchMatchesPiTheLatestCarriedRanking(t *testing.T) {
	var cases []struct {
		Deferred []json.RawMessage `json:"deferred"`
		Query    string            `json:"query"`
		Limit    *float64          `json:"limit"`
		Result   struct {
			Output []struct {
				Text string `json:"text"`
			} `json:"output"`
			Details map[string]any `json:"details"`
			Control map[string]any `json:"control"`
		} `json:"result"`
	}
	if err := json.Unmarshal(toolSearchGoldens, &cases); err != nil {
		t.Fatal(err)
	}
	tool := CreateTool()
	loaded, several := 0, 0
	for i, c := range cases {
		args := map[string]any{"query": c.Query}
		if c.Limit != nil {
			args["limit"] = *c.Limit
		}
		got, err := tool.Execute(t.Context(), args, oracleAPI{deferred: deferredTools(t, c.Deferred)})
		if err != nil {
			t.Errorf("case %d %q: %v", i, c.Query, err)
			continue
		}
		text, ok := got.Output[0].(ai.TextContent)
		if !ok || text.Text != c.Result.Output[0].Text {
			t.Errorf("case %d %q limit %v: output %+v, Pi %q", i, c.Query, c.Limit, got.Output[0], c.Result.Output[0].Text)
		}
		var details map[string]any
		encoded, _ := json.Marshal(got.Details)
		_ = json.Unmarshal(encoded, &details)
		if !reflect.DeepEqual(details, c.Result.Details) {
			t.Errorf("case %d %q: details %s, Pi %v", i, c.Query, encoded, c.Result.Details)
		}
		var control map[string]any
		if got.Control != nil {
			encoded, _ := json.Marshal(got.Control)
			_ = json.Unmarshal(encoded, &control)
		}
		if !reflect.DeepEqual(control, c.Result.Control) {
			t.Errorf("case %d %q: control %v, Pi %v", i, c.Query, control, c.Result.Control)
		}
		if names, _ := c.Result.Details["loaded"].([]any); len(names) > 0 {
			loaded++
			if len(names) > 1 {
				several++
			}
		}
	}
	if loaded < 100 || several < 50 {
		t.Errorf("the goldens reach %d loads and %d with an order to check", loaded, several)
	}
}
